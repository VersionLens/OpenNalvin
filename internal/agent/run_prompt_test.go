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
	if !strings.Contains(got, "DELEGATION STRATEGY") {
		t.Fatalf("expected root prompt to include delegation guidance, got %q", got)
	}
	if !strings.Contains(got, "you MUST explicitly consider") {
		t.Fatalf("expected root prompt to require explicit delegation consideration, got %q", got)
	}
	if !strings.Contains(got, "web research on separate subtopics") {
		t.Fatalf("expected root prompt to include delegation examples, got %q", got)
	}
	if !strings.Contains(got, "If there are 3 clearly separable research threads, default to using 3 child") {
		t.Fatalf("expected root prompt to include stronger multi-thread delegation guidance, got %q", got)
	}
	if !strings.Contains(got, "TODO TRACKING") {
		t.Fatalf("expected root prompt to include todo guidance, got %q", got)
	}
	if !strings.Contains(got, "use todoread and todowrite") {
		t.Fatalf("expected root prompt to mention todo tools, got %q", got)
	}
	if !strings.Contains(got, "you MUST explicitly decide whether") {
		t.Fatalf("expected root prompt to require explicit todo consideration, got %q", got)
	}
	if !strings.Contains(got, "Todo tracking is required when") {
		t.Fatalf("expected root prompt to define when todo tracking is required, got %q", got)
	}
	if !strings.Contains(got, "medium or large research/comparison work") {
		t.Fatalf("expected root prompt to require todo tracking for medium research work, got %q", got)
	}
	if !strings.Contains(got, "do not keep the task list only in your reasoning") {
		t.Fatalf("expected root prompt to forbid keeping required todos only in reasoning, got %q", got)
	}
	if !strings.Contains(got, "your first substantive tool action should") {
		t.Fatalf("expected root prompt to prioritize early todo tool usage, got %q", got)
	}
	if !strings.Contains(got, "On a fresh run, create the todo list early with todowrite") {
		t.Fatalf("expected root prompt to require early todowrite on fresh runs, got %q", got)
	}
	if !strings.Contains(got, "Keep exactly one item in_progress") {
		t.Fatalf("expected root prompt to include todo usage guidance, got %q", got)
	}
	if !strings.Contains(got, "Before waiting on multiple child agents") {
		t.Fatalf("expected root prompt to include child-agent todo guidance, got %q", got)
	}
	if !strings.Contains(got, "the default behavior should be to create and maintain a todo list") {
		t.Fatalf("expected root prompt to default to todo tracking for substantial tasks, got %q", got)
	}
	if !strings.Contains(got, "maintaining a todo list even if you are not using child agents") {
		t.Fatalf("expected root prompt to default todo tracking for medium research without child agents, got %q", got)
	}
	if !strings.Contains(got, "hidden domain-specific tools may exist") {
		t.Fatalf("expected root prompt to mention hidden domain-specific tools, got %q", got)
	}
	if !strings.Contains(got, "news, headlines, RSS feeds") {
		t.Fatalf("expected root prompt to mention news-style requests, got %q", got)
	}
	if !strings.Contains(got, "news, rss, headlines, reddit, subreddit") {
		t.Fatalf("expected root prompt to include concise news discovery queries, got %q", got)
	}
	if !strings.Contains(got, "over broad web search") {
		t.Fatalf("expected root prompt to prefer domain-specific tools over web search, got %q", got)
	}
	if !strings.Contains(got, "NEWS TOOL AWARENESS") {
		t.Fatalf("expected root prompt to include child-safe news tool awareness, got %q", got)
	}
	if !strings.Contains(got, "NEWS COLLECTION PLAYBOOK") {
		t.Fatalf("expected root prompt to include news collection guidance, got %q", got)
	}
	if !strings.Contains(got, "Use todowrite early to track the categories or sources") {
		t.Fatalf("expected root prompt to require early todo tracking for news collection, got %q", got)
	}
	if !strings.Contains(got, "news_rss_headlines, news_reddit_top_posts, and news_reddit_post_details") {
		t.Fatalf("expected root prompt to name the news tools explicitly, got %q", got)
	}
	if !strings.Contains(got, "Use both RSS and Reddit tools") {
		t.Fatalf("expected root prompt to combine RSS and Reddit tools for broad news requests, got %q", got)
	}
	if !strings.Contains(got, "mapped onto the available") {
		t.Fatalf("expected root prompt to mention category mapping for news collection, got %q", got)
	}
	if !strings.Contains(got, "one child per category or per source family") {
		t.Fatalf("expected root prompt to suggest category-level news delegation, got %q", got)
	}
	if !strings.Contains(got, "use that response to correct") {
		t.Fatalf("expected root prompt to use available categories to correct the plan, got %q", got)
	}
}

func TestEffectiveSystemPromptForChildOmitsDelegationGuidance(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.March, 31, 22, 21, 0, 0, zone)

	got := effectiveSystemPromptForRunAt(now, "", RunKindChild)
	if strings.Contains(got, "DELEGATION STRATEGY") {
		t.Fatalf("expected child prompt to omit delegation guidance, got %q", got)
	}
	if strings.Contains(got, "web research on separate subtopics") {
		t.Fatalf("expected child prompt to omit delegation examples, got %q", got)
	}
	if strings.Contains(got, "TODO TRACKING") {
		t.Fatalf("expected child prompt to omit todo guidance, got %q", got)
	}
	if strings.Contains(got, "use todoread and todowrite") {
		t.Fatalf("expected child prompt to omit todo tool guidance, got %q", got)
	}
	if !strings.Contains(got, "NEWS TOOL AWARENESS") {
		t.Fatalf("expected child prompt to include news tool awareness, got %q", got)
	}
	if !strings.Contains(got, "news_rss_headlines, news_reddit_top_posts, and news_reddit_post_details") {
		t.Fatalf("expected child prompt to name the news tools explicitly, got %q", got)
	}
	if !strings.Contains(got, "mapped onto the available") {
		t.Fatalf("expected child prompt to mention category mapping for news collection, got %q", got)
	}
	if !strings.Contains(got, "Use web search only as a fallback") {
		t.Fatalf("expected child prompt to keep web search as fallback for news collection, got %q", got)
	}
	if strings.Contains(got, "NEWS COLLECTION PLAYBOOK") {
		t.Fatalf("expected child prompt to omit root-only news collection playbook, got %q", got)
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
