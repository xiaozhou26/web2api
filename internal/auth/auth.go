// Package auth 实现 Sentinel 认证三步流程:prepare → PoW → finalize。
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"

	"web2api/internal/httpclient"
)

// Logger 简单日志接口(与 sentinel.Client.logf 对齐)。
type Logger func(format string, args ...interface{})

// Truncate 截断字符串到 maxLen。
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// previewStr 安全预览字符串 (前 maxLen, 省略中间)。
func previewStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// mergeHeaders 把 commonHeader (Authorization / Origin / Referer 等)
// 与 extra (业务 / 请求特定) 合并。common 优先(以 Set 覆盖),保证
// Authorization 等关键 header 不会被业务头意外清空。
func mergeHeaders(common, extra fhttp.Header) fhttp.Header {
	out := fhttp.Header{}
	for k, vs := range common {
		for _, v := range vs {
			out.Set(k, v)
		}
	}
	for k, vs := range extra {
		for _, v := range vs {
			out.Set(k, v)
		}
	}
	return out
}

// NewUUID 调用方注入的实现(sentinel.GenerateUUID)。
type NewUUID func() string

// POWConfigRequirements requirements token 配置器(实际返回 requirements token 字符串)。
type POWConfigRequirements func(userAgent string) string

// POWProof proof token 求解器。
type POWProof func(seed, difficulty, userAgent string) string

// TurnstileSolver 求解 turnstile challenge(dx → response)。可选;无 solver 时返回空串。
type TurnstileSolver func(userAgent, dx string) (string, error)

// ClientParams 注入到 GetConduitToken / GetSentinelToken 的依赖。
type ClientParams struct {
	HTTPClient       httpclient.HTTPClient
	BaseURL          string // 例如 "https://chatgpt.com"
	UserAgent        string
	Logger           Logger
	NewUUID          NewUUID
	NewPOWConfig     POWConfigRequirements
	SolveProof       POWProof
	SolveTurnstile   TurnstileSolver // 可选;若 turnstile required 但无 solver,会发送空 token
	ProfileHeader    fhttp.Header    // 浏览器指纹 header (UA / sec-ch-ua / accept-encoding 等)
	CommonHeader     fhttp.Header    // 业务头 (Authorization / Origin / Referer / oai-* 等) — 与 ProfileHeader 合并后发送
	RequirementsToken string         // 准备阶段生成的 RequirementsToken (gAAAAAC<base64>~S),用作 turnstile solver 的 XOR key
}

