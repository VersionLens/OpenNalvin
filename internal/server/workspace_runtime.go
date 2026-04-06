package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/riverqueue/river"

	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/queue"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type workspaceHandle struct {
	apiDB        *sql.DB
	queueDB      *sql.DB
	store        *knowledge.Store
	runService   *agentrun.Service
	insertClient *river.Client[*sql.Tx]
	workerClient *river.Client[*sql.Tx]
}

type workspaceRuntime struct {
	cfg      config.Config
	logger   *slog.Logger
	executor agentrun.Executor

	mu      sync.RWMutex
	current string
	handles map[string]*workspaceHandle

	ctx    context.Context
	cancel context.CancelFunc
}

func newWorkspaceRuntime(cfg config.Config, logger *slog.Logger, executor agentrun.Executor) *workspaceRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &workspaceRuntime{
		cfg:      cfg,
		logger:   logger,
		executor: executor,
		current:  cfg.Workspace.Current,
		handles:  map[string]*workspaceHandle{},
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (rt *workspaceRuntime) currentName() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.current == "" {
		return rt.cfg.Workspace.Current
	}
	return rt.current
}

func (rt *workspaceRuntime) currentStore(ctx context.Context) (*knowledge.Store, error) {
	handle, err := rt.currentHandle(ctx)
	if err != nil {
		return nil, err
	}
	return handle.store, nil
}

func (rt *workspaceRuntime) currentRunService(ctx context.Context) (*agentrun.Service, error) {
	handle, err := rt.currentHandle(ctx)
	if err != nil {
		return nil, err
	}
	return handle.runService, nil
}

func (rt *workspaceRuntime) currentInsertClient(ctx context.Context) (*river.Client[*sql.Tx], error) {
	handle, err := rt.currentHandle(ctx)
	if err != nil {
		return nil, err
	}
	return handle.insertClient, nil
}

func (rt *workspaceRuntime) currentPaths() (workspacepkg.Paths, error) {
	return workspacepkg.PathsForName(rt.cfg, rt.currentName())
}

func (rt *workspaceRuntime) currentHandle(ctx context.Context) (*workspaceHandle, error) {
	return rt.handleForName(ctx, rt.currentName())
}

func (rt *workspaceRuntime) storeForName(ctx context.Context, name string) (*knowledge.Store, error) {
	handle, err := rt.handleForName(ctx, name)
	if err != nil {
		return nil, err
	}
	return handle.store, nil
}

func (rt *workspaceRuntime) handleForName(ctx context.Context, name string) (*workspaceHandle, error) {
	rt.mu.RLock()
	if handle, ok := rt.handles[name]; ok {
		rt.mu.RUnlock()
		return handle, nil
	}
	rt.mu.RUnlock()

	paths, err := workspacepkg.PathsForName(rt.cfg, name)
	if err != nil {
		return nil, err
	}
	apiDB, _, err := workspacepkg.OpenDB(ctx, rt.cfg, name)
	if err != nil {
		return nil, fmt.Errorf("open workspace %q api database: %w", name, err)
	}
	queueDB, err := database.Open(ctx, paths.DBPath)
	if err != nil {
		_ = apiDB.Close()
		return nil, fmt.Errorf("open workspace %q queue database: %w", name, err)
	}
	store := knowledge.New(apiDB)
	eventBus := agentrun.NewEventBus()
	runService := agentrun.New(store, agentrun.Options{
		Logger:   rt.logger,
		EventBus: eventBus,
		Executor: rt.executor,
	})
	handle := &workspaceHandle{
		apiDB:      apiDB,
		queueDB:    queueDB,
		store:      store,
		runService: runService,
	}
	if rt.cfg.Work.AgentWorkers > 0 {
		workerClient, err := queue.NewWorkerClient(ctx, queue.Options{
			DB:               queueDB,
			Store:            store,
			HelloWorkers:     -1,
			SchedulerWorkers: -1,
			AgentWorkers:     rt.cfg.Work.AgentWorkers,
			AgentJobTimeout:  rt.cfg.Work.AgentJobTimeout,
			ScheduleEnabled:  false,
			Logger:           rt.logger,
			ClientID:         embeddedWorkerClientID(name),
			AgentService:     runService,
			Config:           rt.cfg,
		})
		if err != nil {
			_ = queueDB.Close()
			_ = apiDB.Close()
			return nil, err
		}
		if err := workerClient.Start(rt.ctx); err != nil {
			_ = queueDB.Close()
			_ = apiDB.Close()
			return nil, fmt.Errorf("start embedded agent worker for %q: %w", name, err)
		}
		if err := store.MarkStaleRunningAgentRunsAborted(ctx, "interrupted by worker restart"); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = workerClient.Stop(stopCtx)
			cancel()
			_ = queueDB.Close()
			_ = apiDB.Close()
			return nil, err
		}
		handle.insertClient = workerClient
		handle.workerClient = workerClient
	} else {
		insertClient, err := queue.NewInsertClient(ctx, queueDB)
		if err != nil {
			_ = queueDB.Close()
			_ = apiDB.Close()
			return nil, err
		}
		handle.insertClient = insertClient
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if existing, ok := rt.handles[name]; ok {
		if handle.workerClient != nil {
			_ = handle.workerClient.Stop(context.Background())
		}
		_ = handle.queueDB.Close()
		_ = handle.apiDB.Close()
		return existing, nil
	}
	rt.handles[name] = handle
	return handle, nil
}

func (rt *workspaceRuntime) currentInfo() (workspacepkg.Info, error) {
	cfg := rt.cfg
	cfg.Workspace.Current = rt.currentName()
	return workspacepkg.CurrentInfo(cfg, "")
}

func (rt *workspaceRuntime) list() ([]workspacepkg.Info, error) {
	cfg := rt.cfg
	cfg.Workspace.Current = rt.currentName()
	return workspacepkg.List(cfg)
}

func (rt *workspaceRuntime) switchCurrent(name string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.current = name
	rt.cfg.Workspace.Current = name
}

func (rt *workspaceRuntime) close() error {
	rt.cancel()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var firstErr error
	for name, handle := range rt.handles {
		if handle.workerClient != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := handle.workerClient.Stop(stopCtx); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("stop workspace %q worker client: %w", name, err)
			}
			cancel()
		}
		if err := handle.queueDB.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close workspace %q queue database: %w", name, err)
		}
		if err := handle.apiDB.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close workspace %q api database: %w", name, err)
		}
	}
	rt.handles = map[string]*workspaceHandle{}
	return firstErr
}

func embeddedWorkerClientID(name string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("serve_%s_%s_%d", host, name, time.Now().UTC().UnixNano())
}
