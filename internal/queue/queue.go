package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/versionlens/OpenNalvin/internal/agentrun"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/robfig/cron/v3"

	"github.com/versionlens/OpenNalvin/internal/jobs"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
)

const (
	defaultHelloWorkers     = 2
	defaultSchedulerWorkers = 1
	defaultHelloCron        = "*/5 * * * *"
)

type Options struct {
	DB                *sql.DB
	Store             *knowledge.Store
	HelloWorkers      int
	SchedulerWorkers  int
	AgentWorkers      int
	AgentJobTimeout   time.Duration
	ScheduleEnabled   bool
	ScheduleHelloCron string
	Logger            *slog.Logger
	ClientID          string
	AgentService      *agentrun.Service
	Config            configpkg.Config
}

type HelloRunResult struct {
	Message     string    `json:"message"`
	RunKey      string    `json:"run_key"`
	ScheduledBy string    `json:"scheduled_by"`
	ProcessedAt time.Time `json:"processed_at"`
}

type JobStateCount struct {
	Queue string `json:"queue"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Count int    `json:"count"`
}

type HelloJobStatus struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	State       string     `json:"state"`
	Attempt     int        `json:"attempt"`
	Message     string     `json:"message"`
	RunKey      string     `json:"run_key"`
	ScheduledBy string     `json:"scheduled_by"`
	AttemptedAt *time.Time `json:"attempted_at,omitempty"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	FinalizedAt *time.Time `json:"finalized_at,omitempty"`
}

type RiverClientStatus struct {
	ID        string     `json:"id"`
	UpdatedAt time.Time  `json:"updated_at"`
	PausedAt  *time.Time `json:"paused_at,omitempty"`
	Metadata  string     `json:"metadata,omitempty"`
}

type Status struct {
	QueueCounts    []JobStateCount            `json:"queue_counts"`
	HelloJobs      []HelloJobStatus           `json:"hello_jobs"`
	Clients        []RiverClientStatus        `json:"clients"`
	ScheduleStates []knowledge.SchedulerState `json:"schedule_states"`
}

func NewInsertClient(ctx context.Context, db *sql.DB) (*river.Client[*sql.Tx], error) {
	workers := registerWorkers(Options{})
	client, err := river.NewClient(riversqlite.New(db), &river.Config{
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create river insert client: %w", err)
	}
	return client, nil
}

func NewWorkerClient(ctx context.Context, opts Options) (*river.Client[*sql.Tx], error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("worker options require a database")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("worker options require a store")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.HelloWorkers == 0 {
		opts.HelloWorkers = defaultHelloWorkers
	}
	if opts.SchedulerWorkers == 0 {
		opts.SchedulerWorkers = defaultSchedulerWorkers
	}
	if opts.ScheduleHelloCron == "" {
		opts.ScheduleHelloCron = defaultHelloCron
	}

	if err := syncPeriodicSchedulerState(ctx, opts); err != nil {
		return nil, fmt.Errorf("initialize scheduler state: %w", err)
	}

	queueConfigs := map[string]river.QueueConfig{}
	if opts.HelloWorkers > 0 {
		queueConfigs[jobs.QueueHello] = river.QueueConfig{MaxWorkers: opts.HelloWorkers}
	}
	if opts.SchedulerWorkers > 0 {
		queueConfigs[jobs.QueueScheduler] = river.QueueConfig{MaxWorkers: opts.SchedulerWorkers}
	}
	if opts.AgentWorkers > 0 {
		queueConfigs[jobs.QueueAgent] = river.QueueConfig{MaxWorkers: opts.AgentWorkers}
	}

	client, err := river.NewClient(riversqlite.New(opts.DB), &river.Config{
		ID:           opts.ClientID,
		JobTimeout:   opts.AgentJobTimeout,
		Logger:       opts.Logger,
		PeriodicJobs: periodicJobs(opts),
		Queues:       queueConfigs,
		Workers:      registerWorkers(opts),
	})
	if err != nil {
		return nil, fmt.Errorf("create river worker client: %w", err)
	}
	return client, nil
}

func registerWorkers(opts Options) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &HelloWorker{opts: opts})
	river.AddWorker(workers, &ScheduleHelloWorker{opts: opts})
	river.AddWorker(workers, &AgentRunWorker{opts: opts})
	return workers
}

type HelloWorker struct {
	river.WorkerDefaults[jobs.HelloArgs]
	opts Options
}

