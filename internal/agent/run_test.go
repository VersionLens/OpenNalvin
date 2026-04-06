package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	anthropicprovider "charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
)

func TestFantasyProviderOptionsForOpenAIReasoningEffort(t *testing.T) {
	opts, err := fantasyProviderOptions(ProviderConfig{
		Type:            ProviderTypeOpenAI,
		Model:           "gpt-5.4",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("fantasyProviderOptions: %v", err)
	}

	effort := openai.ReasoningEffortHigh
	want := openai.NewResponsesProviderOptions(&openai.ResponsesProviderOptions{
		ReasoningEffort: &effort,
	})
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("unexpected provider options: got %#v want %#v", opts, want)
	}
}

func TestFantasyProviderOptionsForOpenAINonResponsesModel(t *testing.T) {
	opts, err := fantasyProviderOptions(ProviderConfig{
		Type:            ProviderTypeOpenAI,
		Model:           "legacy-test-model",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("fantasyProviderOptions: %v", err)
	}

	effort := openai.ReasoningEffortHigh
	want := openai.NewProviderOptions(&openai.ProviderOptions{
		ReasoningEffort: &effort,
	})
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("unexpected provider options: got %#v want %#v", opts, want)
	}
}

func TestFantasyProviderOptionsForOpenAICompatReasoningEffort(t *testing.T) {
	opts, err := fantasyProviderOptions(ProviderConfig{
		Type:            ProviderTypeOpenAICompat,
		Model:           "local-model",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("fantasyProviderOptions: %v", err)
	}

	effort := openai.ReasoningEffortHigh
	want := openaicompat.NewProviderOptions(&openaicompat.ProviderOptions{
		ReasoningEffort: &effort,
	})
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("unexpected provider options: got %#v want %#v", opts, want)
	}
}

func TestFantasyProviderOptionsForAnthropicReasoningEffort(t *testing.T) {
	opts, err := fantasyProviderOptions(ProviderConfig{
		Type:            ProviderTypeAnthropic,
		ReasoningEffort: "max",
	})
	if err != nil {
		t.Fatalf("fantasyProviderOptions: %v", err)
	}

	effort := anthropicprovider.EffortMax
	want := anthropicprovider.NewProviderOptions(&anthropicprovider.ProviderOptions{
		Effort: &effort,
	})
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("unexpected provider options: got %#v want %#v", opts, want)
	}
}

func TestFantasyProviderOptionsUnsetReasoningEffortReturnsNil(t *testing.T) {
	opts, err := fantasyProviderOptions(ProviderConfig{
		Type: ProviderTypeOpenAI,
	})
	if err != nil {
		t.Fatalf("fantasyProviderOptions: %v", err)
	}
	if opts != nil {
		t.Fatalf("expected nil provider options, got %#v", opts)
	}
}

