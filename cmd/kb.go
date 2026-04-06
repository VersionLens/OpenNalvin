package cmd

import (
	"context"

	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/spf13/cobra"
)

var kbCmd = &cobra.Command{
	Use:   "kb",
	Short: "Knowledge graph operations",
}

func init() {
	rootCmd.AddCommand(kbCmd)
}

func kbStore(ctx context.Context) (*knowledge.Store, func() error, error) {
	db, _, _, err := openWorkspaceDB(ctx)
	if err != nil {
		return nil, nil, err
	}
	return knowledge.New(db), db.Close, nil
}
