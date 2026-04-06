package cmd

import "github.com/spf13/cobra"

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Enqueue or run River jobs from the CLI",
}

func init() {
	rootCmd.AddCommand(jobsCmd)
}
