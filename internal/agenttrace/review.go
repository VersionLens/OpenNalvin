package agenttrace

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const (
	defaultPreviewChars = 220
	longPreviewChars    = 520
)

func BuildReview(ctx context.Context, store *knowledge.Store, loaded *LoadedTrace) Review {
	trace := loaded.Trace
	stats := ComputeStats(loaded)
	review := Review{
		SourceKind:      loaded.Kind,
		ID:              loaded.ID,
		SourceRunID:     sourceRunID(loaded),
		Title:           traceTitle(loaded),
		Model:           trace.Model,
		Provider:        trace.Provider,
		RunKind:         trace.Metadata.RunKind,
		TaskName:        trace.Metadata.TaskName,
		RequestedSkills: append([]string(nil), trace.Metadata.RequestedSkillNames...),
		EnabledTools:    append([]string(nil), trace.Metadata.Tools.EnabledIDs...),
		PinnedTools:     append([]string(nil), trace.Metadata.Tools.PinnedIDs...),
		RevealedTools:   append([]string(nil), trace.Metadata.Tools.RevealedIDs...),
		Skills:          activeSkillNames(trace),
		Stats:           stats,
		Timeline:        BuildTimeline(trace),
		Warnings:        ReviewWarnings(ctx, store, loaded),
		ExportPreview: ExportPreview{
			Format:              "tinker-sft",
			SystemMode:          "effective",
			ReasoningMode:       "include",
			MessageCount:        exportMessageCount(trace, "effective"),
			AssistantTrainTurns: stats.AssistantTurns,
			ToolMessages:        stats.ToolMessages,
			IncludesReasoning:   stats.ReasoningChars > 0,
		},
		CreatedAt: trace.CreatedAt,
		UpdatedAt: trace.UpdatedAt,
	}
	if loaded.Run != nil {
		review.Status = loaded.Run.Status
		review.Prompt = loaded.Run.Prompt
	}
	if loaded.Curation != nil {
		review.CurationStatus = loaded.Curation.Status
		if review.Status == "" {
			review.Status = loaded.Curation.Status
		}
	}
	return review
}

func BuildTimeline(trace agentpkg.StoredTrace) []TimelineItem {
	items := make([]TimelineItem, 0, len(trace.Messages))
	for index, message := range trace.Messages {
		item := TimelineItem{
			Index:           index + 1,
			MessageID:       message.MessageID,
			Role:            message.Role,
			EstimatedTokens: message.EstimatedTokens,
			DurationMs:      durationMs(message.StartedAt, message.EndedAt),
		}
		switch message.Role {
		case "user":
			item.Label = "User"
			item.Preview = preview(message.Content, defaultPreviewChars)
			item.FullContentLength = len(message.Content)
			if strings.Contains(message.Content, "<subagent_notification>") {
				item.Label = "Sub-agent notification"
			}
		case "assistant":
			item.Label = "Assistant"
			item.Preview = preview(message.Content, defaultPreviewChars)
			item.ReasoningPreview = preview(message.Reasoning, defaultPreviewChars)
			item.FullContentLength = len(message.Content) + len(message.Reasoning)
			for _, call := range message.ToolCalls {
				item.ToolCallIDs = append(item.ToolCallIDs, call.ID)
			}
			if len(message.ToolCalls) > 0 && strings.TrimSpace(message.Content) == "" {
				item.Label = fmt.Sprintf("Assistant tool request (%d)", len(message.ToolCalls))
			}
		case "tool":
			item.Label = "Tool result"
			item.ToolCallID = message.ToolCallID
			item.ToolName = message.ToolName
			item.IsError = message.IsError
			if message.ToolOutputRef != nil {
				item.SpilledOutputID = message.ToolOutputRef.OutputID
			}
			item.Preview = preview(toolMessageContent(message), defaultPreviewChars)
			item.FullContentLength = len(toolMessageContent(message))
		default:
			item.Label = message.Role
			item.Preview = preview(message.Content, defaultPreviewChars)
			item.FullContentLength = len(message.Content)
		}
		items = append(items, item)
	}
	return items
}

