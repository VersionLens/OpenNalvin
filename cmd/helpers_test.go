package cmd

import (
	"context"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func TestActiveWorkspaceConfigUsesResolvedWorkspaceFromContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	baseCfg := config.Config{}
	baseCfg.Workspace.Current = "alpha"

	ctx := config.WithContext(context.Background(), baseCfg)
	ctx = workspacepkg.WithPaths(ctx, workspacepkg.Paths{
		Name:      "beta",
		DBPath:    "/tmp/beta.sqlite",
		FilesPath: "/tmp/beta",
	})

	activeCfg, activePaths, err := activeWorkspaceConfig(ctx)
	if err != nil {
		t.Fatalf("active workspace config: %v", err)
	}
	if activeCfg.Workspace.Current != "beta" {
		t.Fatalf("expected active workspace current beta, got %q", activeCfg.Workspace.Current)
	}
	if activePaths.Name != "beta" {
		t.Fatalf("expected active workspace paths beta, got %q", activePaths.Name)
	}

	storedCfg, ok := config.FromContext(ctx)
	if !ok {
		t.Fatal("expected config in context")
	}
	if storedCfg.Workspace.Current != "alpha" {
		t.Fatalf("expected persisted config in context to remain alpha, got %q", storedCfg.Workspace.Current)
	}
}

func TestWithAgentSubagentProviderOverrideUpdatesConfigInContext(t *testing.T) {
	baseCfg := config.Config{}
	baseCfg.Agent.Subagents.ProviderName = "default-child"

	ctx := config.WithContext(context.Background(), baseCfg)
	next := withAgentSubagentProviderOverride(ctx, " anthropic ")

	gotCfg, ok := config.FromContext(next)
	if !ok {
		t.Fatal("expected config in context")
	}
	if gotCfg.Agent.Subagents.ProviderName != "anthropic" {
		t.Fatalf("expected overridden subagent provider anthropic, got %q", gotCfg.Agent.Subagents.ProviderName)
	}

	originalCfg, ok := config.FromContext(ctx)
	if !ok {
		t.Fatal("expected original config in context")
	}
	if originalCfg.Agent.Subagents.ProviderName != "default-child" {
		t.Fatalf("expected original context to remain unchanged, got %q", originalCfg.Agent.Subagents.ProviderName)
	}
}
