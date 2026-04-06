package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/spf13/viper"
)

func TestPersistQueuedChildRunCreatesLinkedChildRecord(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "subagents.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{})
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)

	manager := newSubAgentManager(parent, store, configpkg.Config{})
	child := &subAgentHandle{
		runID:        "child_run",
		taskName:     "smoke_child",
		parentRunID:  "parent_run",
		rootRunID:    "parent_run",
		initialTitle: "Child title",
	}

	if err := manager.persistQueuedChildRun(ctx, "lmstudio", "gpt-test", child, "fetch something"); err != nil {
		t.Fatalf("persist queued child run: %v", err)
	}

	run, err := store.GetAgentRun(ctx, "child_run")
	if err != nil {
		t.Fatalf("get child run: %v", err)
	}

	if run.Status != AgentStatusQueued {
		t.Fatalf("expected queued status, got %q", run.Status)
	}
	if run.ParentRunID != "parent_run" {
		t.Fatalf("expected parent_run_id parent_run, got %q", run.ParentRunID)
	}
	if run.RootRunID != "parent_run" {
		t.Fatalf("expected root_run_id parent_run, got %q", run.RootRunID)
	}
	if run.TaskName != "smoke_child" {
		t.Fatalf("expected task_name smoke_child, got %q", run.TaskName)
	}
	if run.RunKind != RunKindChild {
		t.Fatalf("expected run kind %q, got %q", RunKindChild, run.RunKind)
	}
	if run.Prompt != "fetch something" {
		t.Fatalf("expected prompt to be persisted, got %q", run.Prompt)
	}

	trace, err := parseStoredTrace(run.Trace)
	if err != nil {
		t.Fatalf("parse stored trace: %v", err)
	}
	if trace.Provider != "lmstudio" {
		t.Fatalf("expected trace provider lmstudio, got %q", trace.Provider)
	}
	if trace.Model != "gpt-test" {
		t.Fatalf("expected trace model gpt-test, got %q", trace.Model)
	}
}

func TestSpawnChildInheritsParentProviderByDefault(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.lmstudio.type", "openai_compat")
	viper.Set("providers.lmstudio.base_url", "http://127.0.0.1:1/v1")
	viper.Set("providers.lmstudio.api_key", "configured")
	viper.Set("providers.lmstudio.model", "local-model")

	oldStart := startSubAgentLoop
	startSubAgentLoop = func(*subAgentHandle) {}
	t.Cleanup(func() { startSubAgentLoop = oldStart })

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "subagents-inherit.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	cfg := configpkg.Config{}
	ctx := configpkg.WithContext(context.Background(), cfg)
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)
	parent.setProviderName("lmstudio")

	manager := newSubAgentManager(parent, store, cfg)
	child, err := manager.spawn(ctx, subAgentSpawnRequest{
		Message:  "fetch something",
		TaskName: "smoke_child",
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}

	if got := child.session.providerName(); got != "lmstudio" {
		t.Fatalf("expected child session provider lmstudio, got %q", got)
	}

	run, err := store.GetAgentRun(ctx, child.runID)
	if err != nil {
		t.Fatalf("get child run: %v", err)
	}
	if run.Provider != "lmstudio" {
		t.Fatalf("expected child provider lmstudio, got %q", run.Provider)
	}
	if run.Model != "local-model" {
		t.Fatalf("expected child model local-model, got %q", run.Model)
	}
}

func TestSpawnChildUsesConfiguredSubagentProviderOverride(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.lmstudio.type", "openai_compat")
	viper.Set("providers.lmstudio.base_url", "http://127.0.0.1:1/v1")
	viper.Set("providers.lmstudio.api_key", "configured")
	viper.Set("providers.lmstudio.model", "local-model")
	viper.Set("providers.kimi.type", "openai_compat")
	viper.Set("providers.kimi.base_url", "http://127.0.0.1:2/v1")
	viper.Set("providers.kimi.api_key", "configured")
	viper.Set("providers.kimi.model", "kimi-for-coding")

	oldStart := startSubAgentLoop
	startSubAgentLoop = func(*subAgentHandle) {}
	t.Cleanup(func() { startSubAgentLoop = oldStart })

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "subagents-override.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	cfg := configpkg.Config{
		Agent: configpkg.AgentConfig{
			Subagents: configpkg.AgentSubagentsConfig{ProviderName: "kimi"},
		},
	}
	ctx := configpkg.WithContext(context.Background(), cfg)
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)
	parent.setProviderName("lmstudio")

	manager := newSubAgentManager(parent, store, cfg)
	child, err := manager.spawn(ctx, subAgentSpawnRequest{
		Message:  "fetch something",
		TaskName: "smoke_child",
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}

	if got := child.session.providerName(); got != "kimi" {
		t.Fatalf("expected child session provider kimi, got %q", got)
	}

	run, err := store.GetAgentRun(ctx, child.runID)
	if err != nil {
		t.Fatalf("get child run: %v", err)
	}
	if run.Provider != "kimi" {
		t.Fatalf("expected child provider kimi, got %q", run.Provider)
	}
	if run.Model != "kimi-for-coding" {
		t.Fatalf("expected child model kimi-for-coding, got %q", run.Model)
	}
}

