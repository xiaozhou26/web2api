package files

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"

	"web2api/internal/httpclient"
)

// PollForImageFileID 保留为旧调用方兼容:当前 SSE 信号已能直接提供 file_id。
func PollForImageFileID(_ string) (string, error) {
	return "", fmt.Errorf("PollForImageFileID 已弃用,请从 SSE 信号获取图片 file_id")
}

// ProxyImageByFileID 获取文件直链并代理直接将流输出到 http.ResponseWriter。
func ProxyImageByFileID(
	ctx context.Context,
	httpClient httpclient.HTTPClient,
	cleanClient httpclient.HTTPClient,
	log Logger,
	fileID, conversationID string,
	w http.ResponseWriter,
	reqUserAgent string,
) error {
	apiPath := fmt.Sprintf("https://chatgpt.com/backend-api/files/download/%s?conversation_id=%s&inline=false", fileID, conversationID)
	_, respBody, _, err := httpclient.DoJSONCtx(ctx, httpClient, fhttp.MethodGet, apiPath,
		DefaultProfileHeader,
		fhttp.Header{
			"Accept":                {"*/*"},
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {apiPath},
			"x-openai-target-route": {"/backend-api/files/download/{fileId}"},
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("download info failed: %w", err)
	}

	var dr struct {
		DownloadURL string `json:"download_url"`
	}
	if err := jsonUnmarshal(respBody, &dr); err != nil || dr.DownloadURL == "" {
		return fmt.Errorf("no download_url in response")
	}

	if log != nil {
		log("[image] 提取到图片直链: %s", dr.DownloadURL)
	}

	// 如果 DownloadURL 依然是 chatgpt.com 的内部地址(如 estuary/content),则必须携带原有的鉴权 Header(Bearer Token)
	// 如果是外部 CDN 直链(如 files.oaiusercontent.com),则使用干净的客户端防止双重鉴权或跨域被拦截
	isInternalURL := strings.Contains(dr.DownloadURL, "chatgpt.com")

	reqHeader := fhttp.Header{
		"User-Agent": {reqUserAgent},
		"Accept":     {"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"},
	}

	var imgClient httpclient.HTTPClient
	if isInternalURL {
		if log != nil {
			log("[image] 内部链接,使用原生客户端进行代理")
		}
		imgClient = httpClient
	} else {
		if log != nil {
			log("[image] 外部 CDN 链接,使用干净客户端进行代理")
		}
		imgClient = cleanClient
	}

	imgResp, err := httpclient.DoRaw(ctx, imgClient, fhttp.MethodGet, dr.DownloadURL, DefaultProfileHeader, reqHeader, nil)
	if err != nil {
		return fmt.Errorf("proxy fetch image failed: %w", err)
	}
	defer imgResp.Body.Close()
	if imgResp.StatusCode >= 400 {
		return fmt.Errorf("proxy fetch image returned error status: %d", imgResp.StatusCode)
	}

	imgData, err := io.ReadAll(imgResp.Body)
	if err != nil {
		return fmt.Errorf("read image body: %w", err)
	}
	contentType := imgResp.Header.Get("Content-Type")

	if contentType != "" {
		w.Header()["Content-Type"] = []string{contentType}
	}
	w.Header()["Cache-Control"] = []string{"public, max-age=31536000"} // 让浏览器永久缓存

	if _, err := w.Write(imgData); err != nil {
		return fmt.Errorf("proxy write image failed: %w", err)
	}
	if log != nil {
		log("[image] 代理传输完毕, %d bytes", len(imgData))
	}
	return nil
}

// ResolveFileDownloadURL 获取 files/download 直链。
func ResolveFileDownloadURL(ctx context.Context, httpClient httpclient.HTTPClient, fileID, conversationID string) (string, error) {
	apiPath := fmt.Sprintf("https://chatgpt.com/backend-api/files/download/%s?conversation_id=%s&inline=false", fileID, conversationID)
	_, respBody, _, err := httpclient.DoJSONCtx(ctx, httpClient, fhttp.MethodGet, apiPath,
		DefaultProfileHeader,
		fhttp.Header{
			"Accept":                {"*/*"},
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {apiPath},
			"x-openai-target-route": {"/backend-api/files/download/{fileId}"},
		},
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("download info failed: %w", err)
	}
	var dr struct {
		DownloadURL string `json:"download_url"`
	}
	if err := jsonUnmarshal(respBody, &dr); err != nil || dr.DownloadURL == "" {
		return "", fmt.Errorf("no download_url in response")
	}
	return dr.DownloadURL, nil
}

func fetchURLBytes(ctx context.Context, httpClient, cleanClient httpclient.HTTPClient, downloadURL, reqUserAgent string) ([]byte, string, error) {
	isInternal := strings.Contains(downloadURL, "chatgpt.com")
	reqHeader := fhttp.Header{
		"User-Agent": {reqUserAgent},
		"Accept":     {"*/*"},
	}
	var imgClient httpclient.HTTPClient
	if isInternal {
		imgClient = httpClient
	} else {
		imgClient = cleanClient
	}
	imgResp, err := httpclient.DoRaw(ctx, imgClient, fhttp.MethodGet, downloadURL, DefaultProfileHeader, reqHeader, nil)
	if err != nil {
		return nil, "", err
	}
	defer imgResp.Body.Close()
	if imgResp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("fetch %d", imgResp.StatusCode)
	}
	data, err := io.ReadAll(imgResp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, imgResp.Header.Get("Content-Type"), nil
}

// FetchURLBytes 通用 URL 拉取 helper。
func FetchURLBytes(ctx context.Context, httpClient, cleanClient httpclient.HTTPClient, downloadURL, reqUserAgent string) ([]byte, string, error) {
	return fetchURLBytes(ctx, httpClient, cleanClient, downloadURL, reqUserAgent)
}

// DownloadFileByFileID 下载生图/附件 file_id 对应二进制(供 base64 流式下发)。
func DownloadFileByFileID(ctx context.Context, httpClient, cleanClient httpclient.HTTPClient, fallbackUA, conversationID, fileID string) ([]byte, string, error) {
	dl, err := ResolveFileDownloadURL(ctx, httpClient, fileID, conversationID)
	if err != nil {
		return nil, "", err
	}
	ua := fallbackUA
	if ua == "" {
		ua = "Mozilla/5.0"
	}
	return fetchURLBytes(ctx, httpClient, cleanClient, dl, ua)
}
