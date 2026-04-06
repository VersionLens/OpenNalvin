package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

const defaultUserAgent = "nalvin/1.0 (github.com/versionlens/OpenNalvin)"

var errRateLimited = errors.New("rate limited")

type Warning struct {
	Subreddit string `json:"subreddit,omitempty"`
	Message   string `json:"message"`
}

type Comment struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	Score     int       `json:"score"`
	CreatedAt string    `json:"created_at"`
	Depth     int       `json:"depth"`
	Replies   []Comment `json:"replies,omitempty"`
}

type Post struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Score       int     `json:"score"`
	NumComments int     `json:"num_comments"`
	CreatedUTC  float64 `json:"created_utc"`
	Subreddit   string  `json:"subreddit"`
	Author      string  `json:"author"`
	Domain      string  `json:"domain"`
	Permalink   string  `json:"permalink"`
	IsSelf      bool    `json:"is_self"`
	Selftext    string  `json:"selftext,omitempty"`
	UpvoteRatio float64 `json:"upvote_ratio"`
	FlairText   string  `json:"link_flair_text,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

type PostDetails struct {
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	LinkedURL    string    `json:"linked_url,omitempty"`
	Permalink    string    `json:"permalink"`
	PermalinkURL string    `json:"permalink_url"`
	Subreddit    string    `json:"subreddit"`
	Author       string    `json:"author"`
	Score        int       `json:"score"`
	NumComments  int       `json:"num_comments"`
	CreatedAt    string    `json:"created_at"`
	Comments     []Comment `json:"comments"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
	maxRetries int
}

type listing struct {
	Data struct {
		Children []struct {
			Data Post `json:"data"`
		} `json:"children"`
		After string `json:"after"`
	} `json:"data"`
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://www.reddit.com"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		userAgent:  defaultUserAgent,
		maxRetries: 3,
	}
}

func Categories(cfg configpkg.RedditConfig) []string {
	keys := make([]string, 0, len(cfg.Subreddits))
	for category := range cfg.Subreddits {
		keys = append(keys, category)
	}
	sort.Strings(keys)
	return keys
}

func CategorySubreddits(cfg configpkg.RedditConfig, category string) []string {
	return append([]string(nil), cfg.Subreddits[strings.TrimSpace(category)]...)
}

func NormalizePermalink(baseURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") {
		return strings.TrimRight(raw, "/")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Path == "" {
		return ""
	}
	if strings.Contains(parsed.Path, "/comments/") {
		return strings.TrimRight(parsed.Path, "/")
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	if strings.EqualFold(parsed.Hostname(), base.Hostname()) && strings.Contains(parsed.Path, "/comments/") {
		return parsed.Path
	}
	return ""
}

func PermalinkURL(baseURL, permalink string) string {
	permalink = strings.TrimSpace(permalink)
	if permalink == "" {
		return ""
	}
	if strings.HasPrefix(permalink, "http://") || strings.HasPrefix(permalink, "https://") {
		return permalink
	}
	if !strings.HasPrefix(permalink, "/") {
		permalink = "/" + permalink
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://www.reddit.com"
	}
	return baseURL + permalink
}

func FetchCategoryTop(ctx context.Context, cfg configpkg.RedditConfig, category, timeFilter string, limit int) ([]Post, []Warning, error) {
	subs := CategorySubreddits(cfg, category)
	if len(subs) == 0 {
		return nil, nil, fmt.Errorf("unknown reddit category: %s (available: %v)", category, Categories(cfg))
	}
	return NewClient(cfg.BaseURL, nil).FetchTopPostsForSubreddits(ctx, subs, timeFilter, limit)
}

func (c *Client) FetchTopPostsForSubreddits(ctx context.Context, subreddits []string, timeFilter string, limit int) ([]Post, []Warning, error) {
	if len(subreddits) == 0 {
		return nil, nil, fmt.Errorf("no subreddits provided")
	}
	if timeFilter == "" {
		timeFilter = "day"
	}

	type result struct {
		posts    []Post
		warnings []Warning
	}

	results := make([]result, len(subreddits))
	var wg sync.WaitGroup
	for i, subreddit := range subreddits {
		wg.Add(1)
		go func(idx int, subreddit string) {
			defer wg.Done()
			posts, err := c.FetchTopPosts(ctx, subreddit, timeFilter, limit)
			if err != nil {
				results[idx] = result{
					warnings: []Warning{{
						Subreddit: subreddit,
						Message:   err.Error(),
					}},
				}
				return
			}
			results[idx] = result{posts: posts}
		}(i, subreddit)
	}
	wg.Wait()

	posts := make([]Post, 0)
	warnings := make([]Warning, 0)
	for _, result := range results {
		posts = append(posts, result.posts...)
		warnings = append(warnings, result.warnings...)
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].Score > posts[j].Score
	})
	if limit > 0 && len(posts) > limit {
		posts = posts[:limit]
	}

	return posts, warnings, nil
}

func (c *Client) FetchTopPosts(ctx context.Context, subreddit, timeFilter string, limit int) ([]Post, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("raw_json", "1")
	if strings.TrimSpace(timeFilter) != "" {
		params.Set("t", timeFilter)
	}

	rawURL := fmt.Sprintf("%s/r/%s/top.json?%s", c.baseURL, subreddit, params.Encode())
	var result listing
	if err := c.fetchJSON(ctx, rawURL, &result); err != nil {
		return nil, fmt.Errorf("fetch r/%s: %w", subreddit, err)
	}

	posts := make([]Post, 0, len(result.Data.Children))
	for _, child := range result.Data.Children {
		post := child.Data
		post.CreatedAt = formatTime(post.CreatedUTC)
		posts = append(posts, post)
	}
	return posts, nil
}

