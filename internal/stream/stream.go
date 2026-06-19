// Package stream 负责 SSE 流式侧信道：事件类型常量、StreamRecorder。
//
// 不直接引用根包类型，对外通过 sentinel 包以 type alias / wrapper 暴露。
package stream

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"web2api/internal/artifacts"
)

// ArtifactDelivery 产物下发方式（请求 artifact_delivery）。
const (
	ArtifactDeliveryURL           = "url"
	ArtifactDeliveryBase64        = "base64"
	ArtifactDeliveryBase64Chunked = "base64_chunked"
)

// Stream 事件类型（写入 SSE chunk 的 sentinel 字段）。
const (
	EventArtifactPending    = "artifact_pending"
	EventArtifact           = "artifact"
	EventArtifactChunk      = "artifact_chunk"
	EventArtifactDone       = "artifact_done"
	EventArtifactSuperseded = "artifact_superseded" // 同槽位被新版本替换
	EventArtifactSlotFinal  = "artifact_slot_final" // 该图位已定稿（多图 idle 结束）
)

// DefaultChunkSize base64_chunked 分块大小（原始字节）。
const DefaultChunkSize = 384 * 1024

// ImageRevisionLatestPerSlot 生图默认修订模式。
const ImageRevisionLatestPerSlot = "latest_per_slot"

// RevisionMode 返回 ImageRevisions,空时返回默认值。
func (cfg *Config) RevisionMode() string {
	n := cfg.Normalized()
	if n.ImageRevisions == "" {
		return ImageRevisionLatestPerSlot
	}
	return n.ImageRevisions
}

// Event 流式侧信道：正文走 delta.content，产物/进度走 sentinel。
type Event struct {
	Event string `json:"event"`

	// generated_image | sandbox_file
	Kind string `json:"kind,omitempty"`

	// artifact_pending
	Title string `json:"title,omitempty"`

	// 多产物序号（从 1 开始）
	Index int `json:"index,omitempty"`
	Total int `json:"total,omitempty"`

	// 生图多版本：图1/图2 槽位与槽内修订次数
	SlotIndex        int    `json:"slot_index,omitempty"`
	Revision         int    `json:"revision,omitempty"`
	GenID            string `json:"gen_id,omitempty"`
	ParentGenID      string `json:"parent_gen_id,omitempty"`
	UpdateType       string `json:"update_type,omitempty"` // async-task-update-message 等
	IsFinal          bool   `json:"is_final,omitempty"`
	SupersedesFileID string `json:"supersedes_file_id,omitempty"`

	// 产物文件名 / MIME / 体积 / 链接 / 资源 ID
	Name        string `json:"name,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	SizeBytes   int    `json:"size_bytes,omitempty"`
	URL         string `json:"url,omitempty"`
	FileID      string `json:"file_id,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
	SandboxPath string `json:"sandbox_path,omitempty"`

	// base64 或 base64_chunked
	Data       string `json:"data,omitempty"`
	ChunkIndex int    `json:"chunk_index,omitempty"`
	ChunkTotal int    `json:"chunk_total,omitempty"`

	Error string `json:"error,omitempty"`
}

// Config 产物如何流式交给客户端。
type Config struct {
	Delivery        string // url | base64 | base64_chunked
	ChunkSize       int    // base64_chunked 分块大小（原始字节），默认 384KiB
	ImageRevisions  string // all | latest_per_slot | final_only
	OnEvent         func(Event)
	BuildImageURL   func(fileID string) string
	BuildSandboxURL func(messageID, sandboxPath string) string
}

// Normalized 返回带默认值的 Config 副本。
func (cfg *Config) Normalized() Config {
	out := *cfg
	if out.Delivery == "" {
		out.Delivery = ArtifactDeliveryURL
	}
	if out.ChunkSize <= 0 {
		out.ChunkSize = DefaultChunkSize
	}
	return out
}

// Emit 触发回调（OnEvent 为 nil 时为 no-op）。
func (cfg Config) Emit(ev Event) {
	if cfg.OnEvent != nil {
		cfg.OnEvent(ev)
	}
}

