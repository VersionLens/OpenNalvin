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

type AgentTraceCuration struct {
	ID                string          `json:"id"`
	SourceRunID       string          `json:"source_run_id"`
	Title             string          `json:"title"`
	Status            string          `json:"status"`
	Tags              []string        `json:"tags"`
	Notes             string          `json:"notes,omitempty"`
	Quality           string          `json:"quality,omitempty"`
	Reward            *float64        `json:"reward,omitempty"`
	Split             string          `json:"split,omitempty"`
	SourceTraceHash   string          `json:"source_trace_hash,omitempty"`
	ValidationSummary json.RawMessage `json:"validation_summary,omitempty"`
	Trace             json.RawMessage `json:"trace"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type AgentTraceCurationEvent struct {
	EventID    int64           `json:"event_id"`
	CurationID string          `json:"curation_id"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type CreateAgentTraceCurationInput struct {
	ID                string
	SourceRunID       string
	Title             string
	Status            string
	Tags              []string
	Notes             string
	Quality           string
	Reward            *float64
	Split             string
	SourceTraceHash   string
	ValidationSummary json.RawMessage
	Trace             json.RawMessage
}

type UpdateAgentTraceCurationInput struct {
	ID                string
	Title             string
	Status            string
	Tags              []string
	Notes             string
	Quality           string
	Reward            *float64
	Split             string
	SourceTraceHash   string
	ValidationSummary json.RawMessage
	Trace             json.RawMessage
}

type AgentTraceCurationFilter struct {
	Status      string
	SourceRunID string
	Limit       int
	Offset      int
}

type CreateAgentTraceCurationEventInput struct {
	CurationID string
	Kind       string
	Payload    json.RawMessage
}

func (s *Store) CreateAgentTraceCuration(ctx context.Context, input CreateAgentTraceCurationInput) (*AgentTraceCuration, error) {
	if strings.TrimSpace(input.SourceRunID) == "" {
		return nil, fmt.Errorf("source run id is required")
	}
	trace := normalizeTraceJSON(input.Trace)
	if !json.Valid(trace) {
		return nil, fmt.Errorf("curation trace must be valid JSON")
	}
	validationSummary := input.ValidationSummary
	if len(validationSummary) == 0 {
		validationSummary = json.RawMessage(`{}`)
	}
	if !json.Valid(validationSummary) {
		return nil, fmt.Errorf("curation validation summary must be valid JSON")
	}
	tags, err := json.Marshal(compactCurationStringValues(input.Tags))
	if err != nil {
		return nil, fmt.Errorf("marshal curation tags: %w", err)
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = uuid.NewString()
	}
	status := normalizeCurationStatus(input.Status)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO agent_trace_curations (
			id, source_run_id, title, status, tags, notes, quality, reward, split,
			source_trace_hash, validation_summary, trace, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		strings.TrimSpace(input.SourceRunID),
		strings.TrimSpace(input.Title),
		status,
		string(tags),
		input.Notes,
		strings.TrimSpace(input.Quality),
		nullableCurationFloat(input.Reward),
		strings.TrimSpace(input.Split),
		strings.TrimSpace(input.SourceTraceHash),
		string(validationSummary),
		string(trace),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("create agent trace curation: %w", err)
	}
	return s.GetAgentTraceCuration(ctx, id)
}

func (s *Store) UpdateAgentTraceCuration(ctx context.Context, input UpdateAgentTraceCurationInput) error {
	if strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("curation id is required")
	}
	trace := normalizeTraceJSON(input.Trace)
	if !json.Valid(trace) {
		return fmt.Errorf("curation trace must be valid JSON")
	}
	validationSummary := input.ValidationSummary
	if len(validationSummary) == 0 {
		validationSummary = json.RawMessage(`{}`)
	}
	if !json.Valid(validationSummary) {
		return fmt.Errorf("curation validation summary must be valid JSON")
	}
	tags, err := json.Marshal(compactCurationStringValues(input.Tags))
	if err != nil {
		return fmt.Errorf("marshal curation tags: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_trace_curations
		SET title = ?, status = ?, tags = ?, notes = ?, quality = ?, reward = ?, split = ?,
		    source_trace_hash = ?, validation_summary = ?, trace = ?, updated_at = ?
		WHERE id = ?`,
		strings.TrimSpace(input.Title),
		normalizeCurationStatus(input.Status),
		string(tags),
		input.Notes,
		strings.TrimSpace(input.Quality),
		nullableCurationFloat(input.Reward),
		strings.TrimSpace(input.Split),
		strings.TrimSpace(input.SourceTraceHash),
		string(validationSummary),
		string(trace),
		nowString(),
		strings.TrimSpace(input.ID),
	)
	if err != nil {
		return fmt.Errorf("update agent trace curation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent trace curation rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetAgentTraceCuration(ctx context.Context, id string) (*AgentTraceCuration, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, source_run_id, title, status, tags, notes, quality, reward, split,
		       source_trace_hash, validation_summary, trace, created_at, updated_at
		FROM agent_trace_curations
		WHERE id = ?`, strings.TrimSpace(id))

	curation, err := scanAgentTraceCuration(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent trace curation: %w", err)
	}
	return curation, nil
}

func (s *Store) ListAgentTraceCurations(ctx context.Context, filter AgentTraceCurationFilter) ([]AgentTraceCuration, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, source_run_id, title, status, tags, notes, quality, reward, split,
		       source_trace_hash, validation_summary, trace, created_at, updated_at
		FROM agent_trace_curations`
	args := make([]any, 0, 4)
	where := make([]string, 0, 2)
	if status := strings.TrimSpace(filter.Status); status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if sourceRunID := strings.TrimSpace(filter.SourceRunID); sourceRunID != "" {
		where = append(where, "source_run_id = ?")
		args = append(args, sourceRunID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent trace curations: %w", err)
	}
	defer rows.Close()

	curations := make([]AgentTraceCuration, 0)
	for rows.Next() {
		curation, err := scanAgentTraceCuration(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent trace curation: %w", err)
		}
		curations = append(curations, *curation)
	}
	return curations, rows.Err()
}

func (s *Store) CreateAgentTraceCurationEvent(ctx context.Context, input CreateAgentTraceCurationEventInput) (*AgentTraceCurationEvent, error) {
	if strings.TrimSpace(input.CurationID) == "" {
		return nil, fmt.Errorf("curation id is required")
	}
	if strings.TrimSpace(input.Kind) == "" {
		return nil, fmt.Errorf("curation event kind is required")
	}
	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("curation event payload must be valid JSON")
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_trace_curation_events (curation_id, kind, payload_json, created_at)
		VALUES (?, ?, ?, ?)`,
		strings.TrimSpace(input.CurationID),
		strings.TrimSpace(input.Kind),
		string(payload),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("create agent trace curation event: %w", err)
	}
	eventID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("curation event last insert id: %w", err)
	}
	return s.GetAgentTraceCurationEvent(ctx, eventID)
}

