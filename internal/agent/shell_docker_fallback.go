package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"mvdan.cc/sh/v3/interp"
)

// nonContainerNameChars matches characters that are not legal in Docker
// container names. Docker accepts /[a-zA-Z0-9][a-zA-Z0-9_.-]+/ for the first
// character and /[a-zA-Z0-9_.-]/ for the rest.
var nonContainerNameChars = regexp.MustCompile(`[^a-z0-9_.-]+`)

// snapshotShellScratch creates a per-shell-invocation scratch directory used
// for the /tmp mount inside the docker fallback container. The directory is
// not synced back to the workspace.
func snapshotShellScratch() (scratchDir string, cleanup func(), err error) {
	cleanup = func() {}
	scratchDir, err = os.MkdirTemp("", "nalvin-shell-tmp-*")
	if err != nil {
		return "", cleanup, fmt.Errorf("create shell scratch tmp: %w", err)
	}
	cleanup = func() { os.RemoveAll(scratchDir) }
	return scratchDir, cleanup, nil
}

// shellDockerFallback runs commands that are neither shell builtins nor visible
// agent tools inside a temporary docker container with the workspace mounted
// at /workspace and a scratch tmpfs at /tmp. The container is created lazily
// on the first fallback command and torn down at the end of the shell
// invocation. It mirrors the upstream "transparent shell -> docker" behavior.
type shellDockerFallback struct {
	rt           *agentRuntime
	shellCtx     context.Context
	workspaceDir string
	scratchDir   string
	container    string
	started      bool
}

func newShellDockerFallback(rt *agentRuntime, shellCtx context.Context, workspaceDir, scratchDir string) *shellDockerFallback {
	return &shellDockerFallback{
		rt:           rt,
		shellCtx:     shellCtx,
		workspaceDir: workspaceDir,
		scratchDir:   scratchDir,
	}
}

// enabled reports whether the agent config allows the docker fallback.
func (f *shellDockerFallback) enabled() bool {
	if f == nil || f.rt == nil {
		return false
	}
	return f.rt.cfg.Agent.Shell.DockerFallback.Enabled
}

// fallbackImage returns the image to use for the fallback container.
func (f *shellDockerFallback) fallbackImage() string {
	if f == nil || f.rt == nil {
		return ""
	}
	if image := strings.TrimSpace(f.rt.cfg.Agent.Shell.DockerFallback.Image); image != "" {
		return image
	}
	return f.rt.cfg.Docker.DefaultImage
}

// exec runs args inside the fallback container, lazily creating it if needed.
func (f *shellDockerFallback) exec(ctx context.Context, args []string) error {
	if f == nil {
		return interp.ExitStatus(127)
	}
	hc := interp.HandlerCtx(ctx)
	container, err := f.ensure(ctx)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "shell: %s: Docker fallback unavailable: %v\n", args[0], err)
		return interp.ExitStatus(127)
	}
	result, err := dockerpkg.NewRunner(f.rt.cfg).ExecStream(f.shellCtx, dockerpkg.ExecStreamOptions{
		CWD:       f.workspaceDir,
		Container: container,
		Workdir:   shellContainerWorkdir(f.workspaceDir, f.scratchDir, hc.Dir),
		Command:   append([]string(nil), args...),
		Stdin:     stdinOrEmpty(hc),
		Stdout:    hc.Stdout,
		Stderr:    hc.Stderr,
	})
	if err != nil {
		fmt.Fprintf(hc.Stderr, "shell: %s: Docker fallback exec failed: %v\n", args[0], err)
		return interp.ExitStatus(1)
	}
	if !result.OK {
		return interp.ExitStatus(uint8(result.ExitCode))
	}
	return nil
}

