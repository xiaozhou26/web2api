// Package artifacts 负责产物信号（ArtifactSignal）的提取、去重与计划推导，
// 以及沙箱产物的组装。所有外部使用方通过 sentinel 根包的 type alias 访问。
package artifacts

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// SignalType 来自 SSE/对话结构的产物信号（非关键词）。
type SignalType string

const (
	SignalImageGenTaskID   SignalType = "image_gen_task_id"
	SignalGhostrider       SignalType = "ghostrider"
	SignalDalleTool        SignalType = "dalle_tool"
	SignalImageAsset       SignalType = "image_asset_pointer"
	SignalPythonTool       SignalType = "python_tool"
	SignalCodeInterpreter  SignalType = "code_interpreter_recipient"
	SignalExecutionOutput  SignalType = "execution_output"
	SignalSandboxPath      SignalType = "sandbox_path"
	SignalContentReference SignalType = "content_reference"
	SignalToolInvokedMeta  SignalType = "tool_invoked_metadata" // server_ste_metadata
	SignalTurnUseCase      SignalType = "turn_use_case"         // server_ste_metadata.turn_use_case
	SignalFileSearch       SignalType = "file_search"           // 上传文件识图，非 DALL·E 生图
)

// Signal 单条可观测信号。
type Signal struct {
	Type   SignalType `json:"type"`
	Value  string     `json:"value,omitempty"`
	Source string     `json:"source,omitempty"` // sse / conversation
}

// SandboxArtifact Code Interpreter 沙箱产物（pdf/txt/图片等）。
type SandboxArtifact struct {
	MessageID   string `json:"message_id"`
	SandboxPath string `json:"sandbox_path"`
	FileName    string `json:"file_name"`
}

// Plan 流结束后应执行的拉取/轮询动作（由信号推导，非关键词）。
type Plan struct {
	PollImage         bool `json:"poll_image"`
	PollSandboxFiles  bool `json:"poll_sandbox_files"`
	HasUserAttachment bool `json:"has_user_attachment"`
}

type signalFlags struct {
	imageGenTask    bool
	ghostrider      bool
	dalleTool       bool
	imageAsset      bool
	fileSearch      bool
	turnUseCase     string
	pythonTool      bool
	codeInterpreter bool
	executionOutput bool
	sandboxPath     bool
	toolInvokedMeta bool
}

// 沙箱路径可含中文等非 ASCII 文件名（如 你好世界测试123.txt）
var sandboxFileRe = regexp.MustCompile(`/mnt/data/[^"'()\s<>]+\.[A-Za-z0-9]+`)

func isValidSandboxPath(p string) bool {
	if !strings.HasPrefix(p, "/mnt/data/") {
		return false
	}
	if strings.ContainsAny(p, `"'()<>`) {
		return false
	}
	base := path.Base(p)
	return base != "." && base != "/" && strings.Contains(base, ".")
}

// ExtractFileID 从 asset_pointer / sediment URL 提取 file_xxx。
var fileIDRegexp = regexp.MustCompile(`file_[a-f0-9]+`)

func ExtractFileID(pointer string) string {
	if pointer == "" {
		return ""
	}
	return fileIDRegexp.FindString(pointer)
}

// ExtractFromJSON 递归扫描 JSON，收集结构化产物信号。
func ExtractFromJSON(v interface{}) []Signal {
	var out []Signal
	walkSignals(v, "", &out)
	return dedupeSignals(out)
}

func walkSignals(v interface{}, ctx string, out *[]Signal) {
	switch x := v.(type) {
	case map[string]interface{}:
		inspectMessageMap(x, out)
		for k, val := range x {
			walkSignals(val, k, out)
		}
	case []interface{}:
		for _, item := range x {
			walkSignals(item, ctx, out)
		}
	case string:
		for _, m := range sandboxFileRe.FindAllString(x, -1) {
			if isValidSandboxPath(m) {
				*out = append(*out, Signal{Type: SignalSandboxPath, Value: m})
			}
		}
		if strings.HasPrefix(x, "sediment://") {
			if fid := ExtractFileID(x); fid != "" {
				*out = append(*out, Signal{Type: SignalImageAsset, Value: fid})
			}
		}
	}
}

