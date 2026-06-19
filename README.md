# web2api

> 用 Go 逆向实现的 ChatGPT Web 端非官方客户端库（无需 OpenAI API Key），并以 **OpenAI 兼容 API 服务器**形式运行。直接用浏览器 Bearer Token 与 ChatGPT 对话。

---

## 特性

- ✅ 完整实现 ChatGPT Web 端 Sentinel 认证流程（conduit token + SHA3-512 PoW + sentinel token）
- ✅ WebSocket 流式输出（实时回调增量文本，低延迟）
- ✅ DALL-E 图片生成（自动识别生图请求、实时下载到本地）
- ✅ 多模态输入（上传图片到对话）
- ✅ 文件上传（PDF、文档等）
- ✅ 临时模式（不保存对话历史）
- ✅ 浏览器指纹伪装（**Chrome 148 + Edge UA**，`re-tlsclient` 真实指纹，对齐 wreq 参考实现）
- ✅ OpenAI 兼容 API 服务器（`/v1/chat/completions` + 多 Token 池轮换 + 401 自动重试）
- ✅ free 账号模式（`oai-device-id` 替代 Authorization）
- ✅ Team 账号模式（`Chatgpt-Account-Id` header + `_puid` cookie）

---

## 快速开始

### 1. 启动服务器

```bash
# 默认配置（端口 5005,模型 gpt-5-5-thinking）
go run .

# 或显式 build
go build -o web2api.exe .
./web2api.exe
```

