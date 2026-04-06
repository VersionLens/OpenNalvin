package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"

	"github.com/google/uuid"
)

const (
	EventKindQueued     = "queued"
	EventKindStarted    = "started"
	EventKindTraceChunk = "trace_chunk"
	EventKindToolResult = "tool_result"
	EventKindPlanReady  = "plan_ready"
	EventKindStatus     = "status"
)

var (
	ErrRunBusy = errors.New("agent run already has an active turn")
)

type ProviderLoader func(name string) (agentpkg.ProviderConfig, error)
type Executor func(context.Context, *knowledge.Store, agentpkg.RunRequest, agentpkg.RunOptions) (string, error)

type Options struct {
	Logger         *slog.Logger
	EventBus       *EventBus
	ProviderLoader ProviderLoader
	Executor       Executor
}

type Request struct {
	RunID           string
	ClientRequestID string
	Message         string
	Title           string
	SystemPrompt    string
	Model           string
	ProviderName    string
	ParentRunID     string
	RootRunID       string
	TaskName        string
	RunKind         string
	Mode            string
	PlanRef         agentpkg.StoredPlanRef
	Tools           agentpkg.ToolSelection
}

type ExecutionOptions struct {
	Out                   io.Writer
	Status                io.Writer
	Debug                 io.Writer
	Verbose               bool
	WorkerID              string
	ContextWindowOverride int
	EventSink             EventSink
}

type PreparedTurn struct {
	Run          *knowledge.AgentRun
	Turn         *knowledge.AgentRunTurn
	ExistingTurn bool
}

type Event struct {
	EventID    int64
	CreatedAt  time.Time
	Kind       string
	Queued     *QueuedPayload
	Started    *StartedPayload
	Chunk      *agentpkg.TraceChunk
	ToolResult *agentpkg.ToolResultEvent
	PlanReady  *PlanReadyPayload
	Status     *StatusPayload
}

type QueuedPayload struct {
	Status     string `json:"status"`
	QueueJobID int64  `json:"queue_job_id"`
}

type StartedPayload struct {
	Status   string `json:"status"`
	WorkerID string `json:"worker_id,omitempty"`
}

type PlanReadyPayload struct {
	PlanPath    string `json:"plan_path"`
	SourceRunID string `json:"source_run_id,omitempty"`
	Status      string `json:"status,omitempty"`
}

type StatusPayload struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type EventSink interface {
	HandleEvent(context.Context, Event) error
}

type EventSinkFunc func(context.Context, Event) error

func (fn EventSinkFunc) HandleEvent(ctx context.Context, evt Event) error {
	return fn(ctx, evt)
}

type multiSink struct {
	sinks []EventSink
}

func (s multiSink) HandleEvent(ctx context.Context, evt Event) error {
	for _, sink := range s.sinks {
		if sink == nil {
			continue
		}
		if err := sink.HandleEvent(ctx, evt); err != nil {
			return err
		}
	}
	return nil
}

func MultiSink(sinks ...EventSink) EventSink {
	return multiSink{sinks: sinks}
}

type Service struct {
	store        *knowledge.Store
	logger       *slog.Logger
	eventBus     *EventBus
	loadProvider ProviderLoader
	executor     Executor
}

func New(store *knowledge.Store, opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	loadProvider := opts.ProviderLoader
	if loadProvider == nil {
		loadProvider = agentpkg.LoadProviderConfig
	}
	executor := opts.Executor
	if executor == nil {
		executor = agentpkg.Run
	}
	eventBus := opts.EventBus
	if eventBus == nil {
		eventBus = DefaultEventBus()
	}
	return &Service{
		store:        store,
		logger:       logger,
		eventBus:     eventBus,
		loadProvider: loadProvider,
		executor:     executor,
	}
}

func (s *Service) Subscribe(ctx context.Context, runID string) <-chan knowledge.AgentRunEvent {
	if s == nil || s.eventBus == nil {
		ch := make(chan knowledge.AgentRunEvent)
		close(ch)
		return ch
	}
	return s.eventBus.Subscribe(ctx, runID)
}

func (s *Service) PrepareQueuedTurn(ctx context.Context, req Request) (*PreparedTurn, error) {
	return s.prepareTurn(ctx, req, "queued")
}

func (s *Service) PrepareAttachedTurn(ctx context.Context, req Request) (*PreparedTurn, error) {
	return s.prepareTurn(ctx, req, "attached")
}

func normalizeProviderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	return name
}

func effectiveSubagentProviderName(ctx context.Context) string {
	cfg, ok := configpkg.FromContext(ctx)
	if !ok {
		return ""
	}
	return strings.TrimSpace(cfg.Agent.Subagents.ProviderName)
}

