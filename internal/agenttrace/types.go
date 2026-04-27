package agenttrace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const (
	SourceKindRun      = "run"
	SourceKindCuration = "curation"
)

type LoadedTrace struct {
	Kind           string                              `json:"kind"`
	ID             string                              `json:"id"`
	Run            *knowledge.AgentRun                 `json:"run,omitempty"`
	Curation       *knowledge.AgentTraceCuration       `json:"curation,omitempty"`
	Trace          agentpkg.StoredTrace                `json:"trace"`
	RawTrace       json.RawMessage                     `json:"raw_trace,omitempty"`
	Events         []knowledge.AgentRunEvent           `json:"events,omitempty"`
	CurationEvents []knowledge.AgentTraceCurationEvent `json:"curation_events,omitempty"`
}

type TimelineItem struct {
	Index             int      `json:"index"`
	MessageID         string   `json:"message_id,omitempty"`
	Role              string   `json:"role"`
	Label             string   `json:"label"`
	Preview           string   `json:"preview,omitempty"`
	ReasoningPreview  string   `json:"reasoning_preview,omitempty"`
	ToolCallIDs       []string `json:"tool_call_ids,omitempty"`
	ToolName          string   `json:"tool_name,omitempty"`
	ToolCallID        string   `json:"tool_call_id,omitempty"`
	IsError           bool     `json:"is_error,omitempty"`
	SpilledOutputID   string   `json:"spilled_output_id,omitempty"`
	EstimatedTokens   int64    `json:"estimated_tokens,omitempty"`
	DurationMs        int64    `json:"duration_ms,omitempty"`
	FullContentLength int      `json:"full_content_length,omitempty"`
}

type Stats struct {
	Messages           int    `json:"messages"`
	UserMessages       int    `json:"user_messages"`
	AssistantTurns     int    `json:"assistant_turns"`
	ToolMessages       int    `json:"tool_messages"`
	ToolCalls          int    `json:"tool_calls"`
	ToolErrors         int    `json:"tool_errors"`
	SpilledOutputs     int    `json:"spilled_outputs"`
	Compactions        int    `json:"compactions"`
	SubagentNotices    int    `json:"subagent_notices"`
	ReasoningChars     int    `json:"reasoning_chars"`
	ReasoningTokens    int64  `json:"reasoning_tokens"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
	EstimatedTokens    int64  `json:"estimated_tokens"`
	DurationMs         int    `json:"duration_ms"`
	FinalAnswerChars   int    `json:"final_answer_chars"`
	FinalAnswerPreview string `json:"final_answer_preview,omitempty"`
}

type Review struct {
	SourceKind      string         `json:"source_kind"`
	ID              string         `json:"id"`
	SourceRunID     string         `json:"source_run_id,omitempty"`
	Title           string         `json:"title"`
	Status          string         `json:"status"`
	CurationStatus  string         `json:"curation_status,omitempty"`
	Model           string         `json:"model"`
	Provider        string         `json:"provider,omitempty"`
	RunKind         string         `json:"run_kind,omitempty"`
	TaskName        string         `json:"task_name,omitempty"`
	Prompt          string         `json:"prompt,omitempty"`
	Skills          []string       `json:"skills,omitempty"`
	RequestedSkills []string       `json:"requested_skills,omitempty"`
	EnabledTools    []string       `json:"enabled_tools,omitempty"`
	PinnedTools     []string       `json:"pinned_tools,omitempty"`
	RevealedTools   []string       `json:"revealed_tools,omitempty"`
	Stats           Stats          `json:"stats"`
	Timeline        []TimelineItem `json:"timeline"`
	Warnings        []string       `json:"warnings,omitempty"`
	ExportPreview   ExportPreview  `json:"export_preview"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type ExportPreview struct {
	Format              string `json:"format"`
	SystemMode          string `json:"system_mode"`
	ReasoningMode       string `json:"reasoning_mode"`
	MessageCount        int    `json:"message_count"`
	AssistantTrainTurns int    `json:"assistant_train_turns"`
	ToolMessages        int    `json:"tool_messages"`
	IncludesReasoning   bool   `json:"includes_reasoning"`
}

type ReviewOptions struct {
	View string
}

func LoadRun(ctx context.Context, store *knowledge.Store, runID string) (*LoadedTrace, error) {
	if store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	run, err := store.GetAgentRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	trace, err := agentpkg.ParseStoredTrace(run.Trace)
	if err != nil {
		return nil, err
	}
	events, _ := store.ListAgentRunEventsAfter(ctx, run.ID, 0, 1000)
	return &LoadedTrace{
		Kind:     SourceKindRun,
		ID:       run.ID,
		Run:      run,
		Trace:    trace,
		RawTrace: append(json.RawMessage(nil), run.Trace...),
		Events:   events,
	}, nil
}

func LoadCuration(ctx context.Context, store *knowledge.Store, curationID string) (*LoadedTrace, error) {
	if store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	curation, err := store.GetAgentTraceCuration(ctx, strings.TrimSpace(curationID))
	if err != nil {
		return nil, err
	}
	trace, err := agentpkg.ParseStoredTrace(curation.Trace)
	if err != nil {
		return nil, err
	}
	var run *knowledge.AgentRun
	if curation.SourceRunID != "" {
		run, _ = store.GetAgentRun(ctx, curation.SourceRunID)
	}
	var events []knowledge.AgentRunEvent
	if run != nil {
		events, _ = store.ListAgentRunEventsAfter(ctx, run.ID, 0, 1000)
	}
	curationEvents, _ := store.ListAgentTraceCurationEvents(ctx, curation.ID, 1000)
	return &LoadedTrace{
		Kind:           SourceKindCuration,
		ID:             curation.ID,
		Run:            run,
		Curation:       curation,
		Trace:          trace,
		RawTrace:       append(json.RawMessage(nil), curation.Trace...),
		Events:         events,
		CurationEvents: curationEvents,
	}, nil
}

func Resolve(ctx context.Context, store *knowledge.Store, id string, curatedOnly bool) (*LoadedTrace, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("trace id is required")
	}
	if curatedOnly {
		return LoadCuration(ctx, store, id)
	}
	loaded, err := LoadRun(ctx, store, id)
	if err == nil {
		return loaded, nil
	}
	if !errors.Is(err, knowledge.ErrNotFound) {
		return nil, err
	}
	return LoadCuration(ctx, store, id)
}

func SourceTraceHash(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func traceTitle(loaded *LoadedTrace) string {
	if loaded == nil {
		return ""
	}
	if loaded.Curation != nil && strings.TrimSpace(loaded.Curation.Title) != "" {
		return strings.TrimSpace(loaded.Curation.Title)
	}
	if strings.TrimSpace(loaded.Trace.Title) != "" {
		return strings.TrimSpace(loaded.Trace.Title)
	}
	if loaded.Run != nil {
		return strings.TrimSpace(loaded.Run.Title)
	}
	return ""
}

func sourceRunID(loaded *LoadedTrace) string {
	if loaded == nil {
		return ""
	}
	if loaded.Curation != nil {
		return strings.TrimSpace(loaded.Curation.SourceRunID)
	}
	if loaded.Run != nil {
		return strings.TrimSpace(loaded.Run.ID)
	}
	return strings.TrimSpace(loaded.Trace.RunID)
}
