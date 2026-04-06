package cmd

import "github.com/spf13/cobra"

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run remote AI agent operations",
}

func init() {
	rootCmd.AddCommand(agentCmd)
}
