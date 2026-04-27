// Package chatrun is a thin convenience wrapper that lets non-CLI surfaces
// (Discord text bridge, etc.) start an inline agent run with a stable RunID
// per conversation, so prior turns are automatically rejoined when the same
// run is re-used.
package chatrun

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"

	"charm.land/fantasy"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

// Request is the input for an attached chat-style agent run.
type Request struct {
	// Message is the new user turn.
	Message string
	// RunID is the stable identifier for the conversation. If a prior agent
	// run is found in the store with this ID, its trace is reused so the
	// model sees the previous turns. Empty starts a fresh run.
	RunID string
	// History is included only when RunID is empty (i.e. the first turn).
	History []fantasy.Message
	// Out, when provided, captures streamed assistant text output.
	Out io.Writer
	// WorkerID is a free-form identifier recorded in logs for diagnostic
	// purposes (e.g. "discord-<conv_id>").
	WorkerID string
	// ProviderName overrides the provider used for this turn.
	ProviderName string
}

// Result is what an attached chat-style turn returns once the model has
// finished and the trace has been persisted.
type Result struct {
	RunID string
}

// RunAttached executes an inline agent run and returns its RunID. The run is
// persisted in the supplied store so subsequent calls with the same RunID
// can rejoin the conversation.
func RunAttached(ctx context.Context, store *knowledge.Store, logger *slog.Logger, req Request) (Result, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if req.WorkerID != "" {
		logger = logger.With("worker_id", strings.TrimSpace(req.WorkerID))
	}

	out := req.Out
	if out == nil {
		out = &bytes.Buffer{}
	}

	runID, err := agentpkg.Run(ctx, store, agentpkg.RunRequest{
		Message:      req.Message,
		RunID:        strings.TrimSpace(req.RunID),
		History:      append([]fantasy.Message(nil), req.History...),
		ProviderName: strings.TrimSpace(req.ProviderName),
	}, agentpkg.RunOptions{
		Out: out,
	})
	return Result{RunID: runID}, err
}
