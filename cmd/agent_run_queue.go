package cmd

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/jobs"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/riverqueue/river"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{Logger: newAgentRunLogger(logOut)})
	}
	newAgentRunInsertClient = queue.NewInsertClient
	newAgentRunWorkerClient = queue.NewWorkerClient
	agentRunQueuePollDelay  = 50 * time.Millisecond
	externalWorkerFreshness = 15 * time.Second
)

func newAgentRunLogger(out io.Writer) *slog.Logger {
	if out == nil {
		out = io.Discard
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

type queuedAgentRunPrinter struct {
	out            io.Writer
	status         io.Writer
	wroteContent   bool
	wroteReasoning bool
}

type planReadyTracker struct {
	payload *agentrun.PlanReadyPayload
}

func newQueuedAgentRunPrinter(out, status io.Writer) *queuedAgentRunPrinter {
	return &queuedAgentRunPrinter{out: out, status: status}
}

func (p *queuedAgentRunPrinter) RecordEvent(evt agentrun.Event) error {
	if p == nil || evt.Kind != agentrun.EventKindTraceChunk || evt.Chunk == nil {
		return nil
	}
	chunk := evt.Chunk
	if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" && p.status != nil {
		if _, err := io.WriteString(p.status, chunk.Error.Message); err != nil {
			return err
		}
		p.wroteReasoning = true
		return nil
	}
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
		return nil
	}
	delta := chunk.Choices[0].Delta
	if strings.TrimSpace(delta.Reasoning) != "" && p.status != nil {
		if _, err := io.WriteString(p.status, "\033[2m"+delta.Reasoning+"\033[0m"); err != nil {
			return err
		}
		p.wroteReasoning = true
	}
	if strings.TrimSpace(delta.Content) != "" && p.out != nil {
		if _, err := io.WriteString(p.out, delta.Content); err != nil {
			return err
		}
		p.wroteContent = true
	}
	return nil
}

func (t *planReadyTracker) RecordEvent(evt agentrun.Event) {
	if t == nil || evt.Kind != agentrun.EventKindPlanReady || evt.PlanReady == nil {
		return
	}
	payload := *evt.PlanReady
	t.payload = &payload
}

func (p *queuedAgentRunPrinter) Finish() {
	if p == nil {
		return
	}
	if p.wroteContent && p.out != nil {
		_, _ = io.WriteString(p.out, "\n")
	}
	if !p.wroteContent && p.wroteReasoning && p.status != nil {
		_, _ = io.WriteString(p.status, "\n")
	}
}

