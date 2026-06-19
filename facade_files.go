package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"

	"web2api/internal/artifacts"
	"web2api/internal/files"
)

// ----- files 包 facade -----

// 类型别名。
type (
	UploadedFile      = files.UploadedFile
	Attachment        = files.Attachment
	AssetPointerPart  = files.AssetPointerPart
)

// UploadFile 执行三步上传。
func (c *Client) UploadFile(ctx context.Context, data []byte, fileName, mimeHint string) (*UploadedFile, error) {
	return files.UploadFile(ctx, c.httpClient, c.cleanClient, c.userAgent, data, fileName, mimeHint)
}

// isTransientNetErr 暴露给 server 层做重试判定。
func isTransientNetErr(err error) bool { return files.IsTransientNetErr(err) }

// ProxyImageByFileID 代理图片下载。
func (c *Client) ProxyImageByFileID(fileID, conversationID string, w interface{}, reqUserAgent string) error {
	writer, ok := w.(http.ResponseWriter)
	if !ok {
		return fmt.Errorf("invalid http.ResponseWriter")
	}
	return files.ProxyImageByFileID(context.Background(), c.httpClient, c.cleanClient, c.logf, fileID, conversationID, writer, reqUserAgent)
}

// PollForImageFileID 兼容旧调用方。
func (c *Client) PollForImageFileID(conversationID string) (string, error) {
	return files.PollForImageFileID(conversationID)
}

// DownloadFileByFileID 下载生图/附件二进制。
func (c *Client) DownloadFileByFileID(conversationID, fileID string) ([]byte, string, error) {
	return files.DownloadFileByFileID(context.Background(), c.httpClient, c.cleanClient, c.userAgent, conversationID, fileID)
}

// resolveFileDownloadURL 内部 helper。
func (c *Client) resolveFileDownloadURL(fileID, conversationID string) (string, error) {
	return files.ResolveFileDownloadURL(context.Background(), c.httpClient, fileID, conversationID)
}

// fetchURLBytes 内部 helper。
func (c *Client) fetchURLBytes(downloadURL, reqUserAgent string) ([]byte, string, error) {
	return files.FetchURLBytes(context.Background(), c.httpClient, c.cleanClient, downloadURL, reqUserAgent)
}

// ProxyPDFBySandboxPath 代理沙箱文件。
func (c *Client) ProxyPDFBySandboxPath(conversationID, messageID, sandboxPath string, w interface{}, reqUserAgent string) error {
	writer, ok := w.(http.ResponseWriter)
	if !ok {
		return fmt.Errorf("invalid ResponseWriter")
	}
	return files.ProxyPDFBySandboxPath(context.Background(), c.httpClient, c.cleanClient, c.userAgent, conversationID, messageID, sandboxPath, writer, reqUserAgent)
}

// DownloadSandboxFile 下载沙箱产物。
func (c *Client) DownloadSandboxFile(conversationID, messageID, sandboxPath string) ([]byte, string, error) {
	return files.DownloadSandboxFile(context.Background(), c.httpClient, c.cleanClient, c.userAgent, conversationID, messageID, sandboxPath)
}

// resolvePDFDownloadURL 内部 helper。
func (c *Client) resolvePDFDownloadURL(conversationID, messageID, sandboxPath string) (string, error) {
	return files.ResolvePDFDownloadURL(context.Background(), c.httpClient, conversationID, messageID, sandboxPath)
}

// encodeSandboxPathForQuery 内部 helper。
func encodeSandboxPathForQuery(p string) string { return files.EncodeSandboxPathForQuery(p) }

// guessMimeFromName 内部 helper。
func guessMimeFromName(name string) string { return files.GuessMimeFromName(name) }

// sandboxNames 内部 helper（兼容旧引用）。
func sandboxNames(arts []SandboxArtifact) []string {
	names := make([]string, len(arts))
	for i, a := range arts {
		names[i] = a.FileName
	}
	return names
}

// pdfNames 内部 helper（兼容旧引用）。
func pdfNames(pdfs []PDFArtifact) []string {
	arts := make([]SandboxArtifact, len(pdfs))
	for i, p := range pdfs {
		arts[i] = SandboxArtifact(p)
	}
	return sandboxNames(arts)
}

