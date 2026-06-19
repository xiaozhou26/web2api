// Package files 负责文件上传、image 代理、sandbox 文件下载等 HTTP 交互。
//
// 不直接依赖 *Client,通过显式注入 httpclient.HTTPClient / user agent / logger 协作,
// 根包 sentinel 在 facade 层把 *Client 字段传入。
package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"

	"web2api/internal/httpclient"
)

// jsonImpl 是 jsonMarshal/jsonUnmarshal 使用的实现。
var jsonImpl = struct {
	Marshal   func(interface{}) ([]byte, error)
	Unmarshal func([]byte, interface{}) error
}{
	Marshal:   json.Marshal,
	Unmarshal: json.Unmarshal,
}

// Logger 简单日志接口。
type Logger func(format string, args ...interface{})

// DefaultProfileHeader 是默认的浏览器指纹 header (chrome V148 / MacOS)。
// root 包通过 SetDefaultProfileHeader 注入实际的 profile。
var DefaultProfileHeader fhttp.Header

// SetDefaultProfileHeader 设置默认 profile header (root 包调用)。
func SetDefaultProfileHeader(h fhttp.Header) { DefaultProfileHeader = h }

// UploadedFile 是三步上传后沉淀的"可 attach 给 messages"的元数据。
type UploadedFile struct {
	FileID      string `json:"file_id"`
	FileName    string `json:"file_name"`
	FileSize    int    `json:"file_size"`
	MimeType    string `json:"mime_type"`
	UseCase     string `json:"use_case"` // 图片: multimodal, 文件: my_files
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	DownloadURL string `json:"download_url"`
}

// Attachment 是 messages[*].metadata.attachments[*] 的序列化对象。
type Attachment struct {
	ID       string `json:"id"`
	MimeType string `json:"mimeType"`
	Name     string `json:"name"`
	Size     int    `json:"size"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

// AssetPointerPart 是 messages[*].content.parts 里的一项(图片),
// 用于把 file-service:// 挂到多模态消息最前面。
type AssetPointerPart struct {
	ContentType  string `json:"content_type,omitempty"` // "image_asset_pointer"
	AssetPointer string `json:"asset_pointer"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	SizeBytes    int    `json:"size_bytes,omitempty"`
}

// ToAttachment 把一个已上传的 file 转成 messages.metadata.attachments 里的条目。
func (u *UploadedFile) ToAttachment() Attachment {
	a := Attachment{ID: u.FileID, MimeType: u.MimeType, Name: u.FileName, Size: u.FileSize}
	if u.UseCase == "multimodal" {
		a.Width = u.Width
		a.Height = u.Height
	}
	return a
}

// ToAssetPointerPart 返回 multimodal_text.parts 里 insert 在 prompt 前的那一项。
func (u *UploadedFile) ToAssetPointerPart() AssetPointerPart {
	return AssetPointerPart{
		ContentType:  "image_asset_pointer",
		AssetPointer: "file-service://" + u.FileID,
		Width:        u.Width,
		Height:       u.Height,
		SizeBytes:    u.FileSize,
	}
}

// AzureBlobClient 是用于 PUT upload_url (Azure Blob) 的干净 client (无 OpenAI auth header)。
// 这里通过 httpclient.HTTPClient 抽象,调用方用 NewAzureClient() 构造。
type AzureBlobClient httpclient.HTTPClient

// uploadOpts 是 UploadFile 调用的可选参数(ProfileHeader 等)。
type uploadOpts struct {
	ProfileHeader fhttp.Header
}

// UploadFileOptsFunc 配置 UploadFile 选项。
type UploadFileOptsFunc func(*uploadOpts)

// WithProfileHeader 注入浏览器指纹 header (UA / sec-ch-ua / accept-encoding 等)。
func WithProfileHeader(h fhttp.Header) UploadFileOptsFunc {
	return func(o *uploadOpts) { o.ProfileHeader = h }
}

