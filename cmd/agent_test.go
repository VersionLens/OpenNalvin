package cmd

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func TestAgentRunRequiresPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := executeRoot(t, "agent", "run")
	if err == nil {
		t.Fatal("expected prompt validation error")
	}
	if err.Error() != "provide a prompt with --prompt or -p" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentRunRejectsJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := executeRoot(t, "--json", "agent", "run", "--prompt", "hello")
	if err == nil {
		t.Fatal("expected json validation error")
	}
	if !strings.Contains(err.Error(), "does not support --json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentRunAttachedTimingLogsStayOffStdout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				if opts.Out != nil {
					if _, err := io.WriteString(opts.Out, "ATTACHED_OK"); err != nil {
						return req.RunID, err
					}
				}
				if opts.OnChunk != nil {
					if err := opts.OnChunk(agentpkg.TraceChunk{
						Choices: []agentpkg.TraceChoice{{
							Delta: &agentpkg.TraceDelta{Content: "ATTACHED_OK"},
						}},
					}); err != nil {
						return req.RunID, err
					}
				}
				run, err := store.GetAgentRun(ctx, req.RunID)
				if err != nil {
					return req.RunID, err
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
					return req.RunID, err
				}
				return req.RunID, nil
			},
		})
	}
	t.Cleanup(func() {
		newAgentRunService = prevFactory
	})

	resetViperBindings()
	resetCommandFlags(rootCmd)
	cfgFile = ""
	outputJSON = false
	workspaceName = ""

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--timing", "-p", "Reply with exactly ATTACHED_OK and nothing else."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute attached agent run: %v\nstderr:\n%s", err, stderr.String())
	}

	if !strings.Contains(stdout.String(), "ATTACHED_OK") {
		t.Fatalf("expected stdout to contain attached output, got %q", stdout.String())
	}
	for _, needle := range []string{"agent run start timing", "agent run first output timing", "agent run first content timing", "[timing] TTFT breakdown"} {
		if strings.Contains(stdout.String(), needle) {
			t.Fatalf("expected stdout to exclude %q, got:\n%s", needle, stdout.String())
		}
	}
	for _, needle := range []string{"agent run start timing", "agent run first output timing", "agent run first content timing", "[timing] TTFT breakdown", "Run ID:"} {
		if !strings.Contains(stderr.String(), needle) {
			t.Fatalf("expected stderr to contain %q, got:\n%s", needle, stderr.String())
		}
	}
}

func TestAgentRunAttachedSubagentProviderFlagOverridesConfigContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				cfg, ok := config.FromContext(ctx)
				if !ok {
					t.Fatal("expected config in executor context")
				}
				if cfg.Agent.Subagents.ProviderName != "anthropic" {
					t.Fatalf("expected subagent provider override anthropic, got %q", cfg.Agent.Subagents.ProviderName)
				}
				run, err := store.GetAgentRun(ctx, req.RunID)
				if err != nil {
					return req.RunID, err
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
					return req.RunID, err
				}
				return req.RunID, nil
			},
		})
	}
	t.Cleanup(func() {
		newAgentRunService = prevFactory
	})

	resetViperBindings()
	resetCommandFlags(rootCmd)
	cfgFile = ""
	outputJSON = false
	workspaceName = ""

	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--subagent-provider", "anthropic", "-p", "Reply with exactly OK and nothing else."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute attached agent run: %v", err)
	}
}

func TestAgentRunModeFlagPassesPlanMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				if req.Mode != agentpkg.RunModePlan {
					t.Fatalf("expected plan mode, got %q", req.Mode)
				}
				run, err := store.GetAgentRun(ctx, req.RunID)
				if err != nil {
					return req.RunID, err
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
					return req.RunID, err
				}
				return req.RunID, nil
			},
		})
	}
	t.Cleanup(func() {
		newAgentRunService = prevFactory
	})

	resetViperBindings()
	resetCommandFlags(rootCmd)
	cfgFile = ""
	outputJSON = false
	workspaceName = ""

	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--mode", "plan", "-p", "Plan this task."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute attached agent run: %v", err)
	}
}

