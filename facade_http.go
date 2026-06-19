package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"

	"web2api/internal/httpclient"
)

// doJSON 构造一个 GET/POST/PUT 请求,设置 headers + body,执行并返回完整响应。
// 返回 (statusCode, body, contentType, error)。
// commonHeaders 已包含 profile + 业务 header,直接传给 httpclient。
func (c *Client) doJSON(method, path string, headers map[string]string, body []byte) (int, []byte, string, error) {
	return c.doJSONCtx(context.Background(), method, path, headers, body)
}

// doJSONCtx 同 doJSON,但支持 context 取消。
func (c *Client) doJSONCtx(ctx context.Context, method, path string, headers map[string]string, body []byte) (int, []byte, string, error) {
	fh := c.mergedHeaders(headers)
	return httpclient.DoJSONCtx(ctx, c.httpClient, method, joinBaseURL(path), c.profileHeaders, fh, body)
}

// doStream 构造请求,执行并返回 fhttp.Response,body 留给调用方流式读取(SSE / 长连接)。
func (c *Client) doStream(ctx context.Context, method, path string, headers map[string]string, body []byte) (*fhttp.Response, error) {
	fh := c.mergedHeaders(headers)
	return httpclient.DoRaw(ctx, c.httpClient, method, joinBaseURL(path), c.profileHeaders, fh, body)
}

// doStreamDefault 使用 background context 的便捷重载。
func (c *Client) doStreamDefault(method, path string, headers map[string]string, body []byte) (*fhttp.Response, error) {
	return c.doStream(context.Background(), method, path, headers, body)
}

// mergedHeaders 把 c.commonHeaders() 与传入的 headers 合并,传入值优先。
// 注意:不再覆盖 profileHeaders 中的 User-Agent / sec-ch-ua*,这些由
// NewRequest 阶段以 profile 为基础,然后 Set 业务 header 覆盖(允许业务覆盖)。
func (c *Client) mergedHeaders(extra map[string]string) fhttp.Header {
	base := c.commonHeaders()
	if extra == nil {
		return base
	}
	for k, v := range extra {
		base.Set(k, v)
	}
	return base
}

// joinBaseURL 给 path 加上 baseURL (https://chatgpt.com)。
func joinBaseURL(path string) string {
	const base = "https://chatgpt.com"
	if len(path) == 0 {
		return base
	}
	if path[0] == '/' {
		return base + path
	}
	return base + "/" + path
}

// 内部辅助:从 fhttp.Response 读 body,带超时保护。
func readAllWithTimeout(resp *fhttp.Response, d time.Duration) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	if d <= 0 {
		return io.ReadAll(resp.Body)
	}
	done := make(chan struct{})
	var (
		buf []byte
		err error
	)
	go func() {
		buf, err = io.ReadAll(resp.Body)
		close(done)
	}()
	select {
	case <-done:
		return buf, err
	case <-time.After(d):
		return nil, fmt.Errorf("read body timeout after %s", d)
	}
}

// truncateResponse 截断 status 错误信息。
func truncateResponse(status int, body []byte, maxLen int) string {
	if len(body) > maxLen {
		body = body[:maxLen]
	}
	return fmt.Sprintf("%d %s", status, http.StatusText(status)) + ": " + string(body)
}

// parseJSONResponse 解析 body 到 v,返回错误含 status code。
func parseJSONResponse(body []byte, v interface{}, status int) error {
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("parse response %d: %w", status, err)
	}
	return nil
}
