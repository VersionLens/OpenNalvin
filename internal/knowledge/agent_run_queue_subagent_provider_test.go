package knowledge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestAgentRunTurnPersistsSubagentProviderName(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-run-turn-subagent-provider.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:       "run-subagent-provider",
		Title:    "subagent provider persistence",
		Model:    "gpt-test",
		Provider: "default",
		Prompt:   "hello",
		Status:   "queued",
		Trace:    json.RawMessage(`{"schema_version":2}`),
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	created, err := store.CreateAgentRunTurn(ctx, knowledge.CreateAgentRunTurnInput{
		TurnID:               "turn-subagent-provider",
		RunID:                "run-subagent-provider",
		ClientRequestID:      "req-subagent-provider",
		Message:              "hello",
		ProviderName:         "default",
		SubagentProviderName: "glm-turbo",
		Status:               "queued",
	})
	if err != nil {
		t.Fatalf("create agent run turn: %v", err)
	}

	if created.SubagentProviderName != "glm-turbo" {
		t.Fatalf("expected created subagent provider glm-turbo, got %q", created.SubagentProviderName)
	}

	if err := store.UpdateAgentRunTurn(ctx, knowledge.UpdateAgentRunTurnInput{
		TurnID:               created.TurnID,
		Title:                created.Title,
		SystemPrompt:         created.SystemPrompt,
		ProviderName:         created.ProviderName,
		SubagentProviderName: "anthropic",
		EnabledToolIDs:       created.EnabledToolIDs,
		PinnedToolIDs:        created.PinnedToolIDs,
		EnableToolIDs:        created.EnableToolIDs,
		DisableToolIDs:       created.DisableToolIDs,
		PinToolIDs:           created.PinToolIDs,
		UnpinToolIDs:         created.UnpinToolIDs,
		Status:               created.Status,
		QueueJobID:           created.QueueJobID,
		Error:                created.Error,
		StartedAt:            created.StartedAt,
		FinishedAt:           created.FinishedAt,
	}); err != nil {
		t.Fatalf("update agent run turn: %v", err)
	}

	stored, err := store.GetAgentRunTurn(ctx, created.TurnID)
	if err != nil {
		t.Fatalf("get agent run turn: %v", err)
	}

	if stored.SubagentProviderName != "anthropic" {
		t.Fatalf("expected stored subagent provider anthropic, got %q", stored.SubagentProviderName)
	}
}