func (s *Store) GetAgentTraceCurationEvent(ctx context.Context, eventID int64) (*AgentTraceCurationEvent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT event_id, curation_id, kind, payload_json, created_at
		FROM agent_trace_curation_events
		WHERE event_id = ?`, eventID)

	event, err := scanAgentTraceCurationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent trace curation event: %w", err)
	}
	return event, nil
}

func (s *Store) ListAgentTraceCurationEvents(ctx context.Context, curationID string, limit int) ([]AgentTraceCurationEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, curation_id, kind, payload_json, created_at
		FROM agent_trace_curation_events
		WHERE curation_id = ?
		ORDER BY event_id ASC
		LIMIT ?`, strings.TrimSpace(curationID), limit)
	if err != nil {
		return nil, fmt.Errorf("list agent trace curation events: %w", err)
	}
	defer rows.Close()

	events := make([]AgentTraceCurationEvent, 0)
	for rows.Next() {
		event, err := scanAgentTraceCurationEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent trace curation event: %w", err)
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}

func scanAgentTraceCuration(scanner interface{ Scan(dest ...any) error }) (*AgentTraceCuration, error) {
	var (
		curation          AgentTraceCuration
		tags              string
		reward            sql.NullFloat64
		validationSummary string
		trace             string
		createdAt         string
		updatedAt         string
	)
	if err := scanner.Scan(
		&curation.ID,
		&curation.SourceRunID,
		&curation.Title,
		&curation.Status,
		&tags,
		&curation.Notes,
		&curation.Quality,
		&reward,
		&curation.Split,
		&curation.SourceTraceHash,
		&validationSummary,
		&trace,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	curation.Tags = decodeStringSlice(tags)
	if curation.Tags == nil {
		curation.Tags = []string{}
	}
	if reward.Valid {
		value := reward.Float64
		curation.Reward = &value
	}
	curation.ValidationSummary = append(json.RawMessage(nil), validationSummary...)
	curation.Trace = append(json.RawMessage(nil), trace...)
	curation.CreatedAt = mustParseTime(createdAt)
	curation.UpdatedAt = mustParseTime(updatedAt)
	return &curation, nil
}

func scanAgentTraceCurationEvent(scanner interface{ Scan(dest ...any) error }) (*AgentTraceCurationEvent, error) {
	var (
		event     AgentTraceCurationEvent
		payload   string
		createdAt string
	)
	if err := scanner.Scan(&event.EventID, &event.CurationID, &event.Kind, &payload, &createdAt); err != nil {
		return nil, err
	}
	event.Payload = append(json.RawMessage(nil), payload...)
	event.CreatedAt = mustParseTime(createdAt)
	return &event, nil
}

func normalizeCurationStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "draft", "approved", "rejected":
		return strings.TrimSpace(status)
	default:
		return "draft"
	}
}

func nullableCurationFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func compactCurationStringValues(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