func withTurnSubagentProvider(ctx context.Context, providerName string) context.Context {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return ctx
	}
	cfg, ok := configpkg.FromContext(ctx)
	if !ok {
		return ctx
	}
	cfg.Agent.Subagents.ProviderName = providerName
	return configpkg.WithContext(ctx, cfg)
}

func (s *Service) RunAttached(ctx context.Context, req Request, opts ExecutionOptions) (string, string, error) {
	prepared, err := s.PrepareAttachedTurn(ctx, req)
	if err != nil {
		return "", "", err
	}
	if err := s.ExecuteTurn(ctx, prepared.Turn.TurnID, opts); err != nil {
		return prepared.Run.ID, prepared.Turn.TurnID, err
	}
	return prepared.Run.ID, prepared.Turn.TurnID, nil
}

func (s *Service) MarkTurnQueued(ctx context.Context, prepared *PreparedTurn, queueJobID int64) (*knowledge.AgentRun, *knowledge.AgentRunTurn, error) {
	if prepared == nil || prepared.Run == nil || prepared.Turn == nil {
		return nil, nil, fmt.Errorf("prepared turn is required")
	}
	if prepared.ExistingTurn && prepared.Turn.QueueJobID > 0 {
		return prepared.Run, prepared.Turn, nil
	}

	turn := prepared.Turn
	if err := s.store.UpdateAgentRunTurn(ctx, knowledge.UpdateAgentRunTurnInput{
		TurnID:               turn.TurnID,
		Title:                turn.Title,
		SystemPrompt:         turn.SystemPrompt,
		ProviderName:         turn.ProviderName,
		SubagentProviderName: turn.SubagentProviderName,
		EnabledToolIDs:       turn.EnabledToolIDs,
		PinnedToolIDs:        turn.PinnedToolIDs,
		EnableToolIDs:        turn.EnableToolIDs,
		DisableToolIDs:       turn.DisableToolIDs,
		PinToolIDs:           turn.PinToolIDs,
		UnpinToolIDs:         turn.UnpinToolIDs,
		Status:               "queued",
		QueueJobID:           queueJobID,
		Error:                "",
		StartedAt:            turn.StartedAt,
		FinishedAt:           turn.FinishedAt,
	}); err != nil {
		return nil, nil, err
	}
	run := prepared.Run
	if err := s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                run.ID,
		Title:             run.Title,
		Model:             run.Model,
		Provider:          run.Provider,
		ParentRunID:       run.ParentRunID,
		RootRunID:         run.RootRunID,
		TaskName:          run.TaskName,
		RunKind:           run.RunKind,
		Status:            "queued",
		Error:             "",
		DurationMs:        run.DurationMs,
		MessageCount:      run.MessageCount,
		InputTokens:       run.InputTokens,
		OutputTokens:      run.OutputTokens,
		LastEventID:       run.LastEventID,
		ActiveTurnID:      turn.TurnID,
		QueueJobID:        queueJobID,
		WorkerID:          "",
		CancelRequestedAt: nil,
		Trace:             run.Trace,
	}); err != nil {
		return nil, nil, err
	}

	updatedRun, err := s.store.GetAgentRun(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	updatedTurn, err := s.store.GetAgentRunTurn(ctx, turn.TurnID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.emit(ctx, updatedRun.ID, updatedTurn.TurnID, Event{
		Kind: EventKindQueued,
		Queued: &QueuedPayload{
			Status:     updatedRun.Status,
			QueueJobID: queueJobID,
		},
	}, nil); err != nil {
		return nil, nil, err
	}
	updatedRun, err = s.store.GetAgentRun(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return updatedRun, updatedTurn, nil
}

func (s *Service) AbortRun(ctx context.Context, runID string) (*knowledge.AgentRun, error) {
	run, err := s.store.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	switch run.Status {
	case "completed", "failed", "aborted":
		return run, nil
	}
	if err := s.store.RequestAgentRunAbort(ctx, run.ID); err != nil {
		return nil, err
	}
	run, err = s.store.GetAgentRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if run.Status != "queued" {
		return run, nil
	}
	if strings.TrimSpace(run.ActiveTurnID) == "" {
		if err := s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
			ID:                run.ID,
			Title:             run.Title,
			Model:             run.Model,
			Provider:          run.Provider,
			ParentRunID:       run.ParentRunID,
			RootRunID:         run.RootRunID,
			TaskName:          run.TaskName,
			RunKind:           run.RunKind,
			Status:            "aborted",
			Error:             "run aborted",
			DurationMs:        run.DurationMs,
			MessageCount:      run.MessageCount,
			InputTokens:       run.InputTokens,
			OutputTokens:      run.OutputTokens,
			LastEventID:       run.LastEventID,
			ActiveTurnID:      "",
			QueueJobID:        run.QueueJobID,
			WorkerID:          "",
			CancelRequestedAt: nil,
			Trace:             run.Trace,
		}); err != nil {
			return nil, err
		}
		return s.store.GetAgentRun(ctx, run.ID)
	}
	turn, err := s.store.GetAgentRunTurn(ctx, run.ActiveTurnID)
	if err != nil {
		return nil, err
	}
	if err := s.abortTurn(ctx, run, turn, "aborted", "run aborted"); err != nil {
		return nil, err
	}
	return s.store.GetAgentRun(ctx, run.ID)
}

