package cmd

import (
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/epubextract"
	"github.com/raitucarp/epub"
)

func TestFilesURLCommandBuildsWorkspaceFileURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	filePath := filepath.Join(home, ".nalvin", "workspaces", "alpha", "downloads", "videos", "Ghosts S05E14.mp4")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir file dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out, err := executeRoot(t, "--workspace", "alpha", "files", "url", "downloads/videos/Ghosts S05E14.mp4")
	if err != nil {
		t.Fatalf("files url: %v", err)
	}

	got := strings.TrimSpace(out)
	want := "http://127.0.0.1:4210/files/alpha/downloads/videos/Ghosts%20S05E14.mp4"
	if got != want {
		t.Fatalf("files url = %q, want %q", got, want)
	}
}

func TestFilesURLCommandJSONSupportsBaseOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	filePath := filepath.Join(home, ".nalvin", "workspaces", "alpha", "downloads", "video.mp4")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir file dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out, err := executeRoot(t, "--json", "--workspace", "alpha", "files", "url", "--base-url", "https://media.example.test/root", "downloads/video.mp4")
	if err != nil {
		t.Fatalf("files url: %v", err)
	}

	var payload fileURLPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Workspace != "alpha" || payload.Path != "downloads/video.mp4" {
		t.Fatalf("unexpected payload metadata: %#v", payload)
	}
	if payload.BaseURL != "https://media.example.test/root" {
		t.Fatalf("base url = %q", payload.BaseURL)
	}
	if payload.URL != "https://media.example.test/root/files/alpha/downloads/video.mp4" {
		t.Fatalf("url = %q", payload.URL)
	}
}

func TestFilesExtractEPUBCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	epubPath := filepath.Join(home, ".nalvin", "workspaces", "alpha", "books", "demo.epub")
	if err := os.MkdirAll(filepath.Dir(epubPath), 0o755); err != nil {
		t.Fatalf("mkdir epub dir: %v", err)
	}
	writeCommandTestEPUB(t, epubPath)

	out, err := executeRoot(t, "--workspace", "alpha", "files", "extract-epub", "books/demo.epub")
	if err != nil {
		t.Fatalf("files extract-epub: %v", err)
	}

	for _, needle := range []string{
		"Book Title: Demo Book",
		"Author: Command Tester",
		"Output Directory: demo_extracted",
		"Chapter Count: 2",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected output to contain %q, got:\n%s", needle, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".nalvin", "workspaces", "alpha", "demo_extracted", "TOC.md")); err != nil {
		t.Fatalf("expected TOC.md to exist: %v", err)
	}
}

func TestFilesExtractEPUBCommandJSONOutAndForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	epubPath := filepath.Join(home, ".nalvin", "workspaces", "alpha", "books", "demo.epub")
	if err := os.MkdirAll(filepath.Dir(epubPath), 0o755); err != nil {
		t.Fatalf("mkdir epub dir: %v", err)
	}
	writeCommandTestEPUB(t, epubPath)

	out, err := executeRoot(t, "--json", "--workspace", "alpha", "files", "extract-epub", "--out", "exports/demo-md", "books/demo.epub")
	if err != nil {
		t.Fatalf("files extract-epub json: %v", err)
	}

	var payload epubextract.Result
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.OutputPath != "exports/demo-md" || payload.ChapterCount != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	stalePath := filepath.Join(home, ".nalvin", "workspaces", "alpha", "exports", "demo-md", "stale.txt")
	if err := os.WriteFile(stalePath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	if _, err := executeRoot(t, "--workspace", "alpha", "files", "extract-epub", "--out", "exports/demo-md", "books/demo.epub"); err == nil || !strings.Contains(err.Error(), "output path already exists") {
		t.Fatalf("expected existing output error, got %v", err)
	}

	if _, err := executeRoot(t, "--workspace", "alpha", "files", "extract-epub", "--force", "--out", "exports/demo-md", "books/demo.epub"); err != nil {
		t.Fatalf("files extract-epub force: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, stat err=%v", err)
	}
}

func writeCommandTestEPUB(t *testing.T, filename string) {
	t.Helper()

	writer := epub.New("urn:cmd-demo")
	writer.Title("Demo Book")
	writer.Author("Command Tester")
	writer.Languages("en")
	if err := writer.CoverPNG(commandCoverImage()); err != nil {
		t.Fatalf("cover png: %v", err)
	}
	writer.AddContent("chapter-1.xhtml", []byte(commandXHTML("Alpha", "First body")))
	writer.AddContent("chapter-2.xhtml", []byte(commandXHTML("Bravo", "Second body")))
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

func commandCoverImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	return img
}

func commandXHTML(title, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>` + title + `</title></head>
  <body>
    <h1>` + title + `</h1>
    <p>` + body + `</p>
  </body>
</html>`
}