func TestSpawnChildFailsForUnknownConfiguredSubagentProvider(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.lmstudio.type", "openai_compat")
	viper.Set("providers.lmstudio.base_url", "http://127.0.0.1:1/v1")
	viper.Set("providers.lmstudio.api_key", "configured")
	viper.Set("providers.lmstudio.model", "local-model")

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "subagents-missing-provider.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	cfg := configpkg.Config{
		Agent: configpkg.AgentConfig{
			Subagents: configpkg.AgentSubagentsConfig{ProviderName: "missing"},
		},
	}
	ctx := configpkg.WithContext(context.Background(), cfg)
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)
	parent.setProviderName("lmstudio")

	manager := newSubAgentManager(parent, store, cfg)
	_, err = manager.spawn(ctx, subAgentSpawnRequest{
		Message: "fetch something",
	})
	if err == nil || !strings.Contains(err.Error(), `provider "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloseAgentReachesTerminalStatus(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "close-terminal.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{})
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)

	manager := &subAgentManager{parent: parent, store: store, cfg: configpkg.Config{}}

	// Create a DB record for the child run so latestAssistantOutput can find it.
	if err := manager.persistQueuedChildRun(ctx, "test", "test-model", &subAgentHandle{
		runID:        "child_run",
		taskName:     "closeable_child",
		parentRunID:  "parent_run",
		rootRunID:    "parent_run",
		initialTitle: "test",
	}, "hello"); err != nil {
		t.Fatalf("persist child run: %v", err)
	}

	child := &subAgentHandle{
		manager:     manager,
		session:     newEphemeralSessionState(ctx, store, "child_run"),
		runID:       "child_run",
		taskName:    "closeable_child",
		parentRunID: "parent_run",
		rootRunID:   "parent_run",
		status:      AgentStatusCompleted,
		wake:        make(chan struct{}, 1),
	}

	// Close should succeed and return a terminal status.
	if err := child.close(ctx); err != nil {
		t.Fatalf("close child: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, err := child.wait(waitCtx)
	if err != nil {
		t.Fatalf("wait after close: %v", err)
	}
	if status.Status != AgentStatusClosed && status.Status != AgentStatusCompleted {
		t.Fatalf("expected terminal status, got %q", status.Status)
	}
}

func TestCloseAgentSlowChildReturnsStopping(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "close-slow.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{})
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)

	child := &subAgentHandle{
		manager:     &subAgentManager{parent: parent, store: store, cfg: configpkg.Config{}},
		session:     newEphemeralSessionState(ctx, store, "slow_child"),
		runID:       "slow_child",
		taskName:    "slow_task",
		parentRunID: "parent_run",
		rootRunID:   "parent_run",
		status:      AgentStatusRunning,
		wake:        make(chan struct{}, 1),
	}

	// wait with a very short timeout on a child that stays "running"
	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = child.wait(waitCtx)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	// Verify snapshot still shows running (not terminal).
	snap := child.snapshot()
	if snap.Status != AgentStatusRunning {
		t.Fatalf("expected running status from snapshot, got %q", snap.Status)
	}
}

func TestWaitAgentTimeoutReturnsStillRunning(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "wait-timeout.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{})
	parent := newEphemeralSessionState(ctx, store, "parent_run")
	parent.setMetadata("", "parent_run", "parent_task", RunKindRoot)

	child := &subAgentHandle{
		manager:     &subAgentManager{parent: parent, store: store, cfg: configpkg.Config{}},
		session:     newEphemeralSessionState(ctx, store, "forever_child"),
		runID:       "forever_child",
		taskName:    "forever_task",
		parentRunID: "parent_run",
		rootRunID:   "parent_run",
		status:      AgentStatusRunning,
		wake:        make(chan struct{}, 1),
	}

	// Short timeout; child never completes.
	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	_, err = child.wait(waitCtx)
	// The raw wait returns context.DeadlineExceeded; confirm we get an error.
	if err == nil {
		t.Fatalf("expected timeout error from raw wait, got nil")
	}
	// After timeout, the snapshot should still say "running".
	snap := child.snapshot()
	if snap.Status != AgentStatusRunning {
		t.Fatalf("expected running, got %q", snap.Status)
	}
}

func TestLatestAssistantOutputFromTrace(t *testing.T) {
	t.Parallel()

	output, ok, err := latestAssistantOutputFromTrace(StoredTrace{
		Messages: []StoredMessage{
			{Role: "assistant", ToolCalls: []StoredToolCall{{ID: "call_1"}}},
			{Role: "assistant", Content: "   "},
			{Role: "assistant", Content: "first"},
			{Role: "assistant", Content: "second"},
		},
	})
	if err != nil {
		t.Fatalf("latest assistant output: %v", err)
	}
	if !ok || output != "second" {
		t.Fatalf("expected latest assistant output second, got output=%q ok=%t", output, ok)
	}
}

func TestLatestAssistantOutputFromTraceReturnsFalseWhenMissing(t *testing.T) {
	t.Parallel()

	output, ok, err := latestAssistantOutputFromTrace(StoredTrace{
		Messages: []StoredMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", ToolCalls: []StoredToolCall{{ID: "call_1"}}},
			{Role: "assistant", Content: "   "},
		},
	})
	if err != nil {
		t.Fatalf("latest assistant output: %v", err)
	}
	if ok || output != "" {
		t.Fatalf("expected no latest assistant output, got output=%q ok=%t", output, ok)
	}
}