func (s *Service) ExecuteTurn(ctx context.Context, turnID string, opts ExecutionOptions) error {
	turn, err := s.store.GetAgentRunTurn(ctx, turnID)
	if err != nil {
		return err
	}
	run, err := s.store.GetAgentRun(ctx, turn.RunID)
	if err != nil {
		return err
	}
	switch turn.Status {
	case "completed", "aborted":
		return nil
	}
	if run.CancelRequestedAt != nil {
		return s.abortTurn(ctx, run, turn, "aborted", "run aborted before execution")
	}

	startedAt := turn.StartedAt
	if startedAt == nil {
		now := time.Now().UTC()
		startedAt = &now
	}
	if err := s.store.UpdateAgentRunTurn(ctx, knowledge.UpdateAgentRunTurnInput{
		TurnID:               turn.TurnID,
		Title:                turn.Title,
		SystemPrompt:         turn.SystemPrompt,
		ProviderName:         turn.ProviderName,
		SubagentProviderName: turn.SubagentProviderName,
		EnabledToolIDs:       turn.EnabledToolIDs,
		PinnedToolIDs:        turn.PinnedToolIDs,
		EnableToolIDs:        turn.EnableToolIDs,
		DisableToolIDs:       turn.DisableToolIDs,
		PinToolIDs:           turn.PinToolIDs,
		UnpinToolIDs:         turn.UnpinToolIDs,
		Status:               "running",
		QueueJobID:           turn.QueueJobID,
		Error:                "",
		StartedAt:            startedAt,
		FinishedAt:           nil,
	}); err != nil {
		return err
	}
	turn.StartedAt = startedAt
	if err := s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                run.ID,
		Title:             run.Title,
		Model:             run.Model,
		Provider:          run.Provider,
		ParentRunID:       run.ParentRunID,
		RootRunID:         run.RootRunID,
		TaskName:          run.TaskName,
		RunKind:           run.RunKind,
		Status:            "running",
		Error:             "",
		DurationMs:        run.DurationMs,
		MessageCount:      run.MessageCount,
		InputTokens:       run.InputTokens,
		OutputTokens:      run.OutputTokens,
		LastEventID:       run.LastEventID,
		ActiveTurnID:      turn.TurnID,
		QueueJobID:        turn.QueueJobID,
		WorkerID:          strings.TrimSpace(opts.WorkerID),
		CancelRequestedAt: run.CancelRequestedAt,
		Trace:             run.Trace,
	}); err != nil {
		return err
	}
	run, err = s.store.GetAgentRun(ctx, run.ID)
	if err != nil {
		return err
	}
	startAttrs := []any{
		"run_id", run.ID,
		"turn_id", turn.TurnID,
		"worker_id", strings.TrimSpace(opts.WorkerID),
	}
	if !turn.CreatedAt.IsZero() && startedAt != nil {
		startAttrs = append(startAttrs, "queue_wait_ms", startedAt.Sub(turn.CreatedAt).Milliseconds())
	}
	s.logger.Info("agent run start timing", startAttrs...)
	if err := s.emit(ctx, run.ID, turn.TurnID, Event{
		Kind: EventKindStarted,
		Started: &StartedPayload{
			Status:   run.Status,
			WorkerID: strings.TrimSpace(opts.WorkerID),
		},
	}, opts.EventSink); err != nil {
		return err
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopAbortWatcher := s.watchForAbort(execCtx, cancel, run.ID)
	defer stopAbortWatcher()

	err = s.executePreparedTurn(execCtx, run, turn, opts)
	run, loadErr := s.store.GetAgentRun(context.Background(), run.ID)
	if loadErr != nil {
		return loadErr
	}
	if err != nil && (run.Status == "queued" || run.Status == "running") {
		finalStatus := "failed"
		if execCtx.Err() != nil {
			finalStatus = "aborted"
		}
		if abortErr := s.finalizeRun(context.Background(), run, turn, finalStatus, err.Error(), strings.TrimSpace(opts.WorkerID)); abortErr != nil {
			return errors.Join(err, abortErr)
		}
		run, loadErr = s.store.GetAgentRun(context.Background(), run.ID)
		if loadErr != nil {
			return errors.Join(err, loadErr)
		}
	}
	if err := s.finalizeTurn(context.Background(), run, turn, strings.TrimSpace(opts.WorkerID)); err != nil {
		return err
	}
	run, err = s.store.GetAgentRun(context.Background(), run.ID)
	if err != nil {
		return err
	}
	if emitErr := s.emit(context.Background(), run.ID, turn.TurnID, Event{
		Kind: EventKindStatus,
		Status: &StatusPayload{
			Status: run.Status,
			Error:  run.Error,
		},
	}, opts.EventSink); emitErr != nil {
		return emitErr
	}
	return err
}

