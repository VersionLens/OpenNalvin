package agentrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/viper"
)

func TestMarkTurnQueuedReturnsRunWithLatestEventID(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agentrun.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
	})

	prepared, err := service.PrepareQueuedTurn(context.Background(), Request{
		ClientRequestID: "req-1",
		Message:         "hello",
	})
	if err != nil {
		t.Fatalf("prepare queued turn: %v", err)
	}

	run, turn, err := service.MarkTurnQueued(context.Background(), prepared, 42)
	if err != nil {
		t.Fatalf("mark turn queued: %v", err)
	}

	if turn.QueueJobID != 42 {
		t.Fatalf("expected queue job id 42, got %d", turn.QueueJobID)
	}
	if run.LastEventID == 0 {
		t.Fatalf("expected returned run to include last_event_id")
	}

	events, err := store.ListAgentRunEventsAfter(context.Background(), run.ID, 0, 10)
	if err != nil {
		t.Fatalf("list agent run events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	if events[0].Kind != EventKindQueued {
		t.Fatalf("expected queued event, got %q", events[0].Kind)
	}
	if run.LastEventID != events[0].EventID {
		t.Fatalf("expected last_event_id %d, got %d", events[0].EventID, run.LastEventID)
	}
}

func TestEventBusInstancesDoNotCrossPublish(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	busA := NewEventBus()
	busB := NewEventBus()
	subA := busA.Subscribe(ctx, "shared-run")
	subB := busB.Subscribe(ctx, "shared-run")

	busA.Publish(knowledge.AgentRunEvent{
		EventID: 1,
		RunID:   "shared-run",
		Kind:    EventKindStatus,
	})

	select {
	case evt := <-subA:
		if evt.RunID != "shared-run" {
			t.Fatalf("unexpected run id on primary bus: %q", evt.RunID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event on primary bus")
	}

	select {
	case evt := <-subB:
		t.Fatalf("unexpected cross-bus event delivery: %+v", evt)
	default:
	}
}

func TestPrepareQueuedTurnPersistsRequestedRunLinks(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agentrun-links.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
	})

	prepared, err := service.PrepareQueuedTurn(context.Background(), Request{
		ClientRequestID: "req-linked",
		Message:         "hello",
		ParentRunID:     "run-parent",
		RootRunID:       "chat-root",
		TaskName:        "chat_followup",
		RunKind:         agentpkg.RunKindRoot,
	})
	if err != nil {
		t.Fatalf("prepare queued turn: %v", err)
	}

	if prepared.Run.ParentRunID != "run-parent" {
		t.Fatalf("expected parent_run_id run-parent, got %q", prepared.Run.ParentRunID)
	}
	if prepared.Run.RootRunID != "chat-root" {
		t.Fatalf("expected root_run_id chat-root, got %q", prepared.Run.RootRunID)
	}
	if prepared.Run.TaskName != "chat_followup" {
		t.Fatalf("expected task_name chat_followup, got %q", prepared.Run.TaskName)
	}
	if prepared.Run.RunKind != agentpkg.RunKindRoot {
		t.Fatalf("expected run_kind %q, got %q", agentpkg.RunKindRoot, prepared.Run.RunKind)
	}
}

func TestPrepareAttachedTurnResolvesProviderFromModel(t *testing.T) {
	t.Parallel()

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.kimi.type", "openai_compat")
	viper.Set("providers.kimi.base_url", "https://api.kimi.test/v1")
	viper.Set("providers.kimi.api_key", "configured")
	viper.Set("providers.kimi.model", "kimi-for-coding")

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agentrun-model.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(name string) (agentpkg.ProviderConfig, error) {
			if name != "kimi" {
				t.Fatalf("unexpected provider lookup %q", name)
			}
			return agentpkg.ProviderConfig{Model: "kimi-for-coding"}, nil
		},
	})

	prepared, err := service.PrepareAttachedTurn(context.Background(), Request{
		ClientRequestID: "req-model",
		Message:         "hello",
		Model:           "kimi-for-coding",
	})
	if err != nil {
		t.Fatalf("prepare attached turn: %v", err)
	}

	if prepared.Run.Provider != "kimi" {
		t.Fatalf("expected run provider kimi, got %q", prepared.Run.Provider)
	}
	if prepared.Run.Model != "kimi-for-coding" {
		t.Fatalf("expected run model kimi-for-coding, got %q", prepared.Run.Model)
	}
	if prepared.Turn.ProviderName != "kimi" {
		t.Fatalf("expected turn provider kimi, got %q", prepared.Turn.ProviderName)
	}
}