// artsFromPDF 内部 helper（兼容旧引用）。
func artsFromPDF(pdfs []PDFArtifact) []SandboxArtifact {
	out := make([]SandboxArtifact, len(pdfs))
	for i, p := range pdfs {
		out[i] = SandboxArtifact(p)
	}
	return out
}

// PDFArtifact 与 SandboxArtifact 同构。
type PDFArtifact = SandboxArtifact

// ----- artifacts image/conv-update facade -----

// 类型别名。
type (
	GeneratedImageSlot    = artifacts.GeneratedImageSlot
	ParsedGeneratedImage  = artifacts.ParsedGeneratedImage
)

// 生图版本策略常量。
const (
	ImageRevisionAll           = artifacts.ImageRevisionAll
	ImageRevisionLatestPerSlot = artifacts.ImageRevisionLatestPerSlot
	ImageRevisionFinalOnly     = artifacts.ImageRevisionFinalOnly
)

// imageRevisionMode 由 stream.Config.RevisionMode 提供。

// 槽位管理。
func (result *ChatResult) ensureImageSlots() {
	if result.imageSlots == nil {
		result.imageSlots = make(map[string]*GeneratedImageSlot)
	}
	if result.emittedArtifacts == nil {
		result.emittedArtifacts = make(map[string]bool)
	}
}

func slotMapKey(genID, messageID string) string {
	if genID != "" {
		return "gen:" + genID
	}
	if messageID != "" {
		return "msg:" + messageID
	}
	return ""
}

func (result *ChatResult) findSlotByParent(parentGenID string) *GeneratedImageSlot {
	if parentGenID == "" {
		return nil
	}
	for _, s := range result.imageSlots {
		if s.GenID == parentGenID {
			return s
		}
	}
	return nil
}

func (result *ChatResult) assignImageSlot(genID, messageID, parentGenID string) *GeneratedImageSlot {
	result.ensureImageSlots()
	if k := slotMapKey(genID, messageID); k != "" {
		if s, ok := result.imageSlots[k]; ok {
			return s
		}
	}
	if parent := result.findSlotByParent(parentGenID); parent != nil {
		return parent
	}
	idx := len(result.imageSlots) + 1
	s := &GeneratedImageSlot{SlotIndex: idx, GenID: genID, MessageID: messageID}
	k := slotMapKey(genID, messageID)
	if k == "" {
		k = fmt.Sprintf("slot:%d", idx)
	}
	result.imageSlots[k] = s
	return s
}

// imageFileIDSeen / isImageAsyncWSUpdate 由 chat.go 提供（不需要 facade）。

// ImageGenExitBlockReason 诊断：当前为何不能结束 WS。
func (result *ChatResult) ImageGenExitBlockReason() string {
	if result == nil {
		return "blocking=nil"
	}
	if !result.HasDalleGeneratedOutput() {
		return "blocking=no_dalle_image_yet"
	}
	if result.lastImageGenActivityAt == 0 {
		return "blocking=no_image_activity_ts"
	}
	since := time.Since(time.Unix(0, result.lastImageGenActivityAt))
	if result.imageAsyncTaskPending > 0 {
		return fmt.Sprintf("blocking=async_pending(%d,active=%v) idleSinceImg=%.1fs",
			result.imageAsyncTaskPending, result.imageAsyncTaskActive, since.Seconds())
	}
	need := ImageGenIdleDuration(result)
	if result.imageGenAsyncCompleteSeen || result.imageGenConvAsyncStatusDone {
		need = 3 * time.Second
		if since < need {
			return fmt.Sprintf("blocking=post_complete_idle(%.1fs/%.0fs convStatus=%v)",
				since.Seconds(), need.Seconds(), result.imageGenConvAsyncStatusDone)
		}
		return "ok"
	}
	if result.imageGenTurnDone {
		return fmt.Sprintf("blocking=turn_done_but_async_may_continue idleSinceImg=%.1fs", since.Seconds())
	}
	if since < need {
		return fmt.Sprintf("blocking=idle_wait(%.1fs/%.0fs)", since.Seconds(), need.Seconds())
	}
	return "ok"
}

