// Package httpclient 抽象 HTTP 客户端,统一 chat/auth/files 各处的请求构造。
//
// 实现基于 bogdanfinn/tls-client + fhttp,通过 re-tlsclient 的浏览器指纹 profile
// 替换原 req/v3 的 ImpersonateChrome(),指纹完全对齐 wreq 参考实现。
package httpclient

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/klauspost/compress/zstd"
)

// HTTPClient 是 tls-client HttpClient 的最小子集（与 tls_client.HttpClient 兼容）。
type HTTPClient = tlsClient

// tlsClient 本地别名,避免 import 循环。
type tlsClient interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
}

// NewRequest 构造 fhttp 请求并设置 headers 和 body。
// base 是浏览器指纹 profile header (UA / sec-ch-ua / accept-encoding 等) — 必传,
// headers 是业务 headers — 必传。
// 业务 headers 用 Set 覆盖 profile 同名 header。
func NewRequest(ctx context.Context, method, url string, base, headers fhttp.Header, body []byte) (*fhttp.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := fhttp.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	// 1) profile 头先 Add 进去 (作为基础)
	if base != nil {
		for k, vs := range base {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	// 2) 业务 headers 用 Set 覆盖 (覆盖 base 同名 key)
	if headers != nil {
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Set(k, v)
			}
		}
	}
	return req, nil
}

// DoJSON 发送请求并读取完整响应体,自动解码 gzip / deflate / br / zstd。
// 返回 (statusCode, body, contentType, error)。
// base 传入 chrome profile 头(UA / sec-ch-ua 等),headers 业务头覆盖 base。
func DoJSON(client HTTPClient, method, url string, base, headers fhttp.Header, body []byte) (int, []byte, string, error) {
	return DoJSONCtx(context.Background(), client, method, url, base, headers, body)
}

// DoJSONCtx 同 DoJSON,但支持 context 取消。
func DoJSONCtx(ctx context.Context, client HTTPClient, method, url string, base, headers fhttp.Header, body []byte) (int, []byte, string, error) {
	req, err := NewRequest(ctx, method, url, base, headers, body)
	if err != nil {
		return 0, nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, "", fmt.Errorf("read body: %w", err)
	}
	decoded, err := decodeBody(resp.Header.Get("Content-Encoding"), raw)
	if err != nil {
		return resp.StatusCode, nil, "", fmt.Errorf("decode body: %w", err)
	}
	return resp.StatusCode, decoded, resp.Header.Get("Content-Type"), nil
}

// DoRaw 发送请求但只读取 status + header,不读取 body,返回 body 供调用方流式消费。
// 适用于 SSE / 上传下载等长连接。
func DoRaw(ctx context.Context, client HTTPClient, method, url string, base, headers fhttp.Header, body []byte) (*fhttp.Response, error) {
	req, err := NewRequest(ctx, method, url, base, headers, body)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

// decodeBody 按 Content-Encoding 解码;若响应已经被 fhttp 透明解压,
// 嗅探到的字节流已是明文,二次解码会触发 "brotli: RESERVED" 之类错误,
// 这种情况下直接返回原始字节。
func decodeBody(encoding string, raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}

	// 1) 嗅探:如果首字节是明文 (JSON/XML/HTML 等文本) 特征,直接返回。
	if isPlainText(raw) {
		return raw, nil
	}

	// 2) 按 magic bytes 判断真实编码(优先于 Content-Encoding 头,避免重复解压)。
	switch {
	case len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b:
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return raw, nil // 解压失败回退到原始字节
		}
		defer gr.Close()
		out, err := io.ReadAll(gr)
		if err != nil {
			return raw, nil
		}
		return out, nil
	case len(raw) >= 4 && raw[0] == 0x50 && raw[1] == 0x2a && raw[2] == 0x4d && raw[3] == 0x18:
		// zstd magic
		zr, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return raw, nil
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		if err != nil {
			return raw, nil
		}
		return out, nil
	case len(raw) >= 4 && raw[0] == 0x28 && raw[1] == 0xb5 && raw[2] == 0x2f && raw[3] == 0xfd:
		// zstd skippable frame (also 0x184D2A50 系列)
		zr, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return raw, nil
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		if err != nil {
			return raw, nil
		}
		return out, nil
	}

	// 3) 嗅探失败时,才按 Content-Encoding 头尝试(并捕获错误回退)。
	enc := strings.ToLower(strings.TrimSpace(encoding))
	switch enc {
	case "", "identity":
		return raw, nil
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return raw, nil
		}
		defer gr.Close()
		out, err := io.ReadAll(gr)
		if err != nil {
			return raw, nil
		}
		return out, nil
	case "deflate":
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return raw, nil
		}
		return out, nil
	case "br":
		out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return raw, nil
		}
		return out, nil
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return raw, nil
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		if err != nil {
			return raw, nil
		}
		return out, nil
	default:
		return raw, nil
	}
}

// isPlainText 判断字节流是否已是明文(JSON/XML/HTML/UTF-8 文本)。
// 用于检测 fhttp 是否已经做过透明解压。
func isPlainText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	// 跳过 UTF-8 BOM
	if len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		b = b[3:]
	}
	c := b[0]
	// JSON / 常见文本开头
	if c == '{' || c == '[' || c == '"' || c == '<' || c == '-' || c == '+' || c == '.' ||
		(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		return true
	}
	// 排除已知的压缩 magic
	if c == 0x1f {
		return false
	}
	if c == 0x50 || c == 0x28 {
		return false
	}
	// 其它不可打印字节认为是压缩流
	return c < 0x20 || c == 0x7f
}
