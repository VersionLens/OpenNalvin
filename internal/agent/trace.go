package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/fantasy"
)

type StoredTrace struct {
	SchemaVersion  int                  `json:"schema_version"`
	RunID          string               `json:"run_id"`
	Title          string               `json:"title"`
	Model          string               `json:"model"`
	Provider       string               `json:"provider,omitempty"`
	SystemPrompt   string               `json:"system_prompt,omitempty"`
	Todos          []StoredTodoItem     `json:"todos"`
	Metadata       StoredRunMeta        `json:"metadata"`
	Usage          StoredUsage          `json:"usage"`
	EstimatedUsage StoredEstimatedUsage `json:"estimated_usage"`
	Messages       []StoredMessage      `json:"messages"`
	Compactions    []StoredCompaction   `json:"compactions,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

type StoredCompaction struct {
	ID                  string            `json:"id"`
	CreatedAt           time.Time         `json:"created_at"`
	Trigger             string            `json:"trigger"`
	CoveredMessageCount int               `json:"covered_message_count"`
	PreTokens           int64             `json:"pre_tokens"`
	PostTokens          int64             `json:"post_tokens"`
	Summary             CompactionSummary `json:"summary"`
}

type CompactionSummary struct {
	Goal        string                    `json:"goal"`
	Constraints []string                  `json:"constraints,omitempty"`
	Decisions   []string                  `json:"decisions,omitempty"`
	Completed   []string                  `json:"completed,omitempty"`
	Open        []string                  `json:"open,omitempty"`
	Files       []string                  `json:"files,omitempty"`
	ToolOutputs []CompactionToolOutputRef `json:"tool_outputs,omitempty"`
}

type CompactionToolOutputRef struct {
	OutputID     string `json:"output_id"`
	ToolName     string `json:"tool_name"`
	WhyItMatters string `json:"why_it_matters"`
}

type StoredRunMeta struct {
	ParentRunID string          `json:"parent_run_id,omitempty"`
	RootRunID   string          `json:"root_run_id,omitempty"`
	TaskName    string          `json:"task_name,omitempty"`
	RunKind     string          `json:"run_kind,omitempty"`
	Mode        string          `json:"mode,omitempty"`
	PlanRef     StoredPlanRef   `json:"plan_ref,omitempty"`
	Tools       StoredToolState `json:"tools"`
}

type StoredTodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type StoredToolState struct {
	EnabledIDs  []string `json:"enabled_ids"`
	PinnedIDs   []string `json:"pinned_ids"`
	RevealedIDs []string `json:"revealed_ids"`
}

type StoredUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type StoredEstimatedUsage struct {
	SystemPrompt int64 `json:"system_prompt"`
	User         int64 `json:"user"`
	Assistant    int64 `json:"assistant"`
	Reasoning    int64 `json:"reasoning"`
	ToolCalls    int64 `json:"tool_calls"`
	ToolResults  int64 `json:"tool_results"`
	Total        int64 `json:"total"`
}

type StoredMessage struct {
	Role            string               `json:"role"`
	MessageID       string               `json:"message_id,omitempty"`
	Content         string               `json:"content,omitempty"`
	Reasoning       string               `json:"reasoning,omitempty"`
	ToolCalls       []StoredToolCall     `json:"tool_calls,omitempty"`
	ToolCallID      string               `json:"tool_call_id,omitempty"`
	ToolName        string               `json:"tool_name,omitempty"`
	ContentText     string               `json:"content_text,omitempty"`
	ContentJSON     any                  `json:"content_json,omitempty"`
	ToolOutputRef   *StoredToolOutputRef `json:"tool_output_ref,omitempty"`
	IsError         bool                 `json:"is_error,omitempty"`
	Ok              bool                 `json:"ok,omitempty"`
	EstimatedTokens int64                `json:"estimated_tokens,omitempty"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	EndedAt         *time.Time           `json:"ended_at,omitempty"`
}

type StoredToolOutputRef struct {
	OutputID        string `json:"output_id"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	SizeBytes       int    `json:"size_bytes"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	TotalLines      int    `json:"total_lines"`
	StoredTruncated bool   `json:"stored_truncated,omitempty"`
	InlineTruncated bool   `json:"inline_truncated,omitempty"`
}

type StoredToolCall struct {
	ID              string              `json:"id,omitempty"`
	Type            string              `json:"type,omitempty"`
	Function        *StoredToolFunction `json:"function,omitempty"`
	EstimatedTokens int64               `json:"estimated_tokens,omitempty"`
	StartedAt       *time.Time          `json:"started_at,omitempty"`
}

type StoredToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type TraceChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []TraceChoice `json:"choices,omitempty"`
	Error   *TraceError   `json:"error,omitempty"`
}

