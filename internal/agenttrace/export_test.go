package agenttrace_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agenttrace"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestExportTinkerSFTIncludesReasoningByDefault(t *testing.T) {
	trace := sampleTraceForExport()
	loaded := &agenttrace.LoadedTrace{
		Kind:  agenttrace.SourceKindRun,
		ID:    "run_export_001",
		Trace: trace,
		Run: &knowledge.AgentRun{
			ID:     "run_export_001",
			Title:  "Export trace",
			Prompt: "solve it",
			Status: "completed",
		},
	}
	outDir := t.TempDir()
	result, err := agenttrace.Export(context.Background(), nil, []*agenttrace.LoadedTrace{loaded}, agenttrace.ExportOptions{
		OutDir: outDir,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.Records != 1 {
		t.Fatalf("expected one record, got %d", result.Records)
	}

	record := readFirstJSONLRecord(t, filepath.Join(outDir, "conversations.jsonl"))
	messages := record["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" {
		t.Fatalf("expected effective system prompt first, got %#v", messages[0])
	}
	assistant := messages[2].(map[string]any)
	content := assistant["content"].([]any)
	if got := content[0].(map[string]any)["type"]; got != "thinking" {
		t.Fatalf("expected reasoning thinking part first, got %#v", got)
	}
	if got := content[1].(map[string]any)["type"]; got != "text" {
		t.Fatalf("expected visible text part after reasoning, got %#v", got)
	}
	if _, ok := assistant["tool_calls"].([]any); !ok {
		t.Fatalf("expected OpenAI-style tool_calls on assistant message: %#v", assistant)
	}
	tool := messages[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["name"] != "lookup" {
		t.Fatalf("unexpected tool message shape: %#v", tool)
	}
}

func TestExportTinkerSFTCanMoveReasoningToMetadata(t *testing.T) {
	trace := sampleTraceForExport()
	loaded := &agenttrace.LoadedTrace{
		Kind:  agenttrace.SourceKindCuration,
		ID:    "curation_export_001",
		Trace: trace,
		Curation: &knowledge.AgentTraceCuration{
			ID:          "curation_export_001",
			SourceRunID: "run_export_001",
			Status:      "approved",
			Quality:     "good",
			Split:       "train",
		},
	}
	outDir := t.TempDir()
	if _, err := agenttrace.Export(context.Background(), nil, []*agenttrace.LoadedTrace{loaded}, agenttrace.ExportOptions{
		OutDir:        outDir,
		ReasoningMode: agenttrace.ReasoningMetadataOnly,
		SystemMode:    "none",
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	record := readFirstJSONLRecord(t, filepath.Join(outDir, "conversations.jsonl"))
	messages := record["messages"].([]any)
	if messages[0].(map[string]any)["role"] == "system" {
		t.Fatalf("did not expect a system message with --system none")
	}
	assistant := messages[1].(map[string]any)
	if _, ok := assistant["content"].(string); !ok {
		t.Fatalf("expected assistant content to be plain text when reasoning is metadata-only: %#v", assistant)
	}
	metadata := record["agent_metadata"].(map[string]any)
	reasoning := metadata["assistant_reasoning"].(map[string]any)
	if reasoning["msg_002"] != "private reasoning" {
		t.Fatalf("expected reasoning in metadata, got %#v", reasoning)
	}
	if metadata["quality"] != "good" || metadata["split"] != "train" {
		t.Fatalf("expected curation labels in metadata, got %#v", metadata)
	}
}

func sampleTraceForExport() agentpkg.StoredTrace {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	return agentpkg.StoredTrace{
		SchemaVersion:         4,
		RunID:                 "run_export_001",
		Title:                 "Export trace",
		Model:                 "Qwen/Qwen3-8B",
		Provider:              "tinker-target",
		SystemPrompt:          "raw system",
		EffectiveSystemPrompt: "effective system",
		Messages: []agentpkg.StoredMessage{
			{Role: "user", MessageID: "msg_001", Content: "solve it"},
			{
				Role:      "assistant",
				MessageID: "msg_002",
				Reasoning: "private reasoning",
				Content:   "visible answer",
				ToolCalls: []agentpkg.StoredToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: &agentpkg.StoredToolFunction{
						Name:      "lookup",
						Arguments: `{"query":"x"}`,
					},
				}},
			},
			{
				Role:        "tool",
				MessageID:   "msg_003",
				ToolCallID:  "call_1",
				ToolName:    "lookup",
				ContentText: "tool result",
				Ok:          true,
			},
			{Role: "assistant", MessageID: "msg_004", Content: "final answer"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func readFirstJSONLRecord(t *testing.T, path string) map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open jsonl: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("expected at least one jsonl record")
	}
	var record map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal jsonl record: %v", err)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan jsonl: %v", err)
	}
	return record
}