// MaybeClearStaleImageAsyncPending 解除过期 pending。
func (result *ChatResult) MaybeClearStaleImageAsyncPending() bool {
	if result == nil || result.imageAsyncTaskPending <= 0 || result.imageGenAsyncCompleteSeen {
		return false
	}
	if !result.HasDalleGeneratedOutput() {
		return false
	}
	since := time.Since(time.Unix(0, result.lastImageGenActivityAt))
	if since < 20*time.Second {
		return false
	}
	result.imageAsyncTaskPending = 0
	result.imageAsyncTaskActive = false
	return true
}

// ImageGenIdleDuration 计算 idle 等待时长。
func ImageGenIdleDuration(result *ChatResult) time.Duration {
	if result == nil {
		return 15 * time.Second
	}
	return artifacts.ImageGenIdleDuration(artifacts.ImageIdleStats{
		ImageSlots:          len(result.imageSlots),
		AllSlotsFinal:       result.AllImageSlotsFinal(),
		AsyncTaskPending:    result.imageAsyncTaskPending,
		AsyncTaskActive:     result.imageAsyncTaskActive,
		LastActivityNanos:   result.lastImageGenActivityAt,
		HasDalleGenerated:   result.HasDalleGeneratedOutput(),
		AsyncCompleteSeen:   result.imageGenAsyncCompleteSeen,
		ConvAsyncStatusDone: result.imageGenConvAsyncStatusDone,
	})
}

// logImageGenDiag 诊断日志。
func (c *Client) logImageGenDiag(result *ChatResult, tag string) {
	if result == nil {
		return
	}
	sinceImg := -1.0
	if result.lastImageGenActivityAt > 0 {
		sinceImg = time.Since(time.Unix(0, result.lastImageGenActivityAt)).Seconds()
	}
	c.logf("[image-ws][diag] %s pending=%d active=%v complete=%v convStatus=%v turnDone=%v slots=%d dalle=%v idleSinceImg=%.1fs block=%s",
		tag,
		result.imageAsyncTaskPending, result.imageAsyncTaskActive,
		result.imageGenAsyncCompleteSeen, result.imageGenConvAsyncStatusDone, result.imageGenTurnDone,
		len(result.imageSlots), result.HasDalleGeneratedOutput(), sinceImg,
		result.ImageGenExitBlockReason(),
	)
}

// parseGeneratedImagesFromMessage 内部 helper。
func parseGeneratedImagesFromMessage(msg map[string]interface{}) []ParsedGeneratedImage {
	return artifacts.ParseGeneratedImagesFromMessage(msg)
}

// summarizeConvUpdatePayload 内部 helper。
func summarizeConvUpdatePayload(payload map[string]interface{}) string {
	return artifacts.SummarizeConvUpdatePayload(payload)
}

// 包装 FetchConversationRaw / fetchSandboxArtifactsFromConversation / Artifact helpers。

// FetchConversationRaw 拉取完整对话 JSON。
func (c *Client) FetchConversationRaw(conversationID string) ([]byte, error) {
	apiPath := "/backend-api/conversation/" + conversationID
	status, body, _, err := c.doJSON(fhttp.MethodGet, apiPath, map[string]string{
		"x-openai-target-path":  apiPath,
		"x-openai-target-route": "/backend-api/conversation/{conversation_id}",
	}, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("conversation %d: %s", status, truncateStr(string(body), 300))
	}
	return body, nil
}

// fetchSandboxArtifactsFromConversation 内部 helper。
func (c *Client) fetchSandboxArtifactsFromConversation(conversationID string) ([]SandboxArtifact, string, error) {
	return artifacts.FetchSandboxArtifactsFromConversation(c.FetchConversationRaw, conversationID)
}

