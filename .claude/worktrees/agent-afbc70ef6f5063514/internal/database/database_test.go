package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenInitializesVecAndSchemas(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nalvin.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	var vecVersion string
	if err := db.QueryRowContext(context.Background(), "SELECT vec_version()").Scan(&vecVersion); err != nil {
		t.Fatalf("query vec_version: %v", err)
	}
	if vecVersion == "" {
		t.Fatalf("expected vec version")
	}

	for _, table := range []string{"knowledge_nodes", "knowledge_edges", "scheduler_state", "agent_runs", "discord_conversations", "discord_tool_views", "river_job", "river_client"} {
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("expected table %s to exist", table)
		}
	}
}