func TestExecuteTurnUsesPersistedSubagentProviderOverride(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agentrun-subagent-provider.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	var executorSubagentProvider string
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
		Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
			cfg, ok := configpkg.FromContext(ctx)
			if !ok {
				t.Fatal("expected execution context config")
			}
			executorSubagentProvider = cfg.Agent.Subagents.ProviderName

			run, err := store.GetAgentRun(ctx, req.RunID)
			if err != nil {
				t.Fatalf("get running run: %v", err)
			}
			if err := store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
				ID:                run.ID,
				Title:             run.Title,
				Model:             run.Model,
				Provider:          run.Provider,
				ParentRunID:       run.ParentRunID,
				RootRunID:         run.RootRunID,
				TaskName:          run.TaskName,
				RunKind:           run.RunKind,
				Status:            "completed",
				Error:             "",
				DurationMs:        run.DurationMs,
				MessageCount:      run.MessageCount,
				InputTokens:       run.InputTokens,
				OutputTokens:      run.OutputTokens,
				LastEventID:       run.LastEventID,
				ActiveTurnID:      run.ActiveTurnID,
				QueueJobID:        run.QueueJobID,
				WorkerID:          run.WorkerID,
				CancelRequestedAt: run.CancelRequestedAt,
				Trace:             run.Trace,
			}); err != nil {
				t.Fatalf("mark run completed: %v", err)
			}
			return "ok", nil
		},
	})

	prepareCfg := configpkg.Config{}
	prepareCfg.Agent.Subagents.ProviderName = "glm-turbo"
	prepareCtx := configpkg.WithContext(context.Background(), prepareCfg)

	prepared, err := service.PrepareQueuedTurn(prepareCtx, Request{
		ClientRequestID: "req-subagent-provider",
		Message:         "hello",
	})
	if err != nil {
		t.Fatalf("prepare queued turn: %v", err)
	}

	if prepared.Turn.SubagentProviderName != "glm-turbo" {
		t.Fatalf("expected queued turn subagent provider glm-turbo, got %q", prepared.Turn.SubagentProviderName)
	}

	if _, _, err := service.MarkTurnQueued(prepareCtx, prepared, 7); err != nil {
		t.Fatalf("mark turn queued: %v", err)
	}

	executeCfg := configpkg.Config{}
	executeCfg.Agent.Subagents.ProviderName = "anthropic"
	executeCtx := configpkg.WithContext(context.Background(), executeCfg)

	if err := service.ExecuteTurn(executeCtx, prepared.Turn.TurnID, ExecutionOptions{WorkerID: "worker-1"}); err != nil {
		t.Fatalf("execute queued turn: %v", err)
	}

	if executorSubagentProvider != "glm-turbo" {
		t.Fatalf("expected executor subagent provider glm-turbo, got %q", executorSubagentProvider)
	}

	storedTurn, err := store.GetAgentRunTurn(context.Background(), prepared.Turn.TurnID)
	if err != nil {
		t.Fatalf("get stored turn: %v", err)
	}
	if storedTurn.SubagentProviderName != "glm-turbo" {
		t.Fatalf("expected stored turn subagent provider glm-turbo, got %q", storedTurn.SubagentProviderName)
	}
}

func TestPrepareQueuedTurnPlanModeCreatesCanonicalPlanFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	db, err := database.Open(context.Background(), filepath.Join(root, "agentrun-plan.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
	})

	ctx := workspacepkg.WithPaths(context.Background(), workspacepkg.Paths{
		Name:      "test",
		DBPath:    filepath.Join(root, "agentrun-plan.db"),
		FilesPath: filepath.Join(root, "files"),
	})

	prepared, err := service.PrepareQueuedTurn(ctx, Request{
		ClientRequestID: "req-plan",
		Message:         "Plan the migration",
		Mode:            agentpkg.RunModePlan,
	})
	if err != nil {
		t.Fatalf("prepare queued turn: %v", err)
	}

	trace, err := agentpkg.ParseStoredTrace(prepared.Run.Trace)
	if err != nil {
		t.Fatalf("parse stored trace: %v", err)
	}
	if trace.Metadata.Mode != agentpkg.RunModePlan {
		t.Fatalf("expected plan mode, got %q", trace.Metadata.Mode)
	}
	if trace.Metadata.PlanRef.Path == "" {
		t.Fatal("expected canonical plan path")
	}

	planPath := filepath.Join(root, "files", filepath.FromSlash(trace.Metadata.PlanRef.Path))
	body, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read plan file: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected initial plan file content")
	}
}

