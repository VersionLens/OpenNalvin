package agenttrace

import (
	"strings"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
)

func ComputeStats(loaded *LoadedTrace) Stats {
	if loaded == nil {
		return Stats{}
	}
	trace := loaded.Trace
	stats := Stats{
		Messages:        len(trace.Messages),
		Compactions:     len(trace.Compactions),
		InputTokens:     trace.Usage.InputTokens,
		OutputTokens:    trace.Usage.OutputTokens,
		TotalTokens:     trace.Usage.TotalTokens,
		EstimatedTokens: trace.EstimatedUsage.Total,
		ReasoningTokens: trace.EstimatedUsage.Reasoning,
	}
	if loaded.Run != nil {
		stats.DurationMs = loaded.Run.DurationMs
		if stats.TotalTokens == 0 {
			stats.TotalTokens = loaded.Run.TotalTokens
		}
	}
	for _, message := range trace.Messages {
		switch message.Role {
		case "user":
			stats.UserMessages++
			if strings.Contains(message.Content, "<subagent_notification>") {
				stats.SubagentNotices++
			}
		case "assistant":
			stats.AssistantTurns++
			stats.ToolCalls += len(message.ToolCalls)
			stats.ReasoningChars += len(message.Reasoning)
		case "tool":
			stats.ToolMessages++
			if message.IsError {
				stats.ToolErrors++
			}
			if message.ToolOutputRef != nil {
				stats.SpilledOutputs++
			}
		}
	}
	final := lastAssistantContent(trace)
	stats.FinalAnswerChars = len(final)
	stats.FinalAnswerPreview = preview(final, defaultPreviewChars)
	return stats
}

func lastAssistantContent(trace agentpkg.StoredTrace) string {
	for i := len(trace.Messages) - 1; i >= 0; i-- {
		if trace.Messages[i].Role == "assistant" && strings.TrimSpace(trace.Messages[i].Content) != "" {
			return strings.TrimSpace(trace.Messages[i].Content)
		}
	}
	return ""
}
