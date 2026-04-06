package reddit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

func TestFetchTopPostsForSubredditsCollectsWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/r/good/top.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"children":[
				{"data":{"title":"Top post","url":"https://example.com/story","score":42,"num_comments":7,"created_utc":1704103200,"subreddit":"good","author":"alice","domain":"example.com","permalink":"/r/good/comments/abc123/top_post/","is_self":false,"upvote_ratio":0.91}}
			]}}`))
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	posts, warnings, err := client.FetchTopPostsForSubreddits(context.Background(), []string{"good", "broken"}, "day", 10)
	if err != nil {
		t.Fatalf("fetch top posts: %v", err)
	}

	if len(posts) != 1 || posts[0].Title != "Top post" {
		t.Fatalf("unexpected posts: %#v", posts)
	}
	if len(warnings) != 1 || warnings[0].Subreddit != "broken" {
		t.Fatalf("expected one warning for broken subreddit, got %#v", warnings)
	}
}

func TestFetchPostDetailsParsesCommentsAndFiltersRemoved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/r/test/comments/abc123/example.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"data":{"children":[
				{"data":{"title":"Example","selftext":"Post body","url":"https://example.com/outbound","score":99,"num_comments":3,"created_utc":1704103200,"subreddit":"test","author":"author1","domain":"example.com","permalink":"/r/test/comments/abc123/example/","is_self":false,"upvote_ratio":0.95}}
			]}},
			{"data":{"children":[
				{"kind":"t1","data":{"author":"commenter","body":"First comment","score":5,"created_utc":1704103300,"depth":0,"replies":{"data":{"children":[
					{"kind":"t1","data":{"author":"replier","body":"Nested reply","score":2,"created_utc":1704103400,"depth":1,"replies":""}},
					{"kind":"t1","data":{"author":"[deleted]","body":"ignored","score":0,"created_utc":1704103450,"depth":1,"replies":""}}
				]}}}},
				{"kind":"t1","data":{"author":"mod","body":"[removed]","score":0,"created_utc":1704103500,"depth":0,"replies":""}}
			]}}
		]`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	post, err := client.FetchPostDetails(context.Background(), server.URL+"/r/test/comments/abc123/example", 25, 2)
	if err != nil {
		t.Fatalf("fetch post details: %v", err)
	}

	if post.Title != "Example" || post.Body != "Post body" {
		t.Fatalf("unexpected post details: %#v", post)
	}
	if post.LinkedURL != "https://example.com/outbound" {
		t.Fatalf("unexpected linked url: %#v", post)
	}
	if len(post.Comments) != 1 || post.Comments[0].Author != "commenter" {
		t.Fatalf("unexpected comments: %#v", post.Comments)
	}
	if len(post.Comments[0].Replies) != 1 || post.Comments[0].Replies[0].Author != "replier" {
		t.Fatalf("unexpected nested replies: %#v", post.Comments[0].Replies)
	}
}

func TestFetchJSONRetriesOnceAfterRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client())
	client.maxRetries = 2

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	posts, err := client.FetchTopPosts(ctx, "retry", "day", 5)
	if err != nil {
		t.Fatalf("fetch top posts after retry: %v", err)
	}
	if len(posts) != 0 {
		t.Fatalf("expected no posts, got %#v", posts)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls after one retry, got %d", got)
	}
}

func TestFetchCategoryTopReturnsUnknownCategoryError(t *testing.T) {
	_, _, err := FetchCategoryTop(context.Background(), configpkg.RedditConfig{}, "missing", "day", 5)
	if err == nil {
		t.Fatalf("expected unknown category error")
	}
}
