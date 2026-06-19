# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目是什么

`module web2api` — 用 Go 逆向实现的 ChatGPT Web 端非官方客户端（无需 OpenAI API Key），并以 **OpenAI 兼容 API 服务器**形式运行。直接用浏览器 Bearer Token 与 ChatGPT 对话。

完整使用说明 / API 文档 / 认证流程 / Token 管理见 `README.md`。本文档只补充未来 Claude 实例需要知道的**架构与开发上下文**。

## 常用命令

```bash
# 构建
go build -o web2api.exe .

# 静态检查
go vet ./...

# 测试
go test ./...

# 跑服务器
go run .

# 清理未使用依赖
go mod tidy
```

Go 版本：`go 1.26.4`（`go.mod` 第一行）。

## 架构：单 main 包 + internal/*

```
web2api/
├── main.go                          # 入口 (func main)
├── client.go                        # Client 结构体 + 指纹 client 初始化
├── types.go                         # Config / ChatResult / ChatOptions / SessionInfo
├── utils.go                         # GenerateUUID / truncateStr / previewToken / decodeJWTInfo
├── chat.go                          # ★ 1517 行 Chat/ChatStream + WebSocket 状态机 (单文件, 改动前先扫目录)
├── facade_*.go                      # 根包对外 *Client 方法薄壳 wrapper
│   ├── facade_pow.go                # → internal/pow
│   ├── facade_auth.go               # → internal/auth (含 401 诊断日志)
│   ├── facade_stream_artifacts.go   # → internal/{stream,artifacts}
│   ├── facade_files.go              # → internal/{files,artifacts} + emit* / Finalize* / slot*
│   └── facade_http.go               # c.doJSON / c.doStream / c.mergedHeaders
├── config.go                        # ServerConfig 从 env 读取
├── handler_*.go                     # HTTP handler (gin)
├── middleware.go                     # Auth / CORS
├── openai_types.go                  # OpenAI 兼容类型
├── router.go                        # gin router
├── session.go                       # Session 池
├── token_*.go                       # Token 池 / 刷新
└── internal/                        # 不导出子包
    ├── httpclient/                  # HTTP 抽象 (HTTPClient / DoJSON/DoRaw/NewRequest/decodeBody)
    ├── pow/                         # SHA3-512 PoW 求解 (RequirementsToken / SolveProofToken)
    ├── auth/                        # GetConduitToken / GetSentinelToken (三步认证)
    ├── stream/                      # StreamEvent / StreamRecorder
    ├── artifacts/                   # ArtifactSignal / SandboxArtifact / Plan
    └── files/                       # UploadFile / ProxyImage / ProxyPDF
```

### 关键设计

1. **单 `package main`** — 所有 `.go` 在根目录 `package main`，不作为库导出。**只能跑 server**。

2. **`Client` 留根包** — `*Client` 方法都在 `facade_*.go`，调用方零修改。

3. **类型用 type alias 暴露** — `ArtifactSignal = artifacts.Signal`、`StreamEvent = stream.Event` 等。

4. **依赖方向**：
   ```
   root (main) ──→ internal/httpclient (HTTP 抽象)
                ──→ internal/{pow, auth, stream, artifacts, files} (各子包不互相依赖)
   ```
   `internal/*` 只 import 标准库 + 第三方包，绝不互相 import。

5. **HTTP 客户端抽象** (`internal/httpclient/`)：
   - `HTTPClient = tls_client.HttpClient` (type alias, fhttp 风格)
   - 默认 `chrome.NewClient(chrome.V148, chrome.Windows)` (re-tlsclient)
   - `c.doJSON(method, path, headers, body)` / `c.doStream(...)` 是 chat.go 调用风格
   - `DoRaw(...)` 用于 SSE 长连接

### 关键的 c.commonHeaders / c.fullProfileHeaders 拆分

**`c.fullProfileHeaders()`** = TLS 指纹头（`User-Agent` / `sec-ch-ua-*` / `accept-encoding` / `priority` 等），**作 base header 传给 httpclient**

**`c.commonHeaders()`** = 业务头（`Authorization` / `oai-*` / `Origin` / `Referer` / `Cookie`），**Set 覆盖 base**

**绝对不能漏** `commonHeaders` —— 否则 Authorization 缺失 → 401。这是 2026/06/19 修复的根因。

## 改 chat.go 的代价

`chat.go` 是单文件 1517 行的对话主流程（Chat / ChatStream / WebSocket 状态机 / 生图多版本追踪 / 思考步骤提取）。改它前先看 `facade_files.go`（含 noteGeneratedImageRevision / FinalizeImageGenSlots / emit* 等 Client 方法）和 `facade_stream_artifacts.go`（信号/产物 API）。

