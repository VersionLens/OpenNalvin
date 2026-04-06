package epubextract

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/raitucarp/epub"
)

func TestExtractPrefersTOCOrderAndDedupesRepeatedHref(t *testing.T) {
	t.Parallel()

	paths := workspace.Paths{Name: "alpha", FilesPath: t.TempDir()}
	epubPath := filepath.Join(paths.FilesPath, "books", "demo.epub")
	if err := os.MkdirAll(filepath.Dir(epubPath), 0o755); err != nil {
		t.Fatalf("mkdir epub dir: %v", err)
	}

	writeTestEPUB(t, epubPath, testEPUBSpec{
		Title:  "Demo Book",
		Author: "Test Author",
		Docs: []testDoc{
			{Filename: "chapter-1.xhtml", Title: "Alpha", Body: "First chapter"},
			{Filename: "chapter-2.xhtml", Title: "Bravo", Body: "Second chapter"},
			{Filename: "chapter-3.xhtml", Title: "Charlie", Body: "Third chapter"},
		},
		TOC: epub.TOC{
			Title: "Contents",
			Items: []epub.TOC{
				{Title: "Bravo First", Href: "chapter-2.xhtml"},
				{Title: "Alpha Once", Href: "chapter-1.xhtml"},
				{Href: "chapter-3.xhtml"},
			},
		},
	})
	rewriteTOCNavHTML(t, epubPath, `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>Contents</title></head>
  <body>
    <nav epub:type="toc" id="toc">
      <h1>Contents</h1>
      <ol>
        <li><a href="chapter-2.xhtml">Bravo First</a></li>
        <li><span>Part One</span>
          <ol>
            <li><a href="chapter-1.xhtml">Alpha Once</a></li>
            <li><a href="chapter-1.xhtml#repeat">Alpha Again</a></li>
          </ol>
        </li>
        <li><a href="chapter-3.xhtml"></a></li>
      </ol>
    </nav>
  </body>
</html>`)

	result, err := Extract(paths, Options{InputPath: "books/demo.epub"})
	if err != nil {
		t.Fatalf("extract epub: %v", err)
	}

	if result.BookTitle != "Demo Book" {
		t.Fatalf("book title = %q", result.BookTitle)
	}
	if result.Author != "Test Author" {
		t.Fatalf("author = %q", result.Author)
	}
	if result.OutputPath != "demo_extracted" {
		t.Fatalf("output path = %q", result.OutputPath)
	}
	if result.ChapterCount != 3 {
		t.Fatalf("chapter count = %d", result.ChapterCount)
	}

	gotTitles := []string{result.Chapters[0].Title, result.Chapters[1].Title, result.Chapters[2].Title}
	wantTitles := []string{"Bravo First", "Alpha Once", "Charlie"}
	if strings.Join(gotTitles, "|") != strings.Join(wantTitles, "|") {
		t.Fatalf("chapter titles = %#v, want %#v", gotTitles, wantTitles)
	}

	gotPaths := []string{result.Chapters[0].Path, result.Chapters[1].Path, result.Chapters[2].Path}
	wantPaths := []string{
		"demo_extracted/001_bravo_first.md",
		"demo_extracted/002_alpha_once.md",
		"demo_extracted/003_charlie.md",
	}
	if strings.Join(gotPaths, "|") != strings.Join(wantPaths, "|") {
		t.Fatalf("chapter paths = %#v, want %#v", gotPaths, wantPaths)
	}

	tocBytes, err := os.ReadFile(filepath.Join(paths.FilesPath, filepath.FromSlash(result.TOCPath)))
	if err != nil {
		t.Fatalf("read toc: %v", err)
	}
	toc := string(tocBytes)
	for _, needle := range []string{
		"- [Bravo First](001_bravo_first.md)",
		"  - [Alpha Once](002_alpha_once.md)",
		"  - [Alpha Again](002_alpha_once.md)",
		"- [Charlie](003_charlie.md)",
	} {
		if !strings.Contains(toc, needle) {
			t.Fatalf("TOC missing %q in:\n%s", needle, toc)
		}
	}
}

