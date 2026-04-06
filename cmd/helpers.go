package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/workspace"
)

func expandHomePath(path string) string {
	if path == "" || path == "~" {
		return path
	}
	if path == "~/" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func resolvedWorkspaceName(cfg config.Config) string {
	if strings.TrimSpace(workspaceName) != "" {
		return strings.TrimSpace(workspaceName)
	}
	return strings.TrimSpace(cfg.Workspace.Current)
}

func activeWorkspaceConfig(ctx context.Context) (config.Config, workspace.Paths, error) {
	cfg, ok := config.FromContext(ctx)
	if !ok {
		return config.Config{}, workspace.Paths{}, fmt.Errorf("config not found in command context")
	}

	activePaths, ok := workspace.PathsFromContext(ctx)
	if !ok {
		var err error
		activePaths, err = workspace.ResolvePaths(cfg, workspaceName)
		if err != nil {
			return config.Config{}, workspace.Paths{}, err
		}
	}

	cfg.Workspace.Current = activePaths.Name
	return cfg, activePaths, nil
}

func openWorkspaceDB(ctx context.Context) (*sql.DB, workspace.Paths, config.Config, error) {
	cfg, activePaths, err := activeWorkspaceConfig(ctx)
	if err != nil {
		return nil, workspace.Paths{}, config.Config{}, err
	}

	db, paths, err := workspace.OpenDB(ctx, cfg, activePaths.Name)
	if err != nil {
		return nil, workspace.Paths{}, config.Config{}, err
	}
	return db, paths, cfg, nil
}

func withAgentSubagentProviderOverride(ctx context.Context, providerName string) context.Context {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return ctx
	}

	cfg, ok := config.FromContext(ctx)
	if !ok {
		return ctx
	}
	cfg.Agent.Subagents.ProviderName = providerName
	return config.WithContext(ctx, cfg)
}
