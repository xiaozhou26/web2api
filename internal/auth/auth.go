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

// NewUUID 调用方注入的实现(sentinel.GenerateUUID)。
type NewUUID func() string

// POWConfigRequirements requirements token 配置器(实际返回 requirements token 字符串)。
type POWConfigRequirements func(userAgent string) string

// POWProof proof token 求解器。
type POWProof func(seed, difficulty, userAgent string) string

// ClientParams 注入到 GetConduitToken / GetSentinelToken 的依赖。
type ClientParams struct {
	HTTPClient    httpclient.HTTPClient
	BaseURL       string // 例如 "https://chatgpt.com"
	UserAgent     string
	Logger        Logger
	NewUUID       NewUUID
	NewPOWConfig  POWConfigRequirements
	SolveProof    POWProof
	ProfileHeader fhttp.Header // 浏览器指纹 header (UA / sec-ch-ua / accept-encoding 等)
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
		headers,
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
func GetSentinelToken(p ClientParams) (sentinelToken, proofToken string, err error) {
	reqToken := p.NewPOWConfig(p.UserAgent)

	prepBody, err := json.Marshal(map[string]string{
		"p": reqToken,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal prepare body: %w", err)
	}

	status, respBody, _, err := httpclient.DoJSONCtx(
		context.Background(), p.HTTPClient,
		fhttp.MethodPost,
		p.BaseURL+"/backend-api/sentinel/chat-requirements/prepare",
		p.ProfileHeader,
		fhttp.Header{
			"Accept":                {"*/*"},
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {"/backend-api/sentinel/chat-requirements/prepare"},
			"x-openai-target-route": {"/backend-api/sentinel/chat-requirements/prepare"},
		},
		prepBody,
	)
	if err != nil {
		return "", "", fmt.Errorf("sentinel/prepare request: %w", err)
	}
	if status != 200 {
		return "", "", fmt.Errorf("sentinel/prepare %d: %s", status, Truncate(string(respBody), 200))
	}

	var pd struct {
		Persona     string `json:"persona"`
		Proofofwork *struct {
			Required   bool   `json:"required"`
			Seed       string `json:"seed"`
			Difficulty string `json:"difficulty"`
		} `json:"proofofwork"`
		Turnstile *struct {
			Required bool `json:"required"`
		} `json:"turnstile"`
		PrepareToken string `json:"prepare_token"`
	}
	if err := json.Unmarshal(respBody, &pd); err != nil {
		return "", "", fmt.Errorf("parse sentinel/prepare: %w", err)
	}

	powRequired := pd.Proofofwork != nil && pd.Proofofwork.Required
	turnstileRequired := pd.Turnstile != nil && pd.Turnstile.Required
	if p.Logger != nil {
		p.Logger("  [sentinel] persona=%s, PoW=%v, turnstile=%v", pd.Persona, powRequired, turnstileRequired)
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

	fb, err := json.Marshal(map[string]interface{}{
		"prepare_token": pd.PrepareToken,
		"proofofwork":   proofToken,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal finalize body: %w", err)
	}

	status, respBody, _, err = httpclient.DoJSONCtx(
		context.Background(), p.HTTPClient,
		fhttp.MethodPost,
		p.BaseURL+"/backend-api/sentinel/chat-requirements/finalize",
		p.ProfileHeader,
		fhttp.Header{
			"Accept":                {"*/*"},
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {"/backend-api/sentinel/chat-requirements/finalize"},
			"x-openai-target-route": {"/backend-api/sentinel/chat-requirements/finalize"},
		},
		fb,
	)
	if err != nil {
		return "", "", fmt.Errorf("sentinel/finalize request: %w", err)
	}
	if status != 200 {
		return "", "", fmt.Errorf("sentinel/finalize %d: %s", status, Truncate(string(respBody), 200))
	}

	var fd struct {
		Token       string `json:"token"`
		ExpireAfter int    `json:"expire_after"`
	}
	if err := json.Unmarshal(respBody, &fd); err != nil {
		return "", "", fmt.Errorf("parse sentinel/finalize: %w", err)
	}
	if fd.Token == "" {
		return "", "", fmt.Errorf("no sentinel token: %s", Truncate(string(respBody), 200))
	}

	if p.Logger != nil {
		p.Logger("  [finalize] expire=%ds", fd.ExpireAfter)
	}
	return fd.Token, proofToken, nil
}