func TestExtractFallsBackToSpineWhenTOCIsMissing(t *testing.T) {
	t.Parallel()

	paths := workspace.Paths{Name: "alpha", FilesPath: t.TempDir()}
	epubPath := filepath.Join(paths.FilesPath, "novel.epub")

	writeTestEPUB(t, epubPath, testEPUBSpec{
		Title:  "Fallback Book",
		Author: "Fallback Author",
		Docs: []testDoc{
			{Filename: "one.xhtml", Title: "One", Body: "Body one"},
			{Filename: "two.xhtml", Title: "Two", Body: "Body two"},
		},
		TOC: epub.TOC{
			Title: "Contents",
			Items: []epub.TOC{
				{Title: "One", Href: "one.xhtml"},
				{Title: "Two", Href: "two.xhtml"},
			},
		},
	})
	stripTOCFromEPUB(t, epubPath)

	result, err := Extract(paths, Options{InputPath: "novel.epub"})
	if err != nil {
		t.Fatalf("extract fallback epub: %v", err)
	}

	if result.ChapterCount != 2 {
		t.Fatalf("chapter count = %d", result.ChapterCount)
	}
	if result.Chapters[0].Title != "One" || result.Chapters[1].Title != "Two" {
		t.Fatalf("unexpected fallback titles: %#v", result.Chapters)
	}

	tocBytes, err := os.ReadFile(filepath.Join(paths.FilesPath, filepath.FromSlash(result.TOCPath)))
	if err != nil {
		t.Fatalf("read fallback toc: %v", err)
	}
	toc := string(tocBytes)
	if !strings.Contains(toc, "- [One](001_one.md)") || !strings.Contains(toc, "- [Two](002_two.md)") {
		t.Fatalf("unexpected fallback toc:\n%s", toc)
	}
}

func TestExtractRejectsUnsafeAndExistingOutputPaths(t *testing.T) {
	t.Parallel()

	paths := workspace.Paths{Name: "alpha", FilesPath: t.TempDir()}
	epubPath := filepath.Join(paths.FilesPath, "demo.epub")
	writeTestEPUB(t, epubPath, testEPUBSpec{
		Title: "Demo",
		Docs: []testDoc{
			{Filename: "chapter.xhtml", Title: "Chapter", Body: "Hello"},
		},
		TOC: epub.TOC{
			Title: "Contents",
			Items: []epub.TOC{
				{Title: "Chapter", Href: "chapter.xhtml"},
			},
		},
	})

	if _, err := Extract(paths, Options{InputPath: "../demo.epub"}); err == nil || !strings.Contains(err.Error(), "path must stay within the workspace root") {
		t.Fatalf("expected traversal error, got %v", err)
	}
	if _, err := Extract(paths, Options{InputPath: "missing.epub"}); err == nil {
		t.Fatalf("expected missing input error")
	}
	if _, err := Extract(paths, Options{InputPath: "demo.epub", OutputPath: "."}); err == nil || !strings.Contains(err.Error(), "path must not resolve to the workspace root") {
		t.Fatalf("expected workspace-root output error, got %v", err)
	}

	if _, err := Extract(paths, Options{InputPath: "demo.epub"}); err != nil {
		t.Fatalf("initial extract: %v", err)
	}
	if _, err := Extract(paths, Options{InputPath: "demo.epub"}); err == nil || !strings.Contains(err.Error(), "output path already exists") {
		t.Fatalf("expected existing output error, got %v", err)
	}
}

