package files

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"

	"web2api/internal/httpclient"
)

// ResolvePDFDownloadURL 调用 interpreter/download 获取沙箱文件下载直链(pdf/txt 等)。
func ResolvePDFDownloadURL(ctx context.Context, httpClient httpclient.HTTPClient, conversationID, messageID, sandboxPath string) (string, error) {
	apiPath := "https://chatgpt.com/backend-api/conversation/" + conversationID + "/interpreter/download"
	body, err := jsonMarshal(map[string]interface{}{
		"message_id":   messageID,
		"sandbox_path": sandboxPath,
	})
	if err != nil {
		return "", fmt.Errorf("marshal pdf body: %w", err)
	}
	status, respBody, _, err := httpclient.DoJSONCtx(ctx, httpClient, fhttp.MethodPost, apiPath,
		DefaultProfileHeader,
		fhttp.Header{
			"Content-Type":          {"application/json"},
			"x-openai-target-path":  {apiPath},
			"x-openai-target-route": {"/backend-api/conversation/{conversation_id}/interpreter/download"},
		},
		body,
	)
	if err != nil {
		return "", fmt.Errorf("interpreter/download request: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("interpreter/download %d: %s", status, truncateStr(string(respBody), 200))
	}
	var out struct {
		DownloadURL string `json:"download_url"`
	}
	if err := jsonUnmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("parse download response: %w", err)
	}
	if out.DownloadURL == "" {
		return "", fmt.Errorf("empty download_url")
	}
	return out.DownloadURL, nil
}

// ProxyPDFBySandboxPath 代理下载沙箱文件并写入 ResponseWriter。
func ProxyPDFBySandboxPath(
	ctx context.Context,
	httpClient, cleanClient httpclient.HTTPClient,
	fallbackUA, conversationID, messageID, sandboxPath string,
	w http.ResponseWriter,
	reqUserAgent string,
) error {
	downloadURL, err := ResolvePDFDownloadURL(ctx, httpClient, conversationID, messageID, sandboxPath)
	if err != nil {
		return err
	}

	ua := reqUserAgent
	if ua == "" {
		ua = fallbackUA
	}

	resp, err := httpclient.DoRaw(ctx, cleanClient, fhttp.MethodGet, downloadURL,
		DefaultProfileHeader,
		fhttp.Header{"User-Agent": {ua}},
		nil,
	)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download file %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	filename := sandboxPath[strings.LastIndex(sandboxPath, "/")+1:]

	w.Header().Set("Content-Disposition", "attachment; filename=\""+url.PathEscape(filename)+"\"")
	if strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		w.Header().Set("Content-Type", "application/pdf")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	_, err = w.Write(data)
	return err
}

// DownloadSandboxFile 下载沙箱产物二进制(供 base64 流式下发)。
func DownloadSandboxFile(ctx context.Context, httpClient, cleanClient httpclient.HTTPClient, fallbackUA, conversationID, messageID, sandboxPath string) ([]byte, string, error) {
	downloadURL, err := ResolvePDFDownloadURL(ctx, httpClient, conversationID, messageID, sandboxPath)
	if err != nil {
		return nil, "", err
	}
	ua := fallbackUA
	if ua == "" {
		ua = "Mozilla/5.0"
	}
	resp, err := httpclient.DoRaw(ctx, cleanClient, fhttp.MethodGet, downloadURL,
		DefaultProfileHeader,
		fhttp.Header{"User-Agent": {ua}},
		nil,
	)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("download file %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	filename := sandboxPath[strings.LastIndex(sandboxPath, "/")+1:]
	mime := GuessMimeFromName(filename)
	return data, mime, nil
}

// EncodeSandboxPathForQuery URL 编码 sandbox_path。
func EncodeSandboxPathForQuery(sandboxPath string) string {
	return url.QueryEscape(sandboxPath)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
