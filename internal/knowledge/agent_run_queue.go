package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AgentRunTurn struct {
	TurnID               string     `json:"turn_id"`
	RunID                string     `json:"run_id"`
	ClientRequestID      string     `json:"client_request_id"`
	Message              string     `json:"message"`
	Title                string     `json:"title,omitempty"`
	SystemPrompt         string     `json:"system_prompt,omitempty"`
	ProviderName         string     `json:"provider_name,omitempty"`
	SubagentProviderName string     `json:"subagent_provider_name,omitempty"`
	EnabledToolIDs       []string   `json:"enabled_tool_ids"`
	PinnedToolIDs        []string   `json:"pinned_tool_ids"`
	EnableToolIDs        []string   `json:"enable_tool_ids"`
	DisableToolIDs       []string   `json:"disable_tool_ids"`
	PinToolIDs           []string   `json:"pin_tool_ids"`
	UnpinToolIDs         []string   `json:"unpin_tool_ids"`
	Status               string     `json:"status"`
	QueueJobID           int64      `json:"queue_job_id,omitempty"`
	Error                string     `json:"error,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	FinishedAt           *time.Time `json:"finished_at,omitempty"`
}

type CreateAgentRunTurnInput struct {
	TurnID               string
	RunID                string
	ClientRequestID      string
	Message              string
	Title                string
	SystemPrompt         string
	ProviderName         string
	SubagentProviderName string
	EnabledToolIDs       []string
	PinnedToolIDs        []string
	EnableToolIDs        []string
	DisableToolIDs       []string
	PinToolIDs           []string
	UnpinToolIDs         []string
	Status               string
	QueueJobID           int64
	Error                string
	StartedAt            *time.Time
	FinishedAt           *time.Time
}

type UpdateAgentRunTurnInput struct {
	TurnID               string
	Title                string
	SystemPrompt         string
	ProviderName         string
	SubagentProviderName string
	EnabledToolIDs       []string
	PinnedToolIDs        []string
	EnableToolIDs        []string
	DisableToolIDs       []string
	PinToolIDs           []string
	UnpinToolIDs         []string
	Status               string
	QueueJobID           int64
	Error                string
	StartedAt            *time.Time
	FinishedAt           *time.Time
}

type AgentRunEvent struct {
	EventID   int64           `json:"event_id"`
	RunID     string          `json:"run_id"`
	TurnID    string          `json:"turn_id,omitempty"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type CreateAgentRunEventInput struct {
	RunID   string
	TurnID  string
	Kind    string
	Payload json.RawMessage
}

type staleRunningAgentRun struct {
	RunID       string
	EventTurnID string
}

func (s *Store) CreateAgentRunTurn(ctx context.Context, input CreateAgentRunTurnInput) (*AgentRunTurn, error) {
	if strings.TrimSpace(input.RunID) == "" {
		return nil, fmt.Errorf("agent run turn run id is required")
	}
	if strings.TrimSpace(input.ClientRequestID) == "" {
		return nil, fmt.Errorf("agent run turn client request id is required")
	}
	if strings.TrimSpace(input.Message) == "" {
		return nil, fmt.Errorf("agent run turn message is required")
	}

	turnID := strings.TrimSpace(input.TurnID)
	if turnID == "" {
		turnID = uuid.NewString()
	}
	status := normalizeTurnStatus(input.Status)
	enabledToolIDs, err := json.Marshal(input.EnabledToolIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal enabled tool ids: %w", err)
	}
	pinnedToolIDs, err := json.Marshal(input.PinnedToolIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal pinned tool ids: %w", err)
	}
	enableToolIDs, err := json.Marshal(input.EnableToolIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal enable tool ids: %w", err)
	}
	disableToolIDs, err := json.Marshal(input.DisableToolIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal disable tool ids: %w", err)
	}
	pinToolIDs, err := json.Marshal(input.PinToolIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal pin tool ids: %w", err)
	}
	unpinToolIDs, err := json.Marshal(input.UnpinToolIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal unpin tool ids: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO agent_run_turns (
			turn_id, run_id, client_request_id, message, title, system_prompt, provider_name, subagent_provider_name,
			enabled_tool_ids, pinned_tool_ids, enable_tool_ids, disable_tool_ids, pin_tool_ids, unpin_tool_ids,
			status, queue_job_id, error,
			created_at, updated_at, started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		turnID,
		strings.TrimSpace(input.RunID),
		strings.TrimSpace(input.ClientRequestID),
		input.Message,
		strings.TrimSpace(input.Title),
		strings.TrimSpace(input.SystemPrompt),
		strings.TrimSpace(input.ProviderName),
		strings.TrimSpace(input.SubagentProviderName),
		string(enabledToolIDs),
		string(pinnedToolIDs),
		string(enableToolIDs),
		string(disableToolIDs),
		string(pinToolIDs),
		string(unpinToolIDs),
		status,
		input.QueueJobID,
		input.Error,
		nowString(),
		nowString(),
		nullableTimeString(input.StartedAt),
		nullableTimeString(input.FinishedAt),
	)
	if err != nil {
		return nil, fmt.Errorf("create agent run turn: %w", err)
	}

	return s.GetAgentRunTurn(ctx, turnID)
}

func (s *Store) GetAgentRunTurn(ctx context.Context, turnID string) (*AgentRunTurn, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT turn_id, run_id, client_request_id, message, title, system_prompt, provider_name, subagent_provider_name,
		       enabled_tool_ids, pinned_tool_ids, enable_tool_ids, disable_tool_ids, pin_tool_ids, unpin_tool_ids,
		       status, queue_job_id, error,
		       created_at, updated_at, started_at, finished_at
		FROM agent_run_turns
		WHERE turn_id = ?`, strings.TrimSpace(turnID))

	turn, err := scanAgentRunTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run turn: %w", err)
	}
	return turn, nil
}

func (s *Store) GetAgentRunTurnByClientRequestID(ctx context.Context, runID, clientRequestID string) (*AgentRunTurn, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT turn_id, run_id, client_request_id, message, title, system_prompt, provider_name, subagent_provider_name,
		       enabled_tool_ids, pinned_tool_ids, enable_tool_ids, disable_tool_ids, pin_tool_ids, unpin_tool_ids,
		       status, queue_job_id, error,
		       created_at, updated_at, started_at, finished_at
		FROM agent_run_turns
		WHERE run_id = ? AND client_request_id = ?`,
		strings.TrimSpace(runID),
		strings.TrimSpace(clientRequestID),
	)

	turn, err := scanAgentRunTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run turn by client request id: %w", err)
	}
	return turn, nil
}

