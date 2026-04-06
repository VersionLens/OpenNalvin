package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

type agentRunTimingTracker struct {
	out io.Writer

	requestStartedAt time.Time
	turnCreatedAt    time.Time
	turnStartedAt    *time.Time
	jobEnqueuedAt    *time.Time
	turnQueuedAt     *time.Time

	serverStartedAt         *time.Time
	firstMeaningfulKind     string
	serverFirstMeaningfulAt *time.Time
	localFirstMeaningfulAt  *time.Time
	serverFirstContentAt    *time.Time
	localFirstContentAt     *time.Time
}

func newAgentRunTimingTracker(out io.Writer, requestStartedAt, turnCreatedAt time.Time) *agentRunTimingTracker {
	return &agentRunTimingTracker{
		out:              out,
		requestStartedAt: requestStartedAt.UTC(),
		turnCreatedAt:    turnCreatedAt.UTC(),
	}
}

func (t *agentRunTimingTracker) RecordEvent(evt agentrun.Event) {
	if t == nil {
		return
	}
	switch evt.Kind {
	case agentrun.EventKindStarted:
		if t.serverStartedAt == nil && !evt.CreatedAt.IsZero() {
			t.serverStartedAt = timingTimePtr(evt.CreatedAt.UTC())
		}
	case agentrun.EventKindTraceChunk:
		if evt.Chunk == nil {
			return
		}
		now := time.Now().UTC()
		if t.firstMeaningfulKind == "" {
			if kind := timingMeaningfulEventKind(evt); kind != "" {
				t.firstMeaningfulKind = kind
				if !evt.CreatedAt.IsZero() {
					t.serverFirstMeaningfulAt = timingTimePtr(evt.CreatedAt.UTC())
				}
				t.localFirstMeaningfulAt = timingTimePtr(now)
			}
		}
		if t.serverFirstContentAt == nil && timingChunkHasContentDelta(*evt.Chunk) {
			if !evt.CreatedAt.IsZero() {
				t.serverFirstContentAt = timingTimePtr(evt.CreatedAt.UTC())
			}
			t.localFirstContentAt = timingTimePtr(now)
		}
	case agentrun.EventKindToolResult:
		if t.firstMeaningfulKind == "" {
			now := time.Now().UTC()
			t.firstMeaningfulKind = "tool_result"
			if !evt.CreatedAt.IsZero() {
				t.serverFirstMeaningfulAt = timingTimePtr(evt.CreatedAt.UTC())
			}
			t.localFirstMeaningfulAt = timingTimePtr(now)
		}
	}
}

func (t *agentRunTimingTracker) RecordTurn(turn *knowledge.AgentRunTurn) {
	if t == nil || turn == nil || turn.StartedAt == nil || t.turnStartedAt != nil {
		return
	}
	t.turnStartedAt = timingTimePtr(turn.StartedAt.UTC())
}

func (t *agentRunTimingTracker) RecordQueued(enqueuedAt, queuedAt time.Time) {
	if t == nil {
		return
	}
	if !enqueuedAt.IsZero() {
		t.jobEnqueuedAt = timingTimePtr(enqueuedAt.UTC())
	}
	if !queuedAt.IsZero() {
		t.turnQueuedAt = timingTimePtr(queuedAt.UTC())
	}
}

