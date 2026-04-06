package agent

import (
	"strings"
	"testing"
	"time"
)

func TestCurrentDateTimePromptLine(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, zone)

	got := currentDateTimePromptLine(now)
	want := "It is Tuesday 31 March 2026 22:21 CEST"
	if got != want {
		t.Fatalf("unexpected date/time line: got %q want %q", got, want)
	}
}

func TestEffectiveSystemPromptAtIncludesDateTimeLine(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, zone)

	got := effectiveSystemPromptAt(now, "Be concise.")
	if !strings.Contains(got, "It is Tuesday 31 March 2026 22:21 CEST") {
		t.Fatalf("expected prompt to include formatted date/time, got %q", got)
	}
	if !strings.HasSuffix(got, "\nBe concise.") {
		t.Fatalf("expected prompt to end with user-supplied system prompt, got %q", got)
	}
}

func TestEffectiveSystemPromptForRootIncludesDelegationGuidance(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, zone)

	got := effectiveSystemPromptForRunAt(now, "", RunKindRoot)

	// Core workflow guidance
	for _, needle := range []string{
		"HOW TO WORK",
		"search_tools",
		"todowrite",
		"Complete ALL steps",
		// Child agent guidance (root only)
		"CHILD AGENTS",
		"child agents to work in parallel",
		// Todo guidance (root only)
		"TODO TRACKING",
		"call todowrite FIRST",
		"check that every item is completed",
		// News guidance
		"NEWS AND CURRENT EVENTS",
		`"news" or "reddit"`,
		"use those",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected root prompt to include %q, got %q", needle, got)
		}
	}
}

func TestEffectiveSystemPromptForChildOmitsDelegationGuidance(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, zone)

	got := effectiveSystemPromptForRunAt(now, "", RunKindChild)

	// Child should NOT have delegation or todo sections
	if strings.Contains(got, "CHILD AGENTS") {
		t.Fatalf("expected child prompt to omit delegation guidance, got %q", got)
	}
	if strings.Contains(got, "TODO TRACKING") {
		t.Fatalf("expected child prompt to omit todo guidance, got %q", got)
	}

	// Child should still have news awareness and workspace access
	if !strings.Contains(got, "NEWS AND CURRENT EVENTS") {
		t.Fatalf("expected child prompt to include news tool awareness, got %q", got)
	}
	if !strings.Contains(got, "WORKSPACE ACCESS") {
		t.Fatalf("expected child prompt to include workspace access guidance, got %q", got)
	}
}

func TestEffectiveSystemPromptForPlanRunIncludesPlanModeGuidance(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, time.FixedZone("CEST", 2*60*60))
	got := effectiveSystemPromptForRunContextAt(now, "", StoredRunMeta{
		RunKind: RunKindRoot,
		Mode:    RunModePlan,
		PlanRef: StoredPlanRef{Path: ".plans/20260331-example.md"},
	})

	for _, needle := range []string{
		"PLAN MODE",
		".plans/20260331-example.md",
		"inspect code, search files, run tests",
		"Do NOT use tools to carry out or implement the requested repository changes",
		"update that exact file with write, edit, or multiedit",
		"do not draft plan content in /tmp, temp files, or shell redirections",
		"call plan_exit immediately",
		"call plan_exit",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("expected plan prompt to include %q, got %q", needle, got)
		}
	}
}

func TestEffectiveSystemPromptForPlanChildOmitsPlanExitInstruction(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, time.FixedZone("CEST", 2*60*60))
	got := effectiveSystemPromptForRunContextAt(now, "", StoredRunMeta{
		RunKind: RunKindChild,
		Mode:    RunModePlan,
		PlanRef: StoredPlanRef{Path: ".plans/20260331-example.md"},
	})

	if !strings.Contains(got, "Do not finalize the plan or call plan_exit from a child run") {
		t.Fatalf("expected child plan prompt to keep plan_exit with parent, got %q", got)
	}
}