// ensure creates and starts the fallback container on first use.
func (f *shellDockerFallback) ensure(ctx context.Context) (string, error) {
	if f.container != "" {
		return f.container, nil
	}
	image := f.fallbackImage()
	if strings.TrimSpace(image) == "" {
		return "", fmt.Errorf("docker fallback image is not configured")
	}

	workspaceName := ""
	if paths, ok := workspacepkg.PathsFromContext(f.shellCtx); ok {
		workspaceName = paths.Name
	}
	name := shellFallbackContainerName(workspaceName)

	runner := dockerpkg.NewRunner(f.rt.cfg)
	createOpts := dockerpkg.CreateOptions{
		CWD:                f.workspaceDir,
		Name:               name,
		Image:              image,
		MountWorkspace:     true,
		WorkspacePath:      f.workspaceDir,
		Workdir:            dockerpkg.DefaultWorkspaceMountTarget,
		SkipGitCredentials: true,
		Binds: []dockerpkg.BindMount{
			{
				Source: f.scratchDir,
				Target: "/tmp",
			},
		},
	}
	if strings.EqualFold(strings.TrimSpace(f.rt.cfg.Agent.Shell.DockerFallback.Network), "off") {
		createOpts.Network = "none"
	}

	payload, err := runner.CreateContainer(ctx, createOpts)
	if err != nil {
		return "", err
	}
	f.container = strings.TrimSpace(payload.Name)
	if f.container == "" {
		f.container = strings.TrimSpace(payload.ContainerID)
	}
	if f.container == "" {
		f.container = name
	}
	if _, err := runner.StartContainer(ctx, f.workspaceDir, "", f.container); err != nil {
		_, _ = runner.RemoveContainer(context.Background(), f.workspaceDir, "", f.container)
		f.container = ""
		return "", err
	}
	f.started = true
	return f.container, nil
}

// cleanup stops and removes the fallback container if it was created.
func (f *shellDockerFallback) cleanup(ctx context.Context) {
	if f == nil || f.container == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runner := dockerpkg.NewRunner(f.rt.cfg)
	if f.started {
		if _, err := runner.StopContainer(cleanupCtx, f.workspaceDir, "", f.container); err != nil {
			f.rt.session.debugf("shell fallback docker stop error: %v", err)
		}
	}
	if _, err := runner.RemoveContainer(cleanupCtx, f.workspaceDir, "", f.container); err != nil {
		f.rt.session.debugf("shell fallback docker rm error: %v", err)
	}
}

// shellFallbackContainerName builds a unique-but-stable-prefix container name
// from the active workspace name. It guarantees each shell invocation gets a
// distinct container so concurrent calls don't collide.
func shellFallbackContainerName(workspaceName string) string {
	workspaceName = strings.ToLower(strings.TrimSpace(workspaceName))
	workspaceName = nonContainerNameChars.ReplaceAllString(workspaceName, "-")
	workspaceName = strings.Trim(workspaceName, "-")
	if workspaceName == "" {
		workspaceName = "workspace"
	}
	return fmt.Sprintf("nalvin-shell-fallback-%s-%d-%d", workspaceName, os.Getpid(), time.Now().UnixNano())
}

// shellContainerWorkdir maps the host shell's current dir to a container path.
// Paths inside the workspace snapshot map under /workspace; paths inside the
// scratch dir map under /tmp; anything else falls back to /workspace.
func shellContainerWorkdir(workspaceDir, scratchDir, shellDir string) string {
	shellDir = filepath.Clean(shellDir)
	if scratchDir != "" && pathWithinDir(shellDir, scratchDir) {
		rel, err := filepath.Rel(filepath.Clean(scratchDir), shellDir)
		if err == nil && rel != "." {
			return filepath.ToSlash(filepath.Join("/tmp", rel))
		}
		return "/tmp"
	}
	if pathWithinDir(shellDir, workspaceDir) {
		rel, err := filepath.Rel(filepath.Clean(workspaceDir), shellDir)
		if err == nil && rel != "." {
			return filepath.ToSlash(filepath.Join(dockerpkg.DefaultWorkspaceMountTarget, rel))
		}
	}
	return dockerpkg.DefaultWorkspaceMountTarget
}

// pathWithinDir returns true if path is path-equal to or a descendant of dir.
func pathWithinDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if cleanPath == cleanDir {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanDir+string(filepath.Separator))
}

// runShellDockerCLI routes a literal `docker ...` shell command through the
// scoped docker runner so the agent cannot run arbitrary host docker actions.
func (rt *agentRuntime) runShellDockerCLI(shellCtx, handlerCtx context.Context, tmpDir string, args []string) error {
	hc := interp.HandlerCtx(handlerCtx)
	result, err := dockerpkg.NewRunner(rt.cfg).RunArgv(shellCtx, dockerpkg.RawRunArgvOptions{
		CWD:           tmpDir,
		WorkspaceRoot: tmpDir,
		Args:          append([]string(nil), args...),
	})
	if err != nil {
		fmt.Fprintf(hc.Stderr, "docker: %v\n", err)
		return interp.ExitStatus(1)
	}
	if result.Stdout != "" {
		fmt.Fprint(hc.Stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprint(hc.Stderr, result.Stderr)
	}
	if !result.OK {
		return interp.ExitStatus(uint8(result.ExitCode))
	}
	return nil
}
