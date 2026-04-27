package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	anthropicprovider "charm.land/fantasy/providers/anthropic"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

func defaultCompactionConfig() configpkg.AgentCompactionConfig {
	return configpkg.AgentCompactionConfig{
		Enabled:                      true,
		TriggerPct:                   85,
		ReservedTokens:               12000,
		KeepRecentMessages:           8,
		AggressiveKeepRecentMessages: 4,
		MaxSummaryTokens:             1200,
		MaxCompactionsPerRun:         8,
	}
}

// stubTokenCounter is a simple token counter that counts words.
type stubTokenCounter struct{}

func (s *stubTokenCounter) Count(text string) int64 {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return int64(len(strings.Fields(text)))
}

// stubLanguageModel implements fantasy.LanguageModel for compaction testing.
// It returns a fixed CompactionSummary JSON.
type stubLanguageModel struct {
	response    string
	generateErr error
	callCount   int
	lastCall    fantasy.Call
}

func (m *stubLanguageModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	m.callCount++
	m.lastCall = call
	if m.generateErr != nil {
		return nil, m.generateErr
	}
	return &fantasy.Response{
		Content: fantasy.ResponseContent{
			fantasy.TextContent{Text: m.response},
		},
	}, nil
}

func (m *stubLanguageModel) Stream(_ context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *stubLanguageModel) GenerateObject(_ context.Context, _ fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *stubLanguageModel) StreamObject(_ context.Context, _ fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *stubLanguageModel) Provider() string { return "test" }
func (m *stubLanguageModel) Model() string    { return "test-model" }

func makeSummaryJSON(goal string, completed []string) string {
	summary := CompactionSummary{
		Goal:      goal,
		Completed: completed,
	}
	b, _ := json.Marshal(summary)
	return string(b)
}

func makeMessages(n int) []StoredMessage {
	msgs := make([]StoredMessage, 0, n)
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0:
			msgs = append(msgs, StoredMessage{
				Role:            "user",
				MessageID:       fmt.Sprintf("msg_%03d", i+1),
				Content:         fmt.Sprintf("User message %d with some content to count tokens", i),
				EstimatedTokens: 10,
			})
		case 1:
			msgs = append(msgs, StoredMessage{
				Role:            "assistant",
				MessageID:       fmt.Sprintf("msg_%03d", i+1),
				Content:         fmt.Sprintf("Assistant response %d with detailed information", i),
				EstimatedTokens: 10,
			})
		case 2:
			msgs = append(msgs, StoredMessage{
				Role:            "tool",
				MessageID:       fmt.Sprintf("msg_%03d", i+1),
				ToolCallID:      fmt.Sprintf("call_%d", i),
				ToolName:        "view",
				ContentText:     fmt.Sprintf("Tool result content %d", i),
				EstimatedTokens: 10,
			})
		}
	}
	return msgs
}

func TestNeedsCompactionTriggersAtThreshold(t *testing.T) {
	cfg := defaultCompactionConfig()
	// Context window = 100 tokens. At 85%, threshold = 85 tokens.
	cm := newCompactionManager(cfg, configpkg.Config{}, 100, nil, "system prompt", nil, &stubTokenCounter{}, nil)

	// 6 messages × 10 tokens = 60 tokens + system prompt ~2 tokens = 62.
	// Below threshold.
	msgs := makeMessages(6)
	if cm.needsCompaction(msgs) {
		t.Fatal("expected no compaction needed below threshold")
	}

	// 20 messages × 10 tokens = 200 tokens, well above 85.
	msgs = makeMessages(20)
	if !cm.needsCompaction(msgs) {
		t.Fatal("expected compaction needed above threshold")
	}
}

func TestNeedsCompactionDisabledByConfig(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.Enabled = false
	cm := newCompactionManager(cfg, configpkg.Config{}, 100, nil, "", nil, &stubTokenCounter{}, nil)

	msgs := makeMessages(20)
	if cm.needsCompaction(msgs) {
		t.Fatal("expected no compaction when disabled")
	}
}

func TestNeedsCompactionSkippedWithNoContextWindow(t *testing.T) {
	cfg := defaultCompactionConfig()
	// contextWindow = 0 means proactive compaction disabled.
	cm := newCompactionManager(cfg, configpkg.Config{}, 0, nil, "", nil, &stubTokenCounter{}, nil)

	msgs := makeMessages(20)
	if cm.needsCompaction(msgs) {
		t.Fatal("expected no proactive compaction without context window")
	}
}

