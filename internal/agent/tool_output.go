package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const toolOutputPreviewBytes = 2048

type spillContentShape struct {
	Content string
	Details map[string]any
}

type toolResponseClientMetadata struct {
	ServerName    string               `json:"server_name,omitempty"`
	ToolName      string               `json:"tool_name,omitempty"`
	ToolOutputRef *StoredToolOutputRef `json:"tool_output_ref,omitempty"`
	PlanRef       *StoredPlanRef       `json:"plan_ref,omitempty"`
}

type runtimeWrappedTool struct {
	runtime *agentRuntime
	entry   runtimeTool
}

func (t *runtimeWrappedTool) Info() fantasy.ToolInfo {
	return t.entry.tool.Info()
}

func (t *runtimeWrappedTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := t.entry.tool.Run(ctx, params)
	if err != nil {
		return resp, err
	}
	if !t.runtime.shouldSpillToolOutput(t.entry.id, resp) {
		return resp, nil
	}
	return t.runtime.spillToolResponse(ctx, params, t.entry, resp)
}

func (t *runtimeWrappedTool) ProviderOptions() fantasy.ProviderOptions {
	return t.entry.tool.ProviderOptions()
}

func (t *runtimeWrappedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.entry.tool.SetProviderOptions(opts)
}

func (rt *agentRuntime) wrapToolForAgent(entry runtimeTool) fantasy.AgentTool {
	return &runtimeWrappedTool{
		runtime: rt,
		entry:   entry,
	}
}

func (rt *agentRuntime) shouldSpillToolOutput(toolID string, resp fantasy.ToolResponse) bool {
	if toolID == "view_tool_output" || toolID == "grep_tool_output" {
		return false
	}
	if rt == nil || rt.store == nil || rt.session == nil {
		return false
	}
	if resp.IsError || resp.Type != "text" || strings.TrimSpace(resp.Content) == "" {
		return false
	}
	if rt.toolOutputTokenLimit <= 0 {
		return false
	}
	counter := newTokenEstimator(rt.model)
	if counter == nil {
		return false
	}
	content := canonicalizeToolOutput(resp.Content)
	return counter.Count(content) > int64(rt.toolOutputTokenLimit)
}

func (rt *agentRuntime) spillToolResponse(ctx context.Context, call fantasy.ToolCall, entry runtimeTool, resp fantasy.ToolResponse) (fantasy.ToolResponse, error) {
	counter := newTokenEstimator(rt.model)
	if counter == nil {
		return resp, nil
	}

	content := canonicalizeToolOutput(resp.Content)
	shape := shapeSpilledToolContent(entry.id, content)
	storedBaseContent := canonicalizeToolOutput(shape.Content)
	fullEstimatedTokens := counter.Count(storedBaseContent)
	if fullEstimatedTokens <= int64(rt.toolOutputTokenLimit) {
		return resp, nil
	}

	maxStoredBytes := rt.cfg.Agent.ToolOutput.MaxStoredBytes
	if maxStoredBytes <= 0 {
		maxStoredBytes = 4 * 1024 * 1024
	}

	storedContent, storedTruncated := truncateUTF8StringToBytes(storedBaseContent, maxStoredBytes)
	totalLines := len(knowledgeLines(storedContent))
	output, err := rt.store.CreateAgentRunToolOutput(ctx, knowledge.CreateAgentRunToolOutputInput{
		RunID:           rt.session.runID,
		ToolCallID:      call.ID,
		ToolName:        entry.id,
		Content:         storedContent,
		SizeBytes:       len([]byte(storedBaseContent)),
		EstimatedTokens: fullEstimatedTokens,
		TotalLines:      totalLines,
		StoredTruncated: storedTruncated,
		InlineTruncated: true,
	})
	if err != nil {
		rt.session.debugf("spill tool output failed tool=%s error=%v", entry.id, err)
		return resp, nil
	}

	preview, previewTruncated := truncateUTF8StringToBytes(storedContent, toolOutputPreviewBytes)
	summaryPayload := map[string]any{
		"spilled":           true,
		"output_id":         output.OutputID,
		"tool_name":         entry.id,
		"size_bytes":        output.SizeBytes,
		"estimated_tokens":  output.EstimatedTokens,
		"total_lines":       output.TotalLines,
		"stored_truncated":  output.StoredTruncated,
		"inline_truncated":  true,
		"preview":           preview,
		"preview_truncated": previewTruncated,
		"message":           "Full output stored separately. Use view_tool_output or grep_tool_output with the output_id to inspect it.",
	}
	for key, value := range shape.Details {
		summaryPayload[key] = value
	}

	summaryBytes, err := json.MarshalIndent(summaryPayload, "", "  ")
	if err != nil {
		rt.session.debugf("spill summary marshal failed tool=%s error=%v", entry.id, err)
		return resp, nil
	}

	resp.Content = string(summaryBytes)
	resp.Metadata = mergeToolResponseMetadata(resp.Metadata, toolResponseClientMetadata{
		ToolOutputRef: &StoredToolOutputRef{
			OutputID:        output.OutputID,
			ToolCallID:      call.ID,
			SizeBytes:       output.SizeBytes,
			EstimatedTokens: output.EstimatedTokens,
			TotalLines:      output.TotalLines,
			StoredTruncated: output.StoredTruncated,
			InlineTruncated: true,
		},
	})
	rt.revealTools("view_tool_output", "grep_tool_output")
	return resp, nil
}