func (s *Service) prepareTurn(ctx context.Context, req Request, mode string) (*PreparedTurn, error) {
	if strings.TrimSpace(req.Message) == "" {
		return nil, fmt.Errorf("message is required")
	}
	requestedMode := agentpkg.NormalizeRunMode(req.Mode)
	requestedPlanRef := agentpkg.NormalizePlanRef(req.PlanRef)
	runID := strings.TrimSpace(req.RunID)
	clientRequestID := strings.TrimSpace(req.ClientRequestID)
	if clientRequestID == "" {
		clientRequestID = uuid.NewString()
	}
	if runID == "" && mode == "queued" {
		runID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(clientRequestID)).String()
	}
	if runID == "" {
		runID = uuid.NewString()
	}

	run, err := s.store.GetAgentRun(ctx, runID)
	if errors.Is(err, knowledge.ErrNotFound) {
		run = nil
	} else if err != nil {
		return nil, err
	}
	var existingTrace agentpkg.StoredTrace
	if run != nil {
		existingTrace, err = agentpkg.ParseStoredTrace(run.Trace)
		if err != nil {
			return nil, err
		}
		existingMode := agentpkg.NormalizeRunMode(existingTrace.Metadata.Mode)
		if strings.TrimSpace(req.Mode) != "" && requestedMode != existingMode {
			return nil, fmt.Errorf("run mode is immutable for existing runs")
		}
		requestedMode = existingMode
		if planRefProvided(req.PlanRef) && agentpkg.NormalizePlanRef(req.PlanRef) != agentpkg.NormalizePlanRef(existingTrace.Metadata.PlanRef) {
			return nil, fmt.Errorf("plan_ref is immutable for existing runs")
		}
		requestedPlanRef = agentpkg.NormalizePlanRef(existingTrace.Metadata.PlanRef)
		existingTurn, err := s.store.GetAgentRunTurnByClientRequestID(ctx, run.ID, clientRequestID)
		if err == nil {
			return &PreparedTurn{Run: run, Turn: existingTurn, ExistingTurn: true}, nil
		}
		if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
			return nil, err
		}
		if run.Status == "queued" || run.Status == "running" || strings.TrimSpace(run.ActiveTurnID) != "" {
			return nil, ErrRunBusy
		}
	}

	resolvedProvider, resolvedModel, err := s.resolveProviderAndModel(req.ProviderName, req.Model, run)
	if err != nil {
		return nil, err
	}
	title := deriveTitle(req.Title, req.Message, run)
	if run == nil {
		if requestedMode == agentpkg.RunModePlan && requestedPlanRef.Path == "" {
			requestedPlanRef, err = preparePlanArtifact(ctx, runID, title, req.Message)
			if err != nil {
				return nil, err
			}
		}
		runTrace, err := marshalPlaceholderTrace(
			runID,
			title,
			resolvedModel,
			resolvedProvider,
			strings.TrimSpace(req.SystemPrompt),
			strings.TrimSpace(req.ParentRunID),
			strings.TrimSpace(req.RootRunID),
			strings.TrimSpace(req.TaskName),
			strings.TrimSpace(req.RunKind),
			requestedMode,
			requestedPlanRef,
		)
		if err != nil {
			return nil, err
		}
		status := "queued"
		if mode == "attached" {
			status = "running"
		}
		if err := s.store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
			ID:                runID,
			Title:             title,
			Model:             resolvedModel,
			Provider:          resolvedProvider,
			Prompt:            req.Message,
			ParentRunID:       strings.TrimSpace(req.ParentRunID),
			RootRunID:         resolveRootRunID(strings.TrimSpace(req.RootRunID), runID),
			TaskName:          strings.TrimSpace(req.TaskName),
			RunKind:           resolveRunKind(strings.TrimSpace(req.ParentRunID), strings.TrimSpace(req.RunKind)),
			Status:            status,
			Trace:             runTrace,
			LastEventID:       0,
			ActiveTurnID:      "",
			QueueJobID:        0,
			WorkerID:          "",
			CancelRequestedAt: nil,
		}); err != nil {
			return nil, err
		}
		run, err = s.store.GetAgentRun(ctx, runID)
		if err != nil {
			return nil, err
		}
	}

	turnStatus := "queued"
	if mode == "attached" {
		turnStatus = "running"
	}
	turn, err := s.store.CreateAgentRunTurn(ctx, knowledge.CreateAgentRunTurnInput{
		RunID:                run.ID,
		ClientRequestID:      clientRequestID,
		Message:              req.Message,
		Title:                strings.TrimSpace(req.Title),
		SystemPrompt:         strings.TrimSpace(req.SystemPrompt),
		ProviderName:         resolvedProvider,
		SubagentProviderName: effectiveSubagentProviderName(ctx),
		EnabledToolIDs:       append([]string(nil), req.Tools.EnabledToolIDs...),
		PinnedToolIDs:        append([]string(nil), req.Tools.PinnedToolIDs...),
		EnableToolIDs:        append([]string(nil), req.Tools.EnableToolIDs...),
		DisableToolIDs:       append([]string(nil), req.Tools.DisableToolIDs...),
		PinToolIDs:           append([]string(nil), req.Tools.PinToolIDs...),
		UnpinToolIDs:         append([]string(nil), req.Tools.UnpinToolIDs...),
		Status:               turnStatus,
	})
	if err != nil {
		return nil, err
	}

	if err := s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                run.ID,
		Title:             title,
		Model:             resolvedModel,
		Provider:          resolvedProvider,
		ParentRunID:       run.ParentRunID,
		RootRunID:         run.RootRunID,
		TaskName:          run.TaskName,
		RunKind:           run.RunKind,
		Status:            map[string]string{"attached": "running", "queued": "queued"}[mode],
		Error:             "",
		DurationMs:        run.DurationMs,
		MessageCount:      run.MessageCount,
		InputTokens:       run.InputTokens,
		OutputTokens:      run.OutputTokens,
		LastEventID:       run.LastEventID,
		ActiveTurnID:      turn.TurnID,
		QueueJobID:        0,
		WorkerID:          "",
		CancelRequestedAt: nil,
		Trace:             run.Trace,
	}); err != nil {
		return nil, err
	}
	run, err = s.store.GetAgentRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	return &PreparedTurn{Run: run, Turn: turn}, nil
}

