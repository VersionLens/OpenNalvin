package agent

import (
	"io"

	"charm.land/fantasy"
)

const (
	RunKindRoot  = "root"
	RunKindChild = "child"
)

const (
	RunModeDefault = "default"
	RunModePlan    = "plan"
)

const (
	PlanStatusActive   = "active"
	PlanStatusReady    = "ready"
	PlanStatusApproved = "approved"
)

const (
	AgentStatusIdle      = "idle"
	AgentStatusQueued    = "queued"
	AgentStatusRunning   = "running"
	AgentStatusCompleted = "completed"
	AgentStatusFailed    = "failed"
	AgentStatusAborted   = "aborted"
	AgentStatusClosed    = "closed"
)

type ToolSelection struct {
	EnabledToolIDs []string
	PinnedToolIDs  []string
	EnableToolIDs  []string
	DisableToolIDs []string
	PinToolIDs     []string
	UnpinToolIDs   []string
}

type RunRequest struct {
	Message             string
	RunID               string
	Title               string
	ProviderName        string
	SystemPrompt        string
	History             []fantasy.Message
	ParentRunID         string
	RootRunID           string
	TaskName            string
	RunKind             string
	Mode                string
	PlanRef             StoredPlanRef
	Tools               ToolSelection
	RequestedSkillNames []string
}

// ToolResultEvent carries tool result data for streaming consumers.
type ToolResultEvent struct {
	ToolCallID string
	ToolName   string
	Output     string
	IsError    bool
	OutputRef  *StoredToolOutputRef
	PlanRef    *StoredPlanRef
}

// RunOptions configures a direct remote agent call.
type RunOptions struct {
	Out          io.Writer
	Status       io.Writer
	Debug        io.Writer
	Verbose      bool
	OnChunk      func(TraceChunk) error
	OnToolResult func(ToolResultEvent) error

	// ContextWindowOverride, if positive, overrides the provider's
	// context_window_tokens for this run. Useful for testing compaction.
	ContextWindowOverride int
}

type ToolSchema struct {
	Defs                 map[string]any `json:"$defs,omitempty"`
	Type                 string         `json:"type"`
	Properties           map[string]any `json:"properties"`
	Required             []string       `json:"required"`
	AdditionalProperties any            `json:"additional_properties,omitempty"`
}

type ToolDescriptor struct {
	ID             string     `json:"id"`
	Description    string     `json:"description,omitempty"`
	Keywords       []string   `json:"keywords,omitempty"`
	Source         string     `json:"source"`
	ServerName     string     `json:"server_name,omitempty"`
	Enabled        bool       `json:"enabled"`
	Pinned         bool       `json:"pinned"`
	Visible        bool       `json:"visible"`
	DefaultEnabled bool       `json:"default_enabled"`
	DefaultPinned  bool       `json:"default_pinned"`
	Schema         ToolSchema `json:"schema"`
}

type ToolCatalogResult struct {
	Tools    []ToolDescriptor `json:"tools"`
	Warnings []string         `json:"warnings,omitempty"`
}

type ToolRunResult struct {
	ToolID  string `json:"tool_id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}
