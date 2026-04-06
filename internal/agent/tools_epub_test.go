package agent

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/epubextract"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/raitucarp/epub"
)

func TestExtractEpubToolEnabledButHiddenByDefault(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "epub-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	result, err := ListTools(context.Background(), store, "", ToolSelection{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	tool := requireTool(t, result.Tools, "extract_epub")
	if !tool.Enabled {
		t.Fatalf("tool should be enabled by default")
	}
	if tool.Visible || tool.Pinned {
		t.Fatalf("tool should start hidden and unpinned: %#v", tool)
	}
}

func TestSearchToolsRevealExtractEpubToolByKeywords(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "epub-search-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	for _, query := range []string{"epub", "book to markdown", "convert epub"} {
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
			if !slices.Contains(payload.RevealedToolIDs, "extract_epub") {
				t.Fatalf("expected extract_epub to be revealed, got %#v", payload.RevealedToolIDs)
			}
		})
	}
}

func TestExtractEpubToolInvocation(t *testing.T) {
	t.Parallel()

	ctx, paths := workspaceFileToolContext(t)
	epubPath := filepath.Join(paths.FilesPath, "books", "demo.epub")
	if err := os.MkdirAll(filepath.Dir(epubPath), 0o755); err != nil {
		t.Fatalf("mkdir epub dir: %v", err)
	}
	writeAgentTestEPUB(t, epubPath)

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "epub-invoke.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	runResult, err := InvokeTool(ctx, store, "", "extract_epub", map[string]any{
		"path": "books/demo.epub",
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke extract_epub: %v", err)
	}
	if runResult.IsError {
		t.Fatalf("unexpected tool error: %s", runResult.Output)
	}

	var payload epubextract.Result
	if err := json.Unmarshal([]byte(runResult.Output), &payload); err != nil {
		t.Fatalf("parse tool payload: %v", err)
	}
	if payload.OutputPath != "demo_extracted" || payload.ChapterCount != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	viewResp, err := InvokeTool(ctx, store, "", "extract_epub", map[string]any{
		"path":     "books/demo.epub",
		"out_path": "custom/demo-md",
		"force":    true,
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke extract_epub custom out: %v", err)
	}
	customPayload := decodeJSONToolRunResult[epubextract.Result](t, viewResp)
	if customPayload.OutputPath != "custom/demo-md" {
		t.Fatalf("custom output path = %q", customPayload.OutputPath)
	}
	if _, err := os.Stat(filepath.Join(paths.FilesPath, "custom", "demo-md", "TOC.md")); err != nil {
		t.Fatalf("expected custom TOC.md to exist: %v", err)
	}
}

func writeAgentTestEPUB(t *testing.T, filename string) {
	t.Helper()

	writer := epub.New("urn:agent-demo")
	writer.Title("Agent Demo")
	writer.Author("Agent Tester")
	writer.Languages("en")
	if err := writer.CoverPNG(agentCoverImage()); err != nil {
		t.Fatalf("cover png: %v", err)
	}
	writer.AddContent("chapter-1.xhtml", []byte(agentXHTML("Alpha", "First body")))
	writer.AddContent("chapter-2.xhtml", []byte(agentXHTML("Bravo", "Second body")))
	if err := writer.TableOfContents("table_of_contents", epub.TOC{
		Title: "Contents",
		Items: []epub.TOC{
			{Title: "Alpha", Href: "chapter-1.xhtml"},
			{Title: "Bravo", Href: "chapter-2.xhtml"},
		},
	}); err != nil {
		t.Fatalf("table of contents: %v", err)
	}
	if err := writer.Write(filename); err != nil {
		t.Fatalf("write epub: %v", err)
	}
}

func agentCoverImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	return img
}

func agentXHTML(title, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>` + title + `</title></head>
  <body>
    <h1>` + title + `</h1>
    <p>` + body + `</p>
  </body>
</html>`
}

func decodeJSONToolRunResult[T any](t *testing.T, result ToolRunResult) T {
	t.Helper()

	if result.IsError {
		t.Fatalf("expected successful tool result, got error: %s", result.Output)
	}

	var payload T
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("parse tool run result: %v", err)
	}
	return payload
}
