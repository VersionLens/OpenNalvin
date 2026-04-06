package wikipedia

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientFetchSummaryAndSectionIndex(t *testing.T) {
	t.Parallel()

	server := newWikipediaTestServer(t)
	client := NewClient(Config{
		SummaryBaseURLTemplate: server.URL,
		ActionBaseURLTemplate:  server.URL,
		HTTPClient:             server.Client(),
	})

	result, err := client.Fetch(context.Background(), FetchRequest{
		Title:    "Ada Lovelace",
		Language: "en",
	})
	if err != nil {
		t.Fatalf("fetch wikipedia: %v", err)
	}

	if result.Title != "Ada Lovelace" || result.PageID != 42 {
		t.Fatalf("unexpected result metadata: %#v", result)
	}
	if result.Summary.Extract != "Mathematician and writer." {
		t.Fatalf("summary extract = %q", result.Summary.Extract)
	}
	if len(result.Sections) != 2 || result.Sections[0].Title != "Life" || result.Sections[1].Index != "2" {
		t.Fatalf("unexpected sections: %#v", result.Sections)
	}
}

func TestClientFetchSectionContent(t *testing.T) {
	t.Parallel()

	server := newWikipediaTestServer(t)
	client := NewClient(Config{
		SummaryBaseURLTemplate: server.URL,
		ActionBaseURLTemplate:  server.URL,
		HTTPClient:             server.Client(),
	})

	result, err := client.Fetch(context.Background(), FetchRequest{
		Title:        "Ada Lovelace",
		SectionIndex: "2",
	})
	if err != nil {
		t.Fatalf("fetch wikipedia section: %v", err)
	}
	if result.Section == nil {
		t.Fatalf("expected section payload")
	}
	if result.Section.Title != "Legacy" {
		t.Fatalf("section title = %q", result.Section.Title)
	}
	if result.Section.Text != "Legacy She inspired modern computing." {
		t.Fatalf("section text = %q", result.Section.Text)
	}
	if !strings.Contains(result.Section.HTML, "<h2 id=\"Legacy\">Legacy</h2>") {
		t.Fatalf("unexpected section html: %q", result.Section.HTML)
	}
}

func TestClientFetchHandlesErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"missingtitle","info":"The page does not exist"}}`, http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(Config{
		SummaryBaseURLTemplate: server.URL,
		ActionBaseURLTemplate:  server.URL,
		HTTPClient:             server.Client(),
	})

	if _, err := client.Fetch(context.Background(), FetchRequest{Title: "Missing"}); err == nil || !strings.Contains(err.Error(), "The page does not exist") {
		t.Fatalf("expected descriptive error, got %v", err)
	}
}

func newWikipediaTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/rest_v1/page/summary/"):
			fmt.Fprint(w, `{"type":"standard","title":"Ada Lovelace","pageid":42,"revision":"123","timestamp":"2026-04-05T00:00:00Z","description":"English mathematician","extract":"Mathematician and writer.","extract_html":"<p>Mathematician and writer.</p>","content_urls":{"desktop":{"page":"https://en.wikipedia.org/wiki/Ada_Lovelace"}}}`)
		case r.URL.Path == "/w/api.php" && r.URL.Query().Get("prop") == "tocdata":
			fmt.Fprint(w, `{"parse":{"title":"Ada Lovelace","pageid":42,"tocdata":{"sections":[{"tocLevel":1,"hLevel":2,"line":"Life","number":"1","index":"1","anchor":"Life"},{"tocLevel":1,"hLevel":2,"line":"Legacy","number":"2","index":"2","anchor":"Legacy"}]}}}`)
		case r.URL.Path == "/w/api.php" && r.URL.Query().Get("prop") == "text" && r.URL.Query().Get("section") == "2":
			fmt.Fprint(w, `{"parse":{"title":"Ada Lovelace","pageid":42,"text":"<div class=\"mw-parser-output\"><h2 id=\"Legacy\">Legacy</h2><span class=\"mw-editsection\">edit</span><p>She inspired <sup>[1]</sup> modern computing.</p></div>"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
}
