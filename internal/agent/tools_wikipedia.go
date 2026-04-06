package agent

import (
	"context"

	"charm.land/fantasy"
	wikipediapkg "github.com/versionlens/OpenNalvin/internal/wikipedia"
)

type wikipediaGetArticleInput struct {
	Title        string `json:"title" jsonschema_description:"Wikipedia page title to fetch, for example Ada Lovelace or Earth."`
	Language     string `json:"language,omitempty" jsonschema_description:"Optional Wikipedia language code. Defaults to en."`
	SectionIndex string `json:"section_index,omitempty" jsonschema_description:"Optional section index to fetch in addition to the page summary and section index list."`
}

var wikipediaFetchTool = func(ctx context.Context, req wikipediapkg.FetchRequest) (wikipediapkg.FetchResult, error) {
	return wikipediapkg.NewClient(wikipediapkg.Config{}).Fetch(ctx, req)
}

func (rt *agentRuntime) wikipediaGetArticle(ctx context.Context, input wikipediaGetArticleInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	result, err := wikipediaFetchTool(ctx, wikipediapkg.FetchRequest{
		Title:        input.Title,
		Language:     input.Language,
		SectionIndex: input.SectionIndex,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(result)
}
