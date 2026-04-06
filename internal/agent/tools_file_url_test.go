package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestWorkspaceFileURLToolEnabledButHiddenByDefault(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "file-url-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	result, err := ListTools(context.Background(), store, "", ToolSelection{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	tool := requireTool(t, result.Tools, "get_workspace_file_url")
	if !tool.Enabled {
		t.Fatalf("tool should be enabled by default")
	}
	if tool.Visible || tool.Pinned {
		t.Fatalf("tool should start hidden and unpinned: %#v", tool)
	}
}

func TestSearchToolsRevealWorkspaceFileURLToolByKeywords(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "file-url-search-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	for _, query := range []string{"file url", "stream video", "vlc"} {
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
			if !slices.Contains(payload.RevealedToolIDs, "get_workspace_file_url") {
				t.Fatalf("expected get_workspace_file_url to be revealed, got %#v", payload.RevealedToolIDs)
			}
		})
	}
}
