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

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) CreateNode(ctx context.Context, input NodeInput) (*Node, error) {
	if strings.TrimSpace(input.Kind) == "" {
		return nil, fmt.Errorf("node kind is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, fmt.Errorf("node name is required")
	}
	attrs := normalizeJSON(input.Attributes)
	if !json.Valid([]byte(attrs)) {
		return nil, fmt.Errorf("node attributes must be valid JSON")
	}

	id := uuid.NewString()
	now := nowString()
	searchText := buildSearchText(input.Kind, input.Name, input.Content)

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_nodes (
			id, kind, name, content, attributes, search_text, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.Kind, input.Name, input.Content, attrs, searchText, now, now,
	); err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}

	return s.GetNode(ctx, id)
}

func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, kind, name, content, attributes, created_at, updated_at
		FROM knowledge_nodes
		WHERE id = ?`, id)
	node, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) && len(id) >= 8 && len(id) < 36 {
		row = s.db.QueryRowContext(ctx, `
			SELECT id, kind, name, content, attributes, created_at, updated_at
			FROM knowledge_nodes
			WHERE id LIKE ?
			ORDER BY id
			LIMIT 1`, id+"%")
		node, err = scanNode(row)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return node, nil
}

func (s *Store) ListNodes(ctx context.Context, query, kind string, limit int) ([]Node, error) {
	var (
		args       []any
		conditions []string
	)

	base := `
		SELECT n.id, n.kind, n.name, n.content, n.attributes, n.created_at, n.updated_at
		FROM knowledge_nodes n`

	if sanitized := sanitizeFTSQuery(query); sanitized != "" {
		base += ` JOIN knowledge_nodes_fts f ON f.id = n.id`
		conditions = append(conditions, "knowledge_nodes_fts MATCH ?")
		args = append(args, sanitized)
	}
	if strings.TrimSpace(kind) != "" {
		conditions = append(conditions, "n.kind = ?")
		args = append(args, kind)
	}
	if len(conditions) > 0 {
		base += " WHERE " + strings.Join(conditions, " AND ")
	}
	if limit <= 0 {
		limit = 50
	}
	base += " ORDER BY n.updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	nodes := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, *node)
	}
	return nodes, rows.Err()
}

func (s *Store) UpdateNode(ctx context.Context, id string, input NodeInput) (*Node, error) {
	existing, err := s.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	kind := coalesce(input.Kind, existing.Kind)
	name := coalesce(input.Name, existing.Name)
	content := existing.Content
	if input.Content != "" {
		content = input.Content
	}
	attrs := existing.Attributes
	if input.Attributes != "" {
		attrs = input.Attributes
	}
	attrs = normalizeJSON(attrs)
	if !json.Valid([]byte(attrs)) {
		return nil, fmt.Errorf("node attributes must be valid JSON")
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_nodes
		SET kind = ?, name = ?, content = ?, attributes = ?, search_text = ?, updated_at = ?
		WHERE id = ?`,
		kind, name, content, attrs, buildSearchText(kind, name, content), nowString(), existing.ID,
	); err != nil {
		return nil, fmt.Errorf("update node: %w", err)
	}

	return s.GetNode(ctx, existing.ID)
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_nodes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete node rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateEdge(ctx context.Context, input EdgeInput) (*Edge, error) {
	if strings.TrimSpace(input.SourceNodeID) == "" || strings.TrimSpace(input.TargetNodeID) == "" {
		return nil, fmt.Errorf("edge source and target are required")
	}
	if strings.TrimSpace(input.Relation) == "" {
		return nil, fmt.Errorf("edge relation is required")
	}
	attrs := normalizeJSON(input.Attributes)
	if !json.Valid([]byte(attrs)) {
		return nil, fmt.Errorf("edge attributes must be valid JSON")
	}

	id := uuid.NewString()
	now := nowString()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_edges (
			id, source_node_id, target_node_id, relation, attributes, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, input.SourceNodeID, input.TargetNodeID, input.Relation, attrs, now, now,
	); err != nil {
		return nil, fmt.Errorf("create edge: %w", err)
	}
	return s.GetEdge(ctx, id)
}

