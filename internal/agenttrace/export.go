package agenttrace

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const (
	ReasoningInclude      = "include"
	ReasoningOmit         = "omit"
	ReasoningMetadataOnly = "metadata-only"
)

type ExportOptions struct {
	Format                string
	OutDir                string
	SystemMode            string
	ReasoningMode         string
	MaterializeToolOutput bool
	ToolOutputMaxBytes    int
}

type ExportResult struct {
	Format           string   `json:"format"`
	OutDir           string   `json:"out_dir"`
	ConversationPath string   `json:"conversation_path"`
	ManifestPath     string   `json:"manifest_path"`
	Records          int      `json:"records"`
	Warnings         []string `json:"warnings,omitempty"`
}

type exportRecord struct {
	Messages []exportMessage `json:"messages"`
	Metadata map[string]any  `json:"agent_metadata,omitempty"`
}

type exportMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"`
	ToolCalls  []exportToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type exportToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type"`
	Function exportToolFunction `json:"function"`
}

type exportToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func Export(ctx context.Context, store *knowledge.Store, loaded []*LoadedTrace, opts ExportOptions) (ExportResult, error) {
	if strings.TrimSpace(opts.Format) == "" {
		opts.Format = "tinker-sft"
	}
	if opts.Format != "tinker-sft" {
		return ExportResult{}, fmt.Errorf("unsupported export format %q", opts.Format)
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return ExportResult{}, fmt.Errorf("--out is required")
	}
	if opts.SystemMode == "" {
		opts.SystemMode = "effective"
	}
	switch opts.SystemMode {
	case "raw", "effective", "none":
	default:
		return ExportResult{}, fmt.Errorf("unsupported system mode %q", opts.SystemMode)
	}
	if opts.ReasoningMode == "" {
		opts.ReasoningMode = ReasoningInclude
	}
	switch opts.ReasoningMode {
	case ReasoningInclude, ReasoningOmit, ReasoningMetadataOnly:
	default:
		return ExportResult{}, fmt.Errorf("unsupported reasoning mode %q", opts.ReasoningMode)
	}
	if opts.ToolOutputMaxBytes <= 0 {
		opts.ToolOutputMaxBytes = 1024 * 1024
	}
	if len(loaded) == 0 {
		return ExportResult{}, fmt.Errorf("no traces selected for export")
	}

	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return ExportResult{}, fmt.Errorf("create export directory: %w", err)
	}
	conversationPath := filepath.Join(opts.OutDir, "conversations.jsonl")
	manifestPath := filepath.Join(opts.OutDir, "manifest.json")
	file, err := os.Create(conversationPath)
	if err != nil {
		return ExportResult{}, fmt.Errorf("create conversations jsonl: %w", err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)

	result := ExportResult{
		Format:           opts.Format,
		OutDir:           opts.OutDir,
		ConversationPath: conversationPath,
		ManifestPath:     manifestPath,
	}
	manifestRecords := make([]map[string]any, 0, len(loaded))
	for _, item := range loaded {
		record, warnings, err := buildExportRecord(ctx, store, item, opts)
		if err != nil {
			return result, err
		}
		result.Warnings = append(result.Warnings, warnings...)
		data, err := json.Marshal(record)
		if err != nil {
			return result, fmt.Errorf("marshal export record %s: %w", item.ID, err)
		}
		if _, err := writer.Write(data); err != nil {
			return result, err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return result, err
		}
		result.Records++
		manifestRecords = append(manifestRecords, map[string]any{
			"id":            item.ID,
			"source_kind":   item.Kind,
			"source_run_id": sourceRunID(item),
			"title":         traceTitle(item),
			"warnings":      warnings,
		})
	}
	if err := writer.Flush(); err != nil {
		return result, fmt.Errorf("flush conversations jsonl: %w", err)
	}
	manifest := map[string]any{
		"format":              opts.Format,
		"system_mode":         opts.SystemMode,
		"reasoning_mode":      opts.ReasoningMode,
		"materialize_outputs": opts.MaterializeToolOutput,
		"records":             manifestRecords,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(manifestPath, append(manifestData, '\n'), 0o644); err != nil {
		return result, fmt.Errorf("write manifest: %w", err)
	}
	return result, nil
}

func buildExportRecord(ctx context.Context, store *knowledge.Store, loaded *LoadedTrace, opts ExportOptions) (exportRecord, []string, error) {
	trace := loaded.Trace
	messages := make([]exportMessage, 0, len(trace.Messages)+1)
	warnings := make([]string, 0)
	if system := systemPromptForMode(trace, opts.SystemMode); system != "" {
		messages = append(messages, exportMessage{Role: "system", Content: system})
	}
	for _, message := range trace.Messages {
		switch message.Role {
		case "user":
			messages = append(messages, exportMessage{Role: "user", Content: message.Content})
		case "assistant":
			out := exportMessage{Role: "assistant", Content: assistantExportContent(message, opts.ReasoningMode)}
			for _, call := range message.ToolCalls {
				if call.Function == nil {
					warnings = append(warnings, "assistant tool call "+call.ID+" has no function")
					continue
				}
				out.ToolCalls = append(out.ToolCalls, exportToolCall{
					ID:   call.ID,
					Type: fallback(call.Type, "function"),
					Function: exportToolFunction{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				})
			}
			messages = append(messages, out)
		case "tool":
			content := toolMessageContent(message)
			if opts.MaterializeToolOutput && message.ToolOutputRef != nil && store != nil {
				output, err := store.GetAgentRunToolOutput(ctx, sourceRunID(loaded), message.ToolOutputRef.OutputID)
				if err != nil {
					warnings = append(warnings, "could not materialize spilled output "+message.ToolOutputRef.OutputID+": "+err.Error())
				} else {
					content = output.Content
					if len([]byte(content)) > opts.ToolOutputMaxBytes {
						content = string([]byte(content)[:opts.ToolOutputMaxBytes])
						warnings = append(warnings, "materialized output "+message.ToolOutputRef.OutputID+" truncated to cap")
					}
				}
			}
			messages = append(messages, exportMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: message.ToolCallID,
				Name:       message.ToolName,
			})
		default:
			warnings = append(warnings, "skipped unsupported message role "+message.Role)
		}
	}
	metadata := exportMetadata(loaded, opts, warnings)
	return exportRecord{Messages: messages, Metadata: metadata}, warnings, nil
}

func assistantExportContent(message agentpkg.StoredMessage, reasoningMode string) any {
	switch reasoningMode {
	case ReasoningOmit, ReasoningMetadataOnly:
		return message.Content
	default:
		parts := make([]map[string]string, 0, 2)
		if strings.TrimSpace(message.Reasoning) != "" {
			parts = append(parts, map[string]string{"type": "thinking", "thinking": message.Reasoning})
		}
		if strings.TrimSpace(message.Content) != "" {
			parts = append(parts, map[string]string{"type": "text", "text": message.Content})
		}
		if len(parts) == 0 {
			return ""
		}
		return parts
	}
}

func exportMetadata(loaded *LoadedTrace, opts ExportOptions, warnings []string) map[string]any {
	trace := loaded.Trace
	stats := ComputeStats(loaded)
	metadata := map[string]any{
		"source_kind":               loaded.Kind,
		"id":                        loaded.ID,
		"source_run_id":             sourceRunID(loaded),
		"title":                     traceTitle(loaded),
		"model":                     trace.Model,
		"provider":                  trace.Provider,
		"run_kind":                  trace.Metadata.RunKind,
		"task_name":                 trace.Metadata.TaskName,
		"system_mode":               opts.SystemMode,
		"reasoning_mode":            opts.ReasoningMode,
		"materialized_tool_outputs": opts.MaterializeToolOutput,
		"tool_output_max_bytes":     opts.ToolOutputMaxBytes,
		"stats":                     stats,
		"warnings":                  warnings,
		"requested_skills":          trace.Metadata.RequestedSkillNames,
		"enabled_tools":             trace.Metadata.Tools.EnabledIDs,
		"pinned_tools":              trace.Metadata.Tools.PinnedIDs,
		"revealed_tools":            trace.Metadata.Tools.RevealedIDs,
	}
	if opts.ReasoningMode == ReasoningMetadataOnly || opts.ReasoningMode == ReasoningOmit {
		reasoning := map[string]string{}
		for _, message := range trace.Messages {
			if message.Role == "assistant" && strings.TrimSpace(message.Reasoning) != "" {
				reasoning[message.MessageID] = message.Reasoning
			}
		}
		if len(reasoning) > 0 {
			metadata["assistant_reasoning"] = reasoning
		}
	}
	if loaded.Run != nil {
		metadata["run_status"] = loaded.Run.Status
		metadata["prompt"] = loaded.Run.Prompt
	}
	if loaded.Curation != nil {
		metadata["curation_id"] = loaded.Curation.ID
		metadata["curation_status"] = loaded.Curation.Status
		metadata["quality"] = loaded.Curation.Quality
		metadata["reward"] = loaded.Curation.Reward
		metadata["split"] = loaded.Curation.Split
		metadata["tags"] = loaded.Curation.Tags
		metadata["notes"] = loaded.Curation.Notes
	}
	return metadata
}

func systemPromptForMode(trace agentpkg.StoredTrace, mode string) string {
	switch strings.TrimSpace(mode) {
	case "none":
		return ""
	case "raw":
		return strings.TrimSpace(trace.SystemPrompt)
	default:
		if strings.TrimSpace(trace.EffectiveSystemPrompt) != "" {
			return strings.TrimSpace(trace.EffectiveSystemPrompt)
		}
		return strings.TrimSpace(trace.SystemPrompt)
	}
}
