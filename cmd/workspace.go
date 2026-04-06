package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/output"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	workspaceDeleteYes         bool
	workspaceTransferOverwrite bool
)

type workspaceCurrentPayload struct {
	Name        string `json:"name"`
	Persisted   string `json:"persisted"`
	Overridden  bool   `json:"overridden"`
	DBPath      string `json:"db_path"`
	FilesPath   string `json:"files_path"`
	DBExists    bool   `json:"db_exists"`
	FilesExists bool   `json:"files_exists"`
}

type workspaceTransferPayload struct {
	Workspace   string `json:"workspace"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Kind        string `json:"kind"`
	FileCount   int    `json:"file_count"`
	ByteCount   int64  `json:"byte_count"`
}

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage nalvin workspaces",
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		items, err := workspacepkg.List(cfg)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}
		if len(items) == 0 {
			w.Line("No workspaces found.")
			return nil
		}
		for _, item := range items {
			marker := ""
			if item.Current {
				marker = " *"
			}
			w.Line("%s%s", item.Name, marker)
			w.Line("  db: %s", item.DBPath)
			w.Line("  files: %s", item.FilesPath)
		}
		return nil
	},
}

var workspaceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		info, err := workspacepkg.Create(cmd.Context(), cfg, args[0])
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(info)
		}
		w.Line("Created workspace %s", info.Name)
		w.Line("DB: %s", info.DBPath)
		w.Line("Files: %s", info.FilesPath)
		return nil
	},
}

var workspaceDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !workspaceDeleteYes {
			return fmt.Errorf("pass --yes to confirm deletion")
		}

		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}
		if args[0] == cfg.Workspace.Current {
			return fmt.Errorf("cannot delete the current workspace %q", args[0])
		}
		if err := workspacepkg.Delete(cfg, args[0]); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if !w.IsJSON() {
			w.Line("Deleted workspace %s", args[0])
		}
		return nil
	},
}

var workspaceCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current workspace selection",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		info, err := workspacepkg.CurrentInfo(cfg, workspaceName)
		if err != nil {
			return err
		}

		payload := workspaceCurrentPayload{
			Name:        info.Name,
			Persisted:   cfg.Workspace.Current,
			Overridden:  resolvedWorkspaceName(cfg) != cfg.Workspace.Current,
			DBPath:      info.DBPath,
			FilesPath:   info.FilesPath,
			DBExists:    info.DBExists,
			FilesExists: info.FilesExist,
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Workspace: %s", payload.Name)
		w.Line("Persisted: %s", payload.Persisted)
		w.Line("Overridden: %t", payload.Overridden)
		w.Line("DB: %s (exists=%t)", payload.DBPath, payload.DBExists)
		w.Line("Files: %s (exists=%t)", payload.FilesPath, payload.FilesExists)
		return nil
	},
}

var workspaceSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Persist a different default workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		exists, err := workspacepkg.Exists(cfg, args[0])
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("workspace %q does not exist", args[0])
		}
		if err := config.SaveWorkspaceCurrent(cfgFile, args[0]); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if !w.IsJSON() {
			w.Line("Switched current workspace to %s", args[0])
		}
		return nil
	},
}

var workspacePutCmd = &cobra.Command{
	Use:   "put <host-src> [workspace-dest]",
	Short: "Copy files or directories into the active workspace",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		paths, err := workspacepkg.ActivePaths(cmd.Context(), cfg)
		if err != nil {
			return err
		}

		source := filepath.Clean(expandHomePath(args[0]))
		destination := ""
		if len(args) > 1 {
			destination = args[1]
		} else {
			destination = filepath.Base(source)
		}

		_, relativeDestination, absoluteDestination, err := workspacepkg.ResolveFilePath(cmd.Context(), cfg, destination, false)
		if err != nil {
			return err
		}

		stats, err := workspacepkg.CopyTree(source, absoluteDestination, workspaceTransferOverwrite)
		if err != nil {
			return err
		}

		payload := workspaceTransferPayload{
			Workspace:   paths.Name,
			Source:      source,
			Destination: relativeDestination,
			Kind:        stats.Kind,
			FileCount:   stats.FileCount,
			ByteCount:   stats.ByteCount,
		}
		return renderWorkspaceTransfer(cmd, payload, "put")
	},
}

var workspaceGetCmd = &cobra.Command{
	Use:   "get <workspace-src> [host-dest]",
	Short: "Copy files or directories out of the active workspace",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		paths, err := workspacepkg.ActivePaths(cmd.Context(), cfg)
		if err != nil {
			return err
		}

		_, relativeSource, absoluteSource, err := workspacepkg.ResolveFilePath(cmd.Context(), cfg, args[0], false)
		if err != nil {
			return err
		}

		destination := ""
		if len(args) > 1 {
			destination = filepath.Clean(expandHomePath(args[1]))
		} else {
			destination = filepath.Base(relativeSource)
		}
		if strings.TrimSpace(destination) == "" || destination == "." {
			return fmt.Errorf("host destination is required")
		}

		stats, err := workspacepkg.CopyTree(absoluteSource, destination, workspaceTransferOverwrite)
		if err != nil {
			return err
		}

		payload := workspaceTransferPayload{
			Workspace:   paths.Name,
			Source:      relativeSource,
			Destination: destination,
			Kind:        stats.Kind,
			FileCount:   stats.FileCount,
			ByteCount:   stats.ByteCount,
		}
		return renderWorkspaceTransfer(cmd, payload, "get")
	},
}

func renderWorkspaceTransfer(cmd *cobra.Command, payload workspaceTransferPayload, verb string) error {
	w := output.FromContext(cmd.Context())
	if w.IsJSON() {
		return w.JSON(payload)
	}
	w.Line("Workspace: %s", payload.Workspace)
	w.Line("Action: %s", verb)
	w.Line("Source: %s", payload.Source)
	w.Line("Destination: %s", payload.Destination)
	w.Line("Kind: %s", payload.Kind)
	w.Line("Files: %d", payload.FileCount)
	w.Line("Bytes: %d", payload.ByteCount)
	return nil
}

func init() {
	rootCmd.AddCommand(workspaceCmd)

	workspaceCmd.AddCommand(workspaceListCmd)
	workspaceCmd.AddCommand(workspaceCreateCmd)
	workspaceDeleteCmd.Flags().BoolVar(&workspaceDeleteYes, "yes", false, "confirm deletion")
	workspaceCmd.AddCommand(workspaceDeleteCmd)
	workspaceCmd.AddCommand(workspaceCurrentCmd)
	workspaceCmd.AddCommand(workspaceSwitchCmd)
	workspacePutCmd.Flags().BoolVar(&workspaceTransferOverwrite, "overwrite", false, "overwrite the destination path if it already exists")
	workspaceGetCmd.Flags().BoolVar(&workspaceTransferOverwrite, "overwrite", false, "overwrite the destination path if it already exists")
	workspaceCmd.AddCommand(workspacePutCmd)
	workspaceCmd.AddCommand(workspaceGetCmd)
}