func inspectMessageMap(m map[string]interface{}, out *[]Signal) {
	if t, ok := m["type"].(string); ok && t == "server_ste_metadata" {
		if md, ok := m["metadata"].(map[string]interface{}); ok {
			if inv, ok := md["tool_invoked"].(bool); ok && inv {
				toolName, _ := md["tool_name"].(string)
				*out = append(*out, Signal{Type: SignalToolInvokedMeta, Value: toolName})
			}
			if uc, ok := md["turn_use_case"].(string); ok && uc != "" {
				*out = append(*out, Signal{Type: SignalTurnUseCase, Value: uc})
			}
		}
	}
	if meta, ok := m["metadata"].(map[string]interface{}); ok {
		if tid, ok := meta["image_gen_task_id"].(string); ok && tid != "" {
			*out = append(*out, Signal{Type: SignalImageGenTaskID, Value: tid})
		}
		if _, ok := meta["ghostrider"]; ok {
			*out = append(*out, Signal{Type: SignalGhostrider, Value: "1"})
		}
		if refs, ok := meta["content_references"].([]interface{}); ok && len(refs) > 0 {
			*out = append(*out, Signal{Type: SignalContentReference, Value: "present"})
		}
		if agg, ok := meta["aggregate_result"].(map[string]interface{}); ok {
			if code, ok := agg["code"].(string); ok {
				for _, p := range sandboxFileRe.FindAllString(code, -1) {
					if isValidSandboxPath(p) {
						*out = append(*out, Signal{Type: SignalSandboxPath, Value: p})
					}
				}
			}
		}
	}
	if author, ok := m["author"].(map[string]interface{}); ok {
		role, _ := author["role"].(string)
		name, _ := author["name"].(string)
		if role == "tool" {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "dalle") || strings.Contains(lower, "image_gen") {
				*out = append(*out, Signal{Type: SignalDalleTool, Value: name})
			}
			if name == "file_search" {
				*out = append(*out, Signal{Type: SignalFileSearch, Value: name})
			}
			if name == "python" || strings.Contains(lower, "canmore") {
				*out = append(*out, Signal{Type: SignalPythonTool, Value: name})
			}
		}
	}
	if recipient, ok := m["recipient"].(string); ok && recipient == "code_interpreter" {
		*out = append(*out, Signal{Type: SignalCodeInterpreter, Value: recipient})
	}
	if content, ok := m["content"].(map[string]interface{}); ok {
		ct, _ := content["content_type"].(string)
		if ct == "execution_output" || ct == "code" {
			*out = append(*out, Signal{Type: SignalExecutionOutput, Value: ct})
		}
		if ct == "image_asset_pointer" {
			if parts, ok := content["parts"].([]interface{}); ok {
				for _, p := range parts {
					if pm, ok := p.(map[string]interface{}); ok {
						if ap, ok := pm["asset_pointer"].(string); ok && ap != "" {
							*out = append(*out, Signal{Type: SignalImageAsset, Value: ap})
						}
					}
				}
			}
		}
	}
}