// ApplyArtifactsFromSignals 用流式累积信号填充沙箱/图片产物。
func ApplyArtifactsFromSignals(result *ChatResult, opts ChatOptions) {
	if result == nil {
		return
	}
	result.ExpectGeneratedImages = IsGeneratedImageTurn(result.ArtifactSignals, opts)

	if len(result.SandboxArtifacts) == 0 {
		if arts := SandboxArtifactsFromSignals(result.ArtifactSignals, result.LastAssistantMsgID); len(arts) > 0 {
			result.SandboxArtifacts = arts
			result.PDFArtifacts = filterPDFArtifacts(arts)
		}
	}
	if !result.ExpectGeneratedImages {
		result.ImageFileIDs = nil
		result.ImageFileID = ""
		return
	}
	// 生图轮次：ImageFileIDs 仅由 WS conversation-update（含 dalle.gen_id）填充，
	// 勿把用户上传的 sediment 参考图写入，否则会误触发 4s idle 提前结束 WS。
	if result.ImageFileID == "" && len(result.ImageFileIDs) > 0 {
		result.ImageFileID = result.ImageFileIDs[0]
	}
}

// MergeApplyAndEmitArtifacts 合并信号、更新产物列表，并将新产物流式推送给客户端。
func (c *Client) MergeApplyAndEmitArtifacts(result *ChatResult, opts ChatOptions) {
	if result == nil {
		return
	}
	prevS := len(result.SandboxArtifacts)
	ApplyArtifactsFromSignals(result, opts)
	if len(result.SandboxArtifacts) > prevS {
		c.EmitNewArtifacts(opts.Artifacts, result)
	}
}

// ----- 槽位/事件流相关 (Client + ChatResult 方法) -----

// HasDalleGeneratedOutput 是否已有带 gen_id 的 WS 生图产出（非用户上传参考图）。
func (result *ChatResult) HasDalleGeneratedOutput() bool {
	for _, s := range result.imageSlots {
		if s != nil && s.GenID != "" && s.FileID != "" {
			return true
		}
	}
	return false
}

// AllImageSlotsFinal 所有图位是否已定稿。
func (result *ChatResult) AllImageSlotsFinal() bool {
	if len(result.imageSlots) == 0 {
		return false
	}
	for _, s := range result.imageSlots {
		if s == nil || !s.Final {
			return false
		}
	}
	return true
}

// CanImageGenIdleExit 是否允许结束生图 WS（优先服务端 complete / turn [DONE]，辅以 idle）。
func (result *ChatResult) CanImageGenIdleExit() bool {
	if result == nil || !result.HasDalleGeneratedOutput() || result.lastImageGenActivityAt == 0 {
		return false
	}
	result.MaybeClearStaleImageAsyncPending()
	if result.imageAsyncTaskPending > 0 {
		return false
	}
	since := time.Since(time.Unix(0, result.lastImageGenActivityAt))
	// 网页端完成：set-conversation-async-status=4（已有图即可结束，勿再等 ReadMessage 60s）
	if result.imageGenConvAsyncStatusDone && result.HasDalleGeneratedOutput() {
		return true
	}
	if result.imageGenAsyncCompleteSeen {
		return since >= 2*time.Second
	}
	// 不用 turnDone：正文流 [DONE] 常早于生图 conversation-update 结束
	return since >= ImageGenIdleDuration(result)
}

// RebuildImageFileIDsFromSlots 按槽位顺序刷新 ImageFileIDs（最终每槽最新 file_id）。
func (result *ChatResult) RebuildImageFileIDsFromSlots() {
	if len(result.imageSlots) == 0 {
		return
	}
	slots := make([]*GeneratedImageSlot, 0, len(result.imageSlots))
	for _, s := range result.imageSlots {
		slots = append(slots, s)
	}
	// 按 SlotIndex 排序
	for i := 0; i < len(slots); i++ {
		for j := i + 1; j < len(slots); j++ {
			if slots[j].SlotIndex < slots[i].SlotIndex {
				slots[i], slots[j] = slots[j], slots[i]
			}
		}
	}
	result.ImageFileIDs = nil
	for _, s := range slots {
		if s.FileID != "" {
			result.ImageFileIDs = append(result.ImageFileIDs, s.FileID)
		}
	}
	if len(result.ImageFileIDs) > 0 {
		result.ImageFileID = result.ImageFileIDs[len(result.ImageFileIDs)-1]
	}
}

