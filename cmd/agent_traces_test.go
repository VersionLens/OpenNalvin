package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestAgentTracesCLIReviewCurationAndExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	db, err := database.Open(context.Background(), filepath.Join(home, ".nalvin", "workspace-db", "alpha.sqlite"))
	if err != nil {
		t.Fatalf("open workspace db: %v", err)
	}
	store := knowledge.New(db)
	seedAgentTraceRun(t, store)
	if err := db.Close(); err != nil {
		t.Fatalf("close workspace db: %v", err)
	}

	out, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "validate", "run_cli_001")
	if err != nil {
		t.Fatalf("validate trace: %v", err)
	}
	if !strings.Contains(out, "Timeline:") || !strings.Contains(out, "spilled:out_1") {
		t.Fatalf("expected human review timeline with spilled output, got:\n%s", out)
	}

	stats, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "stats", "run_cli_001")
	if err != nil {
		t.Fatalf("trace stats: %v", err)
	}
	if !strings.Contains(stats, "Messages: 4") || !strings.Contains(stats, "Tool calls: 1") {
		t.Fatalf("unexpected stats output:\n%s", stats)
	}

	page, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "output", "run_cli_001", "out_1", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatalf("tool output page: %v", err)
	}
	if !strings.Contains(page, "full line two") {
		t.Fatalf("expected paged tool output, got:\n%s", page)
	}

	candidatesJSON, err := executeRoot(t, "--json", "--workspace", "alpha", "agent", "traces", "candidates", "--min-tool-calls", "1", "--max-tool-errors", "0")
	if err != nil {
		t.Fatalf("trace candidates: %v", err)
	}
	var candidates struct {
		Candidates []map[string]any `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(candidatesJSON), &candidates); err != nil {
		t.Fatalf("decode candidates: %v\n%s", err, candidatesJSON)
	}
	if len(candidates.Candidates) != 1 || candidates.Candidates[0]["id"] != "run_cli_001" {
		t.Fatalf("expected run_cli_001 candidate, got %#v", candidates.Candidates)
	}

	batchJSON, err := executeRoot(t, "--json", "--workspace", "alpha", "agent", "traces", "curate", "run_cli_001", "--quality", "high", "--split", "train", "--tag", "batch", "--approve")
	if err != nil {
		t.Fatalf("batch curate: %v", err)
	}
	var batch struct {
		Curations []map[string]any `json:"curations"`
	}
	if err := json.Unmarshal([]byte(batchJSON), &batch); err != nil {
		t.Fatalf("decode batch curate: %v\n%s", err, batchJSON)
	}
	if len(batch.Curations) != 1 || batch.Curations[0]["status"] != "approved" || batch.Curations[0]["quality"] != "high" {
		t.Fatalf("unexpected batch curation summary: %#v", batch.Curations)
	}
	if _, ok := batch.Curations[0]["trace"]; ok {
		t.Fatalf("batch curation summary should not include full trace JSON")
	}

	deriveJSON, err := executeRoot(t, "--json", "--workspace", "alpha", "agent", "traces", "derive", "run_cli_001", "--tag", "gold", "--note", "reviewed")
	if err != nil {
		t.Fatalf("derive curation: %v", err)
	}
	var curation knowledge.AgentTraceCuration
	if err := json.Unmarshal([]byte(deriveJSON), &curation); err != nil {
		t.Fatalf("decode derived curation: %v\n%s", err, deriveJSON)
	}
	if curation.ID == "" {
		t.Fatalf("expected curation id in derive output")
	}

	if _, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "msg", "replace", curation.ID, "2", "--content", "better answer", "--reasoning", "better reasoning"); err != nil {
		t.Fatalf("replace curated message: %v", err)
	}
	if _, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "label", curation.ID, "--quality", "good", "--reward", "1", "--split", "train", "--tag", "approved"); err != nil {
		t.Fatalf("label curation: %v", err)
	}
	if _, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "approve", curation.ID); err != nil {
		t.Fatalf("approve curation: %v", err)
	}
	evalJSON, err := executeRoot(t, "--json", "--workspace", "alpha", "agent", "traces", "curate", "run_cli_001", "--quality", "high", "--reward", "1", "--split", "eval", "--tag", "harness-intuition-v1", "--approve")
	if err != nil {
		t.Fatalf("create eval curation: %v", err)
	}
	var evalBatch struct {
		Curations []map[string]any `json:"curations"`
	}
	if err := json.Unmarshal([]byte(evalJSON), &evalBatch); err != nil {
		t.Fatalf("decode eval curation: %v\n%s", err, evalJSON)
	}
	if len(evalBatch.Curations) != 1 {
		t.Fatalf("expected one eval curation, got %#v", evalBatch.Curations)
	}

	curatedReview, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "show", curation.ID, "--curated", "--view", "messages")
	if err != nil {
		t.Fatalf("show curated trace: %v", err)
	}
	if !strings.Contains(curatedReview, "better reasoning") || !strings.Contains(curatedReview, "better answer") {
		t.Fatalf("expected curated edits in review output, got:\n%s", curatedReview)
	}

	exportDir := filepath.Join(t.TempDir(), "export")
	exportOut, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "export", curation.ID, "--out", exportDir)
	if err != nil {
		t.Fatalf("export curation: %v", err)
	}
	if !strings.Contains(exportOut, "conversations.jsonl") {
		t.Fatalf("expected export paths, got:\n%s", exportOut)
	}
	splitExportDir := filepath.Join(t.TempDir(), "split-export")
	splitExportJSON, err := executeRoot(t, "--json", "--workspace", "alpha", "agent", "traces", "export", "--out", splitExportDir, "--split", "eval", "--tag", "harness-intuition-v1")
	if err != nil {
		t.Fatalf("export split curations: %v", err)
	}
	var splitExport struct {
		Export struct {
			Records int `json:"records"`
		} `json:"export"`
	}
	if err := json.Unmarshal([]byte(splitExportJSON), &splitExport); err != nil {
		t.Fatalf("decode split export: %v\n%s", err, splitExportJSON)
	}
	if splitExport.Export.Records != 1 {
		t.Fatalf("expected one eval split export record, got %d", splitExport.Export.Records)
	}
	data, err := os.ReadFile(filepath.Join(exportDir, "conversations.jsonl"))
	if err != nil {
		t.Fatalf("read export jsonl: %v", err)
	}
	if !strings.Contains(string(data), `"type":"thinking"`) || !strings.Contains(string(data), "better reasoning") {
		t.Fatalf("expected reasoning to be included by default in export, got:\n%s", string(data))
	}
	verifyOut, err := executeRoot(t, "--workspace", "alpha", "agent", "traces", "verify-export", exportDir)
	if err != nil {
		t.Fatalf("verify export: %v\n%s", err, verifyOut)
	}
	if !strings.Contains(verifyOut, "Shape: ok") || !strings.Contains(verifyOut, "Thinking parts:") {
		t.Fatalf("expected export verification summary, got:\n%s", verifyOut)
	}

	baselineJSON, err := executeRoot(t, "--json", "--workspace", "alpha", "agent", "traces", "baseline-suite", "--category", "tool-discovery", "--limit", "2", "--provider", "macmini", "--workspace", "baseline-qwen35-4b", "--served-model", "Qwen/Qwen3.5-4B")
	if err != nil {
		t.Fatalf("baseline suite: %v", err)
	}
	var baseline struct {
		Prompts     []map[string]any `json:"prompts"`
		ServedModel string           `json:"served_model"`
		Note        string           `json:"note"`
	}
	if err := json.Unmarshal([]byte(baselineJSON), &baseline); err != nil {
		t.Fatalf("decode baseline suite: %v\n%s", err, baselineJSON)
	}
	if len(baseline.Prompts) != 2 || baseline.ServedModel != "Qwen/Qwen3.5-4B" || !strings.Contains(baseline.Note, "llama.cpp") {
		t.Fatalf("unexpected baseline suite output: %#v", baseline)
	}
}

func seedAgentTraceRun(t *testing.T, store *knowledge.Store) {
	t.Helper()
	trace := json.RawMessage(`{
		"schema_version": 4,
		"run_id": "run_cli_001",
		"title": "CLI trace",
		"model": "Qwen/Qwen3-8B",
		"provider": "test-provider",
		"system_prompt": "raw system",
		"effective_system_prompt": "effective system",
		"metadata": {
			"run_kind": "root",
			"requested_skill_names": ["trace-review"],
			"tools": {
				"enabled_ids": ["lookup"],
				"pinned_ids": ["lookup"],
				"revealed_ids": ["lookup"]
			}
		},
		"usage": {"input_tokens": 10, "output_tokens": 20, "total_tokens": 30},
		"estimated_usage": {"reasoning": 4, "total": 34},
		"messages": [
			{"role":"user","message_id":"msg_001","content":"inspect this"},
			{"role":"assistant","message_id":"msg_002","reasoning":"first reasoning","content":"calling tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}}]},
			{"role":"tool","message_id":"msg_003","tool_call_id":"call_1","tool_name":"lookup","content_text":"inline summary","ok":true,"tool_output_ref":{"output_id":"out_1","tool_call_id":"call_1","size_bytes":42,"estimated_tokens":12,"total_lines":3,"inline_truncated":true}},
			{"role":"assistant","message_id":"msg_004","content":"final answer"}
		],
		"created_at": "2026-04-20T00:00:00Z",
		"updated_at": "2026-04-20T00:01:00Z"
	}`)
	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           "run_cli_001",
		Title:        "CLI trace",
		Model:        "Qwen/Qwen3-8B",
		Provider:     "test-provider",
		Prompt:       "inspect this",
		Status:       "completed",
		MessageCount: 4,
		InputTokens:  10,
		OutputTokens: 20,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if _, err := store.CreateAgentRunToolOutput(context.Background(), knowledge.CreateAgentRunToolOutputInput{
		OutputID:        "out_1",
		RunID:           "run_cli_001",
		ToolCallID:      "call_1",
		ToolName:        "lookup",
		Content:         "full line one\nfull line two\nfull line three\n",
		SizeBytes:       42,
		EstimatedTokens: 12,
		TotalLines:      3,
		InlineTruncated: true,
	}); err != nil {
		t.Fatalf("create tool output: %v", err)
	}
}