func dedupeSignals(in []Signal) []Signal {
	seen := make(map[string]bool, len(in))
	out := make([]Signal, 0, len(in))
	for _, s := range in {
		key := string(s.Type) + "\x00" + s.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// Merge 合并并去重。
func Merge(a, b []Signal) []Signal {
	return dedupeSignals(append(append([]Signal{}, a...), b...))
}

func summarizeFlags(signals []Signal) signalFlags {
	var f signalFlags
	for _, s := range signals {
		switch s.Type {
		case SignalImageGenTaskID:
			f.imageGenTask = true
		case SignalGhostrider:
			f.ghostrider = true
		case SignalDalleTool:
			f.dalleTool = true
		case SignalImageAsset:
			f.imageAsset = true
		case SignalFileSearch:
			f.fileSearch = true
		case SignalTurnUseCase:
			f.turnUseCase = s.Value
		case SignalPythonTool:
			f.pythonTool = true
		case SignalCodeInterpreter:
			f.codeInterpreter = true
		case SignalExecutionOutput:
			f.executionOutput = true
		case SignalSandboxPath:
			f.sandboxPath = true
		case SignalToolInvokedMeta:
			f.toolInvokedMeta = true
		}
	}
	return f
}

// IsGeneratedImageTurn 是否为 DALL·E / picture_v2 生图（区别于上传文件识图里的 sediment 引用）。
// opts 需要 ForcePictureV2 和 Images 字段（使用 duck-typed 接口避免包循环）。
type ImageGenOpts struct {
	ForcePictureV2 bool
	HasImages      bool
}

func IsGeneratedImageTurn(signals []Signal, opts ImageGenOpts) bool {
	if opts.ForcePictureV2 {
		return true
	}
	f := summarizeFlags(signals)
	if f.fileSearch && !f.imageGenTask && !f.ghostrider && !f.dalleTool {
		return false
	}
	if f.turnUseCase == "multimodal" && opts.HasImages && !f.imageGenTask && !f.ghostrider {
		return false
	}
	if f.turnUseCase == "image gen" {
		return true
	}
	for _, s := range signals {
		if s.Type == SignalToolInvokedMeta {
			lower := strings.ToLower(s.Value)
			if strings.Contains(lower, "imagegen") {
				return true
			}
		}
	}
	return f.imageGenTask || f.ghostrider || f.dalleTool
}

// SandboxArtifactsFromSignals 从已观测信号组装沙箱产物（无需再查 conversation API）。
func SandboxArtifactsFromSignals(signals []Signal, messageID string) []SandboxArtifact {
	seen := make(map[string]bool)
	var arts []SandboxArtifact
	for _, s := range signals {
		if s.Type != SignalSandboxPath || s.Value == "" || !isValidSandboxPath(s.Value) || seen[s.Value] {
			continue
		}
		seen[s.Value] = true
		arts = append(arts, SandboxArtifact{
			MessageID:   messageID,
			SandboxPath: s.Value,
			FileName:    path.Base(s.Value),
		})
	}
	return arts
}

// ImageFileIDsFromSignals 从 SSE 信号提取图片 file_id（sediment://）。
func ImageFileIDsFromSignals(signals []Signal) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, s := range signals {
		if s.Type != SignalImageAsset || s.Value == "" {
			continue
		}
		fid := ExtractFileID(s.Value)
		if fid == "" || seen[fid] {
			continue
		}
		seen[fid] = true
		ids = append(ids, fid)
	}
	return ids
}

// BuildPlan 根据 SSE 信号生成分析用计划（不触发 conversation 轮询）。
func BuildPlan(signals []Signal, hasUserAttachment bool, imageTaskID string) Plan {
	f := summarizeFlags(signals)
	plan := Plan{HasUserAttachment: hasUserAttachment}

	if imageTaskID != "" || f.imageGenTask || f.ghostrider || f.dalleTool {
		plan.PollImage = true
	}
	// 沙箱/图片产物均从 SSE 信号解析，不轮询 GET conversation。
	plan.PollSandboxFiles = false

	return plan
}

// Analyze 生成可读分析报告（供 stream-capture 与调试）。
func Analyze(name string, signals []Signal, plan Plan) map[string]interface{} {
	byType := make(map[string][]string)
	for _, s := range signals {
		byType[string(s.Type)] = append(byType[string(s.Type)], s.Value)
	}
	return map[string]interface{}{
		"case":            name,
		"signal_count":    len(signals),
		"signals_by_type": byType,
		"plan":            plan,
	}
}

// ExtractFromConversation 从对话 mapping 提取信号。
func ExtractFromConversation(convJSON []byte) []Signal {
	var conv map[string]interface{}
	if err := json.Unmarshal(convJSON, &conv); err != nil {
		return nil
	}
	signals := ExtractFromJSON(conv)
	for _, p := range extractSandboxPathsFromConversation(conv) {
		signals = append(signals, Signal{Type: SignalSandboxPath, Value: p, Source: "conversation"})
	}
	return dedupeSignals(signals)
}

func extractSandboxPathsFromValue(v interface{}) []string {
	var paths []string
	var walk func(interface{})
	walk = func(node interface{}) {
		switch x := node.(type) {
		case string:
			for _, m := range sandboxFileRe.FindAllString(x, -1) {
				paths = append(paths, m)
			}
		case map[string]interface{}:
			for _, val := range x {
				walk(val)
			}
		case []interface{}:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return paths
}

func extractSandboxPathsFromConversation(conv map[string]interface{}) []string {
	mapping, _ := conv["mapping"].(map[string]interface{})
	var all []string
	for _, nodeRaw := range mapping {
		node, ok := nodeRaw.(map[string]interface{})
		if !ok {
			continue
		}
		all = append(all, extractSandboxPathsFromValue(node)...)
	}
	return all
}

// FilterPDFArtifacts 返回 .pdf 后缀的沙箱产物子集。
func FilterPDFArtifacts(arts []SandboxArtifact) []SandboxArtifact {
	var out []SandboxArtifact
	for _, a := range arts {
		if strings.HasSuffix(strings.ToLower(a.FileName), ".pdf") {
			out = append(out, a)
		}
	}
	return out
}

// FetchConversationRaw 拉取完整对话 JSON。httpDoer 由调用方注入，
// 避免本包直接依赖 Client/http 细节。
type HTTPDoer interface {
	Get(path string) (status int, body []byte, err error)
}

// ConversationFetcher 给定对话 ID 拉取原始 JSON；调用方传入实现。
type ConversationFetcher func(conversationID string) ([]byte, error)

// FetchSandboxArtifactsFromConversation 从对话 mapping 提取沙箱文件
// （仅 stream-capture/调试；正常聊天用 SSE 信号）。
func FetchSandboxArtifactsFromConversation(fetcher ConversationFetcher, conversationID string) ([]SandboxArtifact, string, error) {
	raw, err := fetcher(conversationID)
	if err != nil {
		return nil, "", err
	}
	var conv map[string]interface{}
	if err := json.Unmarshal(raw, &conv); err != nil {
		return nil, "", err
	}
	currentNode, _ := conv["current_node"].(string)
	mapping, _ := conv["mapping"].(map[string]interface{})

	type pathInfo struct {
		path      string
		messageID string
	}
	seen := make(map[string]bool)
	var paths []pathInfo

	for nodeID, nodeRaw := range mapping {
		node, ok := nodeRaw.(map[string]interface{})
		if !ok {
			continue
		}
		msg, ok := node["message"].(map[string]interface{})
		if !ok {
			continue
		}
		msgID, _ := msg["id"].(string)
		if msgID == "" {
			msgID = nodeID
		}
		for _, p := range extractSandboxPathsFromValue(msg) {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, pathInfo{path: p, messageID: msgID})
			}
		}
	}

	if len(paths) == 0 {
		return nil, currentNode, fmt.Errorf("对话中未找到沙箱文件")
	}

	ownerMsgID := findSandboxOwnerMessageID(mapping)
	if ownerMsgID == "" {
		ownerMsgID = currentNode
	}

	var artifacts []SandboxArtifact
	for _, pi := range paths {
		artifacts = append(artifacts, SandboxArtifact{
			MessageID:   ownerMsgID,
			SandboxPath: pi.path,
			FileName:    path.Base(pi.path),
		})
	}
	return artifacts, ownerMsgID, nil
}

func findSandboxOwnerMessageID(mapping map[string]interface{}) string {
	for _, nodeRaw := range mapping {
		node, ok := nodeRaw.(map[string]interface{})
		if !ok {
			continue
		}
		msg, ok := node["message"].(map[string]interface{})
		if !ok {
			continue
		}
		author, _ := msg["author"].(map[string]interface{})
		role, _ := author["role"].(string)
		if role != "assistant" {
			continue
		}
		meta, _ := msg["metadata"].(map[string]interface{})
		if refs, ok := meta["content_references"].([]interface{}); ok && len(refs) > 0 {
			if id, ok := msg["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}