如果 chat.go 报错 `undefined: c.X`，先 grep `facade_*.go` 找该方法。

## 重要不变量

- `ChatResult.imageSlots` / `emittedArtifacts` / `lastImageGenActivityAt` 等小写字段是**生图多版本追踪状态**，不要移到根包外。
- `StreamEvent.Event` (不是 `Name`) 是事件类型字段 (json tag `"event"`)。
- `ArtifactStreamConfig.imageRevisionMode()` 在 `stream.Config` 上叫 `RevisionMode()`，根包 facade 不再有同名方法。
- `c.commonHeaders()` 必须**独立**于 `c.fullProfileHeaders()`，否则 Authorization 会被 Set 覆盖错位丢失。

## 浏览器指纹

- TLS 指纹：`xiaozhou26/re-tlsclient/chrome.NewClient(chrome.V148, chrome.Windows)` (ClientHello + h2 SETTINGS 来自 wreq 参考)
- HTTP header UA 写 **Edge** + 完整 `sec-ch-ua-*`（`buildProfileHeaders()`）
- OpenAI 接受 Chrome TLS + Edge UA 的混搭（与 `req/v3 ImpersonateChrome()` 行为一致）
- Cloudflare 反爬要求 TLS ClientHello 必须是 Chromium-based（Chrome/Edge 都算）

## 账号模式

| 模式 | 触发条件 | 行为 |
|------|----------|------|
| 普通 | `Config.IsFree == false` | `Authorization: Bearer <token>` |
| free | `Config.IsFree == true` | token 放到 `oai-device-id`（无 Authorization） |
| Team | `Config.TeamAccountID != ""` | 加 `Chatgpt-Account-Id: <id>` 头 |
| Team cookie | `Config.PUID != ""` | 加 `_puid=<PUID>;` cookie |
| Extra | `Config.ExtraHeaders` map | 注入 `oai-hlib` / `oai-sc` / `oai-gn` 等 |

`Config` 字段：
```go
type Config struct {
    BearerToken   string
    CookieString  string
    IsFree        bool
    PUID          string
    TeamAccountID string
    ExtraHeaders  map[string]string
    Model         string
    DeviceID      string
    BuildHash     string
    BuildNumber   string
    UserAgent     string
    Language      string
    ImageDir      string
    TempMode      bool
}
```

## Token 类型判定 (重要)

JWT 解码后看 `aud` 字段：

| `aud` 值 | 用途 |
|---------|------|
| `["https://api.openai.com/v1"]` 或 `["https://api.openai.com/v1", "..."]` | **platform.openai.com 开发者 API** token（不是 chatgpt.com） |
| `"https://api.openai.com"` 单值无 `/v1` | **chatgpt.com 浏览器** access token（正确） |

**platform.openai.com token 用于 chatgpt.com 一定 401**。拿正确 token 的方法：浏览器登录 chatgpt.com → DevTools → Network → 任一 chatgpt 请求 → 复制 `Authorization: Bearer ...` 头。

## 401 诊断 (自动)

`facade_auth.go` 的 `c.getConduitToken` 在收到 401 时自动打印：
- JWT payload（含 `iss` / `aud` / `scp` / `exp`）
- token 剩余有效时间
- 当前请求发送的关键 header（`User-Agent` / `sec-ch-ua` / `Authorization` / `Cookie` 等）

**如果日志说 "缺 Authorization header"** → `internal/{auth,files}` 没合并 `c.commonHeaders()` —— 立即检查 `facade_auth.go` 的 `CommonHeader:` 字段是否填了 `c.commonHeaders()`。

## 注意事项

- `config.json` / `tokens.json` / `images/` 已在 `.gitignore`，**不要提交真实 Bearer Token**。
- `req/v3` 已完全移除（go.mod 干净），`go mod why` 应该没有 imroc/req 引用。
- `re-tlsclient` v1.0.9 是 README 描述的 6 浏览器子包版本；v1.0.8 结构完全不同（只有 `profile/` 和 `transport/`），升级时要看清。
- 测试文件只有 `artifact_test.go` 和 `server/token_refresh_test.go`（server 测试已合并到根包）。

## 编码陷阱

`internal/httpclient/client.go` 的 `decodeBody` **必须先嗅探首字节**判断是否已明文（fhttp transport 层会自动解压一次），再按 `Content-Encoding` 头尝试。**任何解压错误都回退到原始字节**——否则重复解压触发 `brotli: RESERVED` 错误。