func TestFantasyProviderOptionsRejectsInvalidReasoningEffort(t *testing.T) {
	_, err := fantasyProviderOptions(ProviderConfig{
		Type:            ProviderTypeAnthropic,
		ReasoningEffort: "minimal",
	})
	if err == nil || err.Error() != `reasoning_effort "minimal" is invalid for type "anthropic" (allowed: low, medium, high, max)` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewFantasyProviderUsesOfficialOpenAIProvider(t *testing.T) {
	provider, err := newFantasyProvider(ProviderConfig{
		Type:   ProviderTypeOpenAI,
		APIKey: "secret-key",
	})
	if err != nil {
		t.Fatalf("newFantasyProvider: %v", err)
	}
	if provider.Name() != openai.Name {
		t.Fatalf("unexpected provider name: %q", provider.Name())
	}

	model, err := provider.LanguageModel(context.Background(), "gpt-5.4")
	if err != nil {
		t.Fatalf("LanguageModel: %v", err)
	}
	if model.Provider() != openai.Name {
		t.Fatalf("unexpected language model provider: %q", model.Provider())
	}
}

func TestNewFantasyProviderUsesOpenAICompatProvider(t *testing.T) {
	provider, err := newFantasyProvider(ProviderConfig{
		Type:    ProviderTypeOpenAICompat,
		BaseURL: "http://localhost:1234/v1",
		APIKey:  "secret-key",
	})
	if err != nil {
		t.Fatalf("newFantasyProvider: %v", err)
	}
	if provider.Name() != openai.Name {
		t.Fatalf("unexpected provider name: %q", provider.Name())
	}
	model, err := provider.LanguageModel(context.Background(), "local-model")
	if err != nil {
		t.Fatalf("LanguageModel: %v", err)
	}
	if model.Provider() != openaicompat.Name {
		t.Fatalf("unexpected language model provider: %q", model.Provider())
	}
}

func TestShouldTerminateAfterToolResultForReadyPlanExit(t *testing.T) {
	if !shouldTerminateAfterToolResult(ToolResultEvent{
		ToolName: "plan_exit",
		PlanRef: &StoredPlanRef{
			Path:        ".plans/example.md",
			SourceRunID: "run_plan",
			Status:      PlanStatusReady,
		},
	}) {
		t.Fatal("expected ready plan_exit result to terminate the run")
	}
}

func TestShouldTerminateAfterToolResultIgnoresNonReadyOrErroredResults(t *testing.T) {
	cases := []ToolResultEvent{
		{ToolName: "plan_exit", IsError: true, PlanRef: &StoredPlanRef{Path: ".plans/example.md", Status: PlanStatusReady}},
		{ToolName: "plan_exit", PlanRef: &StoredPlanRef{Path: ".plans/example.md", Status: PlanStatusActive}},
		{ToolName: "write", PlanRef: &StoredPlanRef{Path: ".plans/example.md", Status: PlanStatusReady}},
		{ToolName: "plan_exit"},
	}
	for _, tc := range cases {
		if shouldTerminateAfterToolResult(tc) {
			t.Fatalf("expected tool result %#v not to terminate the run", tc)
		}
	}
}

func TestInferImplementationTargetRootFindsRepoPrefixFromPlanPaths(t *testing.T) {
	planBody := `# Plan

- Create ` + "`demo-default-1775260677/VERSION`" + `
- Create ` + "`demo-default-1775260677/README.md`" + `
- Create ` + "`demo-default-1775260677/scripts/release.sh`" + `
`
	if got := InferImplementationTargetRoot(planBody); got != "demo-default-1775260677" {
		t.Fatalf("expected inferred target root demo-default-1775260677, got %q", got)
	}
}

func TestInferImplementationTargetRootFromContextPrefersNamedWorkspaceDir(t *testing.T) {
	workspaceDirs := []string{".plans", "demo-default-1775260677", "scripts"}
	title := "Plan: Safer Local Release Workflow for demo-default-1775260677"
	prompt := "Immediately write a short concrete plan for turning demo-default-1775260677 into a slightly safer local release workflow."
	planBody := `# Plan

- Write a single line to ` + "`VERSION`" + ` at the repo root.
- Add project docs to ` + "`README.md`" + `.
- Create ` + "`scripts/release.sh`" + `.
`
	if got := InferImplementationTargetRootFromContext(workspaceDirs, title, prompt, planBody); got != "demo-default-1775260677" {
		t.Fatalf("expected inferred target root demo-default-1775260677, got %q", got)
	}
}

func TestImplementPlanMessageIncludesTargetRootGuidance(t *testing.T) {
	planBody := `# Plan

- Create ` + "`demo-default-1775260677/VERSION`" + `
- Create ` + "`demo-default-1775260677/README.md`" + `
- Create ` + "`demo-default-1775260677/scripts/release.sh`" + `
`
	got := ImplementPlanMessage(".plans/example.md", planBody, "demo-default-1775260677")
	for _, needle := range []string{
		"Target workspace-relative root: demo-default-1775260677",
		"Create and update files under that directory unless the plan explicitly says otherwise.",
		"Do not place those files at the workspace root by default.",
		"Prefer direct file tools (`write`, `edit`, `multiedit`) for creating and updating plan files.",
		"Avoid batching file creation in `shell` unless you truly need it: shell writes are rolled back if the script exits non-zero.",
		"If you do use `shell`, separate file writes from follow-up verification or permission commands.",
		"Follow any workspace-relative paths in the plan exactly as written.",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected implementation message to include %q, got %q", needle, got)
		}
	}
}

func TestToolFailureFingerprint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		a, b    string
		wantEq  bool
	}{
		{"identical", "docker: not found", "docker: not found", true},
		{"case insensitive", "Docker: Not Found", "docker: not found", true},
		{"whitespace collapse", "docker:  not   found", "docker: not found", true},
		{"different errors", "docker: not found", "curl: not found", false},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fpA := toolFailureFingerprint(tt.a)
			fpB := toolFailureFingerprint(tt.b)
			if (fpA == fpB) != tt.wantEq {
				t.Fatalf("fingerprint(%q) == fingerprint(%q) = %t, want %t",
					tt.a, tt.b, fpA == fpB, tt.wantEq)
			}
		})
	}
}

func TestToolFailureFingerprintTruncates(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 500)
	fp := toolFailureFingerprint(long)
	if len(fp) > 200 {
		t.Fatalf("expected fingerprint to be at most 200 bytes, got %d", len(fp))
	}
}