func FormatReview(review Review, loaded *LoadedTrace, view string) string {
	view = strings.TrimSpace(view)
	if view == "" {
		view = "validate"
	}
	var b strings.Builder
	switch view {
	case "summary":
		writeSummary(&b, review)
	case "timeline", "messages":
		writeTimeline(&b, review, view == "messages")
	case "tools":
		writeTools(&b, loaded.Trace)
	case "context":
		writeContext(&b, loaded.Trace)
	case "events":
		writeEvents(&b, loaded)
	default:
		writeSummary(&b, review)
		writeWarnings(&b, review)
		writeSkillsTools(&b, review)
		writeTimeline(&b, review, false)
		if review.Stats.ToolCalls > 0 {
			line(&b, "")
			writeTools(&b, loaded.Trace)
		}
		writeCompactions(&b, loaded.Trace)
		writeExportPreview(&b, review)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeSummary(b *strings.Builder, review Review) {
	line(b, "Trace %s (%s)", review.ID, review.SourceKind)
	if review.Title != "" {
		line(b, "  Title: %s", review.Title)
	}
	if review.SourceRunID != "" && review.SourceRunID != review.ID {
		line(b, "  Source run: %s", review.SourceRunID)
	}
	if review.Status != "" {
		line(b, "  Status: %s", review.Status)
	}
	if review.CurationStatus != "" {
		line(b, "  Curation: %s", review.CurationStatus)
	}
	line(b, "  Model: %s", fallback(review.Model, "(unknown)"))
	if review.Provider != "" {
		line(b, "  Provider: %s", review.Provider)
	}
	if review.Prompt != "" {
		line(b, "  Prompt: %s", preview(review.Prompt, longPreviewChars))
	}
	line(b, "  Messages: %d (%d assistant, %d tool)", review.Stats.Messages, review.Stats.AssistantTurns, review.Stats.ToolMessages)
	line(b, "  Tool calls: %d (%d errors, %d spilled)", review.Stats.ToolCalls, review.Stats.ToolErrors, review.Stats.SpilledOutputs)
	line(b, "  Reasoning: %d chars, %d estimated tokens", review.Stats.ReasoningChars, review.Stats.ReasoningTokens)
	line(b, "  Tokens: input=%d output=%d total=%d estimated_trace=%d", review.Stats.InputTokens, review.Stats.OutputTokens, review.Stats.TotalTokens, review.Stats.EstimatedTokens)
	if review.Stats.FinalAnswerPreview != "" {
		line(b, "  Final answer: %s", review.Stats.FinalAnswerPreview)
	}
}

func writeWarnings(b *strings.Builder, review Review) {
	if len(review.Warnings) == 0 {
		line(b, "\nReview notes: none")
		return
	}
	line(b, "\nReview notes:")
	for _, warning := range review.Warnings {
		line(b, "  - %s", warning)
	}
}

func writeSkillsTools(b *strings.Builder, review Review) {
	line(b, "\nSkills and tools:")
	if len(review.RequestedSkills) > 0 {
		line(b, "  Requested skills: %s", strings.Join(review.RequestedSkills, ", "))
	}
	if len(review.Skills) > 0 {
		line(b, "  Active skills: %s", strings.Join(review.Skills, ", "))
	}
	if len(review.EnabledTools) > 0 {
		line(b, "  Enabled tools: %s", strings.Join(review.EnabledTools, ", "))
	}
	if len(review.PinnedTools) > 0 {
		line(b, "  Pinned tools: %s", strings.Join(review.PinnedTools, ", "))
	}
	if len(review.RevealedTools) > 0 {
		line(b, "  Revealed tools: %s", strings.Join(review.RevealedTools, ", "))
	}
}

func writeTimeline(b *strings.Builder, review Review, verbose bool) {
	line(b, "\nTimeline:")
	for _, item := range review.Timeline {
		prefix := fmt.Sprintf("  %02d %-9s", item.Index, item.Role)
		meta := make([]string, 0, 4)
		if item.MessageID != "" {
			meta = append(meta, item.MessageID)
		}
		if item.ToolName != "" {
			meta = append(meta, item.ToolName)
		}
		if len(item.ToolCallIDs) > 0 {
			meta = append(meta, fmt.Sprintf("%d calls", len(item.ToolCallIDs)))
		}
		if item.IsError {
			meta = append(meta, "error")
		}
		if item.SpilledOutputID != "" {
			meta = append(meta, "spilled:"+item.SpilledOutputID)
		}
		line(b, "%s %s%s", prefix, item.Label, formatMeta(meta))
		if item.ReasoningPreview != "" {
			line(b, "     reasoning: %s", item.ReasoningPreview)
		}
		if item.Preview != "" {
			limit := defaultPreviewChars
			if verbose {
				limit = longPreviewChars
			}
			line(b, "     %s", preview(item.Preview, limit))
		}
		if item.SpilledOutputID != "" {
			line(b, "     inspect: nalvin agent traces output %s %s", review.SourceRunID, item.SpilledOutputID)
		}
	}
}

func writeTools(b *strings.Builder, trace agentpkg.StoredTrace) {
	line(b, "Tool calls:")
	toolResults := map[string]agentpkg.StoredMessage{}
	for _, message := range trace.Messages {
		if message.Role == "tool" && message.ToolCallID != "" {
			toolResults[message.ToolCallID] = message
		}
	}
	count := 0
	for _, message := range trace.Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.ToolCalls {
			count++
			name := ""
			args := ""
			if call.Function != nil {
				name = call.Function.Name
				args = call.Function.Arguments
			}
			line(b, "  %02d %s %s", count, fallback(name, "(unknown)"), fallback(call.ID, "(no id)"))
			if args != "" {
				line(b, "     args: %s", preview(args, longPreviewChars))
			}
			if result, ok := toolResults[call.ID]; ok {
				status := "ok"
				if result.IsError {
					status = "error"
				}
				line(b, "     result: %s %s", status, preview(toolMessageContent(result), defaultPreviewChars))
				if result.ToolOutputRef != nil {
					line(b, "     spilled: %s (%d lines, %d bytes)", result.ToolOutputRef.OutputID, result.ToolOutputRef.TotalLines, result.ToolOutputRef.SizeBytes)
				}
			} else {
				line(b, "     result: missing")
			}
		}
	}
	if count == 0 {
		line(b, "  none")
	}
}

func writeContext(b *strings.Builder, trace agentpkg.StoredTrace) {
	line(b, "Context:")
	line(b, "  Raw system prompt: %d chars", len(trace.SystemPrompt))
	if strings.TrimSpace(trace.SystemPrompt) != "" {
		line(b, "    %s", preview(trace.SystemPrompt, longPreviewChars))
	}
	line(b, "  Effective system prompt: %d chars", len(trace.EffectiveSystemPrompt))
	if strings.TrimSpace(trace.EffectiveSystemPrompt) != "" {
		line(b, "    %s", preview(trace.EffectiveSystemPrompt, longPreviewChars))
	}
	writeCompactions(b, trace)
}

func writeCompactions(b *strings.Builder, trace agentpkg.StoredTrace) {
	if len(trace.Compactions) == 0 {
		return
	}
	line(b, "\nCompactions:")
	for _, c := range trace.Compactions {
		line(b, "  %s %s covered=%d tokens=%d->%d goal=%s", c.ID, c.Trigger, c.CoveredMessageCount, c.PreTokens, c.PostTokens, preview(c.Summary.Goal, defaultPreviewChars))
	}
}

func writeEvents(b *strings.Builder, loaded *LoadedTrace) {
	line(b, "Run events:")
	if len(loaded.Events) == 0 {
		line(b, "  none")
	}
	for _, event := range loaded.Events {
		line(b, "  #%d %s %s", event.EventID, event.Kind, event.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	if len(loaded.CurationEvents) > 0 {
		line(b, "\nCuration events:")
		for _, event := range loaded.CurationEvents {
			line(b, "  #%d %s %s %s", event.EventID, event.Kind, event.CreatedAt.Format("2006-01-02 15:04:05"), preview(string(event.Payload), defaultPreviewChars))
		}
	}
}

func writeExportPreview(b *strings.Builder, review Review) {
	line(b, "\nExport preview:")
	line(b, "  Format: %s", review.ExportPreview.Format)
	line(b, "  System: %s", review.ExportPreview.SystemMode)
	line(b, "  Reasoning: %s", review.ExportPreview.ReasoningMode)
	line(b, "  JSONL messages: %d", review.ExportPreview.MessageCount)
	line(b, "  Assistant train turns: %d", review.ExportPreview.AssistantTrainTurns)
}

func ReviewWarnings(ctx context.Context, store *knowledge.Store, loaded *LoadedTrace) []string {
	trace := loaded.Trace
	warnings := make([]string, 0)
	if loaded.Run != nil && loaded.Run.Status != "completed" {
		warnings = append(warnings, "source run status is "+loaded.Run.Status)
	}
	if lastAssistantContent(trace) == "" {
		warnings = append(warnings, "no visible final assistant answer")
	}
	pendingCalls := map[string]string{}
	for _, message := range trace.Messages {
		switch message.Role {
		case "assistant":
			for _, call := range message.ToolCalls {
				if call.ID == "" {
					warnings = append(warnings, "assistant tool call is missing an id")
				}
				name := ""
				if call.Function != nil {
					name = call.Function.Name
					if strings.TrimSpace(call.Function.Arguments) != "" && json.Unmarshal([]byte(call.Function.Arguments), &map[string]any{}) != nil {
						warnings = append(warnings, "tool call "+fallback(call.ID, name)+" has non-object or invalid JSON arguments")
					}
				}
				if call.ID != "" {
					pendingCalls[call.ID] = name
				}
			}
		case "tool":
			if message.ToolCallID == "" {
				warnings = append(warnings, "tool result message "+fallback(message.MessageID, strconv.Itoa(len(warnings)))+" has no tool_call_id")
			} else if _, ok := pendingCalls[message.ToolCallID]; ok {
				delete(pendingCalls, message.ToolCallID)
			} else {
				warnings = append(warnings, "tool result "+message.ToolCallID+" has no matching assistant call in the trace")
			}
			if message.IsError {
				warnings = append(warnings, "tool "+fallback(message.ToolName, message.ToolCallID)+" returned an error")
			}
			if message.ToolOutputRef != nil && store != nil {
				runID := sourceRunID(loaded)
				if runID != "" {
					if _, err := store.GetAgentRunToolOutput(ctx, runID, message.ToolOutputRef.OutputID); err != nil {
						warnings = append(warnings, "spilled output "+message.ToolOutputRef.OutputID+" could not be loaded")
					}
				}
			}
		}
	}
	for callID, name := range pendingCalls {
		warnings = append(warnings, "tool call "+fallback(name, callID)+" has no matching tool result")
	}
	return warnings
}

func activeSkillNames(trace agentpkg.StoredTrace) []string {
	names := make([]string, 0, len(trace.Metadata.Skills.Active))
	for _, skill := range trace.Metadata.Skills.Active {
		if strings.TrimSpace(skill.Name) != "" {
			names = append(names, skill.Name)
		}
	}
	return names
}

func toolMessageContent(message agentpkg.StoredMessage) string {
	if message.ContentText != "" {
		return message.ContentText
	}
	if message.ContentJSON != nil {
		data, _ := json.Marshal(message.ContentJSON)
		return string(data)
	}
	return message.Content
}

func preview(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.ReplaceAll(value, "\x00", "")), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "..."
}

func line(b *strings.Builder, format string, args ...any) {
	_, _ = fmt.Fprintf(b, format+"\n", args...)
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return strings.TrimSpace(value)
}

func formatMeta(items []string) string {
	clean := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			clean = append(clean, item)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return " [" + strings.Join(clean, ", ") + "]"
}

func durationMs(startedAt, endedAt *time.Time) int64 {
	if startedAt == nil || endedAt == nil {
		return 0
	}
	ms := endedAt.Sub(*startedAt).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func exportMessageCount(trace agentpkg.StoredTrace, systemMode string) int {
	count := len(trace.Messages)
	if systemPromptForMode(trace, systemMode) != "" {
		count++
	}
	return count
}