func (s *Service) executePreparedTurn(ctx context.Context, run *knowledge.AgentRun, turn *knowledge.AgentRunTurn, opts ExecutionOptions) error {
	ctx = withTurnSubagentProvider(ctx, turn.SubagentProviderName)
	runTrace, err := agentpkg.ParseStoredTrace(run.Trace)
	if err != nil {
		return err
	}
	eventSink := opts.EventSink
	firstOutputLogged := false
	firstContentLogged := false
	_, returnErr := s.executor(ctx, s.store, agentpkg.RunRequest{
		RunID:        run.ID,
		Message:      turn.Message,
		Title:        turn.Title,
		SystemPrompt: turn.SystemPrompt,
		ProviderName: turn.ProviderName,
		Mode:         runTrace.Metadata.Mode,
		PlanRef:      runTrace.Metadata.PlanRef,
		Tools: agentpkg.ToolSelection{
			EnabledToolIDs: append([]string(nil), turn.EnabledToolIDs...),
			PinnedToolIDs:  append([]string(nil), turn.PinnedToolIDs...),
			EnableToolIDs:  append([]string(nil), turn.EnableToolIDs...),
			DisableToolIDs: append([]string(nil), turn.DisableToolIDs...),
			PinToolIDs:     append([]string(nil), turn.PinToolIDs...),
			UnpinToolIDs:   append([]string(nil), turn.UnpinToolIDs...),
		},
	}, agentpkg.RunOptions{
		Out:                   opts.Out,
		Status:                opts.Status,
		Debug:                 opts.Debug,
		Verbose:               opts.Verbose,
		ContextWindowOverride: opts.ContextWindowOverride,
		OnChunk: func(chunk agentpkg.TraceChunk) error {
			if !firstOutputLogged {
				if kind := firstMeaningfulChunkKind(chunk); kind != "" {
					firstOutputLogged = true
					now := time.Now().UTC()
					attrs := []any{
						"run_id", run.ID,
						"turn_id", turn.TurnID,
						"worker_id", strings.TrimSpace(opts.WorkerID),
						"kind", kind,
					}
					if turn.StartedAt != nil {
						attrs = append(attrs, "started_to_first_output_ms", now.Sub(*turn.StartedAt).Milliseconds())
					}
					if !turn.CreatedAt.IsZero() {
						attrs = append(attrs, "ttft_ms", now.Sub(turn.CreatedAt).Milliseconds())
					}
					s.logger.Info("agent run first output timing", attrs...)
				}
			}
			if !firstContentLogged && chunkHasContentDelta(chunk) {
				firstContentLogged = true
				now := time.Now().UTC()
				attrs := []any{
					"run_id", run.ID,
					"turn_id", turn.TurnID,
					"worker_id", strings.TrimSpace(opts.WorkerID),
				}
				if turn.StartedAt != nil {
					attrs = append(attrs, "started_to_first_content_ms", now.Sub(*turn.StartedAt).Milliseconds())
				}
				if !turn.CreatedAt.IsZero() {
					attrs = append(attrs, "content_ttft_ms", now.Sub(turn.CreatedAt).Milliseconds())
				}
				s.logger.Info("agent run first content timing", attrs...)
			}
			return s.emit(ctx, run.ID, turn.TurnID, Event{
				Kind:  EventKindTraceChunk,
				Chunk: &chunk,
			}, eventSink)
		},
		OnToolResult: func(result agentpkg.ToolResultEvent) error {
			if err := s.emit(ctx, run.ID, turn.TurnID, Event{
				Kind:       EventKindToolResult,
				ToolResult: &result,
			}, eventSink); err != nil {
				return err
			}
			if result.PlanRef != nil && strings.TrimSpace(result.PlanRef.Path) != "" && result.PlanRef.Status == agentpkg.PlanStatusReady {
				return s.emit(ctx, run.ID, turn.TurnID, Event{
					Kind: EventKindPlanReady,
					PlanReady: &PlanReadyPayload{
						PlanPath:    result.PlanRef.Path,
						SourceRunID: firstNonEmpty(result.PlanRef.SourceRunID, run.ID),
						Status:      result.PlanRef.Status,
					},
				}, eventSink)
			}
			return nil
		},
	})
	return returnErr
}

