package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"web2api/internal/auth"
	"web2api/internal/httpclient"
)

// auth facade:根包把 *Client 字段打包成 ClientParams 传给 internal/auth。

func (c *Client) getConduitToken(model, turnTraceID, partialText string) (string, error) {
	token, err := auth.GetConduitToken(auth.ClientParams{
		HTTPClient:    c.httpClient,
		BaseURL:       "https://chatgpt.com",
		UserAgent:     c.userAgent,
		Logger:        c.logf,
		NewUUID:       GenerateUUID,
		ProfileHeader: c.fullProfileHeaders(),
		CommonHeader:  c.commonHeaders(),
	}, model, turnTraceID, partialText)
	if err != nil {
		// 401 通常是 token 失效/类型错;给出可操作提示
		if strings.Contains(err.Error(), "401") {
			_, payload, exp, ok := decodeJWTInfo(c.bearerToken)
			c.logf("[web2api] 401 Unauthorized — token 本身合法,但服务端拒绝")
			c.logf("[web2api] 当前 token 前 60 字符: %s", previewToken(c.bearerToken))
			if ok {
				c.logf("[web2api] JWT payload: %s", payload)
				if exp > 0 {
					now := time.Now().Unix()
					remaining := exp - now
					if remaining > 0 {
						c.logf("[web2api] token 剩余有效时间: %d 秒 (≈ %d 小时)", remaining, remaining/3600)
					} else {
						c.logf("[web2api] ⚠️ token 已过期 %d 秒", -remaining)
					}
				}
			}
			c.logf("[web2api] 401 最常见原因 (token 没问题时):")
			c.logf("[web2api]   1) 缺反爬 header — 需要 oai-hlib / oai-sc / oai-gn (从浏览器 DevTools 复制)")
			c.logf("[web2api]   2) 缺 Cloudflare cookie — 需要 cf_clearance / __cf_bm (DevTools → Application → Cookies)")
			c.logf("[web2api]   3) TLS 指纹与 UA 不一致 — 用 re-tlsclient/chrome V148/V149 即可")
			c.logf("[web2api]   4) IP 信誉差 — 尝试住宅 IP 或重启网络")
			c.logf("[web2api] 当前请求发送的 header:")
			fh := c.fullProfileHeaders()
			c.logf("[web2api] fullProfileHeaders 实际 key 数量: %d", len(fh))
			for k, v := range fh {
				c.logf("[web2api]   [raw] %s = %v", k, v)
			}
			for _, k := range []string{
				"User-Agent", "sec-ch-ua", "sec-ch-ua-platform", "accept-encoding",
				"accept-language", "oai-hlib", "oai-gn", "oai-sc", "Cookie",
			} {
				if v, ok := fh[k]; ok && len(v) > 0 {
					val := v[0]
					if len(val) > 60 {
						val = val[:60] + "..."
					}
					c.logf("[web2api]   %s: %s", k, val)
				} else {
					c.logf("[web2api]   %s: <missing>", k)
				}
			}
		}
		return token, err
	}
	return token, nil
}

// previewToken 安全预览 token (前 30 + 后 10,中间省略)。
func previewToken(t string) string {
	if len(t) <= 40 {
		return t
	}
	return t[:30] + "..." + t[len(t)-10:]
}

// decodeJWTInfo 解码 JWT header + payload,返回 header/payload 字符串和 exp 时间。
// 若不是 JWT 返回 ("","", zero, false)。
func decodeJWTInfo(t string) (header, payload string, exp int64, ok bool) {
	parts := strings.Split(t, ".")
	if len(parts) < 2 {
		return "", "", 0, false
	}
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", 0, false
	}
	p, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return string(h), "", 0, false
	}
	// 简单字符串搜索 "exp":1234567890
	pStr := string(p)
	if idx := strings.Index(pStr, `"exp":`); idx >= 0 {
		rest := pStr[idx+6:]
		end := strings.IndexAny(rest, ",}")
		if end > 0 {
			fmt.Sscanf(rest[:end], "%d", &exp)
		}
	}
	return string(h), pStr, exp, true
}

func (c *Client) getSentinelToken() (sentinelToken, proofToken string, err error) {
	return auth.GetSentinelToken(auth.ClientParams{
		HTTPClient:    c.httpClient,
		BaseURL:       "https://chatgpt.com",
		UserAgent:     c.userAgent,
		Logger:        c.logf,
		NewUUID:       GenerateUUID,
		NewPOWConfig:  powRequirementsToken,
		SolveProof:    SolveProofToken,
		ProfileHeader: c.fullProfileHeaders(),
		CommonHeader:  c.commonHeaders(),
	})
}

// powRequirementsToken 把 pow.Config 适配为 "ua → requirements token string"。
func powRequirementsToken(userAgent string) string {
	return NewPOWConfig(userAgent).RequirementsToken()
}

// 防止未使用 import
var _ = httpclient.HTTPClient(nil)
