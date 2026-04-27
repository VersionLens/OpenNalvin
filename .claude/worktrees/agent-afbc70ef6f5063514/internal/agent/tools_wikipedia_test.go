package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	wikipediapkg "github.com/versionlens/OpenNalvin/internal/wikipedia"
)

func TestWikipediaGetArticleToolEnabledButHiddenByDefault(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "wikipedia-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	result, err := ListTools(context.Background(), store, "", ToolSelection{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	tool := requireTool(t, result.Tools, "wikipedia_get_article")
	if !tool.Enabled {
		t.Fatalf("tool should be enabled by default")
	}
	if tool.Visible || tool.Pinned {
		t.Fatalf("tool should start hidden and unpinned: %#v", tool)
	}
}

func TestSearchToolsRevealWikipediaToolByKeywords(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "wikipedia-search-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	for _, query := range []string{"wikipedia", "wiki section", "encyclopedia"} {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			runResult, err := InvokeTool(context.Background(), store, "", "search_tools", map[string]any{
				"query": query,
			}, ToolSelection{})
			if err != nil {
				t.Fatalf("invoke search_tools: %v", err)
			}

			var payload searchToolsResponse
			if err := json.Unmarshal([]byte(runResult.Output), &payload); err != nil {
				t.Fatalf("parse search_tools response: %v", err)
			}
			if !slices.Contains(payload.RevealedToolIDs, "wikipedia_get_article") {
				t.Fatalf("expected wikipedia_get_article to be revealed, got %#v", payload.RevealedToolIDs)
			}
		})
	}
}

func TestWikipediaGetArticleToolInvocation(t *testing.T) {
	originalFetch := wikipediaFetchTool
	t.Cleanup(func() {
		wikipediaFetchTool = originalFetch
	})

	wikipediaFetchTool = func(_ context.Context, req wikipediapkg.FetchRequest) (wikipediapkg.FetchResult, error) {
		if req.Title != "Ada Lovelace" || req.Language != "en" || req.SectionIndex != "2" {
			t.Fatalf("unexpected request: %#v", req)
		}
		return wikipediapkg.FetchResult{
			Language: "en",
			Title:    "Ada Lovelace",
			PageID:   42,
			Summary: wikipediapkg.Summary{
				Extract: "Mathematician and writer.",
			},
			Sections: []wikipediapkg.SectionIndex{
				{Index: "1", Number: "1", Title: "Life"},
				{Index: "2", Number: "2", Title: "Legacy"},
			},
			Section: &wikipediapkg.Section{
				Index:  "2",
				Number: "2",
				Title:  "Legacy",
				Text:   "She inspired modern computing.",
			},
		}, nil
	}

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "wikipedia-invoke.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	runResult, err := InvokeTool(context.Background(), store, "", "wikipedia_get_article", map[string]any{
		"title":         "Ada Lovelace",
		"language":      "en",
		"section_index": "2",
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke wikipedia_get_article: %v", err)
	}
	if runResult.IsError {
		t.Fatalf("unexpected tool error: %s", runResult.Output)
	}

	var payload wikipediapkg.FetchResult
	if err := json.Unmarshal([]byte(runResult.Output), &payload); err != nil {
		t.Fatalf("parse tool payload: %v", err)
	}
	if payload.Title != "Ada Lovelace" || payload.Section == nil || payload.Section.Title != "Legacy" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
