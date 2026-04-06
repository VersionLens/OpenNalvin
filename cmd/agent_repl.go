package cmd

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/tui"
	"github.com/spf13/cobra"
)

var (
	agentReplProvider         string
	agentReplSubagentProvider string
	agentReplSystem           string
	agentReplEnableTools      []string
	agentReplDisableTools     []string
	agentReplPinTools         []string
	agentReplUnpinTools       []string
	agentReplVerbose          bool
	agentReplContextWindow    int
)

var agentReplCmd = &cobra.Command{
	Use:   "repl",
	Short: "Interactive terminal chat with the agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()

		ctx = withAgentSubagentProviderOverride(ctx, agentReplSubagentProvider)

		db, _, _, err := openWorkspaceDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()

		cfg := tui.Config{
			Store:        knowledge.New(db),
			ProviderName: agentReplProvider,
			SystemPrompt: strings.TrimSpace(agentReplSystem),
			Tools: agent.ToolSelection{
				EnableToolIDs:  agentReplEnableTools,
				DisableToolIDs: agentReplDisableTools,
				PinToolIDs:     agentReplPinTools,
				UnpinToolIDs:   agentReplUnpinTools,
			},
			Verbose:       agentReplVerbose,
			ContextWindow: agentReplContextWindow,
		}

		m := tui.New(cfg)
		p := tea.NewProgram(m)
		go p.Send(tui.ProgramRefMsg{Program: p})
		_, err = p.Run()
		return err
	},
}

func init() {
	agentReplCmd.Flags().StringVar(&agentReplProvider, "provider", "", "provider name from config.yaml (defaults to providers.default)")
	agentReplCmd.Flags().StringVar(&agentReplSubagentProvider, "subagent-provider", "", "provider name from config.yaml for spawned child agents (defaults to config or parent provider)")
	agentReplCmd.Flags().StringVar(&agentReplSystem, "system", "", "optional system prompt to apply to the session")
	agentReplCmd.Flags().StringSliceVar(&agentReplEnableTools, "enable-tool", nil, "enable a tool for this session (repeatable)")
	agentReplCmd.Flags().StringSliceVar(&agentReplDisableTools, "disable-tool", nil, "disable a tool for this session (repeatable)")
	agentReplCmd.Flags().StringSliceVar(&agentReplPinTools, "pin-tool", nil, "pin a tool so it is immediately visible to the agent (repeatable)")
	agentReplCmd.Flags().StringSliceVar(&agentReplUnpinTools, "unpin-tool", nil, "unpin a tool for this session (repeatable)")
	agentReplCmd.Flags().BoolVar(&agentReplVerbose, "verbose", false, "emit debug logging for tool resolution, tool calls, and sub-agent lifecycle events")
	agentReplCmd.Flags().IntVar(&agentReplContextWindow, "context-window", 0, "override context window size in tokens (for testing compaction)")
	agentCmd.AddCommand(agentReplCmd)
}
