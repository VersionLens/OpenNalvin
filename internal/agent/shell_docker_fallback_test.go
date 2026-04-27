package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

// requireDocker skips the test when a working docker binary is not available
// on PATH. Container-creating tests gate on this so make test-go passes on
// machines without docker.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker binary not available: %v", err)
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
}

func TestBashAliasAcceptsCommandAndScript(t *testing.T) {
	rt, ctx, _, cleanup := newDockerToolRuntime(t, ToolSelection{})
	defer cleanup()

	// Disable docker fallback for this purely-builtin path test.
	rt.cfg.Agent.Shell.DockerFallback.Enabled = false

	resp, err := rt.runBash(ctx, bashInput{Command: `echo bash-cmd`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected bash error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "bash-cmd") {
		t.Fatalf("expected 'bash-cmd' in output, got %q", resp.Content)
	}

	resp, err = rt.runBash(ctx, bashInput{Script: `echo bash-script`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run bash with script alias: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected bash error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, "bash-script") {
		t.Fatalf("expected 'bash-script' in output, got %q", resp.Content)
	}

	// command and script that disagree should be rejected.
	resp, err = rt.runBash(ctx, bashInput{Command: "echo a", Script: "echo b"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run bash with conflicting input: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected conflicting bash input to be rejected")
	}
}

func TestShellDispatchOrder(t *testing.T) {
	rt, ctx, _, cleanup := newDockerToolRuntime(t, ToolSelection{})
	defer cleanup()
	rt.cfg.Agent.Shell.DockerFallback.Enabled = false

	// Builtins win over agent tools (echo is a shell builtin).
	resp, err := rt.runShell(ctx, shellInput{Script: `echo hello`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if resp.IsError || !strings.Contains(resp.Content, "hello") {
		t.Fatalf("expected builtin echo output, got %q (err=%v)", resp.Content, resp.IsError)
	}

	// Recursive shell/bash/search_tools are blocked, even if a tool with that
	// id exists.
	resp, err = rt.runShell(ctx, shellInput{Script: `bash -c "echo nope"`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected recursive bash to be rejected")
	}
	if !strings.Contains(resp.Content, "not available inside shell") {
		t.Fatalf("expected blocked-name error, got %q", resp.Content)
	}

	resp, err = rt.runShell(ctx, shellInput{Script: `shell --foo`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected recursive shell to be rejected")
	}

	// With fallback disabled, unknown command is rejected with 127.
	resp, err = rt.runShell(ctx, shellInput{Script: `definitely-not-a-real-cmd`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected unknown command to be rejected when fallback disabled")
	}
	if !strings.Contains(resp.Content, "command not found") {
		t.Fatalf("expected 'command not found' error, got %q", resp.Content)
	}
}

func TestShellLiteralDockerRoutesScopedRunner(t *testing.T) {
	rt, ctx, logPath, cleanup := newDockerToolRuntime(t, ToolSelection{})
	defer cleanup()

	// Even with fallback enabled, literal `docker ...` must use the scoped
	// docker runner (going through validateDockerArgs), not the fallback.
	rt.cfg.Agent.Shell.DockerFallback.Enabled = true

	resp, err := rt.runShell(ctx, shellInput{Script: `docker ps`}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected shell error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"ID":"abc123"`) {
		t.Fatalf("expected docker ps output, got %q", resp.Content)
	}
	log := readDockerToolLog(t, logPath)
	if !strings.Contains(log, "arg=ps") {
		t.Fatalf("expected docker ps in log, got:\n%s", log)
	}
}

func TestShellFallbackContainerName(t *testing.T) {
	name := shellFallbackContainerName("Project Alpha/v2")
	if !strings.HasPrefix(name, "nalvin-shell-fallback-project-alpha-v2-") {
		t.Fatalf("unexpected container name prefix: %s", name)
	}
	empty := shellFallbackContainerName("")
	if !strings.HasPrefix(empty, "nalvin-shell-fallback-workspace-") {
		t.Fatalf("expected fallback to use 'workspace' default, got %s", empty)
	}
}

func TestShellContainerWorkdirMapping(t *testing.T) {
	tmpRoot := t.TempDir()
	workspaceDir := filepath.Join(tmpRoot, "ws")
	scratchDir := filepath.Join(tmpRoot, "scratch")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(scratchDir, "ext"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if got := shellContainerWorkdir(workspaceDir, scratchDir, workspaceDir); got != "/workspace" {
		t.Fatalf("workspace root: got %q want /workspace", got)
	}
	if got := shellContainerWorkdir(workspaceDir, scratchDir, filepath.Join(workspaceDir, "sub")); got != "/workspace/sub" {
		t.Fatalf("workspace sub: got %q", got)
	}
	if got := shellContainerWorkdir(workspaceDir, scratchDir, scratchDir); got != "/tmp" {
		t.Fatalf("scratch root: got %q want /tmp", got)
	}
	if got := shellContainerWorkdir(workspaceDir, scratchDir, filepath.Join(scratchDir, "ext")); got != "/tmp/ext" {
		t.Fatalf("scratch sub: got %q", got)
	}
	// Unknown dir falls back to /workspace.
	if got := shellContainerWorkdir(workspaceDir, scratchDir, tmpRoot); got != "/workspace" {
		t.Fatalf("unknown dir: got %q want /workspace fallback", got)
	}
}

// TestShellFallbackCreatesContainerAndSyncsWorkspace exercises the full lazy
// container lifecycle against a real docker daemon: it writes a file from
// inside the container (workspace mount) and a file in /tmp that must NOT
// sync back to the host workspace.
func TestShellFallbackCreatesContainerAndSyncsWorkspace(t *testing.T) {
	requireDocker(t)

	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "workspaces", "alpha")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	cfg := configpkg.Config{
		Docker: configpkg.DockerConfig{
			DefaultImage: "alpine:3.19",
		},
		Workspace: configpkg.WorkspaceConfig{
			Current:   "alpha",
			DBRoot:    filepath.Join(home, "workspace-db"),
			FilesRoot: filepath.Join(home, "workspaces"),
		},
		Agent: configpkg.AgentConfig{
			Shell: configpkg.AgentShellConfig{
				DockerFallback: configpkg.AgentShellDockerFallbackConfig{
					Enabled: true,
					Image:   "alpine:3.19",
				},
			},
		},
	}

	paths := workspacepkg.Paths{
		Name:      "alpha",
		DBPath:    filepath.Join(home, "workspace-db", "alpha.sqlite"),
		FilesPath: workspaceRoot,
	}

	ctx := configpkg.WithContext(context.Background(), cfg)
	ctx = workspacepkg.WithPaths(ctx, paths)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "shell-fallback-real"), StoredTrace{}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() {
		if rt.mcpManager != nil {
			_ = rt.mcpManager.Close()
		}
	}()

	// Issue a container-side write to /workspace and another to /tmp; the
	// /workspace one should sync back to the host, the /tmp one must not.
	script := `sh -c "echo from-container > /workspace/from-container.txt && echo scratch > /tmp/scratch-only.txt"`
	resp, err := rt.runShell(ctx, shellInput{Script: script, Timeout: 60}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected shell error: %s", resp.Content)
	}

	if data, err := os.ReadFile(filepath.Join(workspaceRoot, "from-container.txt")); err != nil || string(data) != "from-container\n" {
		t.Fatalf("workspace write did not sync back, data=%q err=%v", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "scratch-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected /tmp write to NOT sync back, stat err=%v", err)
	}
}
