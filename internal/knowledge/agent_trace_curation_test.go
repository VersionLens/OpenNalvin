package knowledge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestAgentTraceCurationCreateUpdateListAndEvents(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-trace-curations.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	trace := json.RawMessage(`{
		"schema_version": 4,
		"run_id": "run_curate_001",
		"title": "Curatable trace",
		"model": "qwen-test",
		"messages": [
			{"role":"user","message_id":"msg_001","content":"hello"},
			{"role":"assistant","message_id":"msg_002","content":"world","reasoning":"thinking"}
		],
		"created_at": "2026-04-20T00:00:00Z",
		"updated_at": "2026-04-20T00:01:00Z"
	}`)
	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           "run_curate_001",
		Title:        "Curatable trace",
		Model:        "qwen-test",
		Prompt:       "hello",
		Status:       "completed",
		MessageCount: 2,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	reward := 0.75
	curation, err := store.CreateAgentTraceCuration(ctx, knowledge.CreateAgentTraceCurationInput{
		SourceRunID:       "run_curate_001",
		Title:             "Better trace",
		Tags:              []string{"gold", "gold", "redacted"},
		Notes:             "initial note",
		Quality:           "good",
		Reward:            &reward,
		Split:             "train",
		SourceTraceHash:   "hash-123",
		ValidationSummary: json.RawMessage(`{"reviewed":true}`),
		Trace:             trace,
	})
	if err != nil {
		t.Fatalf("create curation: %v", err)
	}
	if curation.SourceRunID != "run_curate_001" {
		t.Fatalf("unexpected source run id: %q", curation.SourceRunID)
	}
	if curation.Status != "draft" {
		t.Fatalf("expected draft status, got %q", curation.Status)
	}
	if got := len(curation.Tags); got != 2 {
		t.Fatalf("expected compacted tags, got %d: %#v", got, curation.Tags)
	}

	if err := store.UpdateAgentTraceCuration(ctx, knowledge.UpdateAgentTraceCurationInput{
		ID:                curation.ID,
		Title:             curation.Title,
		Status:            "approved",
		Tags:              append(curation.Tags, "approved"),
		Notes:             curation.Notes + "\nsecond note",
		Quality:           "excellent",
		Reward:            curation.Reward,
		Split:             curation.Split,
		SourceTraceHash:   curation.SourceTraceHash,
		ValidationSummary: curation.ValidationSummary,
		Trace:             curation.Trace,
	}); err != nil {
		t.Fatalf("update curation: %v", err)
	}

	approved, err := store.ListAgentTraceCurations(ctx, knowledge.AgentTraceCurationFilter{Status: "approved", Limit: 10})
	if err != nil {
		t.Fatalf("list approved curations: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != curation.ID {
		t.Fatalf("expected updated curation in approved list, got %#v", approved)
	}

	event, err := store.CreateAgentTraceCurationEvent(ctx, knowledge.CreateAgentTraceCurationEventInput{
		CurationID: curation.ID,
		Kind:       "label",
		Payload:    json.RawMessage(`{"quality":"excellent"}`),
	})
	if err != nil {
		t.Fatalf("create curation event: %v", err)
	}
	events, err := store.ListAgentTraceCurationEvents(ctx, curation.ID, 10)
	if err != nil {
		t.Fatalf("list curation events: %v", err)
	}
	if len(events) != 1 || events[0].EventID != event.EventID {
		t.Fatalf("expected event round trip, got %#v", events)
	}
}