服务启动后会监听 `http://0.0.0.0:5005`，对外暴露 OpenAI 兼容接口：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/chat/completions` | OpenAI 兼容聊天接口 |
| GET | `/v1/models` | 列出可用模型 |
| POST | `/chat/completions` | 同 `/v1/chat/completions` |
| GET | `/models` | 同 `/v1/models` |
| GET | `/health` | 健康检查（含 token 池状态） |
| GET/POST | `/tokens/*` | Token 池管理 |

### 2. Token 管理

服务器支持**两种模式**：

#### 模式 A：直接 Token 模式（最简）
不设 `AUTHORIZATION` 环境变量，调用方请求头里的 `Authorization: Bearer <token>` 直接当 ChatGPT token 用。

```bash
curl -X POST http://localhost:5005/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <你的 chatgpt.com accessToken>" \
  -d '{"model":"gpt-5-5-thinking","messages":[{"role":"user","content":"hi"}]}'
```

#### 模式 B：Token 池模式（推荐用于多账号）
设 `AUTHORIZATION` 环境变量作 API Key，token 走 `tokens.json` 池自动轮换。

```bash
AUTHORIZATION=my-secret-key \
TOKENS_FILE=tokens.json \
go run .
```

通过管理接口导入 token：

| 接口 | 说明 |
|------|------|
| `GET  /tokens` | 查看 Token 池状态 |
| `POST /tokens/upload` | 批量上传 Token（JSON body: `{"tokens":"eyJ..."}`） |
| `GET  /tokens/add/:token` | 添加单个 Token |
| `POST /tokens/clear` | 清空 Token 池 |
| `GET  /tokens/errors` | 查看失效 Token 列表 |

**`tokens.json` 格式**：

```json
{
  "version": 1,
  "tokens": [
    {
      "id": "a1b2c3",
      "access_token": "eyJhbGciOiJS...",
      "session_token": "eyJhbGciOiJkaXIi...",
      "expires_at": "2026-08-29T10:19:02Z",
      "updated_at": "2026-05-31T12:00:00Z"
    }
  ]
}
```

**Token 格式**（每行或整段）：
- 仅 Access JWT: `eyJhbGc...`
- Access + Session: `eyJhbGc...----eyJhbGc...`（四个 `-` 分隔）
- 仅 Session: `eyJhbGc...` 或 `st:...`
- 整段 Session JSON: 直接粘贴 `api/auth/session` 返回

**`TOKEN_REFRESH_AHEAD_SEC`**（默认 300）控制 AT 过期前多少秒自动用 ST 换新。

### 3. 服务器环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `5005` | 监听端口 |
| `AUTHORIZATION` | `""` | API Key；留空则用 direct token 模式 |
| `DEFAULT_MODEL` | `gpt-5-5-thinking` | 默认模型 |
| `TEMP_MODE` | `false` | 临时模式（不保存历史） |
| `IMAGE_DIR` | `images` | 图片保存目录 |
| `TOKENS_FILE` | `tokens.json` | Token 持久化路径 |
| `SESSION_TTL_MINUTES` | `120` | Session 不活跃超时 |
| `BASE_URL` | `""` | 对外地址（生成绝对资源 URL 用） |
| `TOKEN_REFRESH_AHEAD_SEC` | `300` | AT 提前刷新秒数 |

### 4. Docker 部署

```bash
docker build -t web2api .
docker run -d \
  -p 5005:5005 \
  -e AUTHORIZATION="your-api-key" \
  -v $(pwd)/tokens.json:/app/tokens.json \
  -v $(pwd)/images:/app/images \
  web2api
```

或 `docker compose up -d`（编辑 `docker-compose.yml` 环境变量）。

---

## 认证流程

每次发送消息前自动完成以下步骤：

```
1. POST /backend-api/f/conversation/prepare
       → 获取 conduit_token (header: x-conduit-token: no-token)

2. POST /backend-api/sentinel/chat-requirements/prepare
       → 获取 PoW 挑战 (seed + difficulty)
       Header: x-openai-target-path/route

3. 本地 SHA3-512 暴力求解 PoW
       → RequirementsToken (前缀 gAAAAAC) + ProofToken (前缀 gAAAAAB)

4. POST /backend-api/sentinel/chat-requirements/finalize
       → 获取 sentinel_token
       Body: {prepare_token, proofofwork}

5. GET  /backend-api/celsius/ws/user
       → 获取 WebSocket URL

6. POST /backend-api/f/conversation (SSE)
       Header: Accept: text/event-stream
              openai-sentinel-chat-requirements-token
              x-conduit-token
              x-oai-turn-trace-id
              x-openai-target-path/route
       Body: 完整对话 payload
       → 初始 SSE 流,获取 stream_handoff / turn_exchange_id

7. WebSocket 订阅 conversation-turn-{id}
       Init: connect + subscribe(calpico-chatgpt, conversations, app_notifications)
       Per-turn: subscribe(conversation-turn-<id>, offset=0)
       → 接收流式文本 delta（文字 / 生图思考 / 图片 asset_pointer）
```

---

## 浏览器指纹

默认使用 **Chrome 148 / Windows TLS 指纹**（`xiaozhou26/re-tlsclient/chrome`），HTTP 头写 **Edge UA + 完整 sec-ch-ua-***（OpenAI 接受这种 Chromium-based 混搭组合）。

**指纹不是 Chrome 单一真实**——是 `Chrome TLS ClientHello + Edge UA` 的混合。这与 `req/v3 ImpersonateChrome()` 行为一致，OpenAI 不会因为 UA 写 Edge 而拦截。

完整 profile header：

```
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) ... Chrome/147.0.0.0 ... Edg/147.0.0.0
sec-ch-ua: "Not)A;Brand";v="8", "Chromium";v="148", "Microsoft Edge";v="148"
sec-ch-ua-arch: "x86"
sec-ch-ua-bitness: "64"
sec-ch-ua-full-version: "148.0.2959.54"
sec-ch-ua-full-version-list: "Not)A;Brand";v="8.0.0.0", "Chromium";v="148.0.2959.54", "Microsoft Edge";v="148.0.2959.54"
sec-ch-ua-mobile: ?0
sec-ch-ua-model: ""
sec-ch-ua-platform: "Windows"
sec-ch-ua-platform-version: "19.0.0"
accept: */*
accept-encoding: gzip, deflate, br, zstd
accept-language: zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6
sec-fetch-dest: empty
sec-fetch-mode: cors
sec-fetch-site: same-origin
priority: u=1, i
```

**关键约束**：

- TLS ClientHello 与 HTTP header 的 `User-Agent` 都是 Chromium-based（Chrome/Edge 都算）即可
- Cloudflare / OpenAI 反爬不要求 UA 和 TLS 严格一致
- 不要设 `accept-encoding` 为空（Cloudflare 拒）

---

## 账号模式

### 普通账号（默认）
- `Authorization: Bearer <accessToken>` 头随每个请求发出
- Token 是 chatgpt.com 的 access JWT（不是 platform.openai.com 的 OAuth 客户端 token）

### free 账号
- Token JWT `aud` 字段以 `["https://api.openai.com/v1"]` 开头的，**是 platform.openai.com 的 OAuth 客户端 token，不是 chatgpt.com 的 access token**
- 必须在 `Config.IsFree = true` 下，token 才放到 `oai-device-id` 头

### Team 账号
- 设 `Config.TeamAccountID` → 自动加 `Chatgpt-Account-Id: <id>` 头
- 设 `Config.PUID` → 自动加 `_puid=<PUID>;` cookie

---

## API 兼容性说明

| 项目 | 说明 |
|------|------|
| 多轮对话 | 服务端 Session 维护，响应返回 `conversation_id`（扩展字段），下次携带即可保持上下文 |
| `usage` 字段 | 全部为 0（逆向无法统计 token 用量） |
| `temperature` / `max_tokens` | 接收但不生效 |
| 图片生成 | 发送生图请求自动触发 DALL-E，图片以 Markdown `![](url)` 追加在回复末尾 |

---

## 架构

```
web2api/
├── main.go                          # 入口
├── config.go                        # 环境变量 → ServerConfig
├── client.go                        # Client 结构体 + 指纹 client 初始化
├── types.go                         # Config / ChatResult / ChatOptions
├── utils.go                         # UUID / truncate / previewToken
├── chat.go                          # Chat/ChatStream + WebSocket 状态机 (1517 行)
├── facade_*.go                      # *Client 方法薄壳 wrapper
│   ├── facade_pow.go                # → internal/pow
│   ├── facade_auth.go               # → internal/auth
│   ├── facade_stream_artifacts.go   # → internal/{stream,artifacts}
│   ├── facade_files.go              # → internal/{files,artifacts} + emit* / Finalize* / slot*
│   └── facade_http.go               # c.doJSON / c.doStream / c.mergedHeaders
├── handler_*.go                     # HTTP handler (server/)
├── middleware.go                     # Auth / CORS
├── openai_types.go                  # OpenAI 兼容类型
├── router.go                        # gin router
├── session.go                       # Session 池
├── token_*.go                       # Token 池 / 刷新
└── internal/                        # 不导出子包
    ├── httpclient/                  # HTTP 抽象 (DoJSON/DoRaw/NewRequest)
    │                                 # 自动解 gzip/deflate/br/zstd
    ├── pow/                         # SHA3-512 PoW 求解
    ├── auth/                        # GetConduitToken / GetSentinelToken
    ├── stream/                      # StreamEvent / StreamRecorder
    ├── artifacts/                   # ArtifactSignal / SandboxArtifact / Plan
    └── files/                       # UploadFile / ProxyImage / ProxyPDF
