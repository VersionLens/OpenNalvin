package cmd

import (
	"fmt"
	"time"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/jobs"
	"github.com/versionlens/OpenNalvin/internal/output"
	queuepkg "github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/spf13/cobra"
)

var (
	jobsRunMessage string
	jobsRunRunKey  string
)

var jobsRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a job handler directly without a River worker",
}

var jobsRunHelloCmd = &cobra.Command{
	Use:   "hello",
	Short: "Run the hello handler directly",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		runKey := jobsRunRunKey
		if runKey == "" {
			runKey = time.Now().UTC().Format("20060102T150405")
		}
		message := jobsRunMessage
		if message == "" {
			message = "hello from nalvin"
		}

		result, err := queuepkg.RunHello(cmd.Context(), queuepkg.Options{
			ScheduleEnabled:   cfg.Schedule.Enabled,
			ScheduleHelloCron: cfg.Schedule.HelloCron,
			ClientID:          "jobs.run",
		}, jobs.HelloArgs{
			Message:     message,
			RunKey:      runKey,
			ScheduledBy: "jobs.run",
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}
		w.Line("Hello job completed: %s", result.Message)
		w.Line("Run key: %s", result.RunKey)
		w.Line("Scheduled by: %s", result.ScheduledBy)
		return nil
	},
}

func init() {
	jobsCmd.AddCommand(jobsRunCmd)

	jobsRunHelloCmd.Flags().StringVar(&jobsRunMessage, "message", "", "message payload")
	jobsRunHelloCmd.Flags().StringVar(&jobsRunRunKey, "run-key", "", "run key")
	jobsRunCmd.AddCommand(jobsRunHelloCmd)
}