func (w *HelloWorker) Work(ctx context.Context, job *river.Job[jobs.HelloArgs]) error {
	result, err := RunHello(ctx, w.opts, job.Args)
	if err != nil {
		return err
	}
	if w.opts.Logger != nil {
		w.opts.Logger.Info("processed hello job",
			"message", result.Message,
			"run_key", result.RunKey,
			"scheduled_by", result.ScheduledBy,
		)
	}
	return nil
}

type ScheduleHelloWorker struct {
	river.WorkerDefaults[jobs.ScheduleHelloArgs]
	opts Options
}

func (w *ScheduleHelloWorker) Work(ctx context.Context, job *river.Job[jobs.ScheduleHelloArgs]) error {
	return RunScheduleHello(ctx, w.opts, nil, job.Args)
}

type AgentRunWorker struct {
	river.WorkerDefaults[jobs.AgentRunArgs]
	opts Options
}

func (w *AgentRunWorker) Work(ctx context.Context, job *river.Job[jobs.AgentRunArgs]) error {
	if w.opts.AgentService == nil {
		return fmt.Errorf("agent run worker requires an agent service")
	}
	ctx = configpkg.WithContext(ctx, w.opts.Config)
	return w.opts.AgentService.ExecuteTurn(ctx, job.Args.TurnID, agentrun.ExecutionOptions{
		WorkerID: w.opts.ClientID,
	})
}

func RunHello(_ context.Context, _ Options, args jobs.HelloArgs) (HelloRunResult, error) {
	message := args.Message
	if message == "" {
		message = "hello from nalvin"
	}
	return HelloRunResult{
		Message:     message,
		RunKey:      args.RunKey,
		ScheduledBy: args.ScheduledBy,
		ProcessedAt: time.Now().UTC(),
	}, nil
}

func RunScheduleHello(ctx context.Context, opts Options, client *river.Client[*sql.Tx], args jobs.ScheduleHelloArgs) error {
	startedAt := time.Now().UTC()
	next, _ := nextCronTime(opts.ScheduleHelloCron, startedAt)
	_ = opts.Store.UpsertSchedulerState(ctx, jobs.KindScheduleHello, "", "running", args.RunKey, opts.ClientID, &startedAt, nil, next, nil, "")

	if client == nil {
		client = river.ClientFromContext[*sql.Tx](ctx)
	}

	message := fmt.Sprintf("hello from schedule %s", args.RunKey)
	_, err := client.Insert(ctx, jobs.HelloArgs{
		Message:     message,
		RunKey:      args.RunKey,
		ScheduledBy: jobs.KindScheduleHello,
	}, nil)
	if err != nil {
		finishedAt := time.Now().UTC()
		_ = opts.Store.UpsertSchedulerState(ctx, jobs.KindScheduleHello, "", "failed", args.RunKey, opts.ClientID, &startedAt, &finishedAt, next, nil, err.Error())
		return fmt.Errorf("enqueue hello job: %w", err)
	}

	finishedAt := time.Now().UTC()
	summary := map[string]any{
		"message":      message,
		"scheduled_by": jobs.KindScheduleHello,
	}
	return opts.Store.UpsertSchedulerState(ctx, jobs.KindScheduleHello, "", "idle", args.RunKey, opts.ClientID, &startedAt, &finishedAt, next, summary, "")
}

func EnqueueAgentRun(ctx context.Context, client *river.Client[*sql.Tx], turnID string) (int64, bool, error) {
	if client == nil {
		return 0, false, fmt.Errorf("river insert client is required")
	}
	res, err := client.Insert(ctx, jobs.AgentRunArgs{TurnID: turnID}, nil)
	if err != nil {
		return 0, false, fmt.Errorf("enqueue agent run: %w", err)
	}
	return res.Job.ID, res.UniqueSkippedAsDuplicate, nil
}

func periodicJobs(opts Options) []*river.PeriodicJob {
	if !opts.ScheduleEnabled {
		return nil
	}

	schedule, err := cron.ParseStandard(firstNonEmpty(opts.ScheduleHelloCron, defaultHelloCron))
	if err != nil {
		return nil
	}

	return []*river.PeriodicJob{
		river.NewPeriodicJob(schedule, func() (river.JobArgs, *river.InsertOpts) {
			return jobs.ScheduleHelloArgs{RunKey: time.Now().UTC().Format("20060102T150405")}, nil
		}, &river.PeriodicJobOpts{
			ID:         jobs.KindScheduleHello,
			RunOnStart: true,
		}),
	}
}