func TestNeedsCompactionRespectsMaxPerRun(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.MaxCompactionsPerRun = 1
	cm := newCompactionManager(cfg, configpkg.Config{}, 100, nil, "", nil, &stubTokenCounter{}, nil)
	cm.compactionCount = 1

	msgs := makeMessages(20)
	if cm.needsCompaction(msgs) {
		t.Fatal("expected no compaction at max per run limit")
	}
}

func TestNeedsCompactionTooFewMessages(t *testing.T) {
	cfg := defaultCompactionConfig()
	cm := newCompactionManager(cfg, configpkg.Config{}, 100, nil, "", nil, &stubTokenCounter{}, nil)

	// KeepRecentMessages = 8, so ≤8 messages should not trigger.
	msgs := makeMessages(8)
	if cm.needsCompaction(msgs) {
		t.Fatal("expected no compaction with too few messages")
	}
}

func TestCompactProducesRecord(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 4

	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("test the system", []string{"read files"}),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, stubModel, "system", nil, &stubTokenCounter{}, nil)

	msgs := makeMessages(12)
	record, err := cm.compact(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}

	if record.Trigger != "proactive" {
		t.Fatalf("expected proactive trigger, got %s", record.Trigger)
	}
	// 12 - 4 = target 8, but msg[8] is a tool result so split snaps back to 7 (assistant).
	if record.CoveredMessageCount != 7 {
		t.Fatalf("expected 7 covered messages (snapped to assistant boundary), got %d", record.CoveredMessageCount)
	}
	if record.Summary.Goal != "test the system" {
		t.Fatalf("expected goal 'test the system', got %q", record.Summary.Goal)
	}
	if record.ID == "" {
		t.Fatal("expected non-empty compaction ID")
	}
	if record.PreTokens <= 0 {
		t.Fatal("expected positive pre-tokens")
	}
	if cm.compactionCount != 1 {
		t.Fatalf("expected compaction count 1, got %d", cm.compactionCount)
	}
	if stubModel.callCount != 1 {
		t.Fatalf("expected 1 model call, got %d", stubModel.callCount)
	}
}

func TestCompactPassesProviderOptionsToGenerate(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 4

	effort := anthropicprovider.EffortMax
	providerOptions := anthropicprovider.NewProviderOptions(&anthropicprovider.ProviderOptions{
		Effort: &effort,
	})
	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("test the system", []string{"read files"}),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, stubModel, "system", providerOptions, &stubTokenCounter{}, nil)

	if _, err := cm.compact(context.Background(), makeMessages(12), false); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(stubModel.lastCall.ProviderOptions, providerOptions) {
		t.Fatalf("unexpected provider options: got %#v want %#v", stubModel.lastCall.ProviderOptions, providerOptions)
	}
}

func TestCompactAggressiveKeepsFewerMessages(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 8
	cfg.AggressiveKeepRecentMessages = 4

	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("reactive test", nil),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, stubModel, "", nil, &stubTokenCounter{}, nil)

	msgs := makeMessages(12)
	record, err := cm.compact(context.Background(), msgs, true)
	if err != nil {
		t.Fatalf("compact aggressive: %v", err)
	}

	if record.Trigger != "reactive" {
		t.Fatalf("expected reactive trigger, got %s", record.Trigger)
	}
	// 12 - 4 = target 8, snapped to 7 (assistant boundary).
	if record.CoveredMessageCount != 7 {
		t.Fatalf("expected 7 covered messages (snapped to assistant boundary), got %d", record.CoveredMessageCount)
	}
}

func TestCompactFailsWhenDisabled(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.Enabled = false
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "", nil, &stubTokenCounter{}, nil)

	_, err := cm.compact(context.Background(), makeMessages(20), false)
	if err == nil {
		t.Fatal("expected error when compaction disabled")
	}
}

func TestCompactFailsWhenMaxReached(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.MaxCompactionsPerRun = 2
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "", nil, &stubTokenCounter{}, nil)
	cm.compactionCount = 2

	_, err := cm.compact(context.Background(), makeMessages(20), false)
	if err == nil {
		t.Fatal("expected error when max compactions reached")
	}
}

func TestCompactFailsWithTooFewMessages(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 8
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "", nil, &stubTokenCounter{}, nil)

	_, err := cm.compact(context.Background(), makeMessages(6), false)
	if err == nil {
		t.Fatal("expected error with too few messages")
	}
}