func TestPrepareQueuedImplementationTurnCreatesFreshDefaultRunWithPlanLoaded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	db, err := database.Open(context.Background(), filepath.Join(root, "agentrun-implement.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
	})

	ctx := workspacepkg.WithPaths(context.Background(), workspacepkg.Paths{
		Name:      "test",
		DBPath:    filepath.Join(root, "agentrun-implement.db"),
		FilesPath: filepath.Join(root, "files"),
	})

	preparedPlan, err := service.PrepareQueuedTurn(ctx, Request{
		ClientRequestID: "req-plan",
		Message:         "Plan the refactor",
		Mode:            agentpkg.RunModePlan,
	})
	if err != nil {
		t.Fatalf("prepare plan turn: %v", err)
	}
	planTrace, err := agentpkg.ParseStoredTrace(preparedPlan.Run.Trace)
	if err != nil {
		t.Fatalf("parse plan trace: %v", err)
	}
	planPath := filepath.Join(root, "files", filepath.FromSlash(planTrace.Metadata.PlanRef.Path))
	if err := os.WriteFile(planPath, []byte("# Approved plan\n\n- Step 1\n"), 0o644); err != nil {
		t.Fatalf("write approved plan: %v", err)
	}

	preparedImpl, err := service.PrepareQueuedImplementationTurn(ctx, preparedPlan.Run.ID, Request{ClientRequestID: "req-implement"})
	if err != nil {
		t.Fatalf("prepare implementation turn: %v", err)
	}
	if preparedImpl.Run.ID == preparedPlan.Run.ID {
		t.Fatal("expected a fresh implementation run id")
	}

	trace, err := agentpkg.ParseStoredTrace(preparedImpl.Run.Trace)
	if err != nil {
		t.Fatalf("parse implementation trace: %v", err)
	}
	if trace.Metadata.Mode != agentpkg.RunModeDefault {
		t.Fatalf("expected default mode, got %q", trace.Metadata.Mode)
	}
	if trace.Metadata.PlanRef.Path != planTrace.Metadata.PlanRef.Path {
		t.Fatalf("expected plan path %q, got %q", planTrace.Metadata.PlanRef.Path, trace.Metadata.PlanRef.Path)
	}
	if trace.Metadata.PlanRef.SourceRunID != preparedPlan.Run.ID {
		t.Fatalf("expected source run id %q, got %q", preparedPlan.Run.ID, trace.Metadata.PlanRef.SourceRunID)
	}
	if trace.Metadata.PlanRef.Status != agentpkg.PlanStatusApproved {
		t.Fatalf("expected approved status, got %q", trace.Metadata.PlanRef.Status)
	}
	if preparedImpl.Turn.Message == "" || !containsAll(preparedImpl.Turn.Message, "Implement the approved plan", "# Approved plan") {
		t.Fatalf("expected implementation prompt to include the approved plan, got %q", preparedImpl.Turn.Message)
	}
}