func (s *Store) GetEdge(ctx context.Context, id string) (*Edge, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, source_node_id, target_node_id, relation, attributes, created_at, updated_at
		FROM knowledge_edges
		WHERE id = ?`, id)
	edge, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get edge: %w", err)
	}
	return edge, nil
}

func (s *Store) ListEdges(ctx context.Context, filter EdgeFilter) ([]Edge, error) {
	query := `
		SELECT id, source_node_id, target_node_id, relation, attributes, created_at, updated_at
		FROM knowledge_edges`
	args := make([]any, 0, 3)
	conditions := make([]string, 0, 2)

	if strings.TrimSpace(filter.NodeID) != "" {
		conditions = append(conditions, "(source_node_id = ? OR target_node_id = ?)")
		args = append(args, filter.NodeID, filter.NodeID)
	}
	if strings.TrimSpace(filter.Relation) != "" {
		conditions = append(conditions, "relation = ?")
		args = append(args, filter.Relation)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}
	defer rows.Close()

	edges := make([]Edge, 0)
	for rows.Next() {
		edge, err := scanEdge(rows)
		if err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, *edge)
	}
	return edges, rows.Err()
}

func (s *Store) UpdateEdge(ctx context.Context, id string, input EdgeInput) (*Edge, error) {
	existing, err := s.GetEdge(ctx, id)
	if err != nil {
		return nil, err
	}

	sourceID := coalesce(input.SourceNodeID, existing.SourceNodeID)
	targetID := coalesce(input.TargetNodeID, existing.TargetNodeID)
	relation := coalesce(input.Relation, existing.Relation)
	attrs := existing.Attributes
	if input.Attributes != "" {
		attrs = input.Attributes
	}
	attrs = normalizeJSON(attrs)
	if !json.Valid([]byte(attrs)) {
		return nil, fmt.Errorf("edge attributes must be valid JSON")
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_edges
		SET source_node_id = ?, target_node_id = ?, relation = ?, attributes = ?, updated_at = ?
		WHERE id = ?`,
		sourceID, targetID, relation, attrs, nowString(), id,
	); err != nil {
		return nil, fmt.Errorf("update edge: %w", err)
	}
	return s.GetEdge(ctx, id)
}

