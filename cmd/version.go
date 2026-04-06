package cmd

import (
	"fmt"

	"github.com/versionlens/OpenNalvin/internal/buildinfo"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print build information",
	Run: func(cmd *cobra.Command, args []string) {
		info := buildinfo.Current()
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", info.Name, info.Version)
		fmt.Fprintf(cmd.OutOrStdout(), "module: %s\n", info.Module)
		fmt.Fprintf(cmd.OutOrStdout(), "commit: %s\n", info.Commit)
		fmt.Fprintf(cmd.OutOrStdout(), "built: %s\n", info.Date)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
