package cmd

import (
	"time"

	"github.com/versionlens/OpenNalvin/internal/jobs"
	"github.com/versionlens/OpenNalvin/internal/output"
	queuepkg "github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/spf13/cobra"
)

var (
	jobsEnqueueMessage string
	jobsEnqueueRunKey  string
)

var jobsEnqueueCmd = &cobra.Command{
	Use:   "enqueue",
	Short: "Enqueue a River job",
}

var jobsEnqueueHelloCmd = &cobra.Command{
	Use:   "hello",
	Short: "Enqueue a hello job",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		client, err := queuepkg.NewInsertClient(cmd.Context(), store.DB())
		if err != nil {
			return err
		}

		runKey := jobsEnqueueRunKey
		if runKey == "" {
			runKey = time.Now().UTC().Format("20060102T150405")
		}
		message := jobsEnqueueMessage
		if message == "" {
			message = "hello from nalvin"
		}
		res, err := client.Insert(cmd.Context(), jobs.HelloArgs{
			Message:     message,
			RunKey:      runKey,
			ScheduledBy: "jobs.enqueue",
		}, nil)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		payload := map[string]any{
			"id":      res.Job.ID,
			"kind":    res.Job.Kind,
			"queue":   res.Job.Queue,
			"run_key": runKey,
			"message": message,
			"skipped": res.UniqueSkippedAsDuplicate,
		}
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Enqueued hello job id=%d run_key=%s skipped=%t", res.Job.ID, runKey, res.UniqueSkippedAsDuplicate)
		return nil
	},
}

var jobsEnqueueScheduleHelloCmd = &cobra.Command{
	Use:   "schedule-hello",
	Short: "Enqueue the scheduler hello job",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		client, err := queuepkg.NewInsertClient(cmd.Context(), store.DB())
		if err != nil {
			return err
		}

		runKey := jobsEnqueueRunKey
		if runKey == "" {
			runKey = time.Now().UTC().Format("20060102T150405")
		}
		res, err := client.Insert(cmd.Context(), jobs.ScheduleHelloArgs{RunKey: runKey}, nil)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{
				"id":      res.Job.ID,
				"kind":    res.Job.Kind,
				"queue":   res.Job.Queue,
				"run_key": runKey,
				"skipped": res.UniqueSkippedAsDuplicate,
			})
		}
		w.Line("Enqueued schedule_hello job id=%d run_key=%s skipped=%t", res.Job.ID, runKey, res.UniqueSkippedAsDuplicate)
		return nil
	},
}

func init() {
	jobsCmd.AddCommand(jobsEnqueueCmd)

	jobsEnqueueHelloCmd.Flags().StringVar(&jobsEnqueueMessage, "message", "", "message payload")
	jobsEnqueueHelloCmd.Flags().StringVar(&jobsEnqueueRunKey, "run-key", "", "run key")
	jobsEnqueueCmd.AddCommand(jobsEnqueueHelloCmd)

	jobsEnqueueScheduleHelloCmd.Flags().StringVar(&jobsEnqueueRunKey, "run-key", "", "run key")
	jobsEnqueueCmd.AddCommand(jobsEnqueueScheduleHelloCmd)
}
