package agent

import (
	"encoding/json"
	"testing"
)

type fakeEncoder struct{}

func (fakeEncoder) Encode(text string, _ []string, _ []string) []int {
	return make([]int, len(text))
}

type fakeCounter struct{}

func (fakeCounter) Count(text string) int64 {
	return int64(len(text))
}

func TestFallbackEncodingForModel(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"gpt-5.4-mini":           "o200k_base",
		"gpt-4o-mini":            "o200k_base",
		"gpt-4-turbo":            "cl100k_base",
		"text-embedding-3-small": "cl100k_base",
		"curie":                  "r50k_base",
		"":                       "o200k_base",
	}

	for model, want := range cases {
		if got := fallbackEncodingForModel(model); got != want {
			t.Fatalf("fallbackEncodingForModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestEstimateTraceTokensPopulatesBreakdown(t *testing.T) {
	t.Parallel()

	trace := estimateTraceTokens(StoredTrace{
		Model:        "unit-test-model",
		SystemPrompt: "rules",
		Messages: []StoredMessage{
			{Role: "user", Content: "hello"},
			{
				Role:      "assistant",
				Content:   "answer",
				Reasoning: "why",
				ToolCalls: []StoredToolCall{{
					ID: "call_001",
					Function: &StoredToolFunction{
						Name:      "web_fetch_get",
						Arguments: `{"url":"https://example.com"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "call_001", ContentText: `{"content":"ok"}`},
		},
	}, fakeCounter{})

	if trace.EstimatedUsage.SystemPrompt != int64(len("rules")) {
		t.Fatalf("unexpected system prompt estimate: %d", trace.EstimatedUsage.SystemPrompt)
	}
	if trace.Messages[0].EstimatedTokens != int64(len("hello")) {
		t.Fatalf("unexpected user estimate: %d", trace.Messages[0].EstimatedTokens)
	}
	if trace.Messages[1].EstimatedTokens != int64(len("answer")+len("why")) {
		t.Fatalf("unexpected assistant estimate: %d", trace.Messages[1].EstimatedTokens)
	}
	wantCallTokens := int64(len("web_fetch_get") + len(`{"url":"https://example.com"}`))
	if trace.Messages[1].ToolCalls[0].EstimatedTokens != wantCallTokens {
		t.Fatalf("unexpected tool call estimate: %d", trace.Messages[1].ToolCalls[0].EstimatedTokens)
	}
	if trace.Messages[2].EstimatedTokens != int64(len(`{"content":"ok"}`)) {
		t.Fatalf("unexpected tool result estimate: %d", trace.Messages[2].EstimatedTokens)
	}
	if trace.EstimatedUsage.Total != trace.EstimatedUsage.SystemPrompt+trace.EstimatedUsage.User+trace.EstimatedUsage.Assistant+trace.EstimatedUsage.Reasoning+trace.EstimatedUsage.ToolCalls+trace.EstimatedUsage.ToolResults {
		t.Fatalf("estimated total did not match bucket sum: %+v", trace.EstimatedUsage)
	}
}

func TestParseStoredTraceBackfillsEstimatedUsage(t *testing.T) {
	const model = "unit-test-model-backfill"

	sharedEncoderCache.mu.Lock()
	sharedEncoderCache.encoders[model] = fakeEncoder{}
	sharedEncoderCache.mu.Unlock()
	defer func() {
		sharedEncoderCache.mu.Lock()
		delete(sharedEncoderCache.encoders, model)
		sharedEncoderCache.mu.Unlock()
	}()

	raw := json.RawMessage(`{
		"schema_version": 2,
		"run_id": "run_001",
		"title": "Test run",
		"model": "` + model + `",
		"messages": [
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "world", "tool_calls": [{"id":"call_001","function":{"name":"search_tools","arguments":"{\"query\":\"hello\"}"}}]},
			{"role": "tool", "tool_call_id": "call_001", "content_text": "{\"ok\":true}"}
		],
		"created_at": "2026-03-29T00:00:00Z",
		"updated_at": "2026-03-29T00:00:00Z"
	}`)

	trace, err := parseStoredTrace(raw)
	if err != nil {
		t.Fatalf("parseStoredTrace: %v", err)
	}

	if trace.EstimatedUsage.Total == 0 {
		t.Fatalf("expected estimated usage to be backfilled")
	}
	if trace.Messages[1].ToolCalls[0].EstimatedTokens == 0 {
		t.Fatalf("expected tool call estimate to be backfilled")
	}
	if trace.Messages[2].EstimatedTokens == 0 {
		t.Fatalf("expected tool result estimate to be backfilled")
	}
}
