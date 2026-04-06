package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/spf13/cobra"
)

var workCmd = &cobra.Command{
	Use:   "work",
	Short: "Run River workers for hello and scheduler queues",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()

		apiDB, paths, cfg, err := openWorkspaceDB(ctx)
		if err != nil {
			return err
		}
		defer apiDB.Close()
		queueDB, err := database.Open(ctx, paths.DBPath)
		if err != nil {
			return err
		}
		defer queueDB.Close()

		store := knowledge.New(apiDB)
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		clientID := workerClientID()
		runService := agentrun.New(store, agentrun.Options{Logger: logger, EventBus: agentrun.NewEventBus()})

		client, err := queue.NewWorkerClient(ctx, queue.Options{
			DB:                queueDB,
			Store:             store,
			HelloWorkers:      cfg.Work.HelloWorkers,
			SchedulerWorkers:  cfg.Work.SchedulerWorkers,
			AgentWorkers:      cfg.Work.AgentWorkers,
			AgentJobTimeout:   cfg.Work.AgentJobTimeout,
			ScheduleEnabled:   cfg.Schedule.Enabled,
			ScheduleHelloCron: cfg.Schedule.HelloCron,
			Logger:            logger,
			ClientID:          clientID,
			AgentService:      runService,
			Config:            cfg,
		})
		if err != nil {
			return err
		}

		logger.Info("starting workers",
			"workspace", paths.Name,
			"client_id", clientID,
			"hello_workers", cfg.Work.HelloWorkers,
			"scheduler_workers", cfg.Work.SchedulerWorkers,
			"agent_workers", cfg.Work.AgentWorkers,
			"agent_job_timeout", cfg.Work.AgentJobTimeout,
			"schedule_enabled", cfg.Schedule.Enabled,
			"hello_cron", cfg.Schedule.HelloCron,
		)

		if err := client.Start(ctx); err != nil {
			return fmt.Errorf("start workers: %w", err)
		}
		if cfg.Work.AgentWorkers > 0 {
			if err := store.MarkStaleRunningAgentRunsAborted(ctx, "interrupted by worker restart"); err != nil {
				stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_ = client.Stop(stopCtx)
				cancel()
				return err
			}
		}

		<-ctx.Done()

		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.Stop(stopCtx); err != nil {
			return fmt.Errorf("stop workers: %w", err)
		}
		logger.Info("workers stopped", "workspace", paths.Name, "client_id", clientID)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(workCmd)
}

func workerClientID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s_%d_%s", host, os.Getpid(), time.Now().UTC().Format("20060102T150405"))
}
