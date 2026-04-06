package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestAgentRunTimingTrackerPrintsTTFTBreakdown(t *testing.T) {
	requestStartedAt := time.Date(2026, 3, 31, 12, 53, 0, 0, time.UTC)
	turnCreatedAt := requestStartedAt.Add(120 * time.Millisecond)
	turnStartedAt := turnCreatedAt.Add(1100 * time.Millisecond)
	firstReasoningAt := turnStartedAt.Add(5600 * time.Millisecond)
	firstContentAt := turnStartedAt.Add(6800 * time.Millisecond)
	jobEnqueuedAt := turnCreatedAt.Add(10 * time.Millisecond)
	turnQueuedAt := jobEnqueuedAt.Add(15 * time.Millisecond)

	var out bytes.Buffer
	tracker := newAgentRunTimingTracker(&out, requestStartedAt, turnCreatedAt)
	tracker.RecordQueued(jobEnqueuedAt, turnQueuedAt)
	tracker.RecordTurn(&knowledge.AgentRunTurn{StartedAt: timingTimePtr(turnStartedAt)})
	tracker.RecordEvent(agentrun.Event{
		Kind:      agentrun.EventKindStarted,
		CreatedAt: turnStartedAt,
	})
	tracker.RecordEvent(agentrun.Event{
		Kind:      agentrun.EventKindTraceChunk,
		CreatedAt: firstReasoningAt,
		Chunk: &agentpkg.TraceChunk{
			Choices: []agentpkg.TraceChoice{{
				Delta: &agentpkg.TraceDelta{Reasoning: "thinking"},
			}},
		},
	})
	tracker.RecordEvent(agentrun.Event{
		Kind:      agentrun.EventKindTraceChunk,
		CreatedAt: firstContentAt,
		Chunk: &agentpkg.TraceChunk{
			Choices: []agentpkg.TraceChoice{{
				Delta: &agentpkg.TraceDelta{Content: "TTFT_OK"},
			}},
		},
	})
	tracker.localFirstMeaningfulAt = timingTimePtr(requestStartedAt.Add(7 * time.Second))
	tracker.localFirstContentAt = timingTimePtr(requestStartedAt.Add(8 * time.Second))

	tracker.PrintSummary()
	output := out.String()

	for _, needle := range []string{
		"[timing] first meaningful stream event=reasoning",
		"[timing] first content token at 2026-03-31T12:53:08.02Z",
		"[timing] TTFT breakdown",
		"[timing] local request->turn created: 120.0ms",
		"[timing] local turn created->job enqueued: 10.0ms",
		"[timing] local job enqueued->turn queued: 15.0ms",
		"[timing] local request->queued: 145.0ms",
		"[timing] server queue wait: 1100ms",
		"[timing] server started->first reasoning: 5600ms",
		"[timing] server TTFT: 6700ms",
		"[timing] local end-to-end TTFT: 7000ms",
		"[timing] server started->first content: 6800ms",
		"[timing] server content TTFT: 7900ms",
		"[timing] local content TTFT: 8000ms",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected output to contain %q, got:\n%s", needle, output)
		}
	}
}
