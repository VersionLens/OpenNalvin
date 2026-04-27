package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AgentRun struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	Model             string          `json:"model"`
	Provider          string          `json:"provider,omitempty"`
	Prompt            string          `json:"prompt"`
	ParentRunID       string          `json:"parent_run_id,omitempty"`
	RootRunID         string          `json:"root_run_id,omitempty"`
	TaskName          string          `json:"task_name,omitempty"`
	RunKind           string          `json:"run_kind"`
	Status            string          `json:"status"`
	Error             string          `json:"error,omitempty"`
	DurationMs        int             `json:"duration_ms"`
	MessageCount      int             `json:"message_count"`
	InputTokens       int64           `json:"input_tokens"`
	OutputTokens      int64           `json:"output_tokens"`
	TotalTokens       int64           `json:"total_tokens"`
	LastEventID       int64           `json:"last_event_id"`
	ActiveTurnID      string          `json:"active_turn_id,omitempty"`
	QueueJobID        int64           `json:"queue_job_id,omitempty"`
	WorkerID          string          `json:"worker_id,omitempty"`
	CancelRequestedAt *time.Time      `json:"cancel_requested_at,omitempty"`
	Trace             json.RawMessage `json:"trace"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type AgentRunSummary struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	Model             string     `json:"model"`
	Provider          string     `json:"provider,omitempty"`
	Prompt            string     `json:"prompt"`
	ParentRunID       string     `json:"parent_run_id,omitempty"`
	RootRunID         string     `json:"root_run_id,omitempty"`
	TaskName          string     `json:"task_name,omitempty"`
	RunKind           string     `json:"run_kind"`
	Status            string     `json:"status"`
	Error             string     `json:"error,omitempty"`
	DurationMs        int        `json:"duration_ms"`
	MessageCount      int        `json:"message_count"`
	InputTokens       int64      `json:"input_tokens"`
	OutputTokens      int64      `json:"output_tokens"`
	TotalTokens       int64      `json:"total_tokens"`
	LastEventID       int64      `json:"last_event_id"`
	ActiveTurnID      string     `json:"active_turn_id,omitempty"`
	QueueJobID        int64      `json:"queue_job_id,omitempty"`
	WorkerID          string     `json:"worker_id,omitempty"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type CreateAgentRunInput struct {
	ID                string
	Title             string
	Model             string
	Provider          string
	Prompt            string
	ParentRunID       string
	RootRunID         string
	TaskName          string
	RunKind           string
	Status            string
	Error             string
	DurationMs        int
	MessageCount      int
	InputTokens       int64
	OutputTokens      int64
	LastEventID       int64
	ActiveTurnID      string
	QueueJobID        int64
	WorkerID          string
	CancelRequestedAt *time.Time
	Trace             json.RawMessage
}

type UpdateAgentRunInput struct {
	ID                  string
	Title               string
	Model               string
	Provider            string
	ParentRunID         string
	RootRunID           string
	TaskName            string
	RunKind             string
	Status              string
	Error               string
	DurationMs          int
	MessageCount        int
	InputTokens         int64
	OutputTokens        int64
	LastEventID         int64
	ActiveTurnID        string
	QueueJobID          int64
	WorkerID            string
	CancelRequestedAt   *time.Time
	PreserveRuntimeMeta bool
	Trace               json.RawMessage
}

func (s *Store) CreateAgentRun(ctx context.Context, input CreateAgentRunInput) error {
	if strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("agent run id is required")
	}
	if strings.TrimSpace(input.Model) == "" {
		return fmt.Errorf("agent run model is required")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return fmt.Errorf("agent run prompt is required")
	}
	trace := normalizeTraceJSON(input.Trace)
	if !json.Valid(trace) {
		return fmt.Errorf("agent run trace must be valid JSON")
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "running"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_runs (
			id, title, model, provider, prompt, parent_run_id, root_run_id, task_name, run_kind,
			status, error, duration_ms, message_count, input_tokens, output_tokens,
			last_event_id, active_turn_id, queue_job_id, worker_id, cancel_requested_at,
			trace, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ID,
		strings.TrimSpace(input.Title),
		strings.TrimSpace(input.Model),
		strings.TrimSpace(input.Provider),
		strings.TrimSpace(input.Prompt),
		strings.TrimSpace(input.ParentRunID),
		strings.TrimSpace(input.RootRunID),
		strings.TrimSpace(input.TaskName),
		normalizeRunKind(input.RunKind),
		status,
		input.Error,
		input.DurationMs,
		input.MessageCount,
		input.InputTokens,
		input.OutputTokens,
		input.LastEventID,
		strings.TrimSpace(input.ActiveTurnID),
		input.QueueJobID,
		strings.TrimSpace(input.WorkerID),
		nullableTimeString(input.CancelRequestedAt),
		string(trace),
		nowString(),
		nowString(),
	)
	if err != nil {
		return fmt.Errorf("create agent run: %w", err)
	}
	return nil
}

