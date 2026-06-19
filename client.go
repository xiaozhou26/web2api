package main

import (
	"log"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/xiaozhou26/re-tlsclient/chrome"

	"web2api/internal/files"
	"web2api/internal/httpclient"
)

const (
	defaultUA          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36 Edg/147.0.0.0"
	defaultBuildHash   = "prod-81e0c5cdf6140e8c5db714d613337f4aeab94029"
	defaultBuildNumber = "6128297"
	defaultLang        = "zh-CN"
	defaultModel       = "gpt-5-5-thinking"
)

// Client 是 ChatGPT 对话客户端,封装了完整的 Sentinel 认证 + SSE 对话流程。
type Client struct {
	httpClient     httpclient.HTTPClient
	cleanClient    httpclient.HTTPClient // 无 auth header 的干净 client,用于外部 CDN
	profileHeaders fhttp.Header          // chrome profile 头 (UA / sec-ch-ua / accept-encoding)
	bearerToken    string
	cookieStr      string
	userAgent      string
	deviceID       string
	buildHash      string
	buildNumber    string
	language       string
	sessionID      string
	imageDir       string
	startTime      time.Time

	conversationID  string
	parentMessageID string
	model           string
	tempMode        bool
	turnCount       int

	// Logf 日志输出函数,设为 nil 可禁用日志。默认 log.Printf。
	Logf LogFunc

	// DisableAutoImage 设为 true 时,Chat/ChatStream 不会自动阻塞等待图片下载。
	// 适合 DLL / 外部调用场景,由调用方自己异步处理图片下载。
	DisableAutoImage bool

	// StreamRecorder 非空时记录全部 SSE 事件(供 stream-capture 分析)。
	StreamRecorder *StreamRecorder
}

// NewClient 创建新的 ChatGPT 客户端。
// 默认使用 chrome V148 / MacOS 指纹 profile。
func NewClient(cfg Config) *Client {
	hc, err := chrome.NewClient(chrome.V148, chrome.MacOS)
	if err != nil {
		// 指纹 client 创建失败时回退到一个空 client,日志告警。
		log.Printf("[web2api] chrome NewClient failed: %v", err)
	}
	clean, _ := chrome.NewClient(chrome.V148, chrome.MacOS)

	// profile header 表(在每次请求前会"追加"到 req.Header,作为基础)
	ph := buildProfileHeaders()

	c := &Client{
		httpClient:      hc,
		cleanClient:     clean,
		profileHeaders:  ph,
		bearerToken:     cfg.BearerToken,
		cookieStr:       cfg.CookieString,
		userAgent:       orDefault(cfg.UserAgent, defaultUA),
		deviceID:        orDefault(cfg.DeviceID, GenerateUUID()),
		buildHash:       orDefault(cfg.BuildHash, defaultBuildHash),
		buildNumber:     orDefault(cfg.BuildNumber, defaultBuildNumber),
		language:        orDefault(cfg.Language, defaultLang),
		imageDir:        orDefault(cfg.ImageDir, "images"),
		model:           orDefault(cfg.Model, defaultModel),
		parentMessageID: "client-created-root",
		sessionID:       GenerateUUID(),
		startTime:       time.Now(),
		tempMode:        cfg.TempMode,
		Logf:            log.Printf,
	}

	// 注入 profile header 到 internal/files 包 (它没有 *Client 引用)。
	files.SetDefaultProfileHeader(ph)

	return c
}

// buildProfileHeaders 返回与 chrome V148 / MacOS 匹配的 header 表。
// 必须在每次请求前应用,确保 User-Agent / sec-ch-ua / accept-encoding 与
// TLS 指纹一致(否则 Cloudflare 拦截返回 403)。
func buildProfileHeaders() fhttp.Header {
	return fhttp.Header{
		"User-Agent":      {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"},
		"sec-ch-ua":       {`"Chromium";v="148", "Google Chrome";v="148", "Not/A)Brand";v="99"`},
		"sec-ch-ua-mobile": {"?0"},
		"sec-ch-ua-platform": {`"macOS"`},
		"accept":          {"application/json, text/plain, */*"},
		"accept-encoding": {"gzip, deflate, br, zstd"},
		"accept-language": {"en-US,en;q=0.9"},
		"sec-fetch-dest":  {"empty"},
		"sec-fetch-mode":  {"cors"},
		"sec-fetch-site":  {"same-origin"},
	}
}

// NewClientWithHTTP 用指定的 HTTPClient 创建客户端（高级用法,允许注入自定义 client）。
func NewClientWithHTTP(cfg Config, hc httpclient.HTTPClient) *Client {
	c := NewClient(cfg)
	if hc != nil {
		c.httpClient = hc
	}
	return c
}

// HTTPClient 返回底层 httpclient.HTTPClient 以便高级自定义。
func (c *Client) HTTPClient() httpclient.HTTPClient {
	return c.httpClient
}

// ResetSession 重置对话上下文（开始新对话）
func (c *Client) ResetSession() {
	c.conversationID = ""
	c.parentMessageID = "client-created-root"
	c.turnCount = 0
}

// SetModel 切换模型
func (c *Client) SetModel(model string) { c.model = model }

// GetModel 获取当前模型
func (c *Client) GetModel() string { return c.model }

// SetTempMode 设置临时模式
func (c *Client) SetTempMode(enabled bool) { c.tempMode = enabled }

// SetDisableAutoImage 设置是否禁用自动图片下载（DLL 场景使用）
func (c *Client) SetDisableAutoImage(disabled bool) { c.DisableAutoImage = disabled }

// SetBearerToken 更新 Bearer Token（Session Token 刷新后调用）。
func (c *Client) SetBearerToken(token string) {
	c.bearerToken = token
	// 不在 fhttp 默认 headers 上直接覆盖,因为 fhttp.WithDefaultHeaders
	// 只在请求头缺失时填充;调用方应在每次新请求前自己合并此 header。
}

// SetConversationID 恢复到指定对话
func (c *Client) SetConversationID(id string) { c.conversationID = id }

// SetParentMessageID 设置父消息 ID（用于指定回复位置）
func (c *Client) SetParentMessageID(id string) { c.parentMessageID = id }

// GetSessionInfo 获取当前会话状态
func (c *Client) GetSessionInfo() SessionInfo {
	return SessionInfo{
		ConversationID:  c.conversationID,
		ParentMessageID: c.parentMessageID,
		Model:           c.model,
		TempMode:        c.tempMode,
		TurnCount:       c.turnCount,
	}
}

func (c *Client) logf(format string, args ...interface{}) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// commonHeaders 返回 OpenAI / Sentinel 业务 header。
// profile (UA / sec-ch-ua / accept-encoding 等) 不在这里,由调用方单独传 base 给 httpclient。
// 内部子包 (auth / files) 通过 ClientParams.ProfileHeader 或 files.DefaultProfileHeader
// 拿到 profile header,无需走 commonHeaders。
func (c *Client) commonHeaders() fhttp.Header {
	h := fhttp.Header{
		"Authorization":           {"Bearer " + c.bearerToken},
		"oai-language":            {c.language},
		"oai-device-id":           {c.deviceID},
		"oai-session-id":          {c.sessionID},
		"oai-client-version":      {c.buildHash},
		"oai-client-build-number": {c.buildNumber},
		"Origin":                  {"https://chatgpt.com"},
		"Referer":                 {"https://chatgpt.com/"},
	}
	if c.cookieStr != "" {
		h.Set("Cookie", c.cookieStr)
	}
	return h
}