func TestExtractForceReplacesExistingOutput(t *testing.T) {
	t.Parallel()

	paths := workspace.Paths{Name: "alpha", FilesPath: t.TempDir()}
	epubPath := filepath.Join(paths.FilesPath, "demo.epub")
	writeTestEPUB(t, epubPath, testEPUBSpec{
		Title: "Demo",
		Docs: []testDoc{
			{Filename: "chapter.xhtml", Title: "Chapter", Body: "Hello"},
		},
		TOC: epub.TOC{
			Title: "Contents",
			Items: []epub.TOC{
				{Title: "Chapter", Href: "chapter.xhtml"},
			},
		},
	})

	result, err := Extract(paths, Options{InputPath: "demo.epub"})
	if err != nil {
		t.Fatalf("initial extract: %v", err)
	}
	stalePath := filepath.Join(paths.FilesPath, filepath.FromSlash(result.OutputPath), "stale.txt")
	if err := os.WriteFile(stalePath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	if _, err := Extract(paths, Options{InputPath: "demo.epub", Force: true}); err != nil {
		t.Fatalf("force extract: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale file to be removed, stat err=%v", err)
	}
}

type testEPUBSpec struct {
	Title  string
	Author string
	Docs   []testDoc
	TOC    epub.TOC
}

type testDoc struct {
	Filename string
	Title    string
	Body     string
}

func writeTestEPUB(t *testing.T, filename string, spec testEPUBSpec) {
	t.Helper()

	writer := epub.New("urn:test-book")
	writer.Title(spec.Title)
	writer.Author(spec.Author)
	writer.Languages("en")
	if err := writer.CoverPNG(singlePixelImage()); err != nil {
		t.Fatalf("cover png: %v", err)
	}

	for _, doc := range spec.Docs {
		writer.AddContent(doc.Filename, []byte(testXHTML(doc.Title, doc.Body)))
	}
	if err := writer.TableOfContents("table_of_contents", spec.TOC); err != nil {
		t.Fatalf("table of contents: %v", err)
	}
	if err := writer.Write(filename); err != nil {
		t.Fatalf("write epub: %v", err)
	}
}

func singlePixelImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	return img
}

func testXHTML(title, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head>
    <title>` + title + `</title>
  </head>
  <body>
    <h1>` + title + `</h1>
    <p>` + body + `</p>
  </body>
</html>`
}

func stripTOCFromEPUB(t *testing.T, filename string) {
	t.Helper()

	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()

	var output bytes.Buffer
	writer := zip.NewWriter(&output)

	manifestNavPattern := regexp.MustCompile(`\s*<item[^>]*href="table_of_contents\.xhtml"[^>]*/>`)
	manifestNCXPattern := regexp.MustCompile(`\s*<item[^>]*href="table_of_contents\.ncx"[^>]*/>`)

	for _, file := range reader.File {
		if file.Name == "epub/table_of_contents.xhtml" || file.Name == "epub/table_of_contents.ncx" {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip member %q: %v", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip member %q: %v", file.Name, err)
		}
		if strings.HasSuffix(file.Name, ".opf") {
			text := string(content)
			text = manifestNavPattern.ReplaceAllString(text, "")
			text = manifestNCXPattern.ReplaceAllString(text, "")
			content = []byte(text)
		}

		header := file.FileHeader
		w, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatalf("create zip member %q: %v", file.Name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip member %q: %v", file.Name, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := os.WriteFile(filename, output.Bytes(), 0o644); err != nil {
		t.Fatalf("write stripped epub: %v", err)
	}
}

func rewriteTOCNavHTML(t *testing.T, filename, navHTML string) {
	t.Helper()

	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()

	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip member %q: %v", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip member %q: %v", file.Name, err)
		}
		if file.Name == "epub/table_of_contents.xhtml" {
			content = []byte(navHTML)
		}

		header := file.FileHeader
		w, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatalf("create zip member %q: %v", file.Name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip member %q: %v", file.Name, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := os.WriteFile(filename, output.Bytes(), 0o644); err != nil {
		t.Fatalf("write patched epub: %v", err)
	}
}
