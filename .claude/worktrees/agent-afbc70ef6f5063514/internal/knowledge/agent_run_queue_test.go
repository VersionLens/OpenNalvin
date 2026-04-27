package knowledge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestMarkStaleRunningAgentRunsAbortedEmitsStatusEvent(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-run-queue.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           "run-stale",
		Title:        "stale run",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "hello",
		Status:       "running",
		ActiveTurnID: "turn-stale",
		Trace:        json.RawMessage(`{"schema_version":2}`),
	}); err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	if _, err := store.CreateAgentRunTurn(ctx, knowledge.CreateAgentRunTurnInput{
		TurnID:          "turn-stale",
		RunID:           "run-stale",
		ClientRequestID: "req-stale",
		Message:         "hello",
		Status:          "running",
	}); err != nil {
		t.Fatalf("create stale turn: %v", err)
	}

	if err := store.MarkStaleRunningAgentRunsAborted(ctx, "interrupted by worker restart"); err != nil {
		t.Fatalf("mark stale running runs aborted: %v", err)
	}

	run, err := store.GetAgentRun(ctx, "run-stale")
	if err != nil {
		t.Fatalf("get stale run: %v", err)
	}
	if run.Status != "aborted" {
		t.Fatalf("expected aborted run status, got %q", run.Status)
	}
	if run.LastEventID == 0 {
		t.Fatal("expected stale cleanup to append a terminal event")
	}

	turn, err := store.GetAgentRunTurn(ctx, "turn-stale")
	if err != nil {
		t.Fatalf("get stale turn: %v", err)
	}
	if turn.Status != "aborted" {
		t.Fatalf("expected aborted turn status, got %q", turn.Status)
	}
	if turn.FinishedAt == nil {
		t.Fatal("expected stale cleanup to set finished_at on the turn")
	}

	events, err := store.ListAgentRunEventsAfter(ctx, "run-stale", 0, 10)
	if err != nil {
		t.Fatalf("list stale run events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one stale cleanup event, got %d", len(events))
	}
	if events[0].Kind != "status" {
		t.Fatalf("expected status event kind, got %q", events[0].Kind)
	}
	if events[0].TurnID != "turn-stale" {
		t.Fatalf("expected cleanup event to point at active turn, got %q", events[0].TurnID)
	}

	var payload struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal stale cleanup payload: %v", err)
	}
	if payload.Status != "aborted" {
		t.Fatalf("expected aborted payload status, got %q", payload.Status)
	}
	if payload.Error != "interrupted by worker restart" {
		t.Fatalf("expected cleanup reason in payload, got %q", payload.Error)
	}
	if run.LastEventID != events[0].EventID {
		t.Fatalf("expected last_event_id %d, got %d", events[0].EventID, run.LastEventID)
	}
}