func (s *Service) finalizeRun(ctx context.Context, run *knowledge.AgentRun, turn *knowledge.AgentRunTurn, status, errText, workerID string) error {
	return s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                run.ID,
		Title:             run.Title,
		Model:             run.Model,
		Provider:          run.Provider,
		ParentRunID:       run.ParentRunID,
		RootRunID:         run.RootRunID,
		TaskName:          run.TaskName,
		RunKind:           run.RunKind,
		Status:            status,
		Error:             strings.TrimSpace(errText),
		DurationMs:        run.DurationMs,
		MessageCount:      run.MessageCount,
		InputTokens:       run.InputTokens,
		OutputTokens:      run.OutputTokens,
		LastEventID:       run.LastEventID,
		ActiveTurnID:      "",
		QueueJobID:        turn.QueueJobID,
		WorkerID:          workerID,
		CancelRequestedAt: nil,
		Trace:             run.Trace,
	})
}

func (s *Service) finalizeTurn(ctx context.Context, run *knowledge.AgentRun, turn *knowledge.AgentRunTurn, workerID string) error {
	finishedAt := time.Now().UTC()
	if err := s.store.UpdateAgentRunTurn(ctx, knowledge.UpdateAgentRunTurnInput{
		TurnID:               turn.TurnID,
		Title:                turn.Title,
		SystemPrompt:         turn.SystemPrompt,
		ProviderName:         turn.ProviderName,
		SubagentProviderName: turn.SubagentProviderName,
		EnabledToolIDs:       turn.EnabledToolIDs,
		PinnedToolIDs:        turn.PinnedToolIDs,
		EnableToolIDs:        turn.EnableToolIDs,
		DisableToolIDs:       turn.DisableToolIDs,
		PinToolIDs:           turn.PinToolIDs,
		UnpinToolIDs:         turn.UnpinToolIDs,
		Status:               normalizeTerminalTurnStatus(run.Status),
		QueueJobID:           turn.QueueJobID,
		Error:                run.Error,
		StartedAt:            turn.StartedAt,
		FinishedAt:           &finishedAt,
	}); err != nil {
		return err
	}
	return s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                run.ID,
		Title:             run.Title,
		Model:             run.Model,
		Provider:          run.Provider,
		ParentRunID:       run.ParentRunID,
		RootRunID:         run.RootRunID,
		TaskName:          run.TaskName,
		RunKind:           run.RunKind,
		Status:            run.Status,
		Error:             run.Error,
		DurationMs:        run.DurationMs,
		MessageCount:      run.MessageCount,
		InputTokens:       run.InputTokens,
		OutputTokens:      run.OutputTokens,
		LastEventID:       run.LastEventID,
		ActiveTurnID:      "",
		QueueJobID:        turn.QueueJobID,
		WorkerID:          workerID,
		CancelRequestedAt: nil,
		Trace:             run.Trace,
	})
}

