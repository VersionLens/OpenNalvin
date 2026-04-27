// Package chatrun is a thin shim over internal/agentrun used by the
// messaging integrations (WhatsApp, Discord) so they can route an inbound
// message into an agent run without taking a direct dependency on the
// agentrun service surface.
package chatrun

import (
	"context"
	"io"
	"log/slog"

	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

// Request describes an inbound message to attach to an agent run.
//
// Prior conversation history is not carried inline; callers are expected to
// supply a stable RunID so subsequent turns reuse the same run, and the
// agentrun service reads earlier persisted turns from the knowledge store.
// To seed history from outside (e.g. backfilled WhatsApp messages or a
// Discord thread), persist the turns via knowledge.Store before invoking
// RunAttached.
type Request struct {
	Message               string
	RunID                 string
	Title                 string
	SystemPrompt          string
	Model                 string
	ProviderName          string
	Out                   io.Writer
	Status                io.Writer
	Debug                 io.Writer
	Verbose               bool
	WorkerID              string
	ContextWindowOverride int
	EventSink             agentrun.EventSink
}

// Result reports the IDs assigned to the run/turn the request produced.
type Result struct {
	RunID  string
	TurnID string
}

// RunAttached executes the request synchronously through agentrun.Service.
// The caller's context controls cancellation; output is streamed to the
// writers in req.
func RunAttached(ctx context.Context, store *knowledge.Store, logger *slog.Logger, req Request) (Result, error) {
	service := agentrun.New(store, agentrun.Options{Logger: logger})
	runID, turnID, err := service.RunAttached(ctx, agentrun.Request{
		RunID:        req.RunID,
		Message:      req.Message,
		Title:        req.Title,
		SystemPrompt: req.SystemPrompt,
		Model:        req.Model,
		ProviderName: req.ProviderName,
	}, agentrun.ExecutionOptions{
		Out:                   req.Out,
		Status:                req.Status,
		Debug:                 req.Debug,
		Verbose:               req.Verbose,
		WorkerID:              req.WorkerID,
		ContextWindowOverride: req.ContextWindowOverride,
		EventSink:             req.EventSink,
	})
	return Result{RunID: runID, TurnID: turnID}, err
}