// FinalizeImageGenSlots 生图 WS 空闲结束时：final_only 推送，并标记各槽位 is_final。
func (c *Client) FinalizeImageGenSlots(result *ChatResult, opts ChatOptions) {
	if result == nil || !result.ExpectGeneratedImages {
		return
	}
	cfg := opts.Artifacts.Normalized()
	mode := cfg.RevisionMode()

	for _, slot := range result.imageSlots {
		if slot == nil || slot.FileID == "" {
			continue
		}
		slot.Final = true
		if mode != ImageRevisionFinalOnly {
			cfg.Emit(StreamEvent{
				Event:      StreamEventArtifactSlotFinal,
				Kind:       "generated_image",
				SlotIndex:  slot.SlotIndex,
				Revision:   slot.Revision,
				GenID:      slot.GenID,
				MessageID:  slot.MessageID,
				FileID:     slot.FileID,
				IsFinal:    true,
				Total:      len(result.imageSlots),
			})
			continue
		}
		emitKey := "img:" + slot.FileID
		if result.emittedArtifacts[emitKey] {
			continue
		}
		result.emittedArtifacts[emitKey] = true
		p := ParsedGeneratedImage{
			FileID:    slot.FileID,
			MessageID: slot.MessageID,
			GenID:     slot.GenID,
		}
		c.emitGeneratedImageEvent(cfg, result, p, slot, "finalize", true, "")
	}
}

// noteGeneratedImageRevision 处理来自 WS 的单条生图修订。
func (c *Client) noteGeneratedImageRevision(result *ChatResult, opts ChatOptions, p ParsedGeneratedImage, wsUpdateType string) {
	if p.FileID == "" || result == nil || !result.ExpectGeneratedImages {
		return
	}
	// 仅有 gen_id 的才是 DALL·E 产出；用户上传的 referenced_image 无 gen_id，不能触发 idle 结束
	if p.GenID == "" && wsUpdateType != "finalize" {
		return
	}
	if !imageFileIDSeen(result.ImageFileIDs, p.FileID) {
		result.ImageFileIDs = append(result.ImageFileIDs, p.FileID)
	}
	result.ImageFileID = p.FileID

	emitKey := "img:" + p.FileID
	if result.emittedArtifacts[emitKey] {
		return
	}

	slot := result.assignImageSlot(p.GenID, p.MessageID, p.ParentGenID)
	if p.MessageID != "" && slot.MessageID == "" {
		slot.MessageID = p.MessageID
	}
	if p.GenID != "" && slot.GenID == "" {
		slot.GenID = p.GenID
	}

	var prevFileID string
	if len(slot.FileHistory) > 0 {
		prevFileID = slot.FileHistory[len(slot.FileHistory)-1]
	}
	if prevFileID == p.FileID {
		return
	}

	now := time.Now().UnixNano()
	result.lastImageAddedAt = now
	result.lastImageGenActivityAt = now

	slot.Revision++
	slot.FileHistory = append(slot.FileHistory, p.FileID)
	slot.FileID = p.FileID
	slot.Final = false

	// 诊断：新图修订（重复 file_id 已在上方 return）
	if c != nil {
		c.logf("[image-ws][img] %s slot=%d rev=%d gen=%s file=%s", wsUpdateType, slot.SlotIndex, slot.Revision, p.GenID, p.FileID)
	}

	mode := opts.Artifacts.RevisionMode()
	cfg := opts.Artifacts.Normalized()

	switch mode {
	case ImageRevisionAll:
		result.emittedArtifacts[emitKey] = true
		c.emitGeneratedImageEvent(cfg, result, p, slot, wsUpdateType, false, "")

	case ImageRevisionLatestPerSlot:
		if prevFileID != "" && prevFileID != p.FileID {
			sup := StreamEvent{
				Event:           StreamEventArtifactSuperseded,
				Kind:            "generated_image",
				SlotIndex:       slot.SlotIndex,
				Revision:        slot.Revision - 1,
				GenID:           slot.GenID,
				MessageID:       slot.MessageID,
				FileID:          prevFileID,
				UpdateType:      wsUpdateType,
			}
			if cfg.BuildImageURL != nil {
				sup.URL = cfg.BuildImageURL(prevFileID)
			}
			cfg.Emit(sup)
		}
		result.emittedArtifacts[emitKey] = true
		c.emitGeneratedImageEvent(cfg, result, p, slot, wsUpdateType, false, prevFileID)

	case ImageRevisionFinalOnly:
		// 仅记录，在 FinalizeImageGenSlots 推送
		return
	}
}

