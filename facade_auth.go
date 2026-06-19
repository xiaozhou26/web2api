package main

import (
	"web2api/internal/auth"
	"web2api/internal/httpclient"
)

// auth facade:根包把 *Client 字段打包成 ClientParams 传给 internal/auth。

func (c *Client) getConduitToken(model, turnTraceID, partialText string) (string, error) {
	return auth.GetConduitToken(auth.ClientParams{
		HTTPClient:    c.httpClient,
		BaseURL:       "https://chatgpt.com",
		UserAgent:     c.userAgent,
		Logger:        c.logf,
		NewUUID:       GenerateUUID,
		ProfileHeader: c.profileHeaders,
	}, model, turnTraceID, partialText)
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
		ProfileHeader: c.profileHeaders,
	})
}

// powRequirementsToken 把 pow.Config 适配为 "ua → requirements token string"。
func powRequirementsToken(userAgent string) string {
	return NewPOWConfig(userAgent).RequirementsToken()
}

// 防止未使用 import
var _ = httpclient.HTTPClient(nil)
