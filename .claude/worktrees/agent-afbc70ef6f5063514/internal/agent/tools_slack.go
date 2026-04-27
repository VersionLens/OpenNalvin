package agent

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/slack-go/slack"
)

type slackSearchInput struct {
	Query            string   `json:"query" jsonschema_description:"Search query or natural language question. Natural language questions (starting with who/what/when/where/why/how or ending with ?) trigger semantic search; other queries use keyword search."`
	ContentTypes     []string `json:"content_types,omitempty" jsonschema_description:"Content types to search: messages, files, channels, users. Defaults to messages."`
	ChannelTypes     []string `json:"channel_types,omitempty" jsonschema_description:"Channel types to include: public_channel, private_channel, mpim, im. Defaults to public_channel."`
	Limit            int      `json:"limit,omitempty" jsonschema_description:"Max results (1-20). Defaults to 10."`
	ContextChannelID string   `json:"context_channel_id,omitempty" jsonschema_description:"Optional Slack channel ID to scope the search to."`
	IncludeBots      bool     `json:"include_bots,omitempty" jsonschema_description:"Include bot messages in results."`
}

type slackSearchResultItem struct {
	Author    string `json:"author"`
	Channel   string `json:"channel"`
	Content   string `json:"content"`
	Permalink string `json:"permalink"`
	Timestamp string `json:"ts"`
	IsBot     bool   `json:"is_bot,omitempty"`
}

func (rt *agentRuntime) slackSearch(ctx context.Context, input slackSearchInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	token := rt.cfg.Slack.UserToken
	if token == "" {
		return fantasy.NewTextErrorResponse("Slack user token not configured. Set slack.user_token in ~/.nalvin/config.yaml with a xoxp- token that has search:read scopes."), nil
	}

	query := strings.TrimSpace(input.Query)
	if query == "" {
		return fantasy.NewTextErrorResponse("query is required"), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	params := slack.AssistantSearchContextParameters{
		Query:            query,
		Limit:            limit,
		IncludeBots:      input.IncludeBots,
		ContextChannelID: strings.TrimSpace(input.ContextChannelID),
	}
	if len(input.ContentTypes) > 0 {
		params.ContentTypes = input.ContentTypes
	}
	if len(input.ChannelTypes) > 0 {
		params.ChannelTypes = input.ChannelTypes
	}

	client := slack.New(token)
	resp, err := client.SearchAssistantContextContext(ctx, params)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Slack RTS search failed: %v", err)), nil
	}

	items := make([]slackSearchResultItem, 0, len(resp.Results.Messages))
	for _, msg := range resp.Results.Messages {
		items = append(items, slackSearchResultItem{
			Author:    msg.AuthorUserID,
			Channel:   msg.ChannelID,
			Content:   msg.Content,
			Permalink: msg.Permalink,
			Timestamp: msg.MessageTS,
			IsBot:     msg.IsAuthorBot,
		})
	}

	result := map[string]any{
		"query":       query,
		"count":       len(items),
		"messages":    items,
		"has_more":    resp.ResponseMetadata.NextCursor != "",
		"next_cursor": resp.ResponseMetadata.NextCursor,
	}

	return jsonToolResponse(result)
}
