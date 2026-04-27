package knowledge

import (
	"encoding/json"
	"time"
)

type Node struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Content    string    `json:"content,omitempty"`
	Attributes string    `json:"attributes"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type NodeInput struct {
	Kind       string
	Name       string
	Content    string
	Attributes string
}

type Edge struct {
	ID           string    `json:"id"`
	SourceNodeID string    `json:"source_node_id"`
	TargetNodeID string    `json:"target_node_id"`
	Relation     string    `json:"relation"`
	Attributes   string    `json:"attributes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type EdgeInput struct {
	SourceNodeID string
	TargetNodeID string
	Relation     string
	Attributes   string
}

type EdgeFilter struct {
	NodeID   string
	Relation string
	Limit    int
}

type Graph struct {
	Node  *Node  `json:"node,omitempty"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type SchedulerState struct {
	Task            string          `json:"task"`
	Category        string          `json:"category"`
	Status          string          `json:"status"`
	RunKey          string          `json:"run_key"`
	WorkerID        string          `json:"worker_id"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	NextScheduledAt *time.Time      `json:"next_scheduled_at,omitempty"`
	Summary         json.RawMessage `json:"summary,omitempty"`
	LastError       string          `json:"last_error,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
