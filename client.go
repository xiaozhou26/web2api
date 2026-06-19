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
	extraHeaders   fhttp.Header          // 用户提供的反爬 header (oai-hlib / oai-sc / oai-gn)
	isFree         bool                  // free 账号:oai-device-id 替代 Authorization
	puid           string                // _puid cookie 值 (Team 账号)
	teamAccountID  string                // Chatgpt-Account-Id header 值 (Team 账号)
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
// 默认使用 chrome V148 / Windows 指纹 profile(对齐 client-go req/v3 ImpersonateChrome)。
// HTTP header 用 Edge UA — Chromium-based 浏览器 OpenAI 接受这种组合。
func NewClient(cfg Config) *Client {
	hc, err := chrome.NewClient(chrome.V148, chrome.Windows)
	if err != nil {
		log.Printf("[web2api] chrome NewClient failed: %v", err)
	}
	clean, _ := chrome.NewClient(chrome.V148, chrome.Windows)

	// profile header 表(在每次请求前会"追加"到 req.Header,作为基础)
	ph := buildProfileHeaders()

	c := &Client{
		httpClient:      hc,
		cleanClient:     clean,
		profileHeaders:  ph,
		extraHeaders:    fhttp.Header{},
		isFree:          cfg.IsFree,
		puid:            cfg.PUID,
		teamAccountID:   cfg.TeamAccountID,
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

	log.Printf("[web2api] [debug] NewClient profileHeaders key count: %d", len(c.profileHeaders))
	for k, v := range c.profileHeaders {
		log.Printf("[web2api] [debug] profileHeader[%q] = %v", k, v)
	}

	// 注入 ExtraHeaders
	for k, v := range cfg.ExtraHeaders {
		c.extraHeaders.Set(k, v)
	}

	// 注入 profile header 到 internal/files 包 (它没有 *Client 引用)。
	files.SetDefaultProfileHeader(c.fullProfileHeaders())
	files.SetDefaultCommonHeader(c.commonHeaders())

	return c
}

// buildProfileHeaders 返回与 chrome V148 / Windows + Edge UA 匹配的 header 表(对齐 client-go)。
// TLS 指纹是 Chrome (req/v3 ImpersonateChrome),UA 写 Edge — OpenAI 接受这种组合。
func buildProfileHeaders() fhttp.Header {
	return fhttp.Header{
		"User-Agent":                  {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36 Edg/147.0.0.0"},
		"sec-ch-ua":                   {`"Not)A;Brand";v="8", "Chromium";v="148", "Microsoft Edge";v="148"`},
		"sec-ch-ua-mobile":            {"?0"},
		"sec-ch-ua-platform":          {`"Windows"`},
		"sec-ch-ua-arch":              {`"x86"`},
		"sec-ch-ua-bitness":           {`"64"`},
		"sec-ch-ua-full-version":      {`"148.0.2959.54"`},
		"sec-ch-ua-full-version-list": {`"Not)A;Brand";v="8.0.0.0", "Chromium";v="148.0.2959.54", "Microsoft Edge";v="148.0.2959.54"`},
		"sec-ch-ua-model":             {`""`},
		"sec-ch-ua-platform-version":  {`"19.0.0"`},
		"accept":                      {"*/*"},
		"accept-encoding":             {"gzip, deflate, br, zstd"},
		"accept-language":             {"zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"},
		"sec-fetch-dest":              {"empty"},
		"sec-fetch-mode":              {"cors"},
		"sec-fetch-site":              {"same-origin"},
		"priority":                    {"u=1, i"},
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

// commonHeaders 返回 OpenAI / Sentinel 业务 header + 用户提供的反爬 header (ExtraHeaders)。
// profile (UA / sec-ch-ua / accept-encoding 等) 不在这里,由调用方单独传 base 给 httpclient。
// 内部子包 (auth / files) 通过 ClientParams.ProfileHeader 或 files.DefaultProfileHeader
// 拿到 profile header,无需走 commonHeaders。
func (c *Client) commonHeaders() fhttp.Header {
	h := fhttp.Header{
		"oai-language":            {c.language},
		"oai-device-id":           {c.deviceID},
		"oai-session-id":          {c.sessionID},
		"oai-client-version":      {c.buildHash},
		"oai-client-build-number": {c.buildNumber},
		"Origin":                  {"https://chatgpt.com"},
		"Referer":                 {"https://chatgpt.com/"},
	}
	// Authorization: free 账号不设 (用 oai-device-id 替代);非 free 设 Bearer token
	if !c.isFree {
		h.Set("Authorization", "Bearer "+c.bearerToken)
	} else {
		// free 账号:把 token 放到 oai-device-id
		h.Set("oai-device-id", c.bearerToken)
	}
	// Team 账号
	if c.teamAccountID != "" {
		h.Set("Chatgpt-Account-Id", c.teamAccountID)
	}
	// 注入用户提供的反爬 header (oai-hlib / oai-sc / oai-gn / priority 等)
	for k, vs := range c.extraHeaders {
		for _, v := range vs {
			h.Set(k, v)
		}
	}
	// Cookie: 用户 cookie + _puid (Team 账号)
	cookieStr := c.cookieStr
	if c.puid != "" {
		if cookieStr != "" {
			cookieStr = "_puid=" + c.puid + "; " + cookieStr
		} else {
			cookieStr = "_puid=" + c.puid + ";"
		}
	}
	if cookieStr != "" {
		h.Set("Cookie", cookieStr)
	}
	return h
}

// fullProfileHeaders 把 profile + extraHeaders 合并,作为 base 给 httpclient。
func (c *Client) fullProfileHeaders() fhttp.Header {
	h := fhttp.Header{}
	for k, vs := range c.profileHeaders {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	for k, vs := range c.extraHeaders {
		for _, v := range vs {
			h.Set(k, v)
		}
	}
	return h
}