func (s *Service) abortTurn(ctx context.Context, run *knowledge.AgentRun, turn *knowledge.AgentRunTurn, status, errText string) error {
	if err := s.store.UpdateAgentRunTurn(ctx, knowledge.UpdateAgentRunTurnInput{
		TurnID:               turn.TurnID,
		Title:                turn.Title,
		SystemPrompt:         turn.SystemPrompt,
		ProviderName:         turn.ProviderName,
		SubagentProviderName: turn.SubagentProviderName,
		EnabledToolIDs:       turn.EnabledToolIDs,
		PinnedToolIDs:        turn.PinnedToolIDs,
		EnableToolIDs:        turn.EnableToolIDs,
		DisableToolIDs:       turn.DisableToolIDs,
		PinToolIDs:           turn.PinToolIDs,
		UnpinToolIDs:         turn.UnpinToolIDs,
		Status:               status,
		QueueJobID:           turn.QueueJobID,
		Error:                errText,
		StartedAt:            turn.StartedAt,
		FinishedAt:           timePtr(time.Now().UTC()),
	}); err != nil {
		return err
	}
	if err := s.store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                run.ID,
		Title:             run.Title,
		Model:             run.Model,
		Provider:          run.Provider,
		ParentRunID:       run.ParentRunID,
		RootRunID:         run.RootRunID,
		TaskName:          run.TaskName,
		RunKind:           run.RunKind,
		Status:            status,
		Error:             errText,
		DurationMs:        run.DurationMs,
		MessageCount:      run.MessageCount,
		InputTokens:       run.InputTokens,
		OutputTokens:      run.OutputTokens,
		LastEventID:       run.LastEventID,
		ActiveTurnID:      "",
		QueueJobID:        turn.QueueJobID,
		WorkerID:          "",
		CancelRequestedAt: nil,
		Trace:             run.Trace,
	}); err != nil {
		return err
	}
	return s.emit(ctx, run.ID, turn.TurnID, Event{
		Kind: EventKindStatus,
		Status: &StatusPayload{
			Status: status,
			Error:  errText,
		},
	}, nil)
}

func (s *Service) emit(ctx context.Context, runID, turnID string, evt Event, external EventSink) error {
	payload, err := marshalEventPayload(evt)
	if err != nil {
		return err
	}
	stored, err := s.store.CreateAgentRunEvent(ctx, knowledge.CreateAgentRunEventInput{
		RunID:   runID,
		TurnID:  turnID,
		Kind:    evt.Kind,
		Payload: payload,
	})
	if err != nil {
		return err
	}
	s.eventBus.Publish(*stored)
	s.logger.Debug("agent run event", "run_id", runID, "turn_id", turnID, "event_id", stored.EventID, "kind", evt.Kind)
	if external != nil {
		evt.EventID = stored.EventID
		evt.CreatedAt = stored.CreatedAt
		if err := external.HandleEvent(ctx, evt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) watchForAbort(ctx context.Context, cancel context.CancelFunc, runID string) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run, err := s.store.GetAgentRun(context.Background(), runID)
				if err != nil {
					continue
				}
				if run.CancelRequestedAt != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Service) resolveProviderAndModel(providerName, model string, run *knowledge.AgentRun) (string, string, error) {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)

	if providerName == "" && model != "" {
		resolvedProvider, err := agentpkg.ResolveProviderNameForModel(model)
		if err != nil {
			return "", "", err
		}
		providerName = resolvedProvider
	}

	if providerName == "" && run != nil && strings.TrimSpace(run.Provider) != "" {
		providerName = strings.TrimSpace(run.Provider)
	}

	if providerName == "" && run != nil && strings.TrimSpace(run.Model) != "" {
		return normalizeProviderName(run.Provider), strings.TrimSpace(run.Model), nil
	}

	cfg, err := s.loadProvider(providerName)
	if err != nil {
		switch {
		case providerName != "":
			s.logger.Debug("using provider name as placeholder model", "provider", providerName, "error", err)
			fallbackModel := providerName
			if model != "" {
				fallbackModel = model
			}
			return normalizeProviderName(providerName), fallbackModel, nil
		case run != nil && strings.TrimSpace(run.Model) != "":
			return normalizeProviderName(run.Provider), strings.TrimSpace(run.Model), nil
		default:
			s.logger.Debug("using pending placeholder model", "error", err)
			if model != "" {
				return normalizeProviderName(providerName), model, nil
			}
			return normalizeProviderName(providerName), "pending", nil
		}
	}
	return normalizeProviderName(providerName), cfg.Model, nil
}

