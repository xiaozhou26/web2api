# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目是什么

`module web2api` — 用 Go 逆向实现 ChatGPT Web 端非官方客户端库（无需 OpenAI API Key，直接用浏览器 Bearer Token）。可作为 **CLI 工具** 或 **本地 OpenAI 兼容 API 服务器** 运行。

完整使用说明 / API 文档 / 认证流程 / Token 管理见 `README.md`。本文档只补充未来 Claude 实例需要知道的**架构与开发上下文**。

## 常用命令

```bash
# 构建
go build ./...

# 静态检查
go vet ./...

# 测试（根包 + server 包有测试，internal/* 子包没有）
go test ./...
go test -run TestBuildArtifactPlan ./...     # 跑单个测试

# CLI（交互式 REPL）
go run ./cmd/chat/ -config config.json

# API 服务器（默认 :5005, OpenAI 兼容 /v1/chat/completions）
go run ./cmd/server/

# 流式抓包（写到 testdata/stream-captures/）
go run ./cmd/stream-capture/ -config config.json -case image

# 清理未使用依赖（req/v3 切换后偶尔要跑一次）
go mod tidy
```

Go 版本：`go 1.26.4`（`go.mod` 第一行）。

## 架构：根包 + internal/* + server/ + cmd/

```
web2api/
├── client.go              # Client 结构体 + httpclient 初始化 + commonHeaders
├── types.go               # Config / ChatResult / ChatOptions / SessionInfo / StreamHandler / LogFunc
├── utils.go               # GenerateUUID / truncateStr / getNestedString / LogContentPreview
├── chat.go                # ★ 1517 行 Chat/ChatStream + WebSocket 状态机 (单文件, 改动前先扫目录)
├── facade_*.go            # 根包对外 API 的薄壳 wrapper,把 *Client 字段转给 internal/*
│   ├── facade_pow.go
│   ├── facade_auth.go
│   ├── facade_stream_artifacts.go
│   ├── facade_files.go    # 最大 (≈800 行),吞下了原 image_revision 的 *Client 方法
│   └── facade_http.go     # c.doJSON / c.doStream / c.mergedHeaders 通用 helper
├── artifact_test.go       # ★ 唯一根包测试,验证 IsGeneratedImageTurn / BuildArtifactPlan 等
├── cmd/
│   ├── chat/main.go       # REPL CLI,import sentinel "web2api"
│   ├── server/main.go
│   └── stream-capture/main.go
├── server/                # HTTP API 层 (gin),独立 package server,依赖根包 sentinel
└── internal/
    ├── httpclient/        # 抽象层: HTTPClient 接口 = tls_client.HttpClient
    │                      #   助手: DoJSON / DoRaw / NewRequest / decodeBody
    ├── pow/               # SHA3-512 PoW 求解 (RequirementsToken / SolveProofToken)
    ├── auth/              # GetConduitToken / GetSentinelToken 三步认证
    ├── stream/            # StreamEvent / ArtifactStreamConfig / StreamRecorder
    ├── artifacts/         # ArtifactSignal / SandboxArtifact / 信号解析 / 生图槽位
    └── files/             # UploadFile (三步上传) / ProxyImage / ProxyPDF
```

### 关键设计

1. **`Client` 留根包** — 所有 `*Client` 方法都在根包 (facade_*.go),`cmd/` 和 `server/` 调用方零修改。

2. **类型用 type alias 暴露** — `ArtifactSignal = artifacts.Signal` / `StreamEvent = stream.Event` / `ArtifactStreamConfig = stream.Config` 等。改内部包时这些名字不变。

3. **依赖方向**：
   ```
   cmd/* ─┐
          ├─→ sentinel (根包 facade) ─→ internal/*
   server ┘                              ↑
                                         internal/* 之间不互相依赖
   ```
   `internal/*` 只 import 标准库 + 第三方包,绝不互相 import。

4. **HTTP 客户端抽象** (`internal/httpclient/`)：
   - `HTTPClient = tls_client.HttpClient` (type alias, fhttp 风格)
   - 默认 `NewClient()` 用 `re-tlsclient/chrome V148 / MacOS` 指纹 (来自 wreq 参考实现)
   - `c.doJSON(method, path, headers, body) → (status, body, ct, err)` 是 chat.go 调用风格
   - `c.doStream(...)` 用于 SSE 长连接 (返回 fhttp.Response, body 留给调用方流式读)

5. **解码陷阱**: fhttp 的 `Transport` 已经在 transport 层做了透明解压。`decodeBody` 必须**先嗅探首字节**,再按 `Content-Encoding` 头尝试,任何解压错误都**回退到原始字节**。否则重复解压会触发 `brotli: RESERVED` 等错误,导致 `getConduitToken` 等认证请求失败。详见 `internal/httpclient/client.go` 的 `decodeBody` / `isPlainText`。

### 改 chat.go 的代价

`chat.go` 是单文件 1517 行的对话主流程 (Chat / ChatStream / WebSocket 状态机 / 生图多版本追踪 / 思考步骤提取)。改它前先看 `facade_files.go` (里面吸收了原 image_revision 的 `noteGeneratedImageRevision` / `FinalizeImageGenSlots` / `emit*` 等 Client 方法) 和 `facade_stream_artifacts.go` (信号/产物 API)。

如果 chat.go 报错"undefined: c.X",先 grep `facade_*.go` 找该方法 — 大概率在 facade 里。

### 重要不变量

- `ChatResult.imageSlots` / `emittedArtifacts` / `lastImageGenActivityAt` 等小写字段是**生图多版本追踪状态**;不要把它们移到根包外。
- `cfg.Normalized()` 不是 `cfg.normalized()` (已大写,跟 `stream.Config` 对齐)。
- `StreamEvent.Event` (不是 `Name`) 是事件类型字段 (json tag `"event"`)。
- `ArtifactStreamConfig.imageRevisionMode()` 在 `stream.Config` 上叫 `RevisionMode()`,根包 facade 不再有同名方法。

## 注意事项

- `config.json` / `tokens.json` / `images/` 已在 `.gitignore`,**不要提交真实 Bearer Token**。
- `req/v3` 已完全移除 (`go mod tidy` 后 `go.mod` 干净),如果 PR 误把 `imroc/req` 加回来 `go build` 不会报错 (因为 internal 包可能不再用),但 `go mod why` 会显示。
- `re-tlsclient` v1.0.9 是 README 描述的 6 个浏览器子包版本,v1.0.8 结构完全不同 (只有 `profile/` 和 `transport/`),升级时要看清。
- 测试文件只有 `artifact_test.go` 和 `server/token_refresh_test.go`,改 internal/* 时不会自动回归。
