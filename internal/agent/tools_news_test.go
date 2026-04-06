package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	redditnews "github.com/versionlens/OpenNalvin/internal/news/reddit"
	rssnews "github.com/versionlens/OpenNalvin/internal/news/rss"
)

type newsRSSHeadlinesResponse struct {
	Items               []rssnews.Item    `json:"items"`
	Warnings            []rssnews.Warning `json:"warnings"`
	AvailableCategories []string          `json:"available_categories"`
}

type newsRedditTopPostsResponse struct {
	Posts               []redditnews.Post    `json:"posts"`
	Warnings            []redditnews.Warning `json:"warnings"`
	AvailableCategories []string             `json:"available_categories"`
}

type newsRedditPostDetailsResponse struct {
	Post redditnews.PostDetails `json:"post"`
}

func TestNewsToolsEnabledByDefaultButHidden(t *testing.T) {
	rt, err := newAgentRuntime(context.Background(), nil, newEphemeralSessionState(context.Background(), nil, "run_test"), StoredTrace{}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	for _, id := range []string{"news_rss_headlines", "news_reddit_top_posts", "news_reddit_post_details"} {
		tool := requireTool(t, rt.catalogResult().Tools, id)
		if !tool.Enabled {
			t.Fatalf("expected %s to be enabled by default", id)
		}
		if tool.Pinned {
			t.Fatalf("expected %s to remain unpinned by default", id)
		}
		if tool.Visible {
			t.Fatalf("expected %s to remain hidden until revealed", id)
		}
	}
}

func TestSearchToolsFindsNewsTools(t *testing.T) {
	rt, err := newAgentRuntime(context.Background(), nil, newEphemeralSessionState(context.Background(), nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "news_rss_headlines", "news_reddit_top_posts", "news_reddit_post_details"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(context.Background(), searchToolsInput{Query: "rss headlines"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools rss: %v", err)
	}
	rssPayload := decodeSearchToolsResponse(t, resp)
	if len(rssPayload.Tools) == 0 || rssPayload.Tools[0].ID != "news_rss_headlines" {
		t.Fatalf("expected news_rss_headlines first for rss query, got %#v", rssPayload.Tools)
	}

	resp, err = rt.searchTools(context.Background(), searchToolsInput{Query: "reddit comments permalink"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools reddit: %v", err)
	}
	redditPayload := decodeSearchToolsResponse(t, resp)
	if len(redditPayload.Tools) == 0 || redditPayload.Tools[0].ID != "news_reddit_post_details" {
		t.Fatalf("expected news_reddit_post_details first for reddit detail query, got %#v", redditPayload.Tools)
	}
}

func TestNewsToolCatalogPreservesFieldDescriptions(t *testing.T) {
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		RSS: configpkg.RSSConfig{
			Feeds: map[string][]configpkg.RSSFeedConfig{
				"ai":         {{Name: "rss-ai", URL: "https://example.com/rss-ai.xml"}},
				"analysis":   {{Name: "rss-analysis", URL: "https://example.com/rss-analysis.xml"}},
				"commentary": {{Name: "rss-commentary", URL: "https://example.com/rss-commentary.xml"}},
				"politics":   {{Name: "rss-politics", URL: "https://example.com/rss-politics.xml"}},
				"tech":       {{Name: "rss-tech", URL: "https://example.com/rss-tech.xml"}},
			},
		},
		Reddit: configpkg.RedditConfig{
			Subreddits: map[string][]string{
				"ai":       {"LocalLLaMA"},
				"cyber":    {"cybersecurity"},
				"finance":  {"Economics"},
				"politics": {"politics"},
				"tech":     {"technology"},
				"world":    {"worldnews"},
			},
		},
	})
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	rssTool := requireTool(t, rt.catalogResult().Tools, "news_rss_headlines")
	categoryProp := rssTool.Schema.Properties["category"].(map[string]any)
	feedNamesProp := rssTool.Schema.Properties["feed_names"].(map[string]any)
	if got := categoryProp["description"]; got != "Configured RSS category to fetch, for example ai, tech, politics, analysis, or commentary." {
		t.Fatalf("unexpected category description: %#v", got)
	}
	if got := feedNamesProp["description"]; got != "Optional explicit configured RSS feed names to fetch instead of a category." {
		t.Fatalf("unexpected feed_names description: %#v", got)
	}

	redditTool := requireTool(t, rt.catalogResult().Tools, "news_reddit_top_posts")
	timeFilterProp := redditTool.Schema.Properties["time_filter"].(map[string]any)
	if got := timeFilterProp["description"]; got != "Top-post time filter. Defaults to day. Supported Reddit values include hour, day, week, month, year, and all." {
		t.Fatalf("unexpected time_filter description: %#v", got)
	}

	if got := rssTool.Description; !strings.Contains(got, "Available categories: ai, analysis, commentary, politics, tech.") {
		t.Fatalf("expected rss tool description to list categories, got %q", got)
	}
	if got := redditTool.Description; !strings.Contains(got, "Available categories: ai, cyber, finance, politics, tech, world.") {
		t.Fatalf("expected reddit tool description to list categories, got %q", got)
	}
}

func TestSearchToolsReturnsNewsFieldDescriptions(t *testing.T) {
	rt, err := newAgentRuntime(context.Background(), nil, newEphemeralSessionState(context.Background(), nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "news_rss_headlines"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(context.Background(), searchToolsInput{Query: "rss headlines"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools rss: %v", err)
	}
	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) == 0 {
		t.Fatalf("expected at least one tool in search response")
	}
	categoryProp := payload.Tools[0].Schema.Properties["category"].(map[string]any)
	if got := categoryProp["description"]; got != "Configured RSS category to fetch, for example ai, tech, politics, analysis, or commentary." {
		t.Fatalf("unexpected search_tools category description: %#v", got)
	}
}

func TestNewsToolsDirectInvocationAndValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rss":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Feed</title>
<item><title>Headline</title><link>https://example.com/headline</link><pubDate>Mon, 01 Jan 2024 10:00:00 GMT</pubDate></item>
</channel></rss>`))
		case r.URL.Path == "/r/test/top.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"children":[
				{"data":{"title":"Reddit headline","url":"https://example.com/story","score":55,"num_comments":3,"created_utc":1704103200,"subreddit":"test","author":"alice","domain":"example.com","permalink":"/r/test/comments/abc123/reddit_headline/","is_self":false,"upvote_ratio":0.9}}
			]}}`))
		case r.URL.Path == "/r/test/comments/abc123/reddit_headline.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"data":{"children":[
					{"data":{"title":"Reddit headline","selftext":"Post body","url":"https://example.com/story","score":55,"num_comments":3,"created_utc":1704103200,"subreddit":"test","author":"alice","domain":"example.com","permalink":"/r/test/comments/abc123/reddit_headline/","is_self":false,"upvote_ratio":0.9}}
				]}},
				{"data":{"children":[
					{"kind":"t1","data":{"author":"bob","body":"Nice post","score":4,"created_utc":1704103300,"depth":0,"replies":""}}
				]}}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		RSS: configpkg.RSSConfig{
			Feeds: map[string][]configpkg.RSSFeedConfig{
				"ai": []configpkg.RSSFeedConfig{
					{Name: "test-feed", URL: server.URL + "/rss"},
				},
			},
		},
		Reddit: configpkg.RedditConfig{
			BaseURL: server.URL,
			Subreddits: map[string][]string{
				"world": []string{"test"},
			},
		},
	})

	rssRun, err := InvokeTool(ctx, nil, "", "news_rss_headlines", map[string]any{
		"feed_names": []string{"test-feed"},
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke rss tool: %v", err)
	}
	if rssRun.IsError {
		t.Fatalf("expected successful rss tool run, got %s", rssRun.Output)
	}
	var rssPayload newsRSSHeadlinesResponse
	if err := json.Unmarshal([]byte(rssRun.Output), &rssPayload); err != nil {
		t.Fatalf("parse rss tool output: %v", err)
	}
	if len(rssPayload.Items) != 1 || rssPayload.Items[0].Title != "Headline" {
		t.Fatalf("unexpected rss tool payload: %#v", rssPayload)
	}

	redditRun, err := InvokeTool(ctx, nil, "", "news_reddit_top_posts", map[string]any{
		"category": "world",
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke reddit top tool: %v", err)
	}
	if redditRun.IsError {
		t.Fatalf("expected successful reddit top tool run, got %s", redditRun.Output)
	}
	var redditPayload newsRedditTopPostsResponse
	if err := json.Unmarshal([]byte(redditRun.Output), &redditPayload); err != nil {
		t.Fatalf("parse reddit top tool output: %v", err)
	}
	if len(redditPayload.Posts) != 1 || redditPayload.Posts[0].Title != "Reddit headline" {
		t.Fatalf("unexpected reddit top payload: %#v", redditPayload)
	}

	detailsRun, err := InvokeTool(ctx, nil, "", "news_reddit_post_details", map[string]any{
		"url_or_permalink": "/r/test/comments/abc123/reddit_headline/",
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke reddit details tool: %v", err)
	}
	if detailsRun.IsError {
		t.Fatalf("expected successful reddit details tool run, got %s", detailsRun.Output)
	}
	var detailsPayload newsRedditPostDetailsResponse
	if err := json.Unmarshal([]byte(detailsRun.Output), &detailsPayload); err != nil {
		t.Fatalf("parse reddit details tool output: %v", err)
	}
	if detailsPayload.Post.Body != "Post body" || len(detailsPayload.Post.Comments) != 1 {
		t.Fatalf("unexpected reddit details payload: %#v", detailsPayload)
	}

	invalidRun, err := InvokeTool(ctx, nil, "", "news_rss_headlines", map[string]any{}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke invalid rss tool: %v", err)
	}
	if !invalidRun.IsError || !strings.Contains(invalidRun.Output, "category or feed_names is required") {
		t.Fatalf("expected validation error, got %#v", invalidRun)
	}
}
