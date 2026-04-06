package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/fantasy"
	redditnews "github.com/versionlens/OpenNalvin/internal/news/reddit"
	rssnews "github.com/versionlens/OpenNalvin/internal/news/rss"
)

func (rt *agentRuntime) newsRSSHeadlinesDescription() string {
	categories := rssnews.Categories(rt.cfg.RSS)
	return fmt.Sprintf(
		"Fetch headlines from configured RSS feeds by category or explicit feed names. Available categories: %s. Returns titles, URLs, publish times, and per-feed warnings.",
		strings.Join(categories, ", "),
	)
}

func (rt *agentRuntime) newsRedditTopPostsDescription() string {
	categories := redditnews.Categories(rt.cfg.Reddit)
	return fmt.Sprintf(
		"Fetch top posts from configured subreddit groups or explicit subreddits. Available categories: %s. Returns post titles, scores, URLs, publish times, and per-subreddit warnings.",
		strings.Join(categories, ", "),
	)
}

type newsRSSHeadlinesInput struct {
	Category  string   `json:"category,omitempty" jsonschema_description:"Configured RSS category to fetch, for example ai, tech, politics, analysis, or commentary."`
	FeedNames []string `json:"feed_names,omitempty" jsonschema_description:"Optional explicit configured RSS feed names to fetch instead of a category."`
	Limit     int      `json:"limit,omitempty" jsonschema_description:"Optional max number of headlines to return. Defaults to 15."`
}

type newsRedditTopPostsInput struct {
	Category   string   `json:"category,omitempty" jsonschema_description:"Configured subreddit category to fetch, for example ai, tech, politics, world, cyber, or finance."`
	Subreddits []string `json:"subreddits,omitempty" jsonschema_description:"Optional explicit subreddit names to fetch instead of a configured category."`
	TimeFilter string   `json:"time_filter,omitempty" jsonschema_description:"Top-post time filter. Defaults to day. Supported Reddit values include hour, day, week, month, year, and all."`
	Limit      int      `json:"limit,omitempty" jsonschema_description:"Optional max number of posts to return. Defaults to 15."`
}

type newsRedditPostDetailsInput struct {
	URLOrPermalink string `json:"url_or_permalink" jsonschema_description:"A Reddit post permalink path like /r/test/comments/abc123/title/ or a full Reddit post URL."`
	CommentLimit   int    `json:"comment_limit,omitempty" jsonschema_description:"Optional max number of comments Reddit should return. Defaults to 25."`
	CommentDepth   int    `json:"comment_depth,omitempty" jsonschema_description:"Optional maximum reply depth to traverse. Defaults to 2."`
}

func (rt *agentRuntime) newsRSSHeadlines(ctx context.Context, input newsRSSHeadlinesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 15
	}

	availableCategories := rssnews.Categories(rt.cfg.RSS)
	feedNames := trimStringList(input.FeedNames)
	if strings.TrimSpace(input.Category) == "" && len(feedNames) == 0 {
		return fantasy.NewTextErrorResponse("category or feed_names is required"), nil
	}

	var (
		items    []rssnews.Item
		warnings []rssnews.Warning
		err      error
	)
	if len(feedNames) > 0 {
		feeds, missing := rssnews.LookupFeedsByName(rt.cfg.RSS, feedNames)
		if len(missing) > 0 {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("unknown rss feed names: %v", missing)), nil
		}
		items, warnings, err = rssnews.NewClient(nil).FetchFeeds(ctx, feeds, limit)
	} else {
		items, warnings, err = rssnews.FetchCategory(ctx, rt.cfg.RSS, strings.TrimSpace(input.Category), limit)
	}
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"items":                items,
		"warnings":             warnings,
		"available_categories": availableCategories,
	})
}

func (rt *agentRuntime) newsRedditTopPosts(ctx context.Context, input newsRedditTopPostsInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 15
	}
	timeFilter := strings.TrimSpace(input.TimeFilter)
	if timeFilter == "" {
		timeFilter = "day"
	}

	availableCategories := redditnews.Categories(rt.cfg.Reddit)
	subreddits := trimStringList(input.Subreddits)
	if strings.TrimSpace(input.Category) == "" && len(subreddits) == 0 {
		return fantasy.NewTextErrorResponse("category or subreddits is required"), nil
	}

	var (
		posts    []redditnews.Post
		warnings []redditnews.Warning
		err      error
	)
	if len(subreddits) > 0 {
		posts, warnings, err = redditnews.NewClient(rt.cfg.Reddit.BaseURL, nil).FetchTopPostsForSubreddits(ctx, subreddits, timeFilter, limit)
	} else {
		posts, warnings, err = redditnews.FetchCategoryTop(ctx, rt.cfg.Reddit, strings.TrimSpace(input.Category), timeFilter, limit)
	}
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"posts":                posts,
		"warnings":             warnings,
		"available_categories": availableCategories,
	})
}

func (rt *agentRuntime) newsRedditPostDetails(ctx context.Context, input newsRedditPostDetailsInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	urlOrPermalink := strings.TrimSpace(input.URLOrPermalink)
	if urlOrPermalink == "" {
		return fantasy.NewTextErrorResponse("url_or_permalink is required"), nil
	}

	commentLimit := input.CommentLimit
	if commentLimit <= 0 {
		commentLimit = 25
	}
	commentDepth := input.CommentDepth
	if commentDepth < 0 {
		commentDepth = 0
	}
	if commentDepth == 0 && input.CommentDepth == 0 {
		commentDepth = 2
	}

	post, err := redditnews.NewClient(rt.cfg.Reddit.BaseURL, nil).FetchPostDetails(ctx, urlOrPermalink, commentLimit, commentDepth)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(map[string]any{"post": post})
}

func trimStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