func (s *Store) UpdateAgentRun(ctx context.Context, input UpdateAgentRunInput) error {
	if strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("agent run id is required")
	}
	if strings.TrimSpace(input.Model) == "" {
		return fmt.Errorf("agent run model is required")
	}
	trace := normalizeTraceJSON(input.Trace)
	if !json.Valid(trace) {
		return fmt.Errorf("agent run trace must be valid JSON")
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "running"
	}
	if input.PreserveRuntimeMeta {
		existing, err := s.GetAgentRun(ctx, input.ID)
		if err != nil {
			return fmt.Errorf("load agent run runtime metadata: %w", err)
		}
		input.LastEventID = existing.LastEventID
		input.ActiveTurnID = existing.ActiveTurnID
		input.QueueJobID = existing.QueueJobID
		input.WorkerID = existing.WorkerID
		input.CancelRequestedAt = existing.CancelRequestedAt
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET title = ?, model = ?, provider = ?, parent_run_id = ?, root_run_id = ?, task_name = ?, run_kind = ?,
		    status = ?, error = ?, duration_ms = ?, message_count = ?, input_tokens = ?, output_tokens = ?,
		    last_event_id = ?, active_turn_id = ?, queue_job_id = ?, worker_id = ?, cancel_requested_at = ?,
		    trace = ?, updated_at = ?
		WHERE id = ?`,
		strings.TrimSpace(input.Title),
		strings.TrimSpace(input.Model),
		strings.TrimSpace(input.Provider),
		strings.TrimSpace(input.ParentRunID),
		strings.TrimSpace(input.RootRunID),
		strings.TrimSpace(input.TaskName),
		normalizeRunKind(input.RunKind),
		status,
		input.Error,
		input.DurationMs,
		input.MessageCount,
		input.InputTokens,
		input.OutputTokens,
		input.LastEventID,
		strings.TrimSpace(input.ActiveTurnID),
		input.QueueJobID,
		strings.TrimSpace(input.WorkerID),
		nullableTimeString(input.CancelRequestedAt),
		string(trace),
		nowString(),
		input.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent run: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent run rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetAgentRun(ctx context.Context, id string) (*AgentRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, model, provider, prompt, parent_run_id, root_run_id, task_name, run_kind,
		       status, error, duration_ms, message_count,
		       input_tokens, output_tokens, last_event_id, active_turn_id, queue_job_id, worker_id,
		       cancel_requested_at, trace, created_at, updated_at
		FROM agent_runs
		WHERE id = ?`, id)

	run, err := scanAgentRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run: %w", err)
	}
	return run, nil
}

func (s *Store) ListAgentRuns(ctx context.Context, status string, limit, offset int) ([]AgentRunSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, title, model, provider, prompt, parent_run_id, root_run_id, task_name, run_kind,
		       status, error, duration_ms, message_count,
		       input_tokens, output_tokens, last_event_id, active_turn_id, queue_job_id, worker_id,
		       cancel_requested_at, created_at, updated_at
		FROM agent_runs`
	args := make([]any, 0, 3)
	if status = strings.TrimSpace(status); status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	defer rows.Close()

	runs := make([]AgentRunSummary, 0)
	for rows.Next() {
		var (
			run               AgentRunSummary
			cancelRequestedAt sql.NullString
			createdAt         string
			updatedAt         string
		)
		if err := rows.Scan(
			&run.ID,
			&run.Title,
			&run.Model,
			&run.Provider,
			&run.Prompt,
			&run.ParentRunID,
			&run.RootRunID,
			&run.TaskName,
			&run.RunKind,
			&run.Status,
			&run.Error,
			&run.DurationMs,
			&run.MessageCount,
			&run.InputTokens,
			&run.OutputTokens,
			&run.LastEventID,
			&run.ActiveTurnID,
			&run.QueueJobID,
			&run.WorkerID,
			&cancelRequestedAt,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent run summary: %w", err)
		}
		run.CancelRequestedAt = parseNullTime(cancelRequestedAt)
		run.CreatedAt = mustParseTime(createdAt)
		run.UpdatedAt = mustParseTime(updatedAt)
		run.TotalTokens = run.InputTokens + run.OutputTokens
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func scanAgentRun(scanner interface{ Scan(dest ...any) error }) (*AgentRun, error) {
	var (
		run               AgentRun
		trace             string
		cancelRequestedAt sql.NullString
		createdAt         string
		updatedAt         string
	)
	if err := scanner.Scan(
		&run.ID,
		&run.Title,
		&run.Model,
		&run.Provider,
		&run.Prompt,
		&run.ParentRunID,
		&run.RootRunID,
		&run.TaskName,
		&run.RunKind,
		&run.Status,
		&run.Error,
		&run.DurationMs,
		&run.MessageCount,
		&run.InputTokens,
		&run.OutputTokens,
		&run.LastEventID,
		&run.ActiveTurnID,
		&run.QueueJobID,
		&run.WorkerID,
		&cancelRequestedAt,
		&trace,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	run.Trace = append(json.RawMessage(nil), trace...)
	run.CancelRequestedAt = parseNullTime(cancelRequestedAt)
	run.CreatedAt = mustParseTime(createdAt)
	run.UpdatedAt = mustParseTime(updatedAt)
	run.TotalTokens = run.InputTokens + run.OutputTokens
	return &run, nil
}

func normalizeTraceJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return raw
}

func normalizeRunKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "root"
	}
	return kind
}