// GetConduitToken 获取 conduit_token(Step 1)。
func GetConduitToken(p ClientParams, model, turnTraceID, partialText string) (string, error) {
	if partialText == "" {
		partialText = "h"
	}

	body := map[string]interface{}{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     "client-created-root",
		"model":                 model,
		"timezone_offset_min":   -480,
		"timezone":              "Asia/Shanghai",
		"conversation_mode":     map[string]string{"kind": "primary_assistant"},
		"system_hints":          []string{},
		"partial_query": map[string]interface{}{
			"id":     p.NewUUID(),
			"author": map[string]string{"role": "user"},
			"content": map[string]interface{}{
				"content_type": "text",
				"parts":        []string{partialText},
			},
		},
		"supports_buffering":     true,
		"supported_encodings":    []string{"v1"},
		"client_contextual_info": map[string]interface{}{"app_name": "chatgpt.com"},
		"thinking_effort":        "standard",
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal conduit body: %w", err)
	}

	headers := fhttp.Header{
		"Accept":                {"*/*"},
		"Content-Type":          {"application/json"},
		"x-conduit-token":       {"no-token"},
		"x-oai-turn-trace-id":   {turnTraceID},
		"x-openai-target-path":  {"/backend-api/f/conversation/prepare"},
		"x-openai-target-route": {"/backend-api/f/conversation/prepare"},
	}

	status, respBody, _, err := httpclient.DoJSONCtx(
		context.Background(), p.HTTPClient,
		fhttp.MethodPost,
		p.BaseURL+"/backend-api/f/conversation/prepare",
		p.ProfileHeader,
		mergeHeaders(p.CommonHeader, headers),
		bodyBytes,
	)
	if err != nil {
		return "", fmt.Errorf("conversation/prepare request: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("conversation/prepare %d: %s", status, Truncate(string(respBody), 200))
	}

	var result struct {
		Status       string `json:"status"`
		ConduitToken string `json:"conduit_token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse conduit response: %w", err)
	}

	if p.Logger != nil {
		p.Logger("  [conduit] status=%s", result.Status)
	}
	return result.ConduitToken, nil
}

// GetSentinelToken 获取 sentinel token(Step 2+3:prepare → PoW → finalize)。
// turnstile token 由 TurnstileSolver 求解(可选,无 solver 时为空)。
// 流程:prepare -> 若 PoW required: 本地求解 -> 若 turnstile required: solver 求解
//       -> finalize {prepare_token, proofofwork, turnstile}
func GetSentinelToken(p ClientParams) (sentinelToken, proofToken, turnstileToken string, err error) {
	reqToken := p.NewPOWConfig(p.UserAgent)

	prepBody, err := json.Marshal(map[string]string{
		"p": reqToken,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("marshal prepare body: %w", err)
	}

	status, respBody, _, err := httpclient.DoJSONCtx(
		context.Background(), p.HTTPClient,
		fhttp.MethodPost,
		p.BaseURL+"/backend-api/sentinel/chat-requirements/prepare",
		p.ProfileHeader,
		mergeHeaders(p.CommonHeader, fhttp.Header{
			"Accept":                {"*/*"},
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {"/backend-api/sentinel/chat-requirements/prepare"},
			"x-openai-target-route": {"/backend-api/sentinel/chat-requirements/prepare"},
		}),
		prepBody,
	)
	if err != nil {
		return "", "", "", fmt.Errorf("sentinel/prepare request: %w", err)
	}
	if status != 200 {
		return "", "", "", fmt.Errorf("sentinel/prepare %d: %s", status, Truncate(string(respBody), 200))
	}

	var pd struct {
		Persona     string `json:"persona"`
		Proofofwork *struct {
			Required   bool   `json:"required"`
			Seed       string `json:"seed"`
			Difficulty string `json:"difficulty"`
		} `json:"proofofwork"`
		Turnstile *struct {
			Required bool   `json:"required"`
			DX       string `json:"dx"` // challenge string
		} `json:"turnstile"`
		PrepareToken string `json:"prepare_token"`
	}
	if err := json.Unmarshal(respBody, &pd); err != nil {
		return "", "", "", fmt.Errorf("parse sentinel/prepare: %w", err)
	}

	powRequired := pd.Proofofwork != nil && pd.Proofofwork.Required
	turnstileRequired := pd.Turnstile != nil && pd.Turnstile.Required
	if p.Logger != nil {
		p.Logger("  [sentinel] persona=%s, PoW=%v, turnstile=%v", pd.Persona, powRequired, turnstileRequired)
		if turnstileRequired && pd.Turnstile != nil {
			p.Logger("  [turnstile] dx 长度: %d, dx 前 40 字符: %s", len(pd.Turnstile.DX), previewStr(pd.Turnstile.DX, 40))
		}
	}

	if powRequired {
		seed := pd.Proofofwork.Seed
		difficulty := pd.Proofofwork.Difficulty
		s0 := time.Now()
		proofToken = p.SolveProof(seed, difficulty, p.UserAgent)
		if p.Logger != nil {
			p.Logger("  [pow] solved in %dms", time.Since(s0).Milliseconds())
		}
	}

	// turnstile token: 若 required + 有 solver 回调则解算,否则留空
	if turnstileRequired {
		if p.SolveTurnstile != nil && pd.Turnstile != nil && pd.Turnstile.DX != "" {
			s0 := time.Now()
			turnstileToken, err = p.SolveTurnstile(p.UserAgent, pd.Turnstile.DX)
			if err != nil {
				if p.Logger != nil {
					p.Logger("  [turnstile] solver failed: %v", err)
				}
				// turnstile 求解失败仍尝试,服务端可能放过
			} else if p.Logger != nil {
				p.Logger("  [turnstile] solved in %dms, token 长度: %d", time.Since(s0).Milliseconds(), len(turnstileToken))
				if turnstileToken == "" {
					p.Logger("  [turnstile] ⚠️ solver 返回空字符串!可能原因: VM 执行失败 / JSON 解析失败 / key 不对")
				}
			}
		} else if p.Logger != nil {
			p.Logger("  [turnstile] required but no solver / dx, will send empty turnstile")
		}
	}

	fb, err := json.Marshal(map[string]interface{}{
		"prepare_token": pd.PrepareToken,
		"proofofwork":   proofToken,
		"turnstile":     turnstileToken,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("marshal finalize body: %w", err)
	}

	status, respBody, _, err = httpclient.DoJSONCtx(
		context.Background(), p.HTTPClient,
		fhttp.MethodPost,
		p.BaseURL+"/backend-api/sentinel/chat-requirements/finalize",
		p.ProfileHeader,
		mergeHeaders(p.CommonHeader, fhttp.Header{
			"Accept":                {"*/*"},
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {"/backend-api/sentinel/chat-requirements/finalize"},
			"x-openai-target-route": {"/backend-api/sentinel/chat-requirements/finalize"},
		}),
		fb,
	)
	if err != nil {
		return "", "", "", fmt.Errorf("sentinel/finalize request: %w", err)
	}
	if status != 200 {
		return "", "", "", fmt.Errorf("sentinel/finalize %d: %s", status, Truncate(string(respBody), 200))
	}

	var fd struct {
		Token       string `json:"token"`
		ExpireAfter int    `json:"expire_after"`
	}
	if err := json.Unmarshal(respBody, &fd); err != nil {
		return "", "", "", fmt.Errorf("parse sentinel/finalize: %w", err)
	}
	if fd.Token == "" {
		return "", "", "", fmt.Errorf("no sentinel token: %s", Truncate(string(respBody), 200))
	}

	if p.Logger != nil {
		p.Logger("  [finalize] expire=%ds", fd.ExpireAfter)
	}
	return fd.Token, proofToken, turnstileToken, nil
}