// Recorder 记录 SSE / WebSocket 原始事件与解析出的产物信号（用于 stream-capture 分析）。
type Recorder struct {
	mu      sync.Mutex
	entries []Record
	file    *os.File
	wsFile  *os.File
	wsSeq   int
}

// Record 单条 SSE 记录（NDJSON 一行）。
type Record struct {
	Seq       int               `json:"seq"`
	At        string            `json:"at"`
	SSEEvent  string            `json:"sse_event,omitempty"`
	Data      string            `json:"data,omitempty"`
	EventType string            `json:"event_type,omitempty"`
	Signals   []artifacts.Signal `json:"signals,omitempty"`
}

// WSRecord 单条 WebSocket 帧记录（NDJSON 一行）。
type WSRecord struct {
	Seq       int    `json:"seq"`
	At        string `json:"at"`
	FrameType string `json:"frame_type,omitempty"`
	Len       int    `json:"len"`
	HasImage  bool   `json:"has_image_ref"`
	Snippet   string `json:"snippet,omitempty"`
}

// NewRecorder 创建记录器；outPath 非空时同步追加写入 sse.ndjson。
func NewRecorder(outPath string) (*Recorder, error) {
	r := &Recorder{}
	if outPath == "" {
		return r, nil
	}
	f, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	r.file = f
	return r, nil
}

// OpenWSLog 追加记录 WebSocket 帧到 ws.ndjson。
func (r *Recorder) OpenWSLog(wsPath string) error {
	if r == nil || wsPath == "" {
		return nil
	}
	f, err := os.Create(wsPath)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.wsFile = f
	r.mu.Unlock()
	return nil
}

// RecordSSE 记录一条 SSE data 事件；内部从 parsed 提取 event_type 与 signals。
func (r *Recorder) RecordSSE(sseEvent, data string, parsed map[string]interface{}) {
	if r == nil {
		return
	}
	var signals []artifacts.Signal
	evtType := ""
	if parsed != nil {
		signals = artifacts.ExtractFromJSON(parsed)
		evtType, _ = parsed["type"].(string)
	}
	rec := Record{
		At:        time.Now().Format(time.RFC3339Nano),
		SSEEvent:  sseEvent,
		Data:      data,
		EventType: evtType,
		Signals:   signals,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.Seq = len(r.entries) + 1
	r.entries = append(r.entries, rec)
	if r.file != nil {
		b, _ := json.Marshal(rec)
		_, _ = r.file.Write(append(b, '\n'))
	}
}

// RecordWS 记录一条 WebSocket 原始帧（截断预览）。
func (r *Recorder) RecordWS(frameType string, raw []byte) {
	if r == nil || r.wsFile == nil {
		return
	}
	s := string(raw)
	rec := WSRecord{
		At:        time.Now().Format(time.RFC3339Nano),
		FrameType: frameType,
		Len:       len(raw),
		HasImage:  strings.Contains(s, "sediment://") || strings.Contains(s, "image_asset_pointer"),
		Snippet:   truncate(s, 800),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wsSeq++
	rec.Seq = r.wsSeq
	b, _ := json.Marshal(rec)
	_, _ = r.wsFile.Write(append(b, '\n'))
}

// Entries 返回全部记录副本。
func (r *Recorder) Entries() []Record {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, len(r.entries))
	copy(out, r.entries)
	return out
}

// AllSignals 合并全部记录中的信号（用 artifacts.Merge 去重）。
func (r *Recorder) AllSignals() []artifacts.Signal {
	entries := r.Entries()
	var all []artifacts.Signal
	for _, e := range entries {
		all = artifacts.Merge(all, e.Signals)
	}
	return all
}

// Close 关闭底层文件。
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	if r.file != nil {
		err = r.file.Close()
		r.file = nil
	}
	if r.wsFile != nil {
		if e := r.wsFile.Close(); e != nil && err == nil {
			err = e
		}
		r.wsFile = nil
	}
	return err
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