func (c *Client) FetchPostDetails(ctx context.Context, urlOrPermalink string, commentLimit, commentDepth int) (PostDetails, error) {
	permalink := NormalizePermalink(c.baseURL, urlOrPermalink)
	if permalink == "" {
		return PostDetails{}, fmt.Errorf("invalid reddit permalink or URL: %q", urlOrPermalink)
	}

	post, comments, err := c.FetchPostAndComments(ctx, permalink, commentLimit, commentDepth)
	if err != nil {
		return PostDetails{}, err
	}

	return PostDetails{
		Title:        post.Title,
		Body:         strings.TrimSpace(post.Selftext),
		LinkedURL:    linkedURL(post, c.baseURL),
		Permalink:    post.Permalink,
		PermalinkURL: PermalinkURL(c.baseURL, post.Permalink),
		Subreddit:    post.Subreddit,
		Author:       post.Author,
		Score:        post.Score,
		NumComments:  post.NumComments,
		CreatedAt:    post.CreatedAt,
		Comments:     comments,
	}, nil
}

func (c *Client) FetchPostAndComments(ctx context.Context, permalink string, limit, maxDepth int) (Post, []Comment, error) {
	permalink = strings.TrimRight(strings.TrimSpace(permalink), "/")

	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("raw_json", "1")
	params.Set("depth", strconv.Itoa(maxDepth+1))

	rawURL := fmt.Sprintf("%s%s.json?%s", c.baseURL, permalink, params.Encode())
	var raw []json.RawMessage
	if err := c.fetchJSON(ctx, rawURL, &raw); err != nil {
		return Post{}, nil, fmt.Errorf("fetch %s: %w", permalink, err)
	}
	if len(raw) < 2 {
		return Post{}, nil, fmt.Errorf("unexpected response for %s", permalink)
	}

	var postListing listing
	if err := json.Unmarshal(raw[0], &postListing); err != nil {
		return Post{}, nil, fmt.Errorf("parse post: %w", err)
	}

	var post Post
	if len(postListing.Data.Children) > 0 {
		post = postListing.Data.Children[0].Data
		post.CreatedAt = formatTime(post.CreatedUTC)
	}

	return post, parseCommentListing(raw[1], maxDepth), nil
}

func (c *Client) fetchJSON(ctx context.Context, rawURL string, dst any) error {
	backoff := 2 * time.Second
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		body, err := c.doHTTP(ctx, rawURL)
		if err == nil {
			if err := json.Unmarshal(body, dst); err != nil {
				return fmt.Errorf("decode JSON: %w", err)
			}
			return nil
		}
		if !errors.Is(err, errRateLimited) || attempt == c.maxRetries-1 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return fmt.Errorf("unreachable")
}

func (c *Client) doHTTP(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, errRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return body, nil
}

func parseCommentListing(raw json.RawMessage, maxDepth int) []Comment {
	var commentListing struct {
		Data struct {
			Children []struct {
				Kind string          `json:"kind"`
				Data json.RawMessage `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &commentListing) != nil {
		return nil
	}

	comments := make([]Comment, 0, len(commentListing.Data.Children))
	for _, child := range commentListing.Data.Children {
		if child.Kind != "t1" {
			continue
		}
		comment, ok := parseComment(child.Data, maxDepth, 0)
		if ok {
			comments = append(comments, comment)
		}
	}
	return comments
}

func parseComment(raw json.RawMessage, maxDepth, currentDepth int) (Comment, bool) {
	var data struct {
		Author     string          `json:"author"`
		Body       string          `json:"body"`
		Score      int             `json:"score"`
		CreatedUTC float64         `json:"created_utc"`
		Depth      int             `json:"depth"`
		Replies    json.RawMessage `json:"replies"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return Comment{}, false
	}

	if data.Author == "[deleted]" || data.Author == "" || data.Body == "[removed]" {
		return Comment{}, false
	}

	comment := Comment{
		Author:    data.Author,
		Body:      data.Body,
		Score:     data.Score,
		CreatedAt: formatTime(data.CreatedUTC),
		Depth:     data.Depth,
	}

	if currentDepth < maxDepth && len(data.Replies) > 2 {
		var replyListing struct {
			Data struct {
				Children []struct {
					Kind string          `json:"kind"`
					Data json.RawMessage `json:"data"`
				} `json:"children"`
			} `json:"data"`
		}
		if json.Unmarshal(data.Replies, &replyListing) == nil {
			for _, child := range replyListing.Data.Children {
				if child.Kind != "t1" {
					continue
				}
				reply, ok := parseComment(child.Data, maxDepth, currentDepth+1)
				if ok {
					comment.Replies = append(comment.Replies, reply)
				}
			}
		}
	}

	return comment, true
}

func linkedURL(post Post, baseURL string) string {
	if strings.TrimSpace(post.URL) == "" {
		return ""
	}
	permalinkURL := PermalinkURL(baseURL, post.Permalink)
	if post.URL == permalinkURL || post.URL == post.Permalink {
		return ""
	}
	return post.URL
}

func formatTime(createdUTC float64) string {
	return time.Unix(int64(createdUTC), 0).UTC().Format("2006-01-02 15:04")
}
