package agent

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
)

func TestStoredMessagesFromFantasyMessagesRoundTrip(t *testing.T) {
	t.Parallel()

	history := []fantasy.Message{
		fantasy.NewUserMessage("hello"),
		{
			Role: fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{
				fantasy.TextPart{Text: "hi there"},
				fantasy.ToolCallPart{ToolCallID: "call_1", ToolName: "ls", Input: `{"path":"."}`},
			},
		},
		{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{
				fantasy.ToolResultPart{
					ToolCallID: "call_1",
					Output:     fantasy.ToolResultOutputContentText{Text: "file.txt"},
				},
			},
		},
	}

	stored := storedMessagesFromFantasyMessages(history)
	if len(stored) != 3 {
		t.Fatalf("expected 3 stored messages, got %d", len(stored))
	}
	if stored[1].Content != "hi there" {
		t.Fatalf("unexpected assistant content: %q", stored[1].Content)
	}
	if len(stored[1].ToolCalls) != 1 || stored[1].ToolCalls[0].Function == nil || stored[1].ToolCalls[0].Function.Name != "ls" {
		t.Fatalf("unexpected assistant tool calls: %+v", stored[1].ToolCalls)
	}

	roundTrip := traceToFantasyMessages(StoredTrace{Messages: stored})
	if len(roundTrip) != 3 {
		t.Fatalf("expected 3 round-trip messages, got %d", len(roundTrip))
	}
	if roundTrip[0].Role != fantasy.MessageRoleUser {
		t.Fatalf("unexpected first role: %v", roundTrip[0].Role)
	}
	if text, ok := fantasy.AsMessagePart[fantasy.TextPart](roundTrip[1].Content[0]); !ok || text.Text != "hi there" {
		t.Fatalf("unexpected assistant round-trip content: %#v", roundTrip[1].Content)
	}
}

func TestStoredTraceRoundTripsEffectiveSystemPromptAndRequestedSkillNames(t *testing.T) {
	t.Parallel()

	original := StoredTrace{
		SchemaVersion:         2,
		RunID:                 "run_eff",
		Model:                 "gpt-test",
		SystemPrompt:          "raw user prompt",
		EffectiveSystemPrompt: "raw user prompt\n\n## Skill: alpha\n\nbody",
		Metadata: StoredRunMeta{
			RunKind:             RunKindRoot,
			RequestedSkillNames: []string{"alpha", "beta"},
		},
		Messages: []StoredMessage{},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}

	trace, err := parseStoredTrace(raw)
	if err != nil {
		t.Fatalf("parse stored trace: %v", err)
	}
	if trace.EffectiveSystemPrompt != original.EffectiveSystemPrompt {
		t.Fatalf("expected effective system prompt %q, got %q", original.EffectiveSystemPrompt, trace.EffectiveSystemPrompt)
	}
	if got := trace.Metadata.RequestedSkillNames; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("expected requested skill names [alpha beta], got %v", got)
	}
}

func TestParseStoredTraceBackfillsDefaultProvider(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(StoredTrace{
		SchemaVersion: 2,
		RunID:         "run_123",
		Model:         "gpt-test",
		Messages:      []StoredMessage{},
	})
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}

	trace, err := parseStoredTrace(raw)
	if err != nil {
		t.Fatalf("parse stored trace: %v", err)
	}
	if trace.Provider != "default" {
		t.Fatalf("expected default provider backfill, got %q", trace.Provider)
	}
}