func (t *agentRunTimingTracker) PrintSummary() {
	if t == nil {
		return
	}
	startedAt := t.serverStartedAt
	if startedAt == nil {
		startedAt = t.turnStartedAt
	}

	if t.firstMeaningfulKind != "" && t.serverFirstMeaningfulAt != nil && t.localFirstMeaningfulAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] first meaningful stream event=%s at %s (+%s local)\n",
			t.firstMeaningfulKind,
			formatTimingDateTime(t.serverFirstMeaningfulAt),
			formatTimingDuration(t.localFirstMeaningfulAt.Sub(t.requestStartedAt)),
		)
	}
	if t.serverFirstContentAt != nil && t.localFirstContentAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] first content token at %s (+%s local)\n",
			formatTimingDateTime(t.serverFirstContentAt),
			formatTimingDuration(t.localFirstContentAt.Sub(t.requestStartedAt)),
		)
	}

	_, _ = fmt.Fprintln(t.out, "[timing] TTFT breakdown")
	if !t.turnCreatedAt.IsZero() {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] local request->turn created: %s\n",
			formatTimingDuration(t.turnCreatedAt.Sub(t.requestStartedAt)),
		)
	}
	if !t.turnCreatedAt.IsZero() && t.jobEnqueuedAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] local turn created->job enqueued: %s\n",
			formatTimingDuration(t.jobEnqueuedAt.Sub(t.turnCreatedAt)),
		)
	}
	if t.jobEnqueuedAt != nil && t.turnQueuedAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] local job enqueued->turn queued: %s\n",
			formatTimingDuration(t.turnQueuedAt.Sub(*t.jobEnqueuedAt)),
		)
	}
	if t.turnQueuedAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] local request->queued: %s\n",
			formatTimingDuration(t.turnQueuedAt.Sub(t.requestStartedAt)),
		)
	}
	if !t.turnCreatedAt.IsZero() && startedAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] server queue wait: %s\n",
			formatTimingDuration(startedAt.Sub(t.turnCreatedAt)),
		)
	}
	if startedAt != nil && t.serverFirstMeaningfulAt != nil && t.firstMeaningfulKind != "" {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] server started->first %s: %s\n",
			t.firstMeaningfulKind,
			formatTimingDuration(t.serverFirstMeaningfulAt.Sub(*startedAt)),
		)
	}
	if !t.turnCreatedAt.IsZero() && t.serverFirstMeaningfulAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] server TTFT: %s\n",
			formatTimingDuration(t.serverFirstMeaningfulAt.Sub(t.turnCreatedAt)),
		)
	}
	if t.localFirstMeaningfulAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] local end-to-end TTFT: %s\n",
			formatTimingDuration(t.localFirstMeaningfulAt.Sub(t.requestStartedAt)),
		)
	}
	if startedAt != nil && t.serverFirstContentAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] server started->first content: %s\n",
			formatTimingDuration(t.serverFirstContentAt.Sub(*startedAt)),
		)
	}
	if !t.turnCreatedAt.IsZero() && t.serverFirstContentAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] server content TTFT: %s\n",
			formatTimingDuration(t.serverFirstContentAt.Sub(t.turnCreatedAt)),
		)
	}
	if t.localFirstContentAt != nil {
		_, _ = fmt.Fprintf(
			t.out,
			"[timing] local content TTFT: %s\n",
			formatTimingDuration(t.localFirstContentAt.Sub(t.requestStartedAt)),
		)
	}
}

func timingMeaningfulEventKind(evt agentrun.Event) string {
	switch evt.Kind {
	case agentrun.EventKindTraceChunk:
		if evt.Chunk == nil {
			return ""
		}
		return timingMeaningfulChunkKind(*evt.Chunk)
	case agentrun.EventKindToolResult:
		return "tool_result"
	default:
		return ""
	}
}

func timingMeaningfulChunkKind(chunk agentpkg.TraceChunk) string {
	if chunk.Error != nil {
		return "error"
	}
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
		return ""
	}
	delta := chunk.Choices[0].Delta
	switch {
	case strings.TrimSpace(delta.Content) != "":
		return "content"
	case strings.TrimSpace(delta.Reasoning) != "":
		return "reasoning"
	case len(delta.ToolCalls) > 0:
		return "tool_call"
	default:
		return ""
	}
}

func timingChunkHasContentDelta(chunk agentpkg.TraceChunk) bool {
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
		return false
	}
	return strings.TrimSpace(chunk.Choices[0].Delta.Content) != ""
}

func formatTimingDuration(duration time.Duration) string {
	micros := duration.Microseconds()
	if micros < 0 {
		micros = -micros
	}
	switch {
	case micros < 1000:
		return fmt.Sprintf("%dus", micros)
	case micros < 1_000_000:
		return fmt.Sprintf("%.1fms", float64(micros)/1000)
	default:
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
}

func formatTimingDateTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func timingTimePtr(value time.Time) *time.Time {
	return &value
}
