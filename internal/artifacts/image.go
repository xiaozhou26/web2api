package artifacts

import (
	"strings"
	"time"
)

// 生图版本推送策略（请求 artifact_image_revisions）。
const (
	ImageRevisionAll           = "all"             // 每个 file_id 各推一次（含中间稿）
	ImageRevisionLatestPerSlot = "latest_per_slot" // 按槽位只推最新，旧图发 superseded
	ImageRevisionFinalOnly     = "final_only"      // 槽位 idle 结束后推最终版
)

// GeneratedImageSlot 一个「图位」（图1/图2…）及其修订历史。
type GeneratedImageSlot struct {
	SlotIndex   int
	GenID       string
	MessageID   string
	FileID      string
	Revision    int
	FileHistory []string
	Final       bool
}

// ParsedGeneratedImage 从 WS message / part 解析出的生图条目。
type ParsedGeneratedImage struct {
	FileID      string
	MessageID   string
	GenID       string
	ParentGenID string
	EditOp      string
	Width       int
	Height      int
}

func ParseGeneratedImagesFromMessage(msg map[string]interface{}) []ParsedGeneratedImage {
	msgID, _ := msg["id"].(string)
	content, _ := msg["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})
	var out []ParsedGeneratedImage
	for _, part := range parts {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			continue
		}
		if partMap["content_type"] != "image_asset_pointer" {
			continue
		}
		ap, _ := partMap["asset_pointer"].(string)
		fileID := ExtractFileID(ap)
		if fileID == "" {
			continue
		}
		p := ParsedGeneratedImage{FileID: fileID, MessageID: msgID}
		if w, ok := partMap["width"].(float64); ok {
			p.Width = int(w)
		}
		if h, ok := partMap["height"].(float64); ok {
			p.Height = int(h)
		}
		if meta, ok := partMap["metadata"].(map[string]interface{}); ok {
			if dalle, ok := meta["dalle"].(map[string]interface{}); ok {
				p.GenID, _ = dalle["gen_id"].(string)
				if pg, ok := dalle["parent_gen_id"].(string); ok {
					p.ParentGenID = pg
				}
				p.EditOp, _ = dalle["edit_op"].(string)
			}
		}
		out = append(out, p)
	}
	return out
}

// SummarizeConvUpdatePayload 简化 conv-update payload 为可读字符串。
func SummarizeConvUpdatePayload(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	updateType, _ := payload["update_type"].(string)
	uc, _ := payload["update_content"].(map[string]interface{})
	if uc == nil {
		return "type=" + updateType
	}
	parts := []string{"type=" + updateType}
	if tid, ok := uc["async_task_id"].(string); ok && tid != "" {
		if len(tid) > 12 {
			tid = tid[:12] + "…"
		}
		parts = append(parts, "task="+tid)
	}
	if _, ok := uc["message"]; ok {
		parts = append(parts, "msg=1")
	}
	if msgs, ok := uc["messages"].([]interface{}); ok {
		parts = append(parts, "msgs="+itoa(len(msgs)))
	}
	if st, ok := uc["conversation_async_status"].(float64); ok {
		parts = append(parts, "async_status="+itoa(int(st)))
	}
	return strings.Join(parts, " ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// IsImageAsyncWSUpdate 判断是否为生图相关 WS update。
func IsImageAsyncWSUpdate(updateType string) bool {
	switch updateType {
	case "async_task", "image_progression", "finalize", "image_remixed",
		"image_edited", "image_redo", "image_remove_bg", "image_uncrop",
		"image_redo_text", "image_expand", "image_zoom":
		return true
	}
	return false
}

// IdleDuration 无新活动后等待多久再结束 WS（网页端修图/多轮 async 需更久）。
// Slots/Async fields 由调用方传入,避免依赖 ChatResult 具体定义。
type ImageIdleStats struct {
	ImageSlots             int
	AllSlotsFinal          bool
	AsyncTaskPending       int
	AsyncTaskActive        bool
	LastActivityNanos      int64
	HasDalleGenerated      bool
	AsyncCompleteSeen      bool
	ConvAsyncStatusDone    bool
}

func ImageGenIdleDuration(s ImageIdleStats) time.Duration {
	if s.AsyncTaskPending > 0 || s.AsyncTaskActive {
		return 25 * time.Second
	}
	if s.ImageSlots >= 2 && !s.AllSlotsFinal {
		return 30 * time.Second
	}
	return 15 * time.Second
}
