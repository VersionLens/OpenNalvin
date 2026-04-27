package knowledge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestAgentRunCreateUpdateListAndGet(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-runs.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	initialTrace := json.RawMessage(`{
		"schema_version": 1,
		"run_id": "run_001",
		"title": "First run",
		"model": "gpt-test",
		"usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
		"messages": [{"role":"user","content":"hello"}],
		"created_at": "2026-03-28T00:00:00Z",
		"updated_at": "2026-03-28T00:00:00Z"
	}`)

	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           "run_001",
		Title:        "First run",
		Model:        "gpt-test",
		Provider:     "openai",
		Prompt:       "hello",
		Status:       "running",
		MessageCount: 1,
		InputTokens:  1,
		OutputTokens: 2,
		Trace:        initialTrace,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	updatedTrace := json.RawMessage(`{
		"schema_version": 1,
		"run_id": "run_001",
		"title": "First run",
		"model": "gpt-test",
		"usage": {"input_tokens": 10, "output_tokens": 20, "total_tokens": 30},
		"messages": [
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"world","reasoning":"thinking"}
		],
		"created_at": "2026-03-28T00:00:00Z",
		"updated_at": "2026-03-28T00:01:00Z"
	}`)

	if err := store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:           "run_001",
		Title:        "First run",
		Model:        "gpt-test",
		Provider:     "openai",
		Status:       "completed",
		DurationMs:   250,
		MessageCount: 2,
		InputTokens:  10,
		OutputTokens: 20,
		Trace:        updatedTrace,
	}); err != nil {
		t.Fatalf("update agent run: %v", err)
	}

	run, err := store.GetAgentRun(ctx, "run_001")
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}

	if run.Status != "completed" {
		t.Fatalf("expected completed status, got %q", run.Status)
	}
	if run.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", run.Provider)
	}
	if run.MessageCount != 2 {
		t.Fatalf("expected 2 messages, got %d", run.MessageCount)
	}
	if run.InputTokens != 10 || run.OutputTokens != 20 {
		t.Fatalf("unexpected token counts: input=%d output=%d", run.InputTokens, run.OutputTokens)
	}
	if run.TotalTokens != 30 {
		t.Fatalf("unexpected total token count: %d", run.TotalTokens)
	}

	var decoded map[string]any
	if err := json.Unmarshal(run.Trace, &decoded); err != nil {
		t.Fatalf("unmarshal stored trace: %v", err)
	}
	if decoded["run_id"] != "run_001" {
		t.Fatalf("expected run_id in trace, got %#v", decoded["run_id"])
	}

	runs, err := store.ListAgentRuns(ctx, "completed", 10, 0)
	if err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 completed run, got %d", len(runs))
	}
	if runs[0].ID != "run_001" {
		t.Fatalf("expected run_001 summary, got %q", runs[0].ID)
	}
	if runs[0].TotalTokens != 30 {
		t.Fatalf("expected run summary total tokens 30, got %d", runs[0].TotalTokens)
	}
}

func TestAgentRunToolOutputCreatePageAndSearch(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-run-tool-output.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	trace := json.RawMessage(`{
		"schema_version": 2,
		"run_id": "run_002",
		"title": "Spill run",
		"model": "gpt-test",
		"usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
		"messages": [{"role":"user","content":"hello"}],
		"created_at": "2026-03-29T00:00:00Z",
		"updated_at": "2026-03-29T00:00:00Z"
	}`)

	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           "run_002",
		Title:        "Spill run",
		Model:        "gpt-test",
		Provider:     "openai",
		Prompt:       "hello",
		Status:       "completed",
		MessageCount: 1,
		InputTokens:  1,
		OutputTokens: 2,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	output, err := store.CreateAgentRunToolOutput(ctx, knowledge.CreateAgentRunToolOutputInput{
		RunID:           "run_002",
		ToolCallID:      "call_123",
		ToolName:        "web_fetch_get",
		Content:         "line one\nline two\nline three\nline four\n",
		SizeBytes:       40,
		EstimatedTokens: 25,
		TotalLines:      4,
		InlineTruncated: true,
	})
	if err != nil {
		t.Fatalf("create agent run tool output: %v", err)
	}

	page, err := store.GetAgentRunToolOutputPage(ctx, "run_002", output.OutputID, 1, 2)
	if err != nil {
		t.Fatalf("get agent run tool output page: %v", err)
	}
	if page.StartLine != 2 || page.EndLine != 3 {
		t.Fatalf("unexpected page bounds: %+v", page)
	}
	if page.Content != "line two\nline three" {
		t.Fatalf("unexpected page content: %q", page.Content)
	}
	if !page.Truncated {
		t.Fatalf("expected page to be truncated")
	}

	search, err := store.SearchAgentRunToolOutput(ctx, "run_002", output.OutputID, "line", true, 2)
	if err != nil {
		t.Fatalf("search agent run tool output: %v", err)
	}
	if len(search.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(search.Matches))
	}
	if !search.Truncated {
		t.Fatalf("expected search result to be truncated")
	}
}

func TestAgentRunToolOutputSearchClipsPreviewAndBudget(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-run-tool-output-grep.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	trace := json.RawMessage(`{
		"schema_version": 2,
		"run_id": "run_003",
		"title": "Spill run",
		"model": "gpt-test",
		"usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
		"messages": [{"role":"user","content":"hello"}],
		"created_at": "2026-03-29T00:00:00Z",
		"updated_at": "2026-03-29T00:00:00Z"
	}`)

	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           "run_003",
		Title:        "Spill run",
		Model:        "gpt-test",
		Provider:     "openai",
		Prompt:       "hello",
		Status:       "completed",
		MessageCount: 1,
		InputTokens:  1,
		OutputTokens: 2,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	longLine := strings.Repeat("prefix data ", 20) + "MATCH-NEEDLE" + strings.Repeat(" suffix data", 20)
	content := strings.Repeat(longLine+"\n", 100)
	output, err := store.CreateAgentRunToolOutput(ctx, knowledge.CreateAgentRunToolOutputInput{
		RunID:           "run_003",
		ToolCallID:      "call_456",
		ToolName:        "web_fetch_get",
		Content:         content,
		SizeBytes:       len([]byte(content)),
		EstimatedTokens: 500,
		TotalLines:      100,
		InlineTruncated: true,
	})
	if err != nil {
		t.Fatalf("create agent run tool output: %v", err)
	}

	search, err := store.SearchAgentRunToolOutput(ctx, "run_003", output.OutputID, "MATCH-NEEDLE", true, 100)
	if err != nil {
		t.Fatalf("search agent run tool output: %v", err)
	}
	if len(search.Matches) == 0 {
		t.Fatalf("expected clipped matches, got none")
	}
	if !search.Truncated {
		t.Fatalf("expected search result to truncate once preview budget is exhausted")
	}
	if first := search.Matches[0].Preview; len([]rune(first)) > 240 {
		t.Fatalf("expected preview to be clipped to 240 chars, got %d chars", len([]rune(first)))
	}
	if first := search.Matches[0].Preview; !strings.Contains(first, "MATCH-NEEDLE") {
		t.Fatalf("expected preview to keep the matching text, got %q", first)
	}
	if first := search.Matches[0].Preview; first == longLine {
		t.Fatalf("expected preview to be a centered snippet instead of the full line")
	}
}