func (s *Store) UpdateAgentRunTurn(ctx context.Context, input UpdateAgentRunTurnInput) error {
	if strings.TrimSpace(input.TurnID) == "" {
		return fmt.Errorf("agent run turn id is required")
	}

	enabledToolIDs, err := json.Marshal(input.EnabledToolIDs)
	if err != nil {
		return fmt.Errorf("marshal enabled tool ids: %w", err)
	}
	pinnedToolIDs, err := json.Marshal(input.PinnedToolIDs)
	if err != nil {
		return fmt.Errorf("marshal pinned tool ids: %w", err)
	}
	enableToolIDs, err := json.Marshal(input.EnableToolIDs)
	if err != nil {
		return fmt.Errorf("marshal enable tool ids: %w", err)
	}
	disableToolIDs, err := json.Marshal(input.DisableToolIDs)
	if err != nil {
		return fmt.Errorf("marshal disable tool ids: %w", err)
	}
	pinToolIDs, err := json.Marshal(input.PinToolIDs)
	if err != nil {
		return fmt.Errorf("marshal pin tool ids: %w", err)
	}
	unpinToolIDs, err := json.Marshal(input.UnpinToolIDs)
	if err != nil {
		return fmt.Errorf("marshal unpin tool ids: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_run_turns
		SET title = ?, system_prompt = ?, provider_name = ?, subagent_provider_name = ?, enabled_tool_ids = ?, pinned_tool_ids = ?,
		    enable_tool_ids = ?, disable_tool_ids = ?, pin_tool_ids = ?, unpin_tool_ids = ?,
		    status = ?, queue_job_id = ?, error = ?, updated_at = ?, started_at = ?, finished_at = ?
		WHERE turn_id = ?`,
		strings.TrimSpace(input.Title),
		strings.TrimSpace(input.SystemPrompt),
		strings.TrimSpace(input.ProviderName),
		strings.TrimSpace(input.SubagentProviderName),
		string(enabledToolIDs),
		string(pinnedToolIDs),
		string(enableToolIDs),
		string(disableToolIDs),
		string(pinToolIDs),
		string(unpinToolIDs),
		normalizeTurnStatus(input.Status),
		input.QueueJobID,
		input.Error,
		nowString(),
		nullableTimeString(input.StartedAt),
		nullableTimeString(input.FinishedAt),
		strings.TrimSpace(input.TurnID),
	)
	if err != nil {
		return fmt.Errorf("update agent run turn: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent run turn rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateAgentRunEvent(ctx context.Context, input CreateAgentRunEventInput) (*AgentRunEvent, error) {
	if strings.TrimSpace(input.RunID) == "" {
		return nil, fmt.Errorf("agent run event run id is required")
	}
	if strings.TrimSpace(input.Kind) == "" {
		return nil, fmt.Errorf("agent run event kind is required")
	}
	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("agent run event payload must be valid JSON")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin agent run event tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO agent_run_events (run_id, turn_id, kind, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		strings.TrimSpace(input.RunID),
		strings.TrimSpace(input.TurnID),
		strings.TrimSpace(input.Kind),
		string(payload),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("insert agent run event: %w", err)
	}
	eventID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("agent run event last insert id: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE agent_runs
		SET last_event_id = ?, updated_at = ?
		WHERE id = ?`,
		eventID,
		nowString(),
		strings.TrimSpace(input.RunID),
	); err != nil {
		return nil, fmt.Errorf("update agent run last event id: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent run event tx: %w", err)
	}
	return s.GetAgentRunEvent(ctx, eventID)
}

func (s *Store) GetAgentRunEvent(ctx context.Context, eventID int64) (*AgentRunEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT event_id, run_id, turn_id, kind, payload_json, created_at
		FROM agent_run_events
		WHERE event_id = ?`, eventID)

	event, err := scanAgentRunEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run event: %w", err)
	}
	return event, nil
}

func (s *Store) ListAgentRunEventsAfter(ctx context.Context, runID string, afterEventID int64, limit int) ([]AgentRunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, run_id, turn_id, kind, payload_json, created_at
		FROM agent_run_events
		WHERE run_id = ? AND event_id > ?
		ORDER BY event_id ASC
		LIMIT ?`,
		strings.TrimSpace(runID),
		afterEventID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent run events: %w", err)
	}
	defer rows.Close()

	events := make([]AgentRunEvent, 0)
	for rows.Next() {
		event, err := scanAgentRunEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent run event: %w", err)
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}

func (s *Store) RequestAgentRunAbort(ctx context.Context, runID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET cancel_requested_at = ?, updated_at = ?
		WHERE id = ?`,
		nowString(),
		nowString(),
		strings.TrimSpace(runID),
	)
	if err != nil {
		return fmt.Errorf("request agent run abort: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("request agent run abort rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkStaleRunningAgentRunsAborted(ctx context.Context, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "interrupted by worker restart"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stale agent run cleanup tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	rows, err := tx.QueryContext(ctx, `
		SELECT r.id,
		       COALESCE(
		           NULLIF(r.active_turn_id, ''),
		           (
		               SELECT t.turn_id
		               FROM agent_run_turns t
		               WHERE t.run_id = r.id
		               ORDER BY t.created_at DESC
		               LIMIT 1
		           )
		       ) AS event_turn_id
		FROM agent_runs r
		WHERE r.status = ?`,
		"running",
	)
	if err != nil {
		return fmt.Errorf("list stale agent runs: %w", err)
	}
	staleRuns := make([]staleRunningAgentRun, 0)
	for rows.Next() {
		var (
			run         staleRunningAgentRun
			eventTurnID sql.NullString
		)
		if err := rows.Scan(&run.RunID, &eventTurnID); err != nil {
			rows.Close()
			return fmt.Errorf("scan stale agent run: %w", err)
		}
		run.EventTurnID = strings.TrimSpace(eventTurnID.String)
		staleRuns = append(staleRuns, run)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stale agent run rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate stale agent runs: %w", err)
	}
	if len(staleRuns) == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit stale agent run cleanup tx: %w", err)
		}
		return nil
	}

	now := nowString()
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = ?, error = ?, active_turn_id = '', worker_id = '', updated_at = ?
		WHERE status = ?`,
		"aborted",
		reason,
		now,
		"running",
	); err != nil {
		return fmt.Errorf("mark stale agent runs aborted: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_run_turns
		SET status = ?, error = ?, updated_at = ?, finished_at = COALESCE(finished_at, ?)
		WHERE status = ?`,
		"aborted",
		reason,
		now,
		now,
		"running",
	); err != nil {
		return fmt.Errorf("mark stale agent run turns aborted: %w", err)
	}

	statusPayload, err := json.Marshal(map[string]string{
		"status": "aborted",
		"error":  reason,
	})
	if err != nil {
		return fmt.Errorf("marshal stale agent run status payload: %w", err)
	}
	for _, run := range staleRuns {
		if run.EventTurnID == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO agent_run_events (run_id, turn_id, kind, payload_json, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			run.RunID,
			run.EventTurnID,
			"status",
			string(statusPayload),
			now,
		)
		if err != nil {
			return fmt.Errorf("insert stale agent run event: %w", err)
		}
		eventID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("stale agent run event last insert id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_runs
			SET last_event_id = ?, updated_at = ?
			WHERE id = ?`,
			eventID,
			now,
			run.RunID,
		); err != nil {
			return fmt.Errorf("update stale agent run last event id: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stale agent run cleanup tx: %w", err)
	}
	return nil
}

func scanAgentRunTurn(scanner interface{ Scan(dest ...any) error }) (*AgentRunTurn, error) {
	var (
		turn           AgentRunTurn
		enabledToolIDs string
		pinnedToolIDs  string
		enableToolIDs  string
		disableToolIDs string
		pinToolIDs     string
		unpinToolIDs   string
		createdAt      string
		updatedAt      string
		startedAt      sql.NullString
		finishedAt     sql.NullString
	)
	if err := scanner.Scan(
		&turn.TurnID,
		&turn.RunID,
		&turn.ClientRequestID,
		&turn.Message,
		&turn.Title,
		&turn.SystemPrompt,
		&turn.ProviderName,
		&turn.SubagentProviderName,
		&enabledToolIDs,
		&pinnedToolIDs,
		&enableToolIDs,
		&disableToolIDs,
		&pinToolIDs,
		&unpinToolIDs,
		&turn.Status,
		&turn.QueueJobID,
		&turn.Error,
		&createdAt,
		&updatedAt,
		&startedAt,
		&finishedAt,
	); err != nil {
		return nil, err
	}
	turn.EnabledToolIDs = decodeStringSlice(enabledToolIDs)
	turn.PinnedToolIDs = decodeStringSlice(pinnedToolIDs)
	turn.EnableToolIDs = decodeStringSlice(enableToolIDs)
	turn.DisableToolIDs = decodeStringSlice(disableToolIDs)
	turn.PinToolIDs = decodeStringSlice(pinToolIDs)
	turn.UnpinToolIDs = decodeStringSlice(unpinToolIDs)
	turn.CreatedAt = mustParseTime(createdAt)
	turn.UpdatedAt = mustParseTime(updatedAt)
	turn.StartedAt = parseNullTime(startedAt)
	turn.FinishedAt = parseNullTime(finishedAt)
	return &turn, nil
}

func scanAgentRunEvent(scanner interface{ Scan(dest ...any) error }) (*AgentRunEvent, error) {
	var (
		event     AgentRunEvent
		payload   string
		createdAt string
	)
	if err := scanner.Scan(
		&event.EventID,
		&event.RunID,
		&event.TurnID,
		&event.Kind,
		&payload,
		&createdAt,
	); err != nil {
		return nil, err
	}
	event.Payload = append(json.RawMessage(nil), payload...)
	event.CreatedAt = mustParseTime(createdAt)
	return &event, nil
}

func decodeStringSlice(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func normalizeTurnStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "queued", "running", "completed", "failed", "aborted":
		return strings.TrimSpace(status)
	default:
		return "queued"
	}
}
