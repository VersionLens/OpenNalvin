package knowledge_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestStoreCRUDSearchGraphAndScheduler(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	alpha, err := store.CreateNode(ctx, knowledge.NodeInput{
		Kind:       "Article",
		Name:       "Alpha",
		Content:    "alpha world summary",
		Attributes: `{"topic":"alpha"}`,
	})
	if err != nil {
		t.Fatalf("create alpha node: %v", err)
	}
	beta, err := store.CreateNode(ctx, knowledge.NodeInput{
		Kind:       "Entity",
		Name:       "Beta",
		Content:    "beta world detail",
		Attributes: `{"topic":"beta"}`,
	})
	if err != nil {
		t.Fatalf("create beta node: %v", err)
	}
	gamma, err := store.CreateNode(ctx, knowledge.NodeInput{
		Kind:       "Entity",
		Name:       "Gamma",
		Content:    "gamma branch",
		Attributes: `{"topic":"gamma"}`,
	})
	if err != nil {
		t.Fatalf("create gamma node: %v", err)
	}

	if _, err := store.CreateEdge(ctx, knowledge.EdgeInput{
		SourceNodeID: alpha.ID,
		TargetNodeID: beta.ID,
		Relation:     "mentions",
	}); err != nil {
		t.Fatalf("create alpha->beta edge: %v", err)
	}
	edgeBG, err := store.CreateEdge(ctx, knowledge.EdgeInput{
		SourceNodeID: beta.ID,
		TargetNodeID: gamma.ID,
		Relation:     "related_to",
	})
	if err != nil {
		t.Fatalf("create beta->gamma edge: %v", err)
	}

	nodes, err := store.ListNodes(ctx, "world", "", 10)
	if err != nil {
		t.Fatalf("search nodes: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected search results, got %d", len(nodes))
	}

	graph, err := store.Neighbors(ctx, alpha.ID, 2)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 graph nodes, got %d", len(graph.Nodes))
	}

	path, err := store.ShortestPath(ctx, alpha.ID, gamma.ID)
	if err != nil {
		t.Fatalf("shortest path: %v", err)
	}
	if len(path.Edges) != 2 {
		t.Fatalf("expected 2 edges in path, got %d", len(path.Edges))
	}

	updated, err := store.UpdateEdge(ctx, edgeBG.ID, knowledge.EdgeInput{Relation: "connected_to"})
	if err != nil {
		t.Fatalf("update edge: %v", err)
	}
	if updated.Relation != "connected_to" {
		t.Fatalf("expected updated relation, got %q", updated.Relation)
	}

	nextRun := time.Now().UTC().Add(time.Minute)
	if err := store.UpsertSchedulerState(ctx, "schedule_hello", "", "idle", "rk1", "worker-1", nil, nil, &nextRun, map[string]any{"ok": true}, ""); err != nil {
		t.Fatalf("upsert scheduler state: %v", err)
	}
	states, err := store.ListSchedulerStates(ctx)
	if err != nil {
		t.Fatalf("list scheduler states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 scheduler state, got %d", len(states))
	}

	if err := store.DeleteNode(ctx, beta.ID); err != nil {
		t.Fatalf("delete beta node: %v", err)
	}
	edges, err := store.ListEdges(ctx, knowledge.EdgeFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list edges after delete: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected cascaded edge delete, got %d edges", len(edges))
	}
}

func TestListNodesHandlesSpecialFTSChars(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "fts-special.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	// Seed a node so the table isn't empty.
	_, err = store.CreateNode(ctx, knowledge.NodeInput{
		Kind:    "Note",
		Name:    "Agent Research",
		Content: "agent research foo bar baz",
	})
	if err != nil {
		t.Fatalf("create seed node: %v", err)
	}

	// Queries that previously caused "no such column" or FTS5 parse errors.
	queries := []string{
		"agent:foo",
		"foo:bar baz",
		"don't",
		`hello "world"`,
		"-term",
		":",
		"agent:research",
		"(nested)",
		"a*b",
		"NEAR AND OR NOT",
		"",
		"   ",
	}

	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			_, err := store.ListNodes(ctx, q, "", 10)
			if err != nil {
				t.Fatalf("ListNodes(%q) returned error: %v", q, err)
			}
		})
	}
}