func (s *Store) DeleteEdge(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_edges WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete edge rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Neighbors(ctx context.Context, nodeID string, depth int) (*Graph, error) {
	if depth <= 0 {
		depth = 1
	}
	root, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	nodeMap := map[string]Node{root.ID: *root}
	edgeMap := map[string]Edge{}
	visited := map[string]struct{}{root.ID: {}}
	current := []string{root.ID}

	for level := 0; level < depth; level++ {
		next := make([]string, 0)
		for _, currentID := range current {
			edges, err := s.ListEdges(ctx, EdgeFilter{NodeID: currentID, Limit: 1000})
			if err != nil {
				return nil, fmt.Errorf("neighbors edges: %w", err)
			}
			for _, edge := range edges {
				edgeMap[edge.ID] = edge
				neighborID := edge.SourceNodeID
				if neighborID == currentID {
					neighborID = edge.TargetNodeID
				}
				if _, ok := nodeMap[neighborID]; !ok {
					node, err := s.GetNode(ctx, neighborID)
					if err != nil {
						return nil, fmt.Errorf("neighbors node %s: %w", neighborID, err)
					}
					nodeMap[node.ID] = *node
				}
				if _, ok := visited[neighborID]; !ok {
					visited[neighborID] = struct{}{}
					next = append(next, neighborID)
				}
			}
		}
		current = next
	}

	return &Graph{
		Node:  root,
		Nodes: mapValues(nodeMap),
		Edges: mapEdgeValues(edgeMap),
	}, nil
}

func (s *Store) ShortestPath(ctx context.Context, startID, endID string) (*Graph, error) {
	start, err := s.GetNode(ctx, startID)
	if err != nil {
		return nil, fmt.Errorf("start node: %w", err)
	}
	end, err := s.GetNode(ctx, endID)
	if err != nil {
		return nil, fmt.Errorf("end node: %w", err)
	}
	if start.ID == end.ID {
		return &Graph{Node: start, Nodes: []Node{*start}, Edges: []Edge{}}, nil
	}

	type step struct {
		prevNode string
		edgeID   string
	}
	queue := []string{start.ID}
	visited := map[string]struct{}{start.ID: {}}
	prev := map[string]step{}
	found := false

	for len(queue) > 0 && !found {
		current := queue[0]
		queue = queue[1:]

		edges, err := s.ListEdges(ctx, EdgeFilter{NodeID: current, Limit: 1000})
		if err != nil {
			return nil, fmt.Errorf("path edges: %w", err)
		}
		for _, edge := range edges {
			neighborID := edge.SourceNodeID
			if neighborID == current {
				neighborID = edge.TargetNodeID
			}
			if _, ok := visited[neighborID]; ok {
				continue
			}
			visited[neighborID] = struct{}{}
			prev[neighborID] = step{prevNode: current, edgeID: edge.ID}
			if neighborID == end.ID {
				found = true
				break
			}
			queue = append(queue, neighborID)
		}
	}

	if !found {
		return &Graph{Node: start, Nodes: []Node{*start, *end}, Edges: []Edge{}}, nil
	}

	nodeMap := map[string]Node{start.ID: *start, end.ID: *end}
	edgeMap := map[string]Edge{}
	for cur := end.ID; cur != start.ID; {
		step := prev[cur]
		edge, err := s.GetEdge(ctx, step.edgeID)
		if err != nil {
			return nil, fmt.Errorf("path edge %s: %w", step.edgeID, err)
		}
		edgeMap[edge.ID] = *edge

		node, err := s.GetNode(ctx, step.prevNode)
		if err != nil {
			return nil, fmt.Errorf("path node %s: %w", step.prevNode, err)
		}
		nodeMap[node.ID] = *node
		cur = step.prevNode
	}

	return &Graph{
		Node:  start,
		Nodes: mapValues(nodeMap),
		Edges: mapEdgeValues(edgeMap),
	}, nil
}

func (s *Store) UpsertSchedulerState(
	ctx context.Context,
	task string,
	category string,
	status string,
	runKey string,
	workerID string,
	startedAt *time.Time,
	finishedAt *time.Time,
	nextScheduledAt *time.Time,
	summary any,
	lastError string,
) error {
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("scheduler state task is required")
	}
	rawSummary := []byte(`{}`)
	if summary != nil {
		encoded, err := json.Marshal(summary)
		if err != nil {
			return fmt.Errorf("marshal scheduler summary: %w", err)
		}
		rawSummary = encoded
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduler_state (
			task, category, status, run_key, worker_id, started_at, finished_at,
			next_scheduled_at, summary, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task, category) DO UPDATE SET
			status = excluded.status,
			run_key = excluded.run_key,
			worker_id = excluded.worker_id,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			next_scheduled_at = excluded.next_scheduled_at,
			summary = excluded.summary,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at`,
		task, category, status, runKey, workerID,
		nullableTimeString(startedAt), nullableTimeString(finishedAt), nullableTimeString(nextScheduledAt),
		string(rawSummary), lastError, nowString(), nowString(),
	); err != nil {
		return fmt.Errorf("upsert scheduler state: %w", err)
	}
	return nil
}

func (s *Store) ListSchedulerStates(ctx context.Context) ([]SchedulerState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task, category, status, run_key, worker_id, started_at, finished_at,
		       next_scheduled_at, summary, last_error, updated_at
		FROM scheduler_state
		ORDER BY task, category`)
	if err != nil {
		return nil, fmt.Errorf("list scheduler states: %w", err)
	}
	defer rows.Close()

	states := make([]SchedulerState, 0)
	for rows.Next() {
		var (
			state      SchedulerState
			startedAt  sql.NullString
			finishedAt sql.NullString
			nextRun    sql.NullString
			summary    string
			updatedAt  string
		)
		if err := rows.Scan(
			&state.Task,
			&state.Category,
			&state.Status,
			&state.RunKey,
			&state.WorkerID,
			&startedAt,
			&finishedAt,
			&nextRun,
			&summary,
			&state.LastError,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan scheduler state: %w", err)
		}
		state.StartedAt = parseNullTime(startedAt)
		state.FinishedAt = parseNullTime(finishedAt)
		state.NextScheduledAt = parseNullTime(nextRun)
		state.UpdatedAt = mustParseTime(updatedAt)
		state.Summary = json.RawMessage(summary)
		states = append(states, state)
	}
	return states, rows.Err()
}