// UploadFile 执行完整三步上传。
// httpClient 必须是带 OpenAI auth header 的 httpclient.HTTPClient。
// mimeHint 可选:来自 data URL header 或 HTTP Content-Type;为空或不可靠时根据文件内容嗅探。
func UploadFile(
	ctx context.Context,
	httpClient httpclient.HTTPClient,
	azureClient httpclient.HTTPClient,
	userAgent string,
	data []byte,
	fileName, mimeHint string,
	opts ...UploadFileOptsFunc,
) (*UploadedFile, error) {
	if len(data) == 0 {
		return nil, errors.New("empty file data")
	}
	mime, ext := resolveMime(data, mimeHint)
	useCase := "multimodal"
	if !strings.HasPrefix(mime, "image/") {
		useCase = "my_files"
	}
	if fileName == "" {
		fileName = fmt.Sprintf("file-%d%s", len(data), ext)
	}

	out := &UploadedFile{
		FileName: fileName,
		FileSize: len(data),
		MimeType: mime,
		UseCase:  useCase,
	}
	if strings.HasPrefix(mime, "image/") {
		if img, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			out.Width = img.Width
			out.Height = img.Height
		}
	}

	// ---- Step 1: POST /backend-api/files ----
	step1Body, err := jsonMarshal(map[string]interface{}{
		"file_name": fileName,
		"file_size": len(data),
		"use_case":  useCase,
		"width":     out.Width,
		"height":    out.Height,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal step1 body: %w", err)
	}
	status, respBody, _, err := httpclient.DoJSONCtx(ctx, httpClient,
		fhttp.MethodPost, "https://chatgpt.com/backend-api/files",
		DefaultProfileHeader,
		fhttp.Header{"Content-Type": {"application/json"}},
		step1Body,
	)
	if err != nil {
		return nil, fmt.Errorf("step1 post files failed: %w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("step1 create file failed: %s", string(respBody))
	}
	var step1Resp struct {
		FileID    string `json:"file_id"`
		UploadURL string `json:"upload_url"`
		Status    string `json:"status"`
	}
	if err := jsonUnmarshal(respBody, &step1Resp); err != nil {
		return nil, fmt.Errorf("step1 decode failed: %w", err)
	}
	if step1Resp.FileID == "" || step1Resp.UploadURL == "" {
		return nil, fmt.Errorf("step1 empty response: %s", string(respBody))
	}
	out.FileID = step1Resp.FileID

	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// ---- Step 2: PUT upload_url (Azure Blob) ----
	// azureClient 已是无 auth header 的干净 client。
	azureResp, err := httpclient.DoRaw(ctx, azureClient, fhttp.MethodPut, step1Resp.UploadURL,
		nil,
		fhttp.Header{
			"Content-Type":  {mime},
			"x-ms-blob-type": {"BlockBlob"},
			"x-ms-version":   {"2020-04-08"},
			"Origin":        {"https://chatgpt.com"},
			"User-Agent":    {userAgent},
			"Accept":        {"application/json, text/plain, */*"},
			"Accept-Language": {"en-US,en;q=0.8"},
			"Referer":       {"https://chatgpt.com/"},
		},
		data,
	)
	if err != nil {
		return nil, fmt.Errorf("step2 azure upload failed: %w", err)
	}
	if azureResp.StatusCode >= 400 {
		b, _ := io.ReadAll(azureResp.Body)
		azureResp.Body.Close()
		return nil, fmt.Errorf("step2 azure upload error: %s", string(b))
	}
	azureResp.Body.Close()

	// ---- Step 3: POST /backend-api/files/{file_id}/uploaded ----
	status, respBody, _, err = httpclient.DoJSONCtx(ctx, httpClient,
		fhttp.MethodPost,
		"https://chatgpt.com/backend-api/files/"+step1Resp.FileID+"/uploaded",
		DefaultProfileHeader,
		fhttp.Header{"Content-Type": {"application/json"}},
		[]byte("{}"),
	)
	if err != nil {
		return nil, fmt.Errorf("step3 register uploaded failed: %w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("step3 register uploaded error: %s", string(respBody))
	}
	var step3Resp struct {
		Status      string `json:"status"`
		DownloadURL string `json:"download_url"`
	}
	_ = jsonUnmarshal(respBody, &step3Resp)
	out.DownloadURL = step3Resp.DownloadURL

	return out, nil
}

// IsTransientNetErr 判定网络错误是否可重试。
func IsTransientNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	s := err.Error()
	for _, kw := range []string{
		"EOF",
		"connection reset",
		"connection refused",
		"broken pipe",
		"no route to host",
		"network is unreachable",
		"TLS handshake",
		"tls: handshake",
		"utls handshake",
		"i/o timeout",
		"unexpected EOF",
		"server closed connection",
		"use of closed network connection",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

// resolveMime 优先使用 mimeHint,否则嗅探文件内容。
func resolveMime(data []byte, mimeHint string) (mime, ext string) {
	sniffed, sniffExt := sniffMime(data)
	hint := normalizeMime(mimeHint)
	if hint != "" && hint != "application/octet-stream" {
		ext = extFromMime(hint)
		if ext == "" {
			ext = sniffExt
		}
		return hint, ext
	}
	return sniffed, sniffExt
}

func normalizeMime(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func extFromMime(mime string) string {
	switch strings.ToLower(normalizeMime(mime)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	case "application/msword":
		return ".doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.ms-powerpoint":
		return ".ppt"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "application/json":
		return ".json"
	case "text/markdown":
		return ".md"
	default:
		return ""
	}
}

func sniffMime(data []byte) (mime, ext string) {
	n := 512
	if len(data) < n {
		n = len(data)
	}
	mime = http.DetectContentType(data[:n])
	ext = extFromMime(mime)
	return mime, ext
}

// GuessMimeFromName 根据扩展名猜测 mime。
func GuessMimeFromName(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

// jsonMarshal / jsonUnmarshal 内部 helper。
func jsonMarshal(v interface{}) ([]byte, error)    { return jsonImpl.Marshal(v) }
func jsonUnmarshal(data []byte, v interface{}) error { return jsonImpl.Unmarshal(data, v) }
