package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	wikipediapkg "github.com/versionlens/OpenNalvin/internal/wikipedia"
)

func TestWikipediaGetCommand(t *testing.T) {
	originalFetch := wikipediaFetch
	t.Cleanup(func() {
		wikipediaFetch = originalFetch
	})

	wikipediaFetch = func(_ context.Context, req wikipediapkg.FetchRequest) (wikipediapkg.FetchResult, error) {
		if req.Title != "Ada Lovelace" {
			t.Fatalf("unexpected title: %q", req.Title)
		}
		return wikipediapkg.FetchResult{
			Language:   "en",
			Title:      "Ada Lovelace",
			PageID:     42,
			ArticleURL: "https://en.wikipedia.org/wiki/Ada_Lovelace",
			Summary: wikipediapkg.Summary{
				Description: "English mathematician",
				Extract:     "She wrote about computing.",
			},
			Sections: []wikipediapkg.SectionIndex{
				{Index: "1", Number: "1", Title: "Life"},
				{Index: "2", Number: "2", Title: "Legacy"},
			},
		}, nil
	}

	out, err := executeRoot(t, "wikipedia", "get", "Ada Lovelace")
	if err != nil {
		t.Fatalf("wikipedia get: %v", err)
	}

	for _, needle := range []string{
		"Title: Ada Lovelace",
		"Description: English mathematician",
		"Article URL: https://en.wikipedia.org/wiki/Ada_Lovelace",
		"She wrote about computing.",
		"Sections:",
		"1 (1) Life",
		"2 (2) Legacy",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("expected output to contain %q, got:\n%s", needle, out)
		}
	}
}

func TestWikipediaGetCommandJSONAndSection(t *testing.T) {
	originalFetch := wikipediaFetch
	t.Cleanup(func() {
		wikipediaFetch = originalFetch
	})

	wikipediaFetch = func(_ context.Context, req wikipediapkg.FetchRequest) (wikipediapkg.FetchResult, error) {
		if req.Language != "sv" || req.SectionIndex != "2" {
			t.Fatalf("unexpected request: %#v", req)
		}
		return wikipediapkg.FetchResult{
			Language: "sv",
			Title:    "Ada Lovelace",
			PageID:   42,
			Summary: wikipediapkg.Summary{
				Extract: "Kort sammanfattning.",
			},
			Sections: []wikipediapkg.SectionIndex{
				{Index: "2", Number: "2", Title: "Legacy"},
			},
			Section: &wikipediapkg.Section{
				Index:  "2",
				Number: "2",
				Title:  "Legacy",
				Text:   "Detaljer om hennes arv.",
			},
		}, nil
	}

	out, err := executeRoot(t, "--json", "wikipedia", "get", "--lang", "sv", "--section-index", "2", "Ada Lovelace")
	if err != nil {
		t.Fatalf("wikipedia get json: %v", err)
	}

	var payload wikipediapkg.FetchResult
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Language != "sv" || payload.Section == nil || payload.Section.Text != "Detaljer om hennes arv." {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