func scanNode(scanner interface{ Scan(dest ...any) error }) (*Node, error) {
	var (
		node      Node
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(&node.ID, &node.Kind, &node.Name, &node.Content, &node.Attributes, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	node.CreatedAt = mustParseTime(createdAt)
	node.UpdatedAt = mustParseTime(updatedAt)
	return &node, nil
}

func scanEdge(scanner interface{ Scan(dest ...any) error }) (*Edge, error) {
	var (
		edge      Edge
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(&edge.ID, &edge.SourceNodeID, &edge.TargetNodeID, &edge.Relation, &edge.Attributes, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	edge.CreatedAt = mustParseTime(createdAt)
	edge.UpdatedAt = mustParseTime(updatedAt)
	return &edge, nil
}

func normalizeJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

func buildSearchText(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}
	return strings.Join(nonEmpty, " ")
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func nullableTimeString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseNullTime(raw sql.NullString) *time.Time {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	value := mustParseTime(raw.String)
	return &value
}

func mustParseTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func coalesce(candidate, fallback string) string {
	if strings.TrimSpace(candidate) == "" {
		return fallback
	}
	return candidate
}

func mapValues(items map[string]Node) []Node {
	values := make([]Node, 0, len(items))
	for _, item := range items {
		values = append(values, item)
	}
	return values
}

func mapEdgeValues(items map[string]Edge) []Edge {
	values := make([]Edge, 0, len(items))
	for _, item := range items {
		values = append(values, item)
	}
	return values
}

// sanitizeFTSQuery escapes an arbitrary user query so it is safe to pass to
// an FTS5 MATCH expression. Tokens containing FTS5 special characters (colons,
// quotes, parens, operators, etc.) are wrapped in double-quotes. Leading
// prefix operators (-/+/~/^) are stripped. Returns "" if nothing usable remains.
func sanitizeFTSQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	tokens := strings.Fields(raw)
	safe := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		// Strip leading prefix operators.
		tok = strings.TrimLeft(tok, "-+~^")
		if tok == "" {
			continue
		}
		// Strip wrapping quotes.
		if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
			tok = tok[1 : len(tok)-1]
		}
		if tok == "" {
			continue
		}
		// Skip bare FTS5 boolean keywords.
		upper := strings.ToUpper(tok)
		if upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			continue
		}
		if needsFTSQuoting(tok) {
			tok = `"` + strings.ReplaceAll(tok, `"`, `""`) + `"`
		}
		safe = append(safe, tok)
	}
	return strings.Join(safe, " ")
}

func needsFTSQuoting(tok string) bool {
	for _, ch := range tok {
		switch ch {
		case ':', '"', '(', ')', '*', '+', '-', '~', '^':
			return true
		}
		// Non-alphanumeric/underscore/space → quote.
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_') {
			return true
		}
	}
	return false
}
