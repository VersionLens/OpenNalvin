package cmd

import (
	"fmt"
	"github.com/versionlens/OpenNalvin/internal/output"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var (
	agentRunPrompt           string
	agentRunResume           string
	agentRunProvider         string
	agentRunSubagentProvider string
	agentRunSystem           string
	agentRunEnableTools      []string
	agentRunDisableTools     []string
	agentRunPinTools         []string
	agentRunUnpinTools       []string
	agentRunVerbose          bool
	agentRunQueue            bool
	agentRunTiming           bool
	agentRunContextWindow    int
	agentRunMode             string
)

func requestedAgentRunMode(cmd *cobra.Command) string {
	if cmd == nil {
		return strings.TrimSpace(agentRunMode)
	}
	if !cmd.Flags().Changed("mode") {
		return ""
	}
	return strings.TrimSpace(agentRunMode)
}

var agentRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a direct prompt against the configured remote provider",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()

		if output.FromContext(cmd.Context()).IsJSON() {
			return fmt.Errorf("agent run does not support --json output")
		}
		if strings.TrimSpace(agentRunPrompt) == "" {
			return fmt.Errorf("provide a prompt with --prompt or -p")
		}

		if agentRunQueue {
			return runQueuedAgent(ctx, cmd)
		}
		return runAttachedAgent(ctx, cmd)
	},
}

func init() {
	agentRunCmd.Flags().StringVarP(&agentRunPrompt, "prompt", "p", "", "prompt to send to the agent")
	agentRunCmd.Flags().StringVar(&agentRunResume, "resume", "", "existing run id to continue with an additional user message")
	agentRunCmd.Flags().StringVar(&agentRunProvider, "provider", "", "provider name from config.yaml (defaults to providers.default)")
	agentRunCmd.Flags().StringVar(&agentRunSubagentProvider, "subagent-provider", "", "provider name from config.yaml for spawned child agents (defaults to config or parent provider)")
	agentRunCmd.Flags().StringVar(&agentRunSystem, "system", "", "optional system prompt to apply to the run")
	agentRunCmd.Flags().StringSliceVar(&agentRunEnableTools, "enable-tool", nil, "enable a tool for this run (repeatable)")
	agentRunCmd.Flags().StringSliceVar(&agentRunDisableTools, "disable-tool", nil, "disable a tool for this run (repeatable)")
	agentRunCmd.Flags().StringSliceVar(&agentRunPinTools, "pin-tool", nil, "pin a tool so it is immediately visible to the agent (repeatable)")
	agentRunCmd.Flags().StringSliceVar(&agentRunUnpinTools, "unpin-tool", nil, "unpin a tool for this run (repeatable)")
	agentRunCmd.Flags().BoolVar(&agentRunQueue, "queue", false, "enqueue the run through the River agent worker queue instead of executing it inline")
	agentRunCmd.Flags().BoolVar(&agentRunVerbose, "verbose", false, "emit debug logging for tool resolution, tool calls, and sub-agent lifecycle events")
	agentRunCmd.Flags().BoolVar(&agentRunTiming, "timing", false, "print timing breakdown for queue wait, first streamed output, and first content token")
	agentRunCmd.Flags().IntVar(&agentRunContextWindow, "context-window", 0, "override context window size in tokens (for testing compaction)")
	agentRunCmd.Flags().StringVar(&agentRunMode, "mode", "", "run mode override: default or plan")
	agentCmd.AddCommand(agentRunCmd)
}