func marshalPlaceholderTrace(
	runID,
	title,
	model,
	provider,
	systemPrompt,
	parentRunID,
	rootRunID,
	taskName,
	runKind,
	runMode string,
	planRef agentpkg.StoredPlanRef,
) (json.RawMessage, error) {
	now := time.Now().UTC()
	trace := agentpkg.StoredTrace{
		SchemaVersion: 2,
		RunID:         runID,
		Title:         title,
		Model:         strings.TrimSpace(model),
		Provider:      normalizeProviderName(provider),
		SystemPrompt:  strings.TrimSpace(systemPrompt),
		Metadata: agentpkg.StoredRunMeta{
			ParentRunID: strings.TrimSpace(parentRunID),
			RootRunID:   resolveRootRunID(strings.TrimSpace(rootRunID), runID),
			TaskName:    strings.TrimSpace(taskName),
			RunKind:     resolveRunKind(strings.TrimSpace(parentRunID), strings.TrimSpace(runKind)),
			Mode:        agentpkg.NormalizeRunMode(runMode),
			PlanRef:     agentpkg.NormalizePlanRef(planRef),
			Tools:       agentpkg.StoredToolState{},
		},
		Usage:          agentpkg.StoredUsage{},
		EstimatedUsage: agentpkg.StoredEstimatedUsage{},
		Messages:       []agentpkg.StoredMessage{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	payload, err := json.Marshal(trace)
	if err != nil {
		return nil, fmt.Errorf("marshal placeholder trace: %w", err)
	}
	return payload, nil
}

func deriveTitle(title, message string, run *knowledge.AgentRun) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	if run != nil && strings.TrimSpace(run.Title) != "" {
		return strings.TrimSpace(run.Title)
	}
	message = strings.TrimSpace(message)
	if len(message) <= 60 {
		return message
	}
	return message[:60] + "..."
}

func resolveRootRunID(rootRunID, runID string) string {
	rootRunID = strings.TrimSpace(rootRunID)
	if rootRunID != "" {
		return rootRunID
	}
	return strings.TrimSpace(runID)
}

func resolveRunKind(parentRunID, runKind string) string {
	runKind = strings.TrimSpace(runKind)
	if runKind != "" {
		return runKind
	}
	if strings.TrimSpace(parentRunID) != "" {
		return agentpkg.RunKindChild
	}
	return agentpkg.RunKindRoot
}

func marshalEventPayload(evt Event) (json.RawMessage, error) {
	switch evt.Kind {
	case EventKindQueued:
		return json.Marshal(evt.Queued)
	case EventKindStarted:
		return json.Marshal(evt.Started)
	case EventKindTraceChunk:
		return json.Marshal(evt.Chunk)
	case EventKindToolResult:
		return json.Marshal(evt.ToolResult)
	case EventKindPlanReady:
		return json.Marshal(evt.PlanReady)
	case EventKindStatus:
		return json.Marshal(evt.Status)
	default:
		return nil, fmt.Errorf("unsupported run event kind %q", evt.Kind)
	}
}

func normalizeTerminalTurnStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "aborted":
		return strings.TrimSpace(status)
	default:
		return "failed"
	}
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func firstMeaningfulChunkKind(chunk agentpkg.TraceChunk) string {
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

func chunkHasContentDelta(chunk agentpkg.TraceChunk) bool {
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
		return false
	}
	return strings.TrimSpace(chunk.Choices[0].Delta.Content) != ""
}

type EventBus struct {
	mu   sync.RWMutex
	subs map[string]map[chan knowledge.AgentRunEvent]struct{}
}

var (
	defaultEventBus     *EventBus
	defaultEventBusOnce sync.Once
)

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[string]map[chan knowledge.AgentRunEvent]struct{})}
}

func DefaultEventBus() *EventBus {
	defaultEventBusOnce.Do(func() {
		defaultEventBus = NewEventBus()
	})
	return defaultEventBus
}

func (b *EventBus) Publish(evt knowledge.AgentRunEvent) {
	if b == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[evt.RunID] {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (b *EventBus) Subscribe(ctx context.Context, runID string) <-chan knowledge.AgentRunEvent {
	ch := make(chan knowledge.AgentRunEvent, 128)
	if b == nil {
		close(ch)
		return ch
	}
	b.mu.Lock()
	if b.subs[runID] == nil {
		b.subs[runID] = make(map[chan knowledge.AgentRunEvent]struct{})
	}
	b.subs[runID][ch] = struct{}{}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if subs := b.subs[runID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subs, runID)
			}
		}
		b.mu.Unlock()
		close(ch)
	}()

	return ch
}