func parseToolResultClientMetadata(raw string) toolResponseClientMetadata {
	var metadata toolResponseClientMetadata
	if strings.TrimSpace(raw) == "" {
		return metadata
	}
	_ = json.Unmarshal([]byte(raw), &metadata)
	return metadata
}

func mergeToolResponseMetadata(existing string, patch toolResponseClientMetadata) string {
	metadata := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &metadata)
	}
	if patch.ServerName != "" {
		metadata["server_name"] = patch.ServerName
	}
	if patch.ToolName != "" {
		metadata["tool_name"] = patch.ToolName
	}
	if patch.ToolOutputRef != nil {
		metadata["tool_output_ref"] = patch.ToolOutputRef
	}
	if patch.PlanRef != nil {
		metadata["plan_ref"] = patch.PlanRef
	}
	if len(metadata) == 0 {
		return ""
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return existing
	}
	return string(payload)
}

// ansiCSIPattern matches CSI sequences (\x1b[ ... final byte) and OSC
// sequences (\x1b] ... BEL or ST). This covers SGR color codes, cursor
// movement, and terminal title sequences that bloat stored tool output.
var ansiCSIPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\x1b\][^\x1b]*\x1b\\`)

func stripANSI(s string) string {
	return ansiCSIPattern.ReplaceAllString(s, "")
}

func canonicalizeToolOutput(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = stripANSI(value)
	return value
}

func shapeSpilledToolContent(toolID, content string) spillContentShape {
	switch toolID {
	case "view":
		var payload struct {
			Path       string `json:"path"`
			Offset     int    `json:"offset"`
			Limit      int    `json:"limit"`
			StartLine  int    `json:"start_line"`
			EndLine    int    `json:"end_line"`
			TotalLines int    `json:"total_lines"`
			Content    string `json:"content"`
			Truncated  bool   `json:"truncated"`
		}
		if json.Unmarshal([]byte(content), &payload) == nil && payload.Content != "" {
			return spillContentShape{
				Content: payload.Content,
				Details: map[string]any{
					"source_tool":        toolID,
					"source_path":        payload.Path,
					"source_offset":      payload.Offset,
					"source_limit":       payload.Limit,
					"source_start_line":  payload.StartLine,
					"source_end_line":    payload.EndLine,
					"source_total_lines": payload.TotalLines,
					"source_truncated":   payload.Truncated,
				},
			}
		}
	case "web_fetch_get":
		var payload webFetchResponsePayload
		if json.Unmarshal([]byte(content), &payload) == nil {
			details := map[string]any{
				"source_tool":      toolID,
				"source_url":       payload.URL,
				"source_truncated": payload.Truncated,
			}
			if payload.Mode != "" {
				details["source_mode"] = payload.Mode
			}
			if payload.Title != "" {
				details["source_title"] = payload.Title
			}
			if payload.Excerpt != "" {
				details["source_excerpt"] = payload.Excerpt
			}
			if payload.Encoding != "" {
				details["source_encoding"] = payload.Encoding
			}
			if payload.Selector != "" {
				details["source_selector"] = payload.Selector
			}
			if payload.SavedToFilepath != "" {
				details["source_saved_to_filepath"] = payload.SavedToFilepath
			}
			return spillContentShape{
				Content: payload.Body,
				Details: details,
			}
		}
	}
	return spillContentShape{Content: content}
}

func truncateUTF8StringToBytes(value string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", len(value) > 0
	}
	if len([]byte(value)) <= maxBytes {
		return value, false
	}

	size := 0
	for index := range value {
		if size > maxBytes {
			cut := index
			for cut > 0 && !utf8.ValidString(value[:cut]) {
				cut--
			}
			return value[:cut], true
		}
		size = len([]byte(value[:index]))
	}
	for len(value) > 0 && len([]byte(value)) > maxBytes {
		_, width := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-width]
	}
	return value, true
}

func knowledgeLines(content string) []string {
	return knowledgeSplitLines(content)
}

func knowledgeSplitLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
