package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

func TestFetchFeedsSortsNewestFirstAndCollectsWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed-a":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Feed A</title>
<item><title>Older</title><link>https://example.com/older</link><pubDate>Mon, 01 Jan 2024 08:00:00 GMT</pubDate></item>
<item><title>Newest</title><link>https://example.com/newest</link><pubDate>Mon, 01 Jan 2024 10:00:00 GMT</pubDate></item>
</channel></rss>`))
		case "/feed-b":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Feed B</title>
<item><title>Middle</title><link>https://example.com/middle</link><pubDate>Mon, 01 Jan 2024 09:00:00 GMT</pubDate></item>
</channel></rss>`))
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client())
	items, warnings, err := client.FetchFeeds(context.Background(), []configpkg.RSSFeedConfig{
		{Name: "feed-a", URL: server.URL + "/feed-a"},
		{Name: "broken", URL: server.URL + "/broken"},
		{Name: "feed-b", URL: server.URL + "/feed-b"},
	}, 0)
	if err != nil {
		t.Fatalf("fetch feeds: %v", err)
	}

	if len(warnings) != 1 || warnings[0].FeedName != "broken" {
		t.Fatalf("expected one warning for broken feed, got %#v", warnings)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 rss items, got %d", len(items))
	}
	if items[0].Title != "Newest" || items[1].Title != "Middle" || items[2].Title != "Older" {
		t.Fatalf("expected newest-first sort order, got %#v", items)
	}
}

func TestFetchCategoryReturnsUnknownCategoryError(t *testing.T) {
	_, _, err := FetchCategory(context.Background(), configpkg.RSSConfig{}, "missing", 5)
	if err == nil {
		t.Fatalf("expected unknown category error")
	}
}