// emitGeneratedImageEvent 推发生图产物事件（最终落盘在 stream.Config.OnEvent）。
func (c *Client) emitGeneratedImageEvent(cfg ArtifactStreamConfig, result *ChatResult, p ParsedGeneratedImage, slot *GeneratedImageSlot, wsUpdateType string, isFinal bool, supersedes string) {
	evBase := StreamEvent{
		Event:            StreamEventArtifact,
		Kind:             "generated_image",
		Index:            slot.SlotIndex,
		SlotIndex:        slot.SlotIndex,
		Revision:         slot.Revision,
		GenID:            p.GenID,
		MessageID:        p.MessageID,
		ParentGenID:      p.ParentGenID,
		FileID:           p.FileID,
		UpdateType:       wsUpdateType,
		IsFinal:          isFinal,
		SupersedesFileID: supersedes,
		MimeType:         "image/png",
		Name:             fmt.Sprintf("generated_slot%d_rev%d.png", slot.SlotIndex, slot.Revision),
	}
	if cfg.BuildImageURL != nil {
		evBase.URL = cfg.BuildImageURL(p.FileID)
	}

	if cfg.Delivery == ArtifactDeliveryURL {
		cfg.Emit(evBase)
		return
	}
	data, mime, err := c.DownloadFileByFileID(result.ConversationID, p.FileID)
	if err != nil {
		evBase.Error = err.Error()
		cfg.Emit(evBase)
		return
	}
	evBase.SizeBytes = len(data)
	if mime != "" {
		evBase.MimeType = mime
	}
	c.emitArtifactBytes(cfg, evBase, data)
}

// emitArtifactBytes 把 base64 / base64_chunked 产物通过 cfg.OnEvent 推送。
func (c *Client) emitArtifactBytes(cfg ArtifactStreamConfig, meta StreamEvent, data []byte) {
	cfg = cfg.Normalized()
	if len(data) == 0 {
		cfg.Emit(meta)
		cfg.Emit(StreamEvent{
			Event:       StreamEventArtifactDone,
			Kind:       meta.Kind,
			Index:      meta.Index,
			FileID:     meta.FileID,
			MessageID:  meta.MessageID,
			SandboxPath: meta.SandboxPath,
			SizeBytes:  0,
		})
		return
	}

	if cfg.Delivery == ArtifactDeliveryBase64 {
		meta.Data = base64.StdEncoding.EncodeToString(data)
		meta.SizeBytes = len(data)
		cfg.Emit(meta)
		cfg.Emit(StreamEvent{
			Event:       StreamEventArtifactDone,
			Kind:       meta.Kind,
			Index:      meta.Index,
			FileID:     meta.FileID,
			MessageID:  meta.MessageID,
			SandboxPath: meta.SandboxPath,
			SizeBytes:  len(data),
		})
		return
	}

	chunkSize := cfg.ChunkSize
	total := (len(data) + chunkSize - 1) / chunkSize
	for i := 0; i < total; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]
		cfg.Emit(StreamEvent{
			Event:       StreamEventArtifactChunk,
			Kind:       meta.Kind,
			Index:      meta.Index,
			FileID:     meta.FileID,
			MessageID:  meta.MessageID,
			SandboxPath: meta.SandboxPath,
			Name:       meta.Name,
			MimeType:   meta.MimeType,
			ChunkIndex: i + 1,
			ChunkTotal: total,
			Data:       base64.StdEncoding.EncodeToString(chunk),
			SizeBytes:  len(chunk),
		})
	}
	cfg.Emit(StreamEvent{
		Event:       StreamEventArtifactDone,
		Kind:       meta.Kind,
		Index:      meta.Index,
		FileID:     meta.FileID,
		MessageID:  meta.MessageID,
		SandboxPath: meta.SandboxPath,
		SizeBytes:  len(data),
	})
}

// emitImageGenPending 推送 "正在画图" 提示。
func (c *Client) emitImageGenPending(cfg ArtifactStreamConfig, title string) {
	cfg.Emit(StreamEvent{
		Event:  StreamEventArtifactPending,
		Kind:  "generated_image",
		Title: title,
	})
}

