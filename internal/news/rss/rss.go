package rss

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/araddon/dateparse"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/mmcdole/gofeed"
)

const defaultUserAgent = "nalvin/1.0"

type Warning struct {
	FeedName string `json:"feed_name,omitempty"`
	Message  string `json:"message"`
}

type Item struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Published   string    `json:"published"`
	PublishedAt time.Time `json:"-"`
	Source      string    `json:"source"`
}

type Client struct {
	parser     *gofeed.Parser
	httpClient *http.Client
	userAgent  string
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		parser:     gofeed.NewParser(),
		httpClient: httpClient,
		userAgent:  defaultUserAgent,
	}
}

func Categories(cfg configpkg.RSSConfig) []string {
	keys := make([]string, 0, len(cfg.Feeds))
	for category := range cfg.Feeds {
		keys = append(keys, category)
	}
	sort.Strings(keys)
	return keys
}

func CategoryFeeds(cfg configpkg.RSSConfig, category string) []configpkg.RSSFeedConfig {
	feeds := cfg.Feeds[strings.TrimSpace(category)]
	return append([]configpkg.RSSFeedConfig(nil), feeds...)
}

func LookupFeedsByName(cfg configpkg.RSSConfig, names []string) ([]configpkg.RSSFeedConfig, []string) {
	index := make(map[string]configpkg.RSSFeedConfig)
	for _, categoryFeeds := range cfg.Feeds {
		for _, feed := range categoryFeeds {
			index[feed.Name] = feed
		}
	}

	feeds := make([]configpkg.RSSFeedConfig, 0, len(names))
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		feed, ok := index[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		feeds = append(feeds, feed)
	}
	sort.Strings(missing)
	return feeds, missing
}

func FetchCategory(ctx context.Context, cfg configpkg.RSSConfig, category string, limit int) ([]Item, []Warning, error) {
	feeds := CategoryFeeds(cfg, category)
	if len(feeds) == 0 {
		return nil, nil, fmt.Errorf("unknown rss category: %s (available: %v)", category, Categories(cfg))
	}
	return NewClient(nil).FetchFeeds(ctx, feeds, limit)
}

func (c *Client) FetchFeeds(ctx context.Context, feeds []configpkg.RSSFeedConfig, limit int) ([]Item, []Warning, error) {
	if len(feeds) == 0 {
		return nil, nil, fmt.Errorf("no rss feeds configured")
	}

	type result struct {
		items    []Item
		warnings []Warning
	}

	results := make([]result, len(feeds))
	var wg sync.WaitGroup
	for i, feed := range feeds {
		wg.Add(1)
		go func(idx int, feed configpkg.RSSFeedConfig) {
			defer wg.Done()
			items, err := c.fetchFeed(ctx, feed)
			if err != nil {
				results[idx] = result{
					warnings: []Warning{{
						FeedName: feed.Name,
						Message:  err.Error(),
					}},
				}
				return
			}
			results[idx] = result{items: items}
		}(i, feed)
	}
	wg.Wait()

	items := make([]Item, 0)
	warnings := make([]Warning, 0)
	for _, result := range results {
		items = append(items, result.items...)
		warnings = append(warnings, result.warnings...)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	return items, warnings, nil
}

func (c *Client) fetchFeed(ctx context.Context, feed configpkg.RSSFeedConfig) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", feed.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", feed.Name, resp.StatusCode)
	}

	parsed, err := c.parser.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", feed.Name, err)
	}

	items := make([]Item, 0, len(parsed.Items))
	for _, rawItem := range parsed.Items {
		link := strings.TrimSpace(rawItem.Link)
		if link == "" && len(rawItem.Links) > 0 {
			link = strings.TrimSpace(rawItem.Links[0])
		}

		published, publishedAt := normalizePublished(rawItem)
		items = append(items, Item{
			Title:       strings.TrimSpace(rawItem.Title),
			URL:         link,
			Published:   published,
			PublishedAt: publishedAt,
			Source:      feed.Name,
		})
	}
	return items, nil
}

func normalizePublished(item *gofeed.Item) (string, time.Time) {
	if item == nil {
		return "", time.Time{}
	}
	if item.PublishedParsed != nil {
		publishedAt := item.PublishedParsed.UTC()
		return publishedAt.Format("2006-01-02 15:04"), publishedAt
	}

	published := strings.TrimSpace(item.Published)
	if published == "" {
		return "", time.Time{}
	}
	parsed, err := dateparse.ParseAny(published)
	if err != nil {
		return published, time.Time{}
	}
	return published, parsed.UTC()
}
