package cmd

import (
	"github.com/versionlens/OpenNalvin/internal/output"
	queuepkg "github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/spf13/cobra"
)

var workStatusLimit int

var workStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show River worker and scheduler progress",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		status, err := queuepkg.LoadStatus(cmd.Context(), store, workStatusLimit)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(status)
		}

		w.Line("Queue Counts")
		for _, item := range status.QueueCounts {
			w.Line("  %s/%s/%s = %d", item.Queue, item.Kind, item.State, item.Count)
		}
		w.Line("")

		w.Line("Hello Jobs")
		for _, job := range status.HelloJobs {
			w.Line("  #%d %s state=%s attempt=%d run_key=%s", job.ID, job.Message, job.State, job.Attempt, job.RunKey)
		}
		w.Line("")

		w.Line("Scheduler State")
		for _, state := range status.ScheduleStates {
			w.Line("  %s status=%s run_key=%s error=%s", state.Task, state.Status, state.RunKey, state.LastError)
		}
		w.Line("")

		w.Line("River Clients")
		for _, client := range status.Clients {
			w.Line("  %s updated=%s", client.ID, client.UpdatedAt.Format("2006-01-02 15:04:05"))
		}

		return nil
	},
}

func init() {
	workStatusCmd.Flags().IntVarP(&workStatusLimit, "limit", "n", 20, "max jobs and clients to show")
	workCmd.AddCommand(workStatusCmd)
}
