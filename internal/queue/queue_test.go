package queue

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/jobs"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/riverqueue/river"
)

func TestInsertClientEnqueuesHelloJob(t *testing.T) {
	t.Parallel()

	db, store := testDBAndStore(t)
	defer db.Close()

	client, err := NewInsertClient(context.Background(), db)
	if err != nil {
		t.Fatalf("new insert client: %v", err)
	}

	if _, err := client.Insert(context.Background(), jobs.HelloArgs{
		Message:     "hello test",
		RunKey:      "insert-1",
		ScheduledBy: "test",
	}, nil); err != nil {
		t.Fatalf("insert hello job: %v", err)
	}

	status, err := LoadStatus(context.Background(), store, 10)
	if err != nil {
		t.Fatalf("load status: %v", err)
	}
	if len(status.HelloJobs) != 1 {
		t.Fatalf("expected 1 hello job, got %d", len(status.HelloJobs))
	}
}

func TestWorkerClientProcessesHelloAndPeriodicScheduler(t *testing.T) {
	t.Parallel()

	db, store := testDBAndStore(t)
	defer db.Close()

	client, err := NewWorkerClient(context.Background(), Options{
		DB:                db,
		Store:             store,
		HelloWorkers:      1,
		SchedulerWorkers:  1,
		ScheduleEnabled:   true,
		ScheduleHelloCron: "*/10 * * * *",
		ClientID:          "test-worker",
	})
	if err != nil {
		t.Fatalf("new worker client: %v", err)
	}

	subscribeChan, cancel := client.Subscribe(river.EventKindJobCompleted)
	defer cancel()

	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start worker client: %v", err)
	}
	defer client.Stop(context.Background())

	if _, err := client.Insert(ctx, jobs.HelloArgs{
		Message:     "hello worker",
		RunKey:      "worker-1",
		ScheduledBy: "test",
	}, nil); err != nil {
		t.Fatalf("insert hello job: %v", err)
	}

	waitForEvents(t, subscribeChan, 2)

	status, err := LoadStatus(context.Background(), store, 20)
	if err != nil {
		t.Fatalf("load status: %v", err)
	}
	if len(status.HelloJobs) == 0 {
		t.Fatalf("expected hello jobs in status")
	}
	if len(status.ScheduleStates) == 0 {
		t.Fatalf("expected scheduler state entries")
	}
	if status.ScheduleStates[0].Task != jobs.KindScheduleHello {
		t.Fatalf("expected schedule state task %q, got %q", jobs.KindScheduleHello, status.ScheduleStates[0].Task)
	}
}

func TestAgentRunWorkerPropagatesConfigToExecutionContext(t *testing.T) {
	t.Parallel()

	db, store := testDBAndStore(t)
	defer db.Close()

	cfg := configpkg.Config{
		Workspace: configpkg.WorkspaceConfig{
			Current: "queue-test",
		},
		Agent: configpkg.AgentConfig{
			Tools: configpkg.AgentToolsConfig{
				DefaultPinned: []string{"mcp__exa__web_search_exa"},
			},
		},
	}

	service := agentrun.New(store, agentrun.Options{
		ProviderLoader: func(name string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{
				Type:   agentpkg.ProviderTypeOpenAI,
				APIKey: "test-key",
				Model:  "test-model",
			}, nil
		},
		Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
			got, ok := configpkg.FromContext(ctx)
			if !ok {
				t.Fatalf("expected config on worker execution context")
			}
			if got.Workspace.Current != cfg.Workspace.Current {
				t.Fatalf("expected workspace %q, got %q", cfg.Workspace.Current, got.Workspace.Current)
			}
			if len(got.Agent.Tools.DefaultPinned) != 1 || got.Agent.Tools.DefaultPinned[0] != cfg.Agent.Tools.DefaultPinned[0] {
				t.Fatalf("expected default pinned %v, got %v", cfg.Agent.Tools.DefaultPinned, got.Agent.Tools.DefaultPinned)
			}

			run, err := store.GetAgentRun(ctx, req.RunID)
			if err != nil {
				return "", err
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

	prepared, err := service.PrepareQueuedTurn(context.Background(), agentrun.Request{
		ClientRequestID: "worker-config-test",
		Message:         "hello from worker",
	})
	if err != nil {
		t.Fatalf("prepare queued turn: %v", err)
	}

	worker := &AgentRunWorker{
		opts: Options{
			AgentService: service,
			ClientID:     "worker-config-test",
			Config:       cfg,
		},
	}
	job := &river.Job[jobs.AgentRunArgs]{
		Args: jobs.AgentRunArgs{TurnID: prepared.Turn.TurnID},
	}

	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("worker work: %v", err)
	}
}

func TestWorkerClientAgentJobTimeoutAbortsLongRun(t *testing.T) {
	t.Parallel()

	db, store := testDBAndStore(t)
	defer db.Close()

	service := agentrun.New(store, agentrun.Options{
		ProviderLoader: func(string) (agentpkg.ProviderConfig, error) {
			return agentpkg.ProviderConfig{Model: "gpt-test"}, nil
		},
		Executor: func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
			<-ctx.Done()
			return req.RunID, ctx.Err()
		},
	})

	client, err := NewWorkerClient(context.Background(), Options{
		DB:               db,
		Store:            store,
		HelloWorkers:     -1,
		SchedulerWorkers: -1,
		AgentWorkers:     1,
		AgentJobTimeout:  50 * time.Millisecond,
		ScheduleEnabled:  false,
		ClientID:         "agent-timeout-test",
		AgentService:     service,
	})
	if err != nil {
		t.Fatalf("new worker client: %v", err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start worker client: %v", err)
	}
	defer client.Stop(context.Background())

	prepared, err := service.PrepareQueuedTurn(context.Background(), agentrun.Request{
		ClientRequestID: "agent-timeout-test",
		Message:         "wait until timeout",
	})
	if err != nil {
		t.Fatalf("prepare queued turn: %v", err)
	}

	jobID, _, err := EnqueueAgentRun(context.Background(), client, prepared.Turn.TurnID)
	if err != nil {
		t.Fatalf("enqueue agent run: %v", err)
	}
	if _, _, err := service.MarkTurnQueued(context.Background(), prepared, jobID); err != nil {
		t.Fatalf("mark queued turn: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		run, err := store.GetAgentRun(context.Background(), prepared.Run.ID)
		if err != nil {
			t.Fatalf("get agent run: %v", err)
		}
		if run.Status == "aborted" {
			if run.Error != context.DeadlineExceeded.Error() {
				t.Fatalf("expected deadline exceeded error, got %q", run.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run abort, last status=%q error=%q", run.Status, run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testDBAndStore(t *testing.T) (*sql.DB, *knowledge.Store) {
	t.Helper()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return db, knowledge.New(db)
}

func waitForEvents(t *testing.T, ch <-chan *river.Event, n int) {
	t.Helper()

	timeout := time.After(10 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-timeout:
			t.Fatalf("timed out waiting for river events")
		}
	}
}