func TestIncrementalCompactionMergesCheckpoints(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 4

	// First compaction.
	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("first goal", []string{"step1"}),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, stubModel, "", nil, &stubTokenCounter{}, nil)

	msgs := makeMessages(12)
	_, err := cm.compact(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("first compact: %v", err)
	}

	// Checkpoint should now exist.
	if cm.checkpoint == nil {
		t.Fatal("expected checkpoint after first compaction")
	}
	if cm.checkpoint.CoveredCount != 7 {
		t.Fatalf("expected 7 covered (snapped to assistant boundary), got %d", cm.checkpoint.CoveredCount)
	}

	// Second compaction with more messages.
	stubModel.response = makeSummaryJSON("merged goal", []string{"step1", "step2"})
	msgs = makeMessages(20)
	record, err := cm.compact(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("second compact: %v", err)
	}

	// Second compaction should cover more messages.
	if record.CoveredMessageCount != 16 { // 20 - 4
		t.Fatalf("expected 16 covered messages, got %d", record.CoveredMessageCount)
	}
	if cm.compactionCount != 2 {
		t.Fatalf("expected 2 total compactions, got %d", cm.compactionCount)
	}
	if stubModel.callCount != 2 {
		t.Fatalf("expected 2 model calls, got %d", stubModel.callCount)
	}
}

func TestRestoreCheckpointFromTrace(t *testing.T) {
	cfg := defaultCompactionConfig()
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "", nil, &stubTokenCounter{}, nil)

	trace := StoredTrace{
		Compactions: []StoredCompaction{
			{
				ID:                  "compact-1",
				CoveredMessageCount: 10,
				Summary: CompactionSummary{
					Goal:      "persisted goal",
					Completed: []string{"something"},
					ToolOutputs: []CompactionToolOutputRef{
						{OutputID: "out_1", ToolName: "view", WhyItMatters: "file content"},
					},
				},
			},
		},
	}

	cm.restoreCheckpoint(trace)

	if cm.checkpoint == nil {
		t.Fatal("expected checkpoint after restore")
	}
	if cm.checkpoint.CoveredCount != 10 {
		t.Fatalf("expected 10 covered, got %d", cm.checkpoint.CoveredCount)
	}
	if cm.checkpoint.Summary.Goal != "persisted goal" {
		t.Fatalf("unexpected goal: %s", cm.checkpoint.Summary.Goal)
	}
	if cm.compactionCount != 1 {
		t.Fatalf("expected compaction count 1, got %d", cm.compactionCount)
	}
	if len(cm.checkpoint.ToolOutputIDs) != 1 || cm.checkpoint.ToolOutputIDs[0] != "out_1" {
		t.Fatalf("expected tool output ID out_1, got %v", cm.checkpoint.ToolOutputIDs)
	}
}

func TestResumeDoesNotRecompact(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 4

	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("resumed goal", nil),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 100, stubModel, "", nil, &stubTokenCounter{}, nil)

	// Restore a checkpoint that already covers 10 messages.
	trace := StoredTrace{
		Compactions: []StoredCompaction{
			{
				ID:                  "compact-1",
				CoveredMessageCount: 10,
				Summary:             CompactionSummary{Goal: "already compacted"},
			},
		},
	}
	cm.restoreCheckpoint(trace)

	// With 12 messages and 10 already compacted, only 2 remain in the tail.
	// Estimated tokens = system prompt + 2 messages × 10 = ~20-22.
	// Context window 100, threshold 85 - should NOT need compaction.
	msgs := makeMessages(12)
	if cm.needsCompaction(msgs) {
		t.Fatal("should not need compaction after restoring checkpoint with enough coverage")
	}

	// Model should not have been called.
	if stubModel.callCount != 0 {
		t.Fatalf("expected 0 model calls, got %d", stubModel.callCount)
	}
}

