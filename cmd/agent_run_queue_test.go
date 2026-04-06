package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/riverqueue/river"
)

func TestAgentRunQueueFlagUsesExternalWorkerWhenPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	prevWorkerFactory := newAgentRunWorkerClient
	prevPollDelay := agentRunQueuePollDelay
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				if opts.OnChunk != nil {
					if err := opts.OnChunk(agentpkg.TraceChunk{}); err != nil {
						return req.RunID, err
					}
				}
				time.Sleep(10 * time.Millisecond)
				if opts.OnChunk != nil {
					if err := opts.OnChunk(agentpkg.TraceChunk{
						Choices: []agentpkg.TraceChoice{{
							Delta: &agentpkg.TraceDelta{Reasoning: "thinking"},
						}},
					}); err != nil {
						return req.RunID, err
					}
				}
				time.Sleep(10 * time.Millisecond)
				if opts.OnChunk != nil {
					if err := opts.OnChunk(agentpkg.TraceChunk{
						Choices: []agentpkg.TraceChoice{{
							Delta: &agentpkg.TraceDelta{Content: "TTFT_OK"},
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
	embeddedStarted := false
	newAgentRunWorkerClient = func(ctx context.Context, opts queue.Options) (*river.Client[*sql.Tx], error) {
		embeddedStarted = true
		return queue.NewWorkerClient(ctx, opts)
	}
	agentRunQueuePollDelay = 2 * time.Millisecond
	t.Cleanup(func() {
		newAgentRunService = prevFactory
		newAgentRunWorkerClient = prevWorkerFactory
		agentRunQueuePollDelay = prevPollDelay
	})

	dbPath := filepath.Join(home, ".nalvin", "workspace-db", "alpha.sqlite")
	apiDB, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open api database: %v", err)
	}
	defer apiDB.Close()
	queueDB, err := database.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open queue database: %v", err)
	}
	defer queueDB.Close()

	store := knowledge.New(apiDB)
	worker, err := queue.NewWorkerClient(context.Background(), queue.Options{
		DB:               queueDB,
		Store:            store,
		HelloWorkers:     -1,
		SchedulerWorkers: -1,
		AgentWorkers:     1,
		ScheduleEnabled:  false,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		ClientID:         "test-worker",
		AgentService:     newAgentRunService(store, io.Discard),
	})
	if err != nil {
		t.Fatalf("create worker client: %v", err)
	}
	if err := worker.Start(context.Background()); err != nil {
		t.Fatalf("start worker client: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		active, err := hasActiveQueueWorker(context.Background(), queueDB)
		if err != nil {
			t.Fatalf("check active queue worker: %v", err)
		}
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for external worker registration")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Stop(stopCtx)
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
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--queue", "--timing", "-p", "Reply with exactly TTFT_OK and nothing else."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute queued agent run: %v\nstderr:\n%s", err, stderr.String())
	}

	if !strings.Contains(stdout.String(), "TTFT_OK") {
		t.Fatalf("expected queued output to contain TTFT_OK, got %q", stdout.String())
	}
	if embeddedStarted {
		t.Fatalf("expected command to reuse external worker instead of starting embedded worker")
	}
	for _, needle := range []string{
		"[timing] TTFT breakdown",
		"[timing] local request->queued:",
		"[timing] server queue wait:",
		"[timing] server content TTFT:",
		"Run ID:",
	} {
		if !strings.Contains(stderr.String(), needle) {
			t.Fatalf("expected stderr to contain %q, got:\n%s", needle, stderr.String())
		}
	}
}

func TestAgentRunQueueFlagStartsEmbeddedWorkerWhenNoExternalWorkerDetected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	prevFactory := newAgentRunService
	prevWorkerFactory := newAgentRunWorkerClient
	prevPollDelay := agentRunQueuePollDelay
	newAgentRunService = func(store *knowledge.Store, logOut io.Writer) *agentrun.Service {
		return agentrun.New(store, agentrun.Options{
			Logger: newAgentRunLogger(logOut),
			ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
				return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
			},
			Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
				if opts.OnChunk != nil {
					if err := opts.OnChunk(agentpkg.TraceChunk{
						Choices: []agentpkg.TraceChoice{{
							Delta: &agentpkg.TraceDelta{Content: "TTFT_OK"},
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
	embeddedStarts := 0
	newAgentRunWorkerClient = func(ctx context.Context, opts queue.Options) (*river.Client[*sql.Tx], error) {
		embeddedStarts++
		return queue.NewWorkerClient(ctx, opts)
	}
	agentRunQueuePollDelay = 2 * time.Millisecond
	t.Cleanup(func() {
		newAgentRunService = prevFactory
		newAgentRunWorkerClient = prevWorkerFactory
		agentRunQueuePollDelay = prevPollDelay
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
	rootCmd.SetArgs([]string{"--workspace", "alpha", "agent", "run", "--queue", "--timing", "-p", "Reply with exactly TTFT_OK and nothing else."})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute queued agent run with embedded worker: %v\nstderr:\n%s", err, stderr.String())
	}

	if embeddedStarts != 1 {
		t.Fatalf("expected command to start one embedded worker, got %d", embeddedStarts)
	}
	if !strings.Contains(stdout.String(), "TTFT_OK") {
		t.Fatalf("expected queued output to contain TTFT_OK, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Run ID:") {
		t.Fatalf("expected stderr to contain run id, got:\n%s", stderr.String())
	}
}