```

### 关键设计

1. **Client 留根包** — 所有 `*Client` 方法在 facade_*.go，外部调用方零修改
2. **类型用 type alias** — `StreamEvent = stream.Event`、`ArtifactSignal = artifacts.Signal`
3. **依赖方向** — `internal/*` 只 import 标准库和第三方包；子包间不互相依赖
4. **HTTP 抽象** (`internal/httpclient/`) — `HTTPClient = tls_client.HttpClient`
5. **fingerprint + business header 分离** — `c.fullProfileHeaders()` 是 TLS profile，`c.commonHeaders()` 是业务头（Authorization/Origin/oai-*），两个独立 base + Set 合并

---

## 依赖

| 依赖 | 说明 |
|------|------|
| [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) | TLS ClientHello 伪装 |
| [xiaozhou26/re-tlsclient](https://github.com/xiaozhou26/re-tlsclient) | 6 浏览器指纹 profile（chrome/edge/firefox/opera/safari/okhttp） |
| [bogdanfinn/fhttp](https://github.com/bogdanfinn/fhttp) | fhttp 风格的 HTTP client |
| [gorilla/websocket](https://github.com/gorilla/websocket) | WebSocket 客户端 |
| [gin-gonic/gin](https://github.com/gin-gonic/gin) | API 服务器 HTTP 框架 |
| [golang.org/x/crypto/sha3](https://pkg.go.dev/golang.org/x/crypto/sha3) | SHA3-512（PoW 求解） |
| [andybalholm/brotli](https://github.com/andybalholm/brotli) | brotli 解压 |
| [klauspost/compress](https://github.com/klauspost/compress) | zstd 解压 |

---

## 注意事项

- 本项目仅供学习与研究使用，请勿用于商业或违反 OpenAI 服务条款的场景
- Bearer Token 是个人凭证，请勿泄露，**不要将 `config.json` / `tokens.json` 提交到公开仓库**
- `tokens.json` 已在 `.gitignore`

---

## License

MIT