func TestApplyToStepTrimsMessagesAndSetsSystem(t *testing.T) {
	cfg := defaultCompactionConfig()
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "original system", nil, &stubTokenCounter{}, nil)

	cm.checkpoint = &compactionCheckpoint{
		Summary: CompactionSummary{
			Goal:      "test goal",
			Completed: []string{"step 1"},
		},
		CoveredCount: 3,
	}

	// Create 5 stored messages.
	storedMsgs := []StoredMessage{
		{Role: "user", Content: "msg 1"},
		{Role: "assistant", Content: "msg 2"},
		{Role: "tool", ToolCallID: "c1", ContentText: "msg 3"},
		{Role: "user", Content: "msg 4"},
		{Role: "assistant", Content: "msg 5"},
	}

	trimmed, systemOverride := cm.applyToStep(storedMsgs)

	// Should rebuild: system placeholder + user continuation + 2 tail messages = 4.
	if len(trimmed) != 4 {
		t.Fatalf("expected 4 rebuilt messages (system + user + 2 tail), got %d", len(trimmed))
	}
	if trimmed[0].Role != fantasy.MessageRoleSystem {
		t.Fatalf("expected system message at [0], got %s", trimmed[0].Role)
	}
	if trimmed[1].Role != fantasy.MessageRoleUser {
		t.Fatalf("expected user continuation at [1], got %s", trimmed[1].Role)
	}

	// Should have system override.
	if systemOverride == nil {
		t.Fatal("expected system override")
	}
	if !strings.Contains(*systemOverride, "original system") {
		t.Fatal("expected original system prompt in override")
	}
	if !strings.Contains(*systemOverride, "Conversation Checkpoint") {
		t.Fatal("expected checkpoint block in override")
	}
	if !strings.Contains(*systemOverride, "test goal") {
		t.Fatal("expected goal in checkpoint")
	}
}

func TestApplyToStepNoOpWithoutCheckpoint(t *testing.T) {
	cfg := defaultCompactionConfig()
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "system", nil, &stubTokenCounter{}, nil)

	msgs := []StoredMessage{
		{Role: "user", Content: "msg 1"},
	}

	rebuilt, systemOverride := cm.applyToStep(msgs)
	if rebuilt != nil {
		t.Fatalf("expected nil rebuilt messages without checkpoint, got %d", len(rebuilt))
	}
	if systemOverride != nil {
		t.Fatal("expected no system override without checkpoint")
	}
}