func TestAgentRunResumeFlagPassesExistingRunID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				if req.RunID != "run_existing" {
					t.Fatalf("expected resumed run id run_existing, got %q", req.RunID)
				}
				run, err := store.GetAgentRun(ctx, req.RunID)
				if err != nil {
					return req.RunID, err
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
					return req.RunID, err
				}
				return req.RunID, nil
			},
		})
	}
	t.Cleanup(func() {
		newAgentRunService = prevFactory
	})

	resetViperBindings()
	resetCommandFlags(rootCmd)
	cfgFile = ""
	outputJSON = false
	workspaceName = ""

	cfg := config.Config{
		Workspace: config.WorkspaceConfig{
			DBRoot:    filepath.Join(home, ".nalvin", "workspace-db"),
			FilesRoot: filepath.Join(home, ".nalvin", "workspaces"),
			Current:   "alpha",
		},
	}
	db, _, err := workspacepkg.OpenDB(context.Background(), cfg, "alpha")
	if err != nil {
		t.Fatalf("open workspace db: %v", err)
	}
	store := knowledge.New(db)
	trace := []byte(`{"schema_version":2,"run_id":"run_existing","title":"Existing","model":"gpt-test","provider":"default","metadata":{"root_run_id":"run_existing","run_kind":"root","tools":{}},"usage":{},"estimated_usage":{},"messages":[],"created_at":"2026-04-04T00:00:00Z","updated_at":"2026-04-04T00:00:00Z"}`)
	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           "run_existing",
		Title:        "Existing",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "Earlier prompt",
		Status:       "completed",
		MessageCount: 0,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create existing run: %v", err)
	}
	_ = db.Close()

	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--resume", "run_existing", "-p", "Continue this conversation."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute resumed agent run: %v", err)
	}
}

func TestAgentRunResumeInheritsExistingPlanModeWhenModeFlagIsOmitted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				if req.RunID != "run_existing" {
					t.Fatalf("expected resumed run id run_existing, got %q", req.RunID)
				}
				if req.Mode != agentpkg.RunModePlan {
					t.Fatalf("expected resumed run to inherit plan mode, got %q", req.Mode)
				}
				run, err := store.GetAgentRun(ctx, req.RunID)
				if err != nil {
					return req.RunID, err
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
					return req.RunID, err
				}
				return req.RunID, nil
			},
		})
	}
	t.Cleanup(func() {
		newAgentRunService = prevFactory
	})

	resetViperBindings()
	resetCommandFlags(rootCmd)
	cfgFile = ""
	outputJSON = false
	workspaceName = ""

	cfg := config.Config{
		Workspace: config.WorkspaceConfig{
			DBRoot:    filepath.Join(home, ".nalvin", "workspace-db"),
			FilesRoot: filepath.Join(home, ".nalvin", "workspaces"),
			Current:   "alpha",
		},
	}
	db, _, err := workspacepkg.OpenDB(context.Background(), cfg, "alpha")
	if err != nil {
		t.Fatalf("open workspace db: %v", err)
	}
	store := knowledge.New(db)
	trace := []byte(`{"schema_version":2,"run_id":"run_existing","title":"Existing plan","model":"gpt-test","provider":"default","metadata":{"mode":"plan","plan_ref":{"path":".plans/example.md","source_run_id":"run_existing","status":"active"},"root_run_id":"run_existing","run_kind":"root","tools":{}},"usage":{},"estimated_usage":{},"messages":[],"created_at":"2026-04-04T00:00:00Z","updated_at":"2026-04-04T00:00:00Z"}`)
	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           "run_existing",
		Title:        "Existing plan",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "Earlier prompt",
		Status:       "completed",
		MessageCount: 0,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create existing run: %v", err)
	}
	_ = db.Close()

	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--resume", "run_existing", "-p", "Refine the plan."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute resumed plan agent run: %v", err)
	}
}