func syncPeriodicSchedulerState(ctx context.Context, opts Options) error {
	now := time.Now().UTC()
	if !opts.ScheduleEnabled {
		return opts.Store.UpsertSchedulerState(ctx, jobs.KindScheduleHello, "", "disabled", "", opts.ClientID, nil, nil, nil, nil, "")
	}
	next, err := nextCronTime(firstNonEmpty(opts.ScheduleHelloCron, defaultHelloCron), now)
	if err != nil {
		return err
	}
	return opts.Store.UpsertSchedulerState(ctx, jobs.KindScheduleHello, "", "idle", "", opts.ClientID, nil, nil, next, map[string]any{"cron": firstNonEmpty(opts.ScheduleHelloCron, defaultHelloCron)}, "")
}

func nextCronTime(spec string, now time.Time) (*time.Time, error) {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return nil, err
	}
	next := schedule.Next(now)
	return &next, nil
}

func LoadStatus(ctx context.Context, store *knowledge.Store, limit int) (Status, error) {
	if limit <= 0 {
		limit = 20
	}
	status := Status{}

	countRows, err := store.DB().QueryContext(ctx, `
		SELECT queue, kind, state, COUNT(*)
		FROM river_job
		GROUP BY queue, kind, state
		ORDER BY queue, kind, state`)
	if err != nil {
		return Status{}, fmt.Errorf("list queue counts: %w", err)
	}
	defer countRows.Close()
	for countRows.Next() {
		var item JobStateCount
		if err := countRows.Scan(&item.Queue, &item.Kind, &item.State, &item.Count); err != nil {
			return Status{}, fmt.Errorf("scan queue count: %w", err)
		}
		status.QueueCounts = append(status.QueueCounts, item)
	}
	if err := countRows.Err(); err != nil {
		return Status{}, fmt.Errorf("iterate queue counts: %w", err)
	}

	jobRows, err := store.DB().QueryContext(ctx, `
		SELECT id, kind, state, attempt,
		       COALESCE(json_extract(args, '$.message'), ''),
		       COALESCE(json_extract(args, '$.run_key'), ''),
		       COALESCE(json_extract(args, '$.scheduled_by'), ''),
		       attempted_at, scheduled_at, finalized_at
		FROM river_job
		WHERE kind = ?
		ORDER BY scheduled_at DESC, id DESC
		LIMIT ?`, jobs.KindHelloJob, limit)
	if err != nil {
		return Status{}, fmt.Errorf("list hello jobs: %w", err)
	}
	defer jobRows.Close()
	for jobRows.Next() {
		var (
			item        HelloJobStatus
			attemptedAt sql.NullTime
			finalizedAt sql.NullTime
		)
		if err := jobRows.Scan(
			&item.ID,
			&item.Kind,
			&item.State,
			&item.Attempt,
			&item.Message,
			&item.RunKey,
			&item.ScheduledBy,
			&attemptedAt,
			&item.ScheduledAt,
			&finalizedAt,
		); err != nil {
			return Status{}, fmt.Errorf("scan hello job: %w", err)
		}
		if attemptedAt.Valid {
			item.AttemptedAt = &attemptedAt.Time
		}
		if finalizedAt.Valid {
			item.FinalizedAt = &finalizedAt.Time
		}
		status.HelloJobs = append(status.HelloJobs, item)
	}
	if err := jobRows.Err(); err != nil {
		return Status{}, fmt.Errorf("iterate hello jobs: %w", err)
	}

	clientRows, err := store.DB().QueryContext(ctx, `
		SELECT id, updated_at, paused_at, metadata
		FROM river_client
		ORDER BY updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return Status{}, fmt.Errorf("list river clients: %w", err)
	}
	defer clientRows.Close()
	for clientRows.Next() {
		var (
			item     RiverClientStatus
			pausedAt sql.NullTime
			metadata []byte
		)
		if err := clientRows.Scan(&item.ID, &item.UpdatedAt, &pausedAt, &metadata); err != nil {
			return Status{}, fmt.Errorf("scan river client: %w", err)
		}
		if pausedAt.Valid {
			item.PausedAt = &pausedAt.Time
		}
		if len(metadata) > 0 {
			item.Metadata = string(metadata)
		}
		status.Clients = append(status.Clients, item)
	}
	if err := clientRows.Err(); err != nil {
		return Status{}, fmt.Errorf("iterate river clients: %w", err)
	}

	states, err := store.ListSchedulerStates(ctx)
	if err != nil {
		return Status{}, err
	}
	status.ScheduleStates = states

	return status, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func EncodeJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