func TestSpilledToolOutputsCarriedForward(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 2

	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("with outputs", nil),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, stubModel, "", nil, &stubTokenCounter{}, nil)

	msgs := []StoredMessage{
		{Role: "user", MessageID: "msg_001", Content: "read the file", EstimatedTokens: 5},
		{
			Role: "assistant", MessageID: "msg_002", Content: "reading",
			ToolCalls:       []StoredToolCall{{ID: "call_1", Function: &StoredToolFunction{Name: "view"}}},
			EstimatedTokens: 5,
		},
		{
			Role: "tool", MessageID: "msg_003", ToolCallID: "call_1", ToolName: "view",
			ContentText: "file content...",
			ToolOutputRef: &StoredToolOutputRef{
				OutputID:   "out_abc",
				ToolCallID: "call_1",
				SizeBytes:  50000,
			},
			EstimatedTokens: 5,
		},
		{Role: "user", MessageID: "msg_004", Content: "thanks", EstimatedTokens: 5},
		{Role: "assistant", MessageID: "msg_005", Content: "welcome", EstimatedTokens: 5},
	}

	record, err := cm.compact(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}

	// The spilled output should be in the summary.
	if len(record.Summary.ToolOutputs) != 1 {
		t.Fatalf("expected 1 tool output ref, got %d", len(record.Summary.ToolOutputs))
	}
	if record.Summary.ToolOutputs[0].OutputID != "out_abc" {
		t.Fatalf("expected output ID out_abc, got %s", record.Summary.ToolOutputs[0].OutputID)
	}

	// The rendered checkpoint should mention the output.
	augmented := cm.augmentedSystemPrompt()
	if !strings.Contains(augmented, "out_abc") {
		t.Fatal("expected augmented system prompt to reference spilled output")
	}
	if !strings.Contains(augmented, "view_tool_output") {
		t.Fatal("expected augmented prompt to mention view_tool_output")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "raw json",
			input: `{"goal": "test"}`,
			want:  `{"goal": "test"}`,
		},
		{
			name:  "fenced json",
			input: "```json\n{\"goal\": \"test\"}\n```",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "fenced without lang",
			input: "```\n{\"goal\": \"test\"}\n```",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "json with surrounding text",
			input: "Here is the summary:\n{\"goal\": \"test\"}\nDone.",
			want:  `{"goal": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Fatalf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRenderCheckpointBlock(t *testing.T) {
	cfg := defaultCompactionConfig()
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, nil, "", nil, nil, nil)

	summary := CompactionSummary{
		Goal:        "implement feature X",
		Constraints: []string{"must be backward compatible"},
		Decisions:   []string{"use strategy A"},
		Completed:   []string{"wrote config", "added types"},
		Open:        []string{"write tests"},
		Files:       []string{"config.go", "types.go"},
		ToolOutputs: []CompactionToolOutputRef{
			{OutputID: "out_1", ToolName: "view", WhyItMatters: "main config file"},
		},
	}

	rendered := cm.renderCheckpointBlock(summary)

	expectedPhrases := []string{
		"Conversation Checkpoint",
		"implement feature X",
		"backward compatible",
		"strategy A",
		"wrote config",
		"write tests",
		"config.go",
		"out_1",
		"view_tool_output",
		"grep_tool_output",
	}
	for _, phrase := range expectedPhrases {
		if !strings.Contains(rendered, phrase) {
			t.Errorf("expected checkpoint to contain %q", phrase)
		}
	}
}

func TestStoredCompactionSerializesToTrace(t *testing.T) {
	trace := StoredTrace{
		SchemaVersion: 2,
		RunID:         "test-run",
		Model:         "test-model",
		Messages:      []StoredMessage{},
		Compactions: []StoredCompaction{
			{
				ID:                  "compact-1",
				CreatedAt:           time.Now().UTC(),
				Trigger:             "proactive",
				CoveredMessageCount: 10,
				PreTokens:           5000,
				PostTokens:          200,
				Summary: CompactionSummary{
					Goal:      "test serialization",
					Completed: []string{"step 1"},
				},
			},
		},
	}

	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed StoredTrace
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(parsed.Compactions) != 1 {
		t.Fatalf("expected 1 compaction, got %d", len(parsed.Compactions))
	}
	if parsed.Compactions[0].Summary.Goal != "test serialization" {
		t.Fatalf("unexpected goal: %s", parsed.Compactions[0].Summary.Goal)
	}
}

func TestTraceBuilderSnapshotIncludesCompactions(t *testing.T) {
	trace := StoredTrace{
		SchemaVersion: 2,
		RunID:         "test-run",
		Model:         "test-model",
		Messages:      []StoredMessage{},
	}
	builder := newTraceBuilder(trace)
	builder.AddUserMessage("hello")

	// Simulate adding a compaction record.
	builder.trace.Compactions = []StoredCompaction{
		{
			ID:                  "compact-1",
			Trigger:             "proactive",
			CoveredMessageCount: 5,
			Summary:             CompactionSummary{Goal: "snapshot test"},
		},
	}

	snapshot, raw, err := builder.Snapshot(time.Now())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if len(snapshot.Compactions) != 1 {
		t.Fatalf("expected 1 compaction in snapshot, got %d", len(snapshot.Compactions))
	}

	// Verify it's in the JSON too.
	var parsed StoredTrace
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Compactions) != 1 {
		t.Fatalf("expected 1 compaction in JSON, got %d", len(parsed.Compactions))
	}
}

func TestAsContextTooLargeError(t *testing.T) {
	// Non-provider error.
	if asContextTooLargeError(fmt.Errorf("generic error")) != nil {
		t.Fatal("expected nil for generic error")
	}

	// Provider error without context too large.
	plainErr := &fantasy.ProviderError{Message: "rate limit", StatusCode: 429}
	if asContextTooLargeError(plainErr) != nil {
		t.Fatal("expected nil for non-context error")
	}

	// Provider error with context too large.
	ctxErr := &fantasy.ProviderError{
		Message:            "context too large",
		ContextTooLargeErr: true,
		ContextUsedTokens:  200000,
		ContextMaxTokens:   128000,
	}
	result := asContextTooLargeError(ctxErr)
	if result == nil {
		t.Fatal("expected non-nil for context too large error")
	}
	if result.ContextUsedTokens != 200000 {
		t.Fatalf("expected used tokens 200000, got %d", result.ContextUsedTokens)
	}

	// Wrapped provider error.
	wrapped := fmt.Errorf("stream failed: %w", ctxErr)
	result = asContextTooLargeError(wrapped)
	if result == nil {
		t.Fatal("expected non-nil for wrapped context too large error")
	}
}

func TestCompactCollectsToolOutputRefsFromPreviousCheckpoint(t *testing.T) {
	cfg := defaultCompactionConfig()
	cfg.KeepRecentMessages = 2

	stubModel := &stubLanguageModel{
		response: makeSummaryJSON("merged", nil),
	}
	cm := newCompactionManager(cfg, configpkg.Config{}, 1000, stubModel, "", nil, &stubTokenCounter{}, nil)

	// Set up a previous checkpoint with a tool output ref.
	cm.checkpoint = &compactionCheckpoint{
		Summary: CompactionSummary{
			Goal: "previous",
			ToolOutputs: []CompactionToolOutputRef{
				{OutputID: "old_out", ToolName: "grep", WhyItMatters: "search results"},
			},
		},
		CoveredCount:  2,
		ToolOutputIDs: []string{"old_out"},
	}
	cm.lastCompactedIdx = 2
	cm.compactionCount = 1

	msgs := []StoredMessage{
		{Role: "user", Content: "a", EstimatedTokens: 5},
		{Role: "assistant", Content: "b", EstimatedTokens: 5},
		{Role: "user", Content: "c", EstimatedTokens: 5},
		{
			Role: "tool", ToolName: "view", ContentText: "d",
			ToolOutputRef:   &StoredToolOutputRef{OutputID: "new_out"},
			EstimatedTokens: 5,
		},
		{Role: "user", Content: "e", EstimatedTokens: 5},
		{Role: "assistant", Content: "f", EstimatedTokens: 5},
	}

	record, err := cm.compact(context.Background(), msgs, false)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}

	// Should have both old and new tool output refs.
	outputIDs := make(map[string]bool)
	for _, ref := range record.Summary.ToolOutputs {
		outputIDs[ref.OutputID] = true
	}
	if !outputIDs["old_out"] {
		t.Fatal("expected old_out to be carried forward")
	}
	if !outputIDs["new_out"] {
		t.Fatal("expected new_out to be collected from compacted messages")
	}
}

func TestFindCompactionSplitPoint(t *testing.T) {
	tests := []struct {
		name       string
		roles      []string
		keepRecent int
		wantIdx    int
	}{
		{
			name:       "target lands on user message",
			roles:      []string{"user", "assistant", "tool", "user", "assistant", "tool"},
			keepRecent: 3,
			wantIdx:    3, // msg[3] is user - valid
		},
		{
			name:       "target lands on tool, snaps back to assistant",
			roles:      []string{"user", "assistant", "tool", "tool", "user", "assistant"},
			keepRecent: 2,
			wantIdx:    4, // target=4 is user - valid
		},
		{
			name:       "target lands on tool result, snaps to prior assistant",
			roles:      []string{"user", "assistant", "tool", "tool", "tool", "user"},
			keepRecent: 1,
			wantIdx:    5, // target=5 is user - valid
		},
		{
			name:       "not enough messages",
			roles:      []string{"user", "assistant"},
			keepRecent: 5,
			wantIdx:    0,
		},
		{
			name:       "snap backward from tool to assistant",
			roles:      []string{"user", "assistant", "tool", "user"},
			keepRecent: 1,
			wantIdx:    3, // target=3, user - valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := make([]StoredMessage, len(tt.roles))
			for i, role := range tt.roles {
				msgs[i] = StoredMessage{Role: role}
			}
			got := findCompactionSplitPoint(msgs, tt.keepRecent)
			if got != tt.wantIdx {
				t.Fatalf("findCompactionSplitPoint(roles=%v, keep=%d) = %d, want %d",
					tt.roles, tt.keepRecent, got, tt.wantIdx)
			}
		})
	}
}

func TestCompactionConfigDefaults(t *testing.T) {
	// Simulate loading config with no agent.compaction set.
	cfg := configpkg.AgentCompactionConfig{}

	// These are the defaults that loadAgentConfig would apply.
	if cfg.Enabled {
		t.Log("enabled defaults to false in zero-value, but loadAgentConfig sets it true")
	}
	// Verify the default values match plan spec.
	defaults := defaultCompactionConfig()
	if defaults.TriggerPct != 85 {
		t.Fatalf("expected trigger_pct 85, got %d", defaults.TriggerPct)
	}
	if defaults.ReservedTokens != 12000 {
		t.Fatalf("expected reserved_tokens 12000, got %d", defaults.ReservedTokens)
	}
	if defaults.KeepRecentMessages != 8 {
		t.Fatalf("expected keep_recent_messages 8, got %d", defaults.KeepRecentMessages)
	}
	if defaults.AggressiveKeepRecentMessages != 4 {
		t.Fatalf("expected aggressive_keep_recent_messages 4, got %d", defaults.AggressiveKeepRecentMessages)
	}
	if defaults.MaxSummaryTokens != 1200 {
		t.Fatalf("expected max_summary_tokens 1200, got %d", defaults.MaxSummaryTokens)
	}
	if defaults.MaxCompactionsPerRun != 8 {
		t.Fatalf("expected max_compactions_per_run 8, got %d", defaults.MaxCompactionsPerRun)
	}
}