// emitGeneratedImage 推送单张生图产物。
func (c *Client) emitGeneratedImage(cfg ArtifactStreamConfig, result *ChatResult, fileID string) {
	if fileID == "" || result == nil {
		return
	}
	key := "img:" + fileID
	if result.emittedArtifacts == nil {
		result.emittedArtifacts = make(map[string]bool)
	}
	if result.emittedArtifacts[key] {
		return
	}
	result.emittedArtifacts[key] = true

	cfg = cfg.Normalized()
	idx := len(result.emittedArtifacts)
	for i, id := range result.ImageFileIDs {
		if id == fileID {
			idx = i + 1
			break
		}
	}
	if idx == 0 {
		idx = len(result.ImageFileIDs)
		if idx == 0 {
			idx = 1
		}
	}

	evBase := StreamEvent{
		Event:    StreamEventArtifact,
		Kind:     "generated_image",
		Index:    idx,
		FileID:   fileID,
		MimeType: "image/png",
		Name:     fmt.Sprintf("generated_%d.png", idx),
	}
	if cfg.BuildImageURL != nil {
		evBase.URL = cfg.BuildImageURL(fileID)
	}

	if cfg.Delivery == ArtifactDeliveryURL {
		cfg.Emit(evBase)
		return
	}

	data, mime, err := c.DownloadFileByFileID(result.ConversationID, fileID)
	if err != nil {
		evBase.Error = err.Error()
		cfg.Emit(evBase)
		return
	}
	evBase.SizeBytes = len(data)
	if mime != "" {
		evBase.MimeType = mime
	}
	c.emitArtifactBytes(cfg, evBase, data)
}

// emitSandboxFile 推送沙箱文件产物。
func (c *Client) emitSandboxFile(cfg ArtifactStreamConfig, result *ChatResult, art SandboxArtifact) {
	if art.SandboxPath == "" {
		return
	}
	msgID := art.MessageID
	if msgID == "" {
		msgID = result.LastAssistantMsgID
	}
	key := "sandbox:" + msgID + ":" + art.SandboxPath
	if result.emittedArtifacts == nil {
		result.emittedArtifacts = make(map[string]bool)
	}
	if result.emittedArtifacts[key] {
		return
	}
	result.emittedArtifacts[key] = true

	cfg = cfg.Normalized()
	name := art.FileName
	if name == "" {
		name = path.Base(art.SandboxPath)
	}
	idx := 0
	for i, a := range result.SandboxArtifacts {
		if a.SandboxPath == art.SandboxPath && a.MessageID == art.MessageID {
			idx = i + 1
			break
		}
	}
	if idx == 0 {
		idx = len(result.SandboxArtifacts)
		if idx == 0 {
			idx = 1
		}
	}

	evBase := StreamEvent{
		Event:       StreamEventArtifact,
		Kind:        "sandbox_file",
		Index:       idx,
		Name:        name,
		MimeType:    guessMimeFromName(name),
		MessageID:   msgID,
		SandboxPath: art.SandboxPath,
	}
	if cfg.BuildSandboxURL != nil {
		evBase.URL = cfg.BuildSandboxURL(msgID, art.SandboxPath)
	}

	if cfg.Delivery == ArtifactDeliveryURL {
		cfg.Emit(evBase)
		return
	}

	data, mime, err := c.DownloadSandboxFile(result.ConversationID, msgID, art.SandboxPath)
	if err != nil {
		evBase.Error = err.Error()
		cfg.Emit(evBase)
		return
	}
	evBase.SizeBytes = len(data)
	if mime != "" {
		evBase.MimeType = mime
	}
	c.emitArtifactBytes(cfg, evBase, data)
}

// EmitNewArtifacts 将本轮新出现的沙箱产物推送给客户端。
func (c *Client) EmitNewArtifacts(cfg ArtifactStreamConfig, result *ChatResult) {
	if cfg.OnEvent == nil || result == nil {
		return
	}
	for _, art := range result.SandboxArtifacts {
		c.emitSandboxFile(cfg, result, art)
	}
}