func TestPrepareQueuedImplementationTurnIncludesInferredTargetRootGuidance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	db, err := database.Open(context.Background(), filepath.Join(root, "agentrun-implement-root.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
	})

	ctx := workspacepkg.WithPaths(context.Background(), workspacepkg.Paths{
		Name:      "test",
		DBPath:    filepath.Join(root, "agentrun-implement-root.db"),
		FilesPath: filepath.Join(root, "files"),
	})
	if err := os.MkdirAll(filepath.Join(root, "files", "demo-default-1775260677"), 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}

	preparedPlan, err := service.PrepareQueuedTurn(ctx, Request{
		ClientRequestID: "req-plan",
		Message:         "Plan a safer local release workflow for demo-default-1775260677 by adding VERSION, README.md, and scripts/release.sh at the repo root.",
		Mode:            agentpkg.RunModePlan,
	})
	if err != nil {
		t.Fatalf("prepare plan turn: %v", err)
	}
	planTrace, err := agentpkg.ParseStoredTrace(preparedPlan.Run.Trace)
	if err != nil {
		t.Fatalf("parse plan trace: %v", err)
	}
	planPath := filepath.Join(root, "files", filepath.FromSlash(planTrace.Metadata.PlanRef.Path))
	body := "# Approved plan\n\n- Write a single line to `VERSION` at the repo root.\n- Add project docs to `README.md`.\n- Create `scripts/release.sh`.\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write approved plan: %v", err)
	}

	preparedImpl, err := service.PrepareQueuedImplementationTurn(ctx, preparedPlan.Run.ID, Request{ClientRequestID: "req-implement"})
	if err != nil {
		t.Fatalf("prepare implementation turn: %v", err)
	}

	if !containsAll(preparedImpl.Turn.Message,
		"Target workspace-relative root: demo-default-1775260677",
		"Create and update files under that directory unless the plan explicitly says otherwise.",
		"Do not place those files at the workspace root by default.",
		"Follow any workspace-relative paths in the plan exactly as written.",
	) {
		t.Fatalf("expected implementation prompt to include target-root guidance, got %q", preparedImpl.Turn.Message)
	}
}

func TestExecuteTurnEmitsPlanReadyEvent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	db, err := database.Open(context.Background(), filepath.Join(root, "agentrun-plan-ready.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	service := New(store, Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
		Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
			run, err := store.GetAgentRun(ctx, req.RunID)
			if err != nil {
				return "", err
			}
			if opts.OnToolResult != nil {
				if err := opts.OnToolResult(agentpkg.ToolResultEvent{
					ToolCallID: "call_plan_exit",
					ToolName:   "plan_exit",
					Output:     `{"status":"ready"}`,
					PlanRef: &agentpkg.StoredPlanRef{
						Path:        ".plans/example.md",
						SourceRunID: req.RunID,
						Status:      agentpkg.PlanStatusReady,
					},
				}); err != nil {
					return "", err
				}
			}
			if err := store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
				ID:                run.ID,
				Title:             run.Title,
				Model:             run.Model,
				Provider:          run.Provider,
				ParentRunID:       run.ParentRunID,
				RootRunID:         run.RootRunID,
				TaskName:          run.TaskName,
				RunKind:           run.RunKind,
				Status:            "completed",
				Error:             "",
				DurationMs:        run.DurationMs,
				MessageCount:      run.MessageCount,
				InputTokens:       run.InputTokens,
				OutputTokens:      run.OutputTokens,
				LastEventID:       run.LastEventID,
				ActiveTurnID:      run.ActiveTurnID,
				QueueJobID:        run.QueueJobID,
				WorkerID:          run.WorkerID,
				CancelRequestedAt: run.CancelRequestedAt,
				Trace:             run.Trace,
			}); err != nil {
				return "", err
			}
			return req.RunID, nil
		},
	})

	ctx := workspacepkg.WithPaths(context.Background(), workspacepkg.Paths{
		Name:      "test",
		DBPath:    filepath.Join(root, "agentrun-plan-ready.db"),
		FilesPath: filepath.Join(root, "files"),
	})

	prepared, err := service.PrepareAttachedTurn(ctx, Request{
		ClientRequestID: "req-plan-ready",
		Message:         "Plan the work",
		Mode:            agentpkg.RunModePlan,
	})
	if err != nil {
		t.Fatalf("prepare attached turn: %v", err)
	}

	if err := service.ExecuteTurn(ctx, prepared.Turn.TurnID, ExecutionOptions{}); err != nil {
		t.Fatalf("execute turn: %v", err)
	}

	events, err := store.ListAgentRunEventsAfter(ctx, prepared.Run.ID, 0, 20)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, evt := range events {
		if evt.Kind != EventKindPlanReady {
			continue
		}
		var payload PlanReadyPayload
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			t.Fatalf("decode plan ready payload: %v", err)
		}
		if payload.PlanPath != ".plans/example.md" {
			t.Fatalf("expected plan path .plans/example.md, got %q", payload.PlanPath)
		}
		found = true
	}
	if !found {
		t.Fatal("expected a plan_ready event")
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
