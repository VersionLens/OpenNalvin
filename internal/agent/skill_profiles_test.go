package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func TestParseSkillMarkdownIncludesProfileMetadata(t *testing.T) {
	skill, err := parseSkillMarkdown(`---
name: test-profile
description: Example profile skill
metadata:
  agent:
    activation: manual
    run_scopes: [child]
    profile:
      provider: macmini
      fallback_providers: [backup-a, backup-b]
      pinned_tools: [tool_a]
      enabled_tools: [tool_b]
      exclusive_tools: [tool_a, tool_b]
---

body`, "workspace:/tmp/test-profile/SKILL.md", "workspace")
	if err != nil {
		t.Fatalf("parse skill markdown: %v", err)
	}
	if skill.Profile == nil {
		t.Fatal("expected profile metadata to be parsed")
	}
	if skill.Profile.Provider != "macmini" {
		t.Fatalf("unexpected provider: %q", skill.Profile.Provider)
	}
	if got, want := skill.Profile.FallbackProviders, []string{"backup-a", "backup-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected fallback providers: got %v want %v", got, want)
	}
	if got, want := skill.Profile.PinnedTools, []string{"tool_a"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected pinned tools: got %v want %v", got, want)
	}
	if got, want := skill.Profile.EnabledTools, []string{"tool_b"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected enabled tools: got %v want %v", got, want)
	}
	if got, want := skill.Profile.ExclusiveTools, []string{"tool_a", "tool_b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected exclusive tools: got %v want %v", got, want)
	}
}

func TestResolveSkillProfileRunConfigRejectsMissingSkill(t *testing.T) {
	_, _, _, _, err := ResolveSkillProfileRunConfig(context.Background(), configpkg.Config{}, RunKindChild, []string{"does-not-exist"}, "", ToolSelection{})
	if err == nil || err.Error() != `skill "does-not-exist" not found` {
		t.Fatalf("expected missing skill error, got %v", err)
	}
}

func TestResolveSkillProfileRunConfigRejectsConflictingProfileProviders(t *testing.T) {
	filesPath := t.TempDir()
	ctx := workspacepkg.WithPaths(context.Background(), workspacepkg.Paths{FilesPath: filesPath})

	writeSkill := func(name, provider string) {
		path := filepath.Join(filesPath, ".nalvin", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
		content := `---
name: ` + name + `
description: Test profile skill
metadata:
  agent:
    activation: manual
    run_scopes: [child]
    profile:
      provider: ` + provider + `
---

body`
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write skill: %v", err)
		}
	}

	writeSkill("profile-a", "provider-a")
	writeSkill("profile-b", "provider-b")

	_, _, _, _, err := ResolveSkillProfileRunConfig(ctx, configpkg.Config{}, RunKindChild, []string{"profile-a", "profile-b"}, "", ToolSelection{})
	if err == nil || err.Error() != `conflicting skill profile providers for skills "profile-a" and "profile-b"` {
		t.Fatalf("expected conflicting provider error, got %v", err)
	}
}

func TestResolveSkillProfileRunConfigPreservesExplicitOverrides(t *testing.T) {
	filesPath := t.TempDir()
	ctx := workspacepkg.WithPaths(context.Background(), workspacepkg.Paths{FilesPath: filesPath})

	skillDir := filepath.Join(filesPath, ".nalvin", "skills", "test-profile")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `---
name: test-profile
description: explicit-overrides test
metadata:
  agent:
    activation: manual
    run_scopes: [root, child]
    profile:
      provider: macmini
      pinned_tools: [profile_pinned]
      enabled_tools: [profile_enabled]
---

body`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	toolsIn := ToolSelection{
		EnabledToolIDs: []string{"custom_tool"},
		PinnedToolIDs:  []string{"other_tool"},
	}
	providerName, fallbackProviders, tools, _, err := ResolveSkillProfileRunConfig(ctx, configpkg.Config{}, RunKindRoot, []string{"test-profile"}, "manual-provider", toolsIn)
	if err != nil {
		t.Fatalf("resolve skill profile run config: %v", err)
	}
	if providerName != "manual-provider" {
		t.Fatalf("expected explicit provider to win, got %q", providerName)
	}
	if len(fallbackProviders) != 0 {
		t.Fatalf("expected no fallback providers, got %v", fallbackProviders)
	}
	if len(tools.EnabledToolIDs) != 1 || tools.EnabledToolIDs[0] != "custom_tool" {
		t.Fatalf("expected explicit enabled tools to win, got %v", tools.EnabledToolIDs)
	}
	if len(tools.PinnedToolIDs) != 1 || tools.PinnedToolIDs[0] != "other_tool" {
		t.Fatalf("expected explicit pinned tools to win, got %v", tools.PinnedToolIDs)
	}
}
