package main

import (
	"web2api/internal/artifacts"
	"web2api/internal/stream"
)

// 流式事件/配置/常量：根包以 type alias / const 重新暴露，保持外部 API 兼容。

// 产物下发方式。
const (
	ArtifactDeliveryURL           = stream.ArtifactDeliveryURL
	ArtifactDeliveryBase64        = stream.ArtifactDeliveryBase64
	ArtifactDeliveryBase64Chunked = stream.ArtifactDeliveryBase64Chunked
)

// Stream 事件类型（写入 SSE chunk 的 sentinel 字段）。
const (
	StreamEventArtifactPending    = stream.EventArtifactPending
	StreamEventArtifact           = stream.EventArtifact
	StreamEventArtifactChunk      = stream.EventArtifactChunk
	StreamEventArtifactDone       = stream.EventArtifactDone
	StreamEventArtifactSuperseded = stream.EventArtifactSuperseded
	StreamEventArtifactSlotFinal  = stream.EventArtifactSlotFinal
)

// 类型别名。
type (
	StreamEvent           = stream.Event
	ArtifactStreamConfig  = stream.Config
	StreamRecorder        = stream.Recorder
	StreamRecord          = stream.Record
	WSRecord              = stream.WSRecord
)

// NewStreamRecorder 创建记录器。
func NewStreamRecorder(outPath string) (*StreamRecorder, error) {
	return stream.NewRecorder(outPath)
}

// 信号相关（来自 artifacts 包）。
type (
	ArtifactSignalType = artifacts.SignalType
	ArtifactSignal     = artifacts.Signal
	SandboxArtifact    = artifacts.SandboxArtifact
	ArtifactPlan       = artifacts.Plan
)

// Signal 常量。
const (
	SignalImageGenTaskID   = artifacts.SignalImageGenTaskID
	SignalGhostrider       = artifacts.SignalGhostrider
	SignalDalleTool        = artifacts.SignalDalleTool
	SignalImageAsset       = artifacts.SignalImageAsset
	SignalPythonTool       = artifacts.SignalPythonTool
	SignalCodeInterpreter  = artifacts.SignalCodeInterpreter
	SignalExecutionOutput  = artifacts.SignalExecutionOutput
	SignalSandboxPath      = artifacts.SignalSandboxPath
	SignalContentReference = artifacts.SignalContentReference
	SignalToolInvokedMeta  = artifacts.SignalToolInvokedMeta
	SignalTurnUseCase      = artifacts.SignalTurnUseCase
	SignalFileSearch       = artifacts.SignalFileSearch
)

// ExtractSignalsFromJSON 递归扫描 JSON，收集结构化产物信号。
func ExtractSignalsFromJSON(v interface{}) []ArtifactSignal {
	return artifacts.ExtractFromJSON(v)
}

// ExtractSignalsFromConversation 从对话 mapping 提取信号。
func ExtractSignalsFromConversation(convJSON []byte) []ArtifactSignal {
	return artifacts.ExtractFromConversation(convJSON)
}

// MergeSignals 合并并去重。
func MergeSignals(a, b []ArtifactSignal) []ArtifactSignal {
	return artifacts.Merge(a, b)
}

// IsGeneratedImageTurn 是否为 DALL·E / picture_v2 生图。
func IsGeneratedImageTurn(signals []ArtifactSignal, opts ChatOptions) bool {
	return artifacts.IsGeneratedImageTurn(signals, artifacts.ImageGenOpts{
		ForcePictureV2: opts.ForcePictureV2,
		HasImages:      len(opts.Images) > 0,
	})
}

// SandboxArtifactsFromSignals 从已观测信号组装沙箱产物。
func SandboxArtifactsFromSignals(signals []ArtifactSignal, messageID string) []SandboxArtifact {
	return artifacts.SandboxArtifactsFromSignals(signals, messageID)
}

// ImageFileIDsFromSignals 从 SSE 信号提取图片 file_id。
func ImageFileIDsFromSignals(signals []ArtifactSignal) []string {
	return artifacts.ImageFileIDsFromSignals(signals)
}

// BuildArtifactPlan 根据 SSE 信号生成分析用计划。
func BuildArtifactPlan(signals []ArtifactSignal, opts ChatOptions, imageTaskID string) ArtifactPlan {
	return artifacts.BuildPlan(signals, len(opts.Images) > 0, imageTaskID)
}

// AnalyzeSignals 生成可读分析报告。
func AnalyzeSignals(name string, signals []ArtifactSignal, plan ArtifactPlan) map[string]interface{} {
	return artifacts.Analyze(name, signals, plan)
}

// filterPDFArtifacts 内部 helper（沙箱 .pdf 子集）。
func filterPDFArtifacts(arts []SandboxArtifact) []SandboxArtifact {
	return artifacts.FilterPDFArtifacts(arts)
}