func runAttachedAgent(ctx context.Context, cmd *cobra.Command) error {
	ctx = withAgentSubagentProviderOverride(ctx, agentRunSubagentProvider)

	db, _, _, err := openWorkspaceDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	store := knowledge.New(db)
	serviceLogOut := io.Discard
	if agentRunTiming {
		serviceLogOut = cmd.ErrOrStderr()
	}
	service := newAgentRunService(store, serviceLogOut)
	requestStartedAt := time.Now().UTC()
	prepared, err := service.PrepareAttachedTurn(ctx, agentrun.Request{
		RunID:        strings.TrimSpace(agentRunResume),
		Message:      agentRunPrompt,
		ProviderName: agentRunProvider,
		SystemPrompt: strings.TrimSpace(agentRunSystem),
		Mode:         requestedAgentRunMode(cmd),
		Tools: agentpkg.ToolSelection{
			EnableToolIDs:  agentRunEnableTools,
			DisableToolIDs: agentRunDisableTools,
			PinToolIDs:     agentRunPinTools,
			UnpinToolIDs:   agentRunUnpinTools,
		},
	})
	if err != nil {
		return err
	}

	var timing *agentRunTimingTracker
	var timingSink agentrun.EventSink
	planReady := &planReadyTracker{}
	if agentRunTiming {
		timing = newAgentRunTimingTracker(cmd.ErrOrStderr(), requestStartedAt, prepared.Turn.CreatedAt)
		timingSink = agentrun.EventSinkFunc(func(_ context.Context, evt agentrun.Event) error {
			timing.RecordEvent(evt)
			return nil
		})
	}
	planReadySink := agentrun.EventSinkFunc(func(_ context.Context, evt agentrun.Event) error {
		planReady.RecordEvent(evt)
		return nil
	})

	err = service.ExecuteTurn(ctx, prepared.Turn.TurnID, agentrun.ExecutionOptions{
		Out:                   cmd.OutOrStdout(),
		Status:                cmd.ErrOrStderr(),
		Debug:                 cmd.ErrOrStderr(),
		Verbose:               agentRunVerbose,
		EventSink:             agentrun.MultiSink(timingSink, planReadySink),
		ContextWindowOverride: agentRunContextWindow,
	})
	if timing != nil {
		if turn, turnErr := store.GetAgentRunTurn(ctx, prepared.Turn.TurnID); turnErr == nil {
			timing.RecordTurn(turn)
		}
		timing.PrintSummary()
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nRun ID: %s\n", prepared.Run.ID)
	if err := maybeRunApprovedPlanAttached(ctx, cmd, service, planReady.payload); err != nil {
		return err
	}
	return nil
}

func runQueuedAgent(ctx context.Context, cmd *cobra.Command) error {
	ctx = withAgentSubagentProviderOverride(ctx, agentRunSubagentProvider)

	apiDB, paths, cfg, err := openWorkspaceDB(ctx)
	if err != nil {
		return err
	}
	defer apiDB.Close()

	cfg.Agent.Subagents.ProviderName = strings.TrimSpace(agentRunSubagentProvider)

	queueDB, err := database.Open(ctx, paths.DBPath)
	if err != nil {
		return err
	}
	defer queueDB.Close()

	store := knowledge.New(apiDB)
	serviceLogOut := io.Discard
	if agentRunTiming {
		serviceLogOut = cmd.ErrOrStderr()
	}
	service := newAgentRunService(store, serviceLogOut)
	requestStartedAt := time.Now().UTC()
	prepared, err := service.PrepareQueuedTurn(ctx, agentrun.Request{
		RunID:        strings.TrimSpace(agentRunResume),
		Message:      agentRunPrompt,
		ProviderName: agentRunProvider,
		SystemPrompt: strings.TrimSpace(agentRunSystem),
		Mode:         requestedAgentRunMode(cmd),
		Tools: agentpkg.ToolSelection{
			EnableToolIDs:  agentRunEnableTools,
			DisableToolIDs: agentRunDisableTools,
			PinToolIDs:     agentRunPinTools,
			UnpinToolIDs:   agentRunUnpinTools,
		},
	})
	if err != nil {
		return err
	}

	embeddedWorker, err := shouldUseEmbeddedQueueWorker(ctx, queueDB)
	if err != nil {
		return err
	}

	var (
		insertClient *river.Client[*sql.Tx]
		stopEmbedded func()
	)
	if embeddedWorker {
		workerClient, stopFn, err := startEmbeddedQueueWorker(ctx, queueDB, store, service, cfg)
		if err != nil {
			return err
		}
		insertClient = workerClient
		stopEmbedded = stopFn
	} else {
		client, err := newAgentRunInsertClient(ctx, queueDB)
		if err != nil {
			return err
		}
		insertClient = client
	}
	if stopEmbedded != nil {
		defer stopEmbedded()
	}

	jobID, _, err := queue.EnqueueAgentRun(ctx, insertClient, prepared.Turn.TurnID)
	if err != nil {
		return err
	}
	enqueuedAt := time.Now().UTC()

	run, turn, err := service.MarkTurnQueued(ctx, prepared, jobID)
	if err != nil {
		return err
	}
	queuedAt := time.Now().UTC()

	var timing *agentRunTimingTracker
	if agentRunTiming {
		timing = newAgentRunTimingTracker(cmd.ErrOrStderr(), requestStartedAt, prepared.Turn.CreatedAt)
		timing.RecordQueued(enqueuedAt, queuedAt)
	}
	printer := newQueuedAgentRunPrinter(cmd.OutOrStdout(), cmd.ErrOrStderr())
	planReady := &planReadyTracker{}
	waitErr := waitForQueuedAgentRun(ctx, store, service, run.ID, timing, printer, planReady)
	printer.Finish()
	if timing != nil {
		if latestTurn, turnErr := store.GetAgentRunTurn(ctx, turn.TurnID); turnErr == nil {
			timing.RecordTurn(latestTurn)
		}
		timing.PrintSummary()
	}
	if waitErr != nil {
		return waitErr
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nRun ID: %s\n", run.ID)
	if err := maybeRunApprovedPlanQueued(ctx, cmd, store, service, planReady.payload); err != nil {
		return err
	}
	return nil
}

func waitForQueuedAgentRun(ctx context.Context, store *knowledge.Store, service *agentrun.Service, runID string, timing *agentRunTimingTracker, printer *queuedAgentRunPrinter, planReady *planReadyTracker) error {
	var afterEventID int64
	ticker := time.NewTicker(agentRunQueuePollDelay)
	defer ticker.Stop()

	for {
		if err := replayQueuedAgentRunEvents(ctx, store, runID, &afterEventID, timing, printer, planReady); err != nil {
			return err
		}
		run, err := store.GetAgentRun(ctx, runID)
		if err != nil {
			return err
		}
		switch run.Status {
		case "completed":
			return nil
		case "failed", "aborted":
			if strings.TrimSpace(run.Error) != "" {
				return errors.New(run.Error)
			}
			return fmt.Errorf("run %s", run.Status)
		}

		select {
		case <-ctx.Done():
			abortCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = service.AbortRun(abortCtx, runID)
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func replayQueuedAgentRunEvents(ctx context.Context, store *knowledge.Store, runID string, afterEventID *int64, timing *agentRunTimingTracker, printer *queuedAgentRunPrinter, planReady *planReadyTracker) error {
	events, err := store.ListAgentRunEventsAfter(ctx, runID, *afterEventID, 200)
	if err != nil {
		return err
	}
	for _, stored := range events {
		*afterEventID = stored.EventID
		evt, err := decodeStoredAgentRunEvent(stored)
		if err != nil {
			return err
		}
		if timing != nil {
			timing.RecordEvent(evt)
		}
		if planReady != nil {
			planReady.RecordEvent(evt)
		}
		if err := printer.RecordEvent(evt); err != nil {
			return err
		}
	}
	return nil
}

func maybeRunApprovedPlanAttached(ctx context.Context, cmd *cobra.Command, service *agentrun.Service, payload *agentrun.PlanReadyPayload) error {
	if payload == nil {
		return nil
	}
	confirmed, err := confirmImplementationHandoff(cmd, payload.PlanPath)
	if err != nil || !confirmed {
		return err
	}

	prepared, err := service.PrepareAttachedImplementationTurn(ctx, payload.SourceRunID, agentrun.Request{
		Title:        "",
		Model:        "",
		SystemPrompt: strings.TrimSpace(agentRunSystem),
		ProviderName: strings.TrimSpace(agentRunProvider),
		Tools: agentpkg.ToolSelection{
			EnableToolIDs:  agentRunEnableTools,
			DisableToolIDs: agentRunDisableTools,
			PinToolIDs:     agentRunPinTools,
			UnpinToolIDs:   agentRunUnpinTools,
		},
	})
	if err != nil {
		return err
	}
	if err := service.ExecuteTurn(ctx, prepared.Turn.TurnID, agentrun.ExecutionOptions{
		Out:                   cmd.OutOrStdout(),
		Status:                cmd.ErrOrStderr(),
		Debug:                 cmd.ErrOrStderr(),
		Verbose:               agentRunVerbose,
		ContextWindowOverride: agentRunContextWindow,
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Implementation Run ID: %s\n", prepared.Run.ID)
	return nil
}

func maybeRunApprovedPlanQueued(ctx context.Context, cmd *cobra.Command, store *knowledge.Store, service *agentrun.Service, payload *agentrun.PlanReadyPayload) error {
	if payload == nil {
		return nil
	}
	confirmed, err := confirmImplementationHandoff(cmd, payload.PlanPath)
	if err != nil || !confirmed {
		return err
	}

	prepared, err := service.PrepareQueuedImplementationTurn(ctx, payload.SourceRunID, agentrun.Request{
		SystemPrompt: strings.TrimSpace(agentRunSystem),
		ProviderName: strings.TrimSpace(agentRunProvider),
		Tools: agentpkg.ToolSelection{
			EnableToolIDs:  agentRunEnableTools,
			DisableToolIDs: agentRunDisableTools,
			PinToolIDs:     agentRunPinTools,
			UnpinToolIDs:   agentRunUnpinTools,
		},
	})
	if err != nil {
		return err
	}

	_, paths, cfg, err := openWorkspaceDB(ctx)
	if err != nil {
		return err
	}
	queueDB, err := database.Open(ctx, paths.DBPath)
	if err != nil {
		return err
	}
	defer queueDB.Close()

	embeddedWorker, err := shouldUseEmbeddedQueueWorker(ctx, queueDB)
	if err != nil {
		return err
	}

	var (
		insertClient *river.Client[*sql.Tx]
		stopEmbedded func()
	)
	if embeddedWorker {
		workerClient, stopFn, err := startEmbeddedQueueWorker(ctx, queueDB, store, service, cfg)
		if err != nil {
			return err
		}
		insertClient = workerClient
		stopEmbedded = stopFn
	} else {
		client, err := newAgentRunInsertClient(ctx, queueDB)
		if err != nil {
			return err
		}
		insertClient = client
	}
	if stopEmbedded != nil {
		defer stopEmbedded()
	}

	jobID, _, err := queue.EnqueueAgentRun(ctx, insertClient, prepared.Turn.TurnID)
	if err != nil {
		return err
	}
	run, _, err := service.MarkTurnQueued(ctx, prepared, jobID)
	if err != nil {
		return err
	}
	printer := newQueuedAgentRunPrinter(cmd.OutOrStdout(), cmd.ErrOrStderr())
	waitErr := waitForQueuedAgentRun(ctx, store, service, run.ID, nil, printer, nil)
	printer.Finish()
	if waitErr != nil {
		return waitErr
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Implementation Run ID: %s\n", run.ID)
	return nil
}

func confirmImplementationHandoff(cmd *cobra.Command, planPath string) (bool, error) {
	in := cmd.InOrStdin()
	file, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		if strings.TrimSpace(planPath) != "" {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Plan ready at %s. Start the implementation run manually when you're ready.\n", planPath)
		}
		return false, nil
	}

	prompt := "Start a fresh implementation run"
	if strings.TrimSpace(planPath) != "" {
		prompt += fmt.Sprintf(" using %s", planPath)
	}
	prompt += "? [y/N] "
	if _, err := fmt.Fprint(cmd.ErrOrStderr(), prompt); err != nil {
		return false, err
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes", nil
}

func decodeStoredAgentRunEvent(stored knowledge.AgentRunEvent) (agentrun.Event, error) {
	evt := agentrun.Event{
		EventID:   stored.EventID,
		CreatedAt: stored.CreatedAt,
		Kind:      stored.Kind,
	}
	switch stored.Kind {
	case agentrun.EventKindQueued:
		var payload agentrun.QueuedPayload
		if err := json.Unmarshal(stored.Payload, &payload); err != nil {
			return evt, fmt.Errorf("decode queued event %d: %w", stored.EventID, err)
		}
		evt.Queued = &payload
	case agentrun.EventKindStarted:
		var payload agentrun.StartedPayload
		if err := json.Unmarshal(stored.Payload, &payload); err != nil {
			return evt, fmt.Errorf("decode started event %d: %w", stored.EventID, err)
		}
		evt.Started = &payload
	case agentrun.EventKindTraceChunk:
		var payload agentpkg.TraceChunk
		if err := json.Unmarshal(stored.Payload, &payload); err != nil {
			return evt, fmt.Errorf("decode trace chunk event %d: %w", stored.EventID, err)
		}
		evt.Chunk = &payload
	case agentrun.EventKindToolResult:
		var payload agentpkg.ToolResultEvent
		if err := json.Unmarshal(stored.Payload, &payload); err != nil {
			return evt, fmt.Errorf("decode tool result event %d: %w", stored.EventID, err)
		}
		evt.ToolResult = &payload
	case agentrun.EventKindPlanReady:
		var payload agentrun.PlanReadyPayload
		if err := json.Unmarshal(stored.Payload, &payload); err != nil {
			return evt, fmt.Errorf("decode plan ready event %d: %w", stored.EventID, err)
		}
		evt.PlanReady = &payload
	case agentrun.EventKindStatus:
		var payload agentrun.StatusPayload
		if err := json.Unmarshal(stored.Payload, &payload); err != nil {
			return evt, fmt.Errorf("decode status event %d: %w", stored.EventID, err)
		}
		evt.Status = &payload
	}
	return evt, nil
}

func shouldUseEmbeddedQueueWorker(ctx context.Context, queueDB *sql.DB) (bool, error) {
	active, err := hasActiveQueueWorker(ctx, queueDB)
	if err != nil {
		return false, err
	}
	return !active, nil
}

func hasActiveQueueWorker(ctx context.Context, queueDB *sql.DB) (bool, error) {
	if queueDB == nil {
		return false, fmt.Errorf("queue database is required")
	}
	var updatedAt sql.NullTime
	if err := queueDB.QueryRowContext(ctx, `
		SELECT updated_at
		FROM river_queue
		WHERE name = ? AND paused_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 1`, jobs.QueueAgent).Scan(&updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query active agent queue: %w", err)
	}
	if !updatedAt.Valid {
		return false, nil
	}
	return time.Since(updatedAt.Time) <= externalWorkerFreshness, nil
}

func startEmbeddedQueueWorker(ctx context.Context, queueDB *sql.DB, store *knowledge.Store, service *agentrun.Service, cfg config.Config) (*river.Client[*sql.Tx], func(), error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workerClient, err := newAgentRunWorkerClient(ctx, queue.Options{
		DB:               queueDB,
		Store:            store,
		HelloWorkers:     -1,
		SchedulerWorkers: -1,
		AgentWorkers:     max(cfg.Work.AgentWorkers, 1),
		AgentJobTimeout:  cfg.Work.AgentJobTimeout,
		ScheduleEnabled:  false,
		Logger:           logger,
		ClientID:         "agent_run_queue_" + workerClientID(),
		AgentService:     service,
		Config:           cfg,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := workerClient.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start embedded queue worker: %w", err)
	}
	stop := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = workerClient.Stop(stopCtx)
	}
	return workerClient, stop, nil
}