type TraceChoice struct {
	Index        int         `json:"index"`
	Delta        *TraceDelta `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type ChunkToolCall struct {
	Index    int                 `json:"index"`
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function *StoredToolFunction `json:"function,omitempty"`
}

type TraceDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Reasoning string          `json:"reasoning_content,omitempty"`
	ToolCalls []ChunkToolCall `json:"tool_calls,omitempty"`
}

type TraceError struct {
	Message string `json:"message"`
}

type traceBuilder struct {
	trace             StoredTrace
	assistantContent  strings.Builder
	assistantReason   strings.Builder
	assistantToolCall []StoredToolCall
	assistantStartAt  *time.Time
	toolCallStartAt   map[string]time.Time
}

func newTraceBuilder(trace StoredTrace) *traceBuilder {
	return &traceBuilder{trace: trace}
}

func parseStoredTrace(raw []byte) (StoredTrace, error) {
	if len(raw) == 0 {
		return StoredTrace{}, fmt.Errorf("stored trace is empty")
	}
	var trace StoredTrace
	if err := json.Unmarshal(raw, &trace); err != nil {
		return StoredTrace{}, fmt.Errorf("parse stored trace: %w", err)
	}
	if trace.SchemaVersion == 0 {
		trace.SchemaVersion = 2
	}
	trace.Provider = normalizeProviderName(trace.Provider)
	if trace.Messages == nil {
		trace.Messages = []StoredMessage{}
	}
	if trace.Todos == nil {
		trace.Todos = []StoredTodoItem{}
	}
	if trace.Metadata.RunKind == "" {
		trace.Metadata.RunKind = RunKindRoot
	}
	trace.Metadata.Mode = normalizeRunMode(trace.Metadata.Mode)
	trace.Metadata.PlanRef = normalizePlanRef(trace.Metadata.PlanRef)
	if trace.Metadata.RootRunID == "" && trace.RunID != "" {
		trace.Metadata.RootRunID = trace.RunID
	}
	return enrichStoredTrace(trace), nil
}

func ParseStoredTrace(raw []byte) (StoredTrace, error) {
	return parseStoredTrace(raw)
}

func (tb *traceBuilder) AddUserMessage(content string) {
	now := time.Now().UTC()
	tb.trace.Messages = append(tb.trace.Messages, StoredMessage{
		Role:      "user",
		MessageID: nextMessageID(len(tb.trace.Messages)),
		Content:   content,
		StartedAt: &now,
	})
}

func (tb *traceBuilder) OnTextDelta(text string) {
	if tb.assistantStartAt == nil {
		now := time.Now().UTC()
		tb.assistantStartAt = &now
	}
	tb.assistantContent.WriteString(text)
}

func (tb *traceBuilder) OnReasoningDelta(text string) {
	if tb.assistantStartAt == nil {
		now := time.Now().UTC()
		tb.assistantStartAt = &now
	}
	tb.assistantReason.WriteString(text)
}

func (tb *traceBuilder) OnToolCall(tc fantasy.ToolCallContent) {
	if tb.assistantStartAt == nil {
		now := time.Now().UTC()
		tb.assistantStartAt = &now
	}
	callStartAt := time.Now().UTC()
	if tb.toolCallStartAt == nil {
		tb.toolCallStartAt = make(map[string]time.Time)
	}
	tb.toolCallStartAt[tc.ToolCallID] = callStartAt
	tb.assistantToolCall = append(tb.assistantToolCall, StoredToolCall{
		ID:   tc.ToolCallID,
		Type: "function",
		Function: &StoredToolFunction{
			Name:      tc.ToolName,
			Arguments: tc.Input,
		},
		StartedAt: &callStartAt,
	})
}

func (tb *traceBuilder) OnToolResult(tr fantasy.ToolResultContent) {
	tb.FlushAssistant()

	now := time.Now().UTC()
	msg := StoredMessage{
		Role:       "tool",
		MessageID:  nextMessageID(len(tb.trace.Messages)),
		ToolCallID: tr.ToolCallID,
		ToolName:   tr.ToolName,
		Ok:         true,
		EndedAt:    &now,
	}
	if tb.toolCallStartAt != nil {
		if startAt, ok := tb.toolCallStartAt[tr.ToolCallID]; ok {
			msg.StartedAt = &startAt
			delete(tb.toolCallStartAt, tr.ToolCallID)
		}
	}
	if text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](tr.Result); ok {
		msg.ContentText = text.Text
		var parsed any
		if json.Unmarshal([]byte(text.Text), &parsed) == nil {
			msg.ContentJSON = parsed
		}
	} else {
		data, err := json.Marshal(tr.Result)
		if err == nil {
			msg.ContentText = string(data)
			msg.ContentJSON = json.RawMessage(data)
		}
	}
	if errText, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](tr.Result); ok {
		if errText.Error != nil {
			msg.ContentText = errText.Error.Error()
		}
		msg.IsError = true
		msg.Ok = false
	}
	if metadata := parseToolResultClientMetadata(tr.ClientMetadata); metadata.ToolOutputRef != nil {
		msg.ToolOutputRef = metadata.ToolOutputRef
	}
	tb.trace.Messages = append(tb.trace.Messages, msg)
}

func (tb *traceBuilder) FlushAssistant() {
	content := tb.assistantContent.String()
	reasoning := tb.assistantReason.String()
	if content == "" && reasoning == "" && len(tb.assistantToolCall) == 0 {
		return
	}
	now := time.Now().UTC()
	msg := StoredMessage{
		Role:      "assistant",
		MessageID: nextMessageID(len(tb.trace.Messages)),
		Content:   content,
		Reasoning: reasoning,
		StartedAt: tb.assistantStartAt,
		EndedAt:   &now,
	}
	if len(tb.assistantToolCall) > 0 {
		msg.ToolCalls = append([]StoredToolCall(nil), tb.assistantToolCall...)
	}
	tb.trace.Messages = append(tb.trace.Messages, msg)
	tb.assistantContent.Reset()
	tb.assistantReason.Reset()
	tb.assistantToolCall = nil
	tb.assistantStartAt = nil
}

func (tb *traceBuilder) SetUsage(usage fantasy.Usage) {
	tb.trace.Usage = StoredUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
	}
}

func (tb *traceBuilder) snapshot(now time.Time) StoredTrace {
	trace := tb.trace
	trace.Messages = append([]StoredMessage(nil), tb.trace.Messages...)
	if len(tb.trace.Compactions) > 0 {
		trace.Compactions = append([]StoredCompaction(nil), tb.trace.Compactions...)
	}

	content := tb.assistantContent.String()
	reasoning := tb.assistantReason.String()
	if content != "" || reasoning != "" || len(tb.assistantToolCall) > 0 {
		msg := StoredMessage{
			Role:      "assistant",
			MessageID: nextMessageID(len(trace.Messages)),
			Content:   content,
			Reasoning: reasoning,
			StartedAt: tb.assistantStartAt,
		}
		if len(tb.assistantToolCall) > 0 {
			msg.ToolCalls = append([]StoredToolCall(nil), tb.assistantToolCall...)
		}
		trace.Messages = append(trace.Messages, msg)
	}

	if trace.SchemaVersion == 0 {
		trace.SchemaVersion = 2
	}
	if trace.Metadata.RunKind == "" {
		trace.Metadata.RunKind = RunKindRoot
	}
	trace.Metadata.Mode = normalizeRunMode(trace.Metadata.Mode)
	trace.Metadata.PlanRef = normalizePlanRef(trace.Metadata.PlanRef)
	if trace.Metadata.RootRunID == "" && trace.RunID != "" {
		trace.Metadata.RootRunID = trace.RunID
	}
	if trace.Metadata.Tools.EnabledIDs == nil {
		trace.Metadata.Tools.EnabledIDs = []string{}
	}
	if trace.Metadata.Tools.PinnedIDs == nil {
		trace.Metadata.Tools.PinnedIDs = []string{}
	}
	if trace.Metadata.Tools.RevealedIDs == nil {
		trace.Metadata.Tools.RevealedIDs = []string{}
	}
	if trace.Todos == nil {
		trace.Todos = []StoredTodoItem{}
	}
	if trace.Compactions == nil {
		trace.Compactions = tb.trace.Compactions
	}
	trace = enrichStoredTrace(trace)
	trace.UpdatedAt = now.UTC()
	return trace
}

func (tb *traceBuilder) Snapshot(now time.Time) (StoredTrace, json.RawMessage, error) {
	trace := tb.snapshot(now)
	payload, err := json.Marshal(trace)
	if err != nil {
		return StoredTrace{}, nil, fmt.Errorf("marshal stored trace: %w", err)
	}
	return trace, payload, nil
}

func (tb *traceBuilder) Build(now time.Time) (StoredTrace, json.RawMessage, error) {
	tb.FlushAssistant()
	trace, payload, err := tb.Snapshot(now)
	if err != nil {
		return StoredTrace{}, nil, err
	}
	tb.trace = trace
	return tb.trace, payload, nil
}

func traceToFantasyMessages(trace StoredTrace) []fantasy.Message {
	messages := make([]fantasy.Message, 0, len(trace.Messages))
	for _, message := range trace.Messages {
		switch message.Role {
		case "user":
			messages = append(messages, fantasy.NewUserMessage(message.Content))
		case "assistant":
			parts := make([]fantasy.MessagePart, 0, 1+len(message.ToolCalls))
			if strings.TrimSpace(message.Content) != "" {
				parts = append(parts, fantasy.TextPart{Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				if call.Function == nil {
					continue
				}
				parts = append(parts, fantasy.ToolCallPart{
					ToolCallID: call.ID,
					ToolName:   call.Function.Name,
					Input:      call.Function.Arguments,
				})
			}
			if len(parts) == 0 {
				continue
			}
			messages = append(messages, fantasy.Message{
				Role:    fantasy.MessageRoleAssistant,
				Content: parts,
			})
		case "tool":
			output := fantasy.ToolResultOutputContentText{Text: message.ContentText}
			messages = append(messages, fantasy.Message{
				Role: fantasy.MessageRoleTool,
				Content: []fantasy.MessagePart{
					fantasy.ToolResultPart{
						ToolCallID: message.ToolCallID,
						Output:     output,
					},
				},
			})
		}
	}
	return messages
}

func storedMessagesFromFantasyMessages(history []fantasy.Message) []StoredMessage {
	stored := make([]StoredMessage, 0, len(history))
	for _, message := range history {
		item := StoredMessage{
			MessageID: nextMessageID(len(stored)),
		}
		switch message.Role {
		case fantasy.MessageRoleUser:
			item.Role = "user"
			item.Content = collectFantasyText(message.Content)
		case fantasy.MessageRoleAssistant:
			item.Role = "assistant"
			for _, part := range message.Content {
				if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
					item.Content += text.Text
					continue
				}
				if reasoning, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](part); ok {
					item.Reasoning += reasoning.Text
					continue
				}
				if call, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
					item.ToolCalls = append(item.ToolCalls, StoredToolCall{
						ID:   call.ToolCallID,
						Type: "function",
						Function: &StoredToolFunction{
							Name:      call.ToolName,
							Arguments: call.Input,
						},
					})
				}
			}
		case fantasy.MessageRoleTool:
			item.Role = "tool"
			for _, part := range message.Content {
				result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
				if !ok {
					continue
				}
				item.ToolCallID = result.ToolCallID
				item.Ok = true
				if text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output); ok {
					item.ContentText = text.Text
					var parsed any
					if json.Unmarshal([]byte(text.Text), &parsed) == nil {
						item.ContentJSON = parsed
					}
				}
				if errText, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](result.Output); ok {
					item.IsError = true
					item.Ok = false
					if errText.Error != nil {
						item.ContentText = errText.Error.Error()
					}
				}
				break
			}
		default:
			continue
		}
		stored = append(stored, item)
	}
	return stored
}

func collectFantasyText(parts []fantasy.MessagePart) string {
	var b strings.Builder
	for _, part := range parts {
		if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func initialChunk(runID, model string) TraceChunk {
	return TraceChunk{
		ID:      runID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []TraceChoice{{
			Index: 0,
			Delta: &TraceDelta{Role: "assistant"},
		}},
	}
}

func contentChunk(runID, model, content string) TraceChunk {
	return TraceChunk{
		ID:      runID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []TraceChoice{{
			Index: 0,
			Delta: &TraceDelta{Content: content},
		}},
	}
}

func reasoningChunk(runID, model, reasoning string) TraceChunk {
	return TraceChunk{
		ID:      runID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []TraceChoice{{
			Index: 0,
			Delta: &TraceDelta{Reasoning: reasoning},
		}},
	}
}

func toolCallChunk(runID, model string, call ChunkToolCall) TraceChunk {
	return TraceChunk{
		ID:      runID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []TraceChoice{{
			Index: 0,
			Delta: &TraceDelta{ToolCalls: []ChunkToolCall{call}},
		}},
	}
}

func finishChunk(runID, model, reason string) TraceChunk {
	return TraceChunk{
		ID:      runID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []TraceChoice{{
			Index:        0,
			Delta:        &TraceDelta{},
			FinishReason: &reason,
		}},
	}
}

func errorChunk(runID, model string, err error) TraceChunk {
	return TraceChunk{
		ID:      runID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Error:   &TraceError{Message: err.Error()},
	}
}

func deriveRunTitle(explicitTitle, prompt string) string {
	if title := strings.TrimSpace(explicitTitle); title != "" {
		return title
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "Agent Run"
	}
	prompt = strings.ReplaceAll(prompt, "\n", " ")
	runes := []rune(prompt)
	if len(runes) <= 72 {
		return prompt
	}
	return string(runes[:72]) + "..."
}

func nextMessageID(index int) string {
	return fmt.Sprintf("msg_%03d", index+1)
}

// formatElapsed formats a duration for debug/verbose output.
// Sub-second durations are shown in ms (e.g. "342ms"), longer ones in seconds
// with one decimal place (e.g. "1.2s").
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
