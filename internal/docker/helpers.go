package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	gitpkg "github.com/versionlens/OpenNalvin/internal/git"
	xssh "golang.org/x/crypto/ssh"
)

const (
	defaultKeepAliveCommand = "while true; do sleep 3600; done"
	gitMountDir             = "/run/nalvin-git"
	containerGitKeyPath     = gitMountDir + "/id_nalvin"
	containerKnownHostsPath = gitMountDir + "/known_hosts"
	defaultDevImage         = "nalvin/dev"
	devContainerUser        = "nalvin"
	devContainerHome        = "/home/" + devContainerUser
	devAuthorizedKeysPath   = devContainerHome + "/.ssh/authorized_keys"
	devClaudeConfigDir      = devContainerHome + "/.claude"
	devCodexConfigDir       = devContainerHome + "/.codex"
	devSSHContainerPort     = "22/tcp"
	devCodeServerPort       = "8080/tcp"
)

type ContainerSummary struct {
	ID             string          `json:"id,omitempty"`
	Image          string          `json:"image,omitempty"`
	Command        string          `json:"command,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty"`
	RunningFor     string          `json:"running_for,omitempty"`
	Ports          string          `json:"ports,omitempty"`
	Status         string          `json:"status,omitempty"`
	State          string          `json:"state,omitempty"`
	Names          string          `json:"names,omitempty"`
	Mounts         string          `json:"mounts,omitempty"`
	Networks       string          `json:"networks,omitempty"`
	LocalVolumes   string          `json:"local_volumes,omitempty"`
	PublishedPorts []PublishedPort `json:"published_ports,omitempty"`
	SSHEndpoint    string          `json:"ssh_endpoint,omitempty"`
	CodeServerURL  string          `json:"code_server_url,omitempty"`
}

type PublishedPort struct {
	ContainerPort string `json:"container_port,omitempty"`
	HostIP        string `json:"host_ip,omitempty"`
	HostPort      string `json:"host_port,omitempty"`
}

type ImageSummary struct {
	ID           string `json:"id,omitempty"`
	Repository   string `json:"repository,omitempty"`
	Tag          string `json:"tag,omitempty"`
	Digest       string `json:"digest,omitempty"`
	CreatedSince string `json:"created_since,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	Size         string `json:"size,omitempty"`
}

type NetworkSummary struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Driver string `json:"driver,omitempty"`
	Scope  string `json:"scope,omitempty"`
}

type VolumeSummary struct {
	Driver     string `json:"driver,omitempty"`
	Name       string `json:"name,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Labels     string `json:"labels,omitempty"`
	Links      string `json:"links,omitempty"`
	Size       string `json:"size,omitempty"`
}

type BindMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type VolumeMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type BuildOptions struct {
	Host        string
	CWD         string
	ContextPath string
	Dockerfile  string
	Tag         string
	Target      string
	BuildArgs   map[string]string
}

type BuildImageResult struct {
	ContextPath string        `json:"context_path"`
	Dockerfile  string        `json:"dockerfile,omitempty"`
	Tag         string        `json:"tag,omitempty"`
	Target      string        `json:"target,omitempty"`
	Result      CommandResult `json:"result"`
}

type CreateOptions struct {
	Host           string
	CWD            string
	Image          string
	Name           string
	WorkspacePath  string
	RepoPath       string
	MountWorkspace bool
	Binds          []BindMount
	Volumes        []VolumeMount
	Env            map[string]string
	Network        string
	Workdir        string
	Ports          []string
	Command        []string
}

type CreateContainerResult struct {
	ContainerID    string          `json:"container_id,omitempty"`
	Image          string          `json:"image"`
	Name           string          `json:"name,omitempty"`
	Workdir        string          `json:"workdir,omitempty"`
	MountWorkspace bool            `json:"mount_workspace,omitempty"`
	RepoPath       string          `json:"repo_path,omitempty"`
	PublishedPorts []PublishedPort `json:"published_ports,omitempty"`
	SSHEndpoint    string          `json:"ssh_endpoint,omitempty"`
	CodeServerURL  string          `json:"code_server_url,omitempty"`
	Result         CommandResult   `json:"result"`
}

type PullImageResult struct {
	Image  string        `json:"image"`
	Result CommandResult `json:"result"`
}

type ContainerActionResult struct {
	Container string        `json:"container"`
	Result    CommandResult `json:"result"`
}

type ExecResult struct {
	Container string   `json:"container"`
	OK        bool     `json:"ok"`
	ExitCode  int      `json:"exit_code"`
	Stdout    string   `json:"stdout,omitempty"`
	Stderr    string   `json:"stderr,omitempty"`
	Argv      []string `json:"argv"`
	CWD       string   `json:"cwd"`
}

func (r *Runner) ListContainers(ctx context.Context, cwd, host string) ([]ContainerSummary, CommandResult, error) {
	result, err := r.runArgv(ctx, cwd, host, nil, []string{"ps", "-a", "--format", "{{json .}}"})
	if err != nil {
		return nil, CommandResult{}, err
	}
	if !result.OK {
		return nil, result, nil
	}
	items, err := parseContainers(result.Stdout)
	if err != nil {
		return nil, result, err
	}
	return r.enrichContainerSummaries(ctx, cwd, host, items), result, nil
}

func (r *Runner) ListImages(ctx context.Context, cwd, host string) ([]ImageSummary, CommandResult, error) {
	result, err := r.runArgv(ctx, cwd, host, nil, []string{"images", "--format", "{{json .}}"})
	if err != nil {
		return nil, CommandResult{}, err
	}
	if !result.OK {
		return nil, result, nil
	}
	items, err := parseImages(result.Stdout)
	return items, result, err
}

func (r *Runner) ListNetworks(ctx context.Context, cwd, host string) ([]NetworkSummary, CommandResult, error) {
	result, err := r.runArgv(ctx, cwd, host, nil, []string{"network", "ls", "--format", "{{json .}}"})
	if err != nil {
		return nil, CommandResult{}, err
	}
	if !result.OK {
		return nil, result, nil
	}
	items, err := parseNetworks(result.Stdout)
	return items, result, err
}

func (r *Runner) ListVolumes(ctx context.Context, cwd, host string) ([]VolumeSummary, CommandResult, error) {
	result, err := r.runArgv(ctx, cwd, host, nil, []string{"volume", "ls", "--format", "{{json .}}"})
	if err != nil {
		return nil, CommandResult{}, err
	}
	if !result.OK {
		return nil, result, nil
	}
	items, err := parseVolumes(result.Stdout)
	return items, result, err
}

func (r *Runner) PullImage(ctx context.Context, cwd, host, image string) (PullImageResult, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return PullImageResult{}, fmt.Errorf("image is required")
	}
	result, err := r.runArgv(ctx, cwd, host, nil, []string{"pull", image})
	if err != nil {
		return PullImageResult{}, err
	}
	return PullImageResult{Image: image, Result: result}, nil
}

func (r *Runner) BuildImage(ctx context.Context, opts BuildOptions) (BuildImageResult, error) {
	contextPath := strings.TrimSpace(opts.ContextPath)
	if contextPath == "" {
		return BuildImageResult{}, fmt.Errorf("context path is required")
	}

	args := []string{"build"}
	if dockerfile := strings.TrimSpace(opts.Dockerfile); dockerfile != "" {
		args = append(args, "--file", dockerfile)
	}
	if tag := strings.TrimSpace(opts.Tag); tag != "" {
		args = append(args, "--tag", tag)
	}
	if target := strings.TrimSpace(opts.Target); target != "" {
		args = append(args, "--target", target)
	}
	for _, key := range sortedKeys(opts.BuildArgs) {
		args = append(args, "--build-arg", key+"="+opts.BuildArgs[key])
	}
	args = append(args, contextPath)

	result, err := r.runArgv(ctx, opts.CWD, opts.Host, nil, args)
	if err != nil {
		return BuildImageResult{}, err
	}

	return BuildImageResult{
		ContextPath: contextPath,
		Dockerfile:  strings.TrimSpace(opts.Dockerfile),
		Tag:         strings.TrimSpace(opts.Tag),
		Target:      strings.TrimSpace(opts.Target),
		Result:      result,
	}, nil
}

func (r *Runner) CreateContainer(ctx context.Context, opts CreateOptions) (CreateContainerResult, error) {
	if opts.MountWorkspace && strings.TrimSpace(opts.RepoPath) != "" {
		return CreateContainerResult{}, fmt.Errorf("mount_workspace and repo_path cannot be combined")
	}

	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = strings.TrimSpace(r.cfg.Docker.DefaultImage)
	}
	if image == "" {
		image = defaultDevImage
	}
	devImage := isDevImage(image)

	args := []string{"create"}
	if name := strings.TrimSpace(opts.Name); name != "" {
		args = append(args, "--name", name)
	}
	if network := strings.TrimSpace(opts.Network); network != "" {
		args = append(args, "--network", network)
	}

	env, credentialMounts, err := r.createContainerEnv(opts.Env)
	if err != nil {
		return CreateContainerResult{}, err
	}
	for _, item := range env {
		args = append(args, "--env", item)
	}
	for _, bind := range credentialMounts {
		args = append(args, "--volume", formatBindMount(bind))
	}
	for _, host := range gitpkg.DockerExtraHosts(r.cfg) {
		args = append(args, "--add-host", host)
	}
	for _, port := range opts.Ports {
		if p := strings.TrimSpace(port); p != "" {
			args = append(args, "--publish", p)
		}
	}
	if devImage {
		for _, publish := range devPublishedPortArgs() {
			args = append(args, "--publish", publish)
		}
		authorizedKeysPath, err := ensureDevAuthorizedKeysFile(r.cfg)
		if err != nil {
			return CreateContainerResult{}, err
		}
		args = append(args, "--volume", formatBindMount(BindMount{
			Source:   authorizedKeysPath,
			Target:   devAuthorizedKeysPath,
			ReadOnly: true,
		}))
		for _, volume := range devAuthVolumeMounts(strings.TrimSpace(opts.Name)) {
			args = append(args, "--volume", formatVolumeMount(volume))
		}
	}

	if opts.MountWorkspace {
		args = append(args, "--volume", formatBindMount(BindMount{
			Source: opts.WorkspacePath,
			Target: DefaultWorkspaceMountTarget,
		}))
	}
	if repoPath := strings.TrimSpace(opts.RepoPath); repoPath != "" {
		args = append(args, "--volume", formatBindMount(BindMount{
			Source: repoPath,
			Target: DefaultWorkspaceMountTarget,
		}))
	}
	for _, bind := range opts.Binds {
		args = append(args, "--volume", formatBindMount(bind))
	}
	for _, volume := range opts.Volumes {
		args = append(args, "--volume", formatVolumeMount(volume))
	}

	workdir := strings.TrimSpace(opts.Workdir)
	if workdir == "" && (opts.MountWorkspace || strings.TrimSpace(opts.RepoPath) != "") {
		workdir = DefaultWorkspaceMountTarget
	}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}

	command := opts.Command
	if len(command) == 0 && !devImage {
		args = append(args, "--entrypoint", defaultCreateEntrypoint())
		command = defaultCreateCommand()
	}
	args = append(args, image)
	args = append(args, command...)

	result, err := r.runArgv(ctx, opts.CWD, opts.Host, nil, args)
	if err != nil {
		return CreateContainerResult{}, err
	}

	payload := CreateContainerResult{
		ContainerID:    strings.TrimSpace(firstLine(result.Stdout)),
		Image:          image,
		Name:           strings.TrimSpace(opts.Name),
		Workdir:        workdir,
		MountWorkspace: opts.MountWorkspace,
		RepoPath:       strings.TrimSpace(opts.RepoPath),
		Result:         result,
	}
	if endpoints, err := r.inspectContainerEndpoints(ctx, opts.CWD, opts.Host, payload.ContainerID); err == nil {
		payload.PublishedPorts = endpoints.PublishedPorts
		payload.SSHEndpoint = endpoints.SSHEndpoint
		payload.CodeServerURL = endpoints.CodeServerURL
	}
	return payload, nil
}

func (r *Runner) StartContainer(ctx context.Context, cwd, host, container string) (ContainerActionResult, error) {
	return r.runContainerAction(ctx, cwd, host, "start", container)
}

func (r *Runner) StopContainer(ctx context.Context, cwd, host, container string) (ContainerActionResult, error) {
	return r.runContainerAction(ctx, cwd, host, "stop", container)
}

func (r *Runner) RemoveContainer(ctx context.Context, cwd, host, container string) (ContainerActionResult, error) {
	return r.runContainerAction(ctx, cwd, host, "rm", container)
}

func (r *Runner) Exec(ctx context.Context, cwd, host, container string, command []string) (ExecResult, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return ExecResult{}, fmt.Errorf("container is required")
	}
	if len(command) == 0 {
		return ExecResult{}, fmt.Errorf("exec command is required")
	}

	envDefaults := ensureExecEnvDefaults()
	args := make([]string, 0, 2+len(envDefaults)+1+len(command))
	args = append(args, "exec")
	args = append(args, envDefaults...)
	args = append(args, container)
	args = append(args, command...)
	result, err := r.runArgv(ctx, cwd, host, nil, args)
	if err != nil {
		return ExecResult{}, err
	}

	return ExecResult{
		Container: container,
		OK:        result.OK,
		ExitCode:  result.ExitCode,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		Argv:      result.Argv,
		CWD:       result.CWD,
	}, nil
}

const (
	bgProcDir = "/tmp/nalvin-procs"
)

type ExecBackgroundResult struct {
	Container string   `json:"container"`
	ProcessID string   `json:"process_id"`
	OK        bool     `json:"ok"`
	Stderr    string   `json:"stderr,omitempty"`
	Argv      []string `json:"argv"`
}

type ExecTailResult struct {
	Container string `json:"container"`
	ProcessID string `json:"process_id"`
	OK        bool   `json:"ok"`
	Output    string `json:"output,omitempty"`
	Running   bool   `json:"running"`
	Stderr    string `json:"stderr,omitempty"`
}

type ExecSignalResult struct {
	Container string `json:"container"`
	ProcessID string `json:"process_id"`
	OK        bool   `json:"ok"`
	Stderr    string `json:"stderr,omitempty"`
}

type BackgroundProcess struct {
	ProcessID string `json:"process_id"`
	PID       string `json:"pid"`
	Running   bool   `json:"running"`
	Command   string `json:"command"`
}

type ExecListProcessesResult struct {
	Container string              `json:"container"`
	OK        bool                `json:"ok"`
	Processes []BackgroundProcess `json:"processes"`
	Stderr    string              `json:"stderr,omitempty"`
}

// ExecBackground starts a command inside a container as a background process
// with stdout/stderr redirected to a log file. The process is tracked by a
// user-chosen process_id, which can be used later with ExecTail and ExecSignal.
func (r *Runner) ExecBackground(ctx context.Context, cwd, host, container, processID string, command []string) (ExecBackgroundResult, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return ExecBackgroundResult{}, fmt.Errorf("container is required")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return ExecBackgroundResult{}, fmt.Errorf("process_id is required")
	}
	if len(command) == 0 {
		return ExecBackgroundResult{}, fmt.Errorf("exec command is required")
	}

	// Build wrapper script that:
	// 1. Creates the process tracking directory
	// 2. Stores the original command for listing
	// 3. Runs the command in the background with output redirected to a log file
	// 4. Records the PID and prints it to stdout
	// The shell exits immediately after recording the PID; the backgrounded
	// command keeps running inside the container.
	// Note: parentheses ensure only the command is backgrounded, not the
	// preceding mkdir/printf steps.
	wrapped := fmt.Sprintf(
		`mkdir -p %s; printf '%%s\n' %s > %s/%s.cmd; (%s) > %s/%s.log 2>&1 & echo $! > %s/%s.pid; echo $!`,
		bgProcDir,
		shellQuote(strings.Join(command, " ")), bgProcDir, processID,
		shellJoin(command), bgProcDir, processID,
		bgProcDir, processID,
	)

	envDefaults := ensureExecEnvDefaults()
	args := make([]string, 0, 2+len(envDefaults)+4)
	args = append(args, "exec")
	args = append(args, envDefaults...)
	args = append(args, container)
	args = append(args, "sh", "-c", wrapped)
	result, err := r.runArgv(ctx, cwd, host, nil, args)
	if err != nil {
		return ExecBackgroundResult{}, err
	}

	return ExecBackgroundResult{
		Container: container,
		ProcessID: processID,
		OK:        result.OK,
		Stderr:    result.Stderr,
		Argv:      result.Argv,
	}, nil
}

// ExecTail reads the last N lines of output from a background process's log file.
func (r *Runner) ExecTail(ctx context.Context, cwd, host, container, processID string, lines int) (ExecTailResult, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return ExecTailResult{}, fmt.Errorf("container is required")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return ExecTailResult{}, fmt.Errorf("process_id is required")
	}
	if lines <= 0 {
		lines = 50
	}

	// Read log and check if process is still alive, in a single exec call.
	script := fmt.Sprintf(
		`tail -n %d %s/%s.log 2>/dev/null; `+
			`printf '\n__NALVIN_RUNNING__'; `+
			`if [ -f %s/%s.pid ] && kill -0 "$(cat %s/%s.pid)" 2>/dev/null; then echo true; else echo false; fi`,
		lines, bgProcDir, processID,
		bgProcDir, processID, bgProcDir, processID,
	)

	envDefaults := ensureExecEnvDefaults()
	args := make([]string, 0, 2+len(envDefaults)+4)
	args = append(args, "exec")
	args = append(args, envDefaults...)
	args = append(args, container)
	args = append(args, "sh", "-c", script)
	result, err := r.runArgv(ctx, cwd, host, nil, args)
	if err != nil {
		return ExecTailResult{}, err
	}

	output := result.Stdout
	running := false
	if idx := strings.LastIndex(output, "\n__NALVIN_RUNNING__"); idx >= 0 {
		marker := output[idx+len("\n__NALVIN_RUNNING__"):]
		output = output[:idx]
		running = strings.TrimSpace(marker) == "true"
	}

	return ExecTailResult{
		Container: container,
		ProcessID: processID,
		OK:        result.OK,
		Output:    output,
		Running:   running,
		Stderr:    result.Stderr,
	}, nil
}

// ExecSignal sends a signal to a background process inside a container.
func (r *Runner) ExecSignal(ctx context.Context, cwd, host, container, processID, signal string) (ExecSignalResult, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return ExecSignalResult{}, fmt.Errorf("container is required")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return ExecSignalResult{}, fmt.Errorf("process_id is required")
	}
	signal = strings.TrimSpace(signal)
	if signal == "" {
		signal = "TERM"
	}

	script := fmt.Sprintf(
		`if [ -f %s/%s.pid ]; then kill -%s "$(cat %s/%s.pid)" 2>&1; else echo "no such process: %s" >&2; exit 1; fi`,
		bgProcDir, processID, signal, bgProcDir, processID, processID,
	)

	envDefaults := ensureExecEnvDefaults()
	args := make([]string, 0, 2+len(envDefaults)+4)
	args = append(args, "exec")
	args = append(args, envDefaults...)
	args = append(args, container)
	args = append(args, "sh", "-c", script)
	result, err := r.runArgv(ctx, cwd, host, nil, args)
	if err != nil {
		return ExecSignalResult{}, err
	}

	return ExecSignalResult{
		Container: container,
		ProcessID: processID,
		OK:        result.OK,
		Stderr:    result.Stderr,
	}, nil
}

// ExecListProcesses lists tracked background processes inside a container.
func (r *Runner) ExecListProcesses(ctx context.Context, cwd, host, container string) (ExecListProcessesResult, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return ExecListProcessesResult{}, fmt.Errorf("container is required")
	}

	script := fmt.Sprintf(
		`if [ ! -d %s ]; then exit 0; fi; `+
			`for pidfile in %s/*.pid; do `+
			`[ -f "$pidfile" ] || continue; `+
			`id=$(basename "$pidfile" .pid); `+
			`pid=$(cat "$pidfile"); `+
			`cmd=""; [ -f "%s/$id.cmd" ] && cmd=$(cat "%s/$id.cmd"); `+
			`if kill -0 "$pid" 2>/dev/null; then running=true; else running=false; fi; `+
			`printf '{"process_id":"%%s","pid":"%%s","running":%%s,"command":"%%s"}\n' "$id" "$pid" "$running" "$cmd"; `+
			`done`,
		bgProcDir, bgProcDir, bgProcDir, bgProcDir,
	)

	envDefaults := ensureExecEnvDefaults()
	args := make([]string, 0, 2+len(envDefaults)+4)
	args = append(args, "exec")
	args = append(args, envDefaults...)
	args = append(args, container)
	args = append(args, "sh", "-c", script)
	result, err := r.runArgv(ctx, cwd, host, nil, args)
	if err != nil {
		return ExecListProcessesResult{}, err
	}

	var processes []BackgroundProcess
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var proc BackgroundProcess
		if err := json.Unmarshal([]byte(line), &proc); err != nil {
			continue
		}
		processes = append(processes, proc)
	}

	return ExecListProcessesResult{
		Container: container,
		OK:        result.OK,
		Processes: processes,
		Stderr:    result.Stderr,
	}, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func (r *Runner) runContainerAction(ctx context.Context, cwd, host, action, container string) (ContainerActionResult, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return ContainerActionResult{}, fmt.Errorf("container is required")
	}
	result, err := r.runArgv(ctx, cwd, host, nil, []string{action, container})
	if err != nil {
		return ContainerActionResult{}, err
	}
	return ContainerActionResult{Container: container, Result: result}, nil
}

func (r *Runner) createContainerEnv(extra map[string]string) ([]string, []BindMount, error) {
	env := map[string]string{}
	for _, item := range gitpkg.DockerClientEnv(r.cfg) {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}

	privateKeyPath := strings.TrimSpace(r.cfg.Git.DefaultClient.PrivateKeyPath)
	if privateKeyPath == "" {
		return nil, nil, fmt.Errorf("git default client private key path is required")
	}
	knownHostsPath, err := gitpkg.EnsureKnownHostsFile(r.cfg)
	if err != nil {
		return nil, nil, err
	}
	env["GIT_SSH_COMMAND"] = gitpkg.DefaultClientSSHCommand(containerGitKeyPath, containerKnownHostsPath)

	// Default host binding for dev servers — many frameworks (Vite, Nuxt,
	// Next.js, etc.) respect HOST or HOSTNAME and default to 127.0.0.1
	// which is unreachable from outside the container.  Setting these
	// before the extra-env merge lets callers override if needed.
	env["HOST"] = "0.0.0.0"
	env["HOSTNAME"] = "0.0.0.0"

	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env[key] = value
	}

	keys := sortedKeys(env)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, key+"="+env[key])
	}
	return items, DockerCredentialMounts(privateKeyPath, knownHostsPath), nil
}

func defaultCreateCommand() []string {
	return []string{"-lc", defaultKeepAliveCommand}
}

func defaultCreateEntrypoint() string {
	return "sh"
}

func formatBindMount(bind BindMount) string {
	parts := []string{bind.Source, bind.Target}
	if bind.ReadOnly {
		parts = append(parts, "ro")
	}
	return strings.Join(parts, ":")
}

func formatVolumeMount(volume VolumeMount) string {
	if strings.TrimSpace(volume.Source) == "" {
		if volume.ReadOnly {
			return volume.Target + ":ro"
		}
		return volume.Target
	}
	parts := []string{volume.Source, volume.Target}
	if volume.ReadOnly {
		parts = append(parts, "ro")
	}
	return strings.Join(parts, ":")
}

func firstLine(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if line, _, ok := strings.Cut(value, "\n"); ok {
		return line
	}
	return value
}

func parseContainers(raw string) ([]ContainerSummary, error) {
	objects, err := parseJSONLines(raw)
	if err != nil {
		return nil, err
	}
	items := make([]ContainerSummary, 0, len(objects))
	for _, object := range objects {
		items = append(items, ContainerSummary{
			ID:           stringField(object, "ID"),
			Image:        stringField(object, "Image"),
			Command:      stringField(object, "Command"),
			CreatedAt:    stringField(object, "CreatedAt"),
			RunningFor:   stringField(object, "RunningFor"),
			Ports:        stringField(object, "Ports"),
			Status:       stringField(object, "Status"),
			State:        stringField(object, "State"),
			Names:        stringField(object, "Names"),
			Mounts:       stringField(object, "Mounts"),
			Networks:     stringField(object, "Networks"),
			LocalVolumes: stringField(object, "LocalVolumes"),
		})
	}
	return items, nil
}

func parseImages(raw string) ([]ImageSummary, error) {
	objects, err := parseJSONLines(raw)
	if err != nil {
		return nil, err
	}
	items := make([]ImageSummary, 0, len(objects))
	for _, object := range objects {
		items = append(items, ImageSummary{
			ID:           stringField(object, "ID"),
			Repository:   stringField(object, "Repository"),
			Tag:          stringField(object, "Tag"),
			Digest:       stringField(object, "Digest"),
			CreatedSince: stringField(object, "CreatedSince"),
			CreatedAt:    stringField(object, "CreatedAt"),
			Size:         stringField(object, "Size"),
		})
	}
	return items, nil
}

func parseNetworks(raw string) ([]NetworkSummary, error) {
	objects, err := parseJSONLines(raw)
	if err != nil {
		return nil, err
	}
	items := make([]NetworkSummary, 0, len(objects))
	for _, object := range objects {
		items = append(items, NetworkSummary{
			ID:     stringField(object, "ID"),
			Name:   stringField(object, "Name"),
			Driver: stringField(object, "Driver"),
			Scope:  stringField(object, "Scope"),
		})
	}
	return items, nil
}

func parseVolumes(raw string) ([]VolumeSummary, error) {
	objects, err := parseJSONLines(raw)
	if err != nil {
		return nil, err
	}
	items := make([]VolumeSummary, 0, len(objects))
	for _, object := range objects {
		items = append(items, VolumeSummary{
			Driver:     stringField(object, "Driver"),
			Name:       stringField(object, "Name"),
			Mountpoint: stringField(object, "Mountpoint"),
			Scope:      stringField(object, "Scope"),
			Labels:     stringField(object, "Labels"),
			Links:      stringField(object, "Links"),
			Size:       stringField(object, "Size"),
		})
	}
	return items, nil
}

func parseJSONLines(raw string) ([]map[string]any, error) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	objects := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			return nil, fmt.Errorf("parse docker json line: %w", err)
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func stringField(object map[string]any, key string) string {
	if value, ok := object[key]; ok {
		switch typed := value.(type) {
		case string:
			return typed
		default:
			return fmt.Sprint(typed)
		}
	}
	return ""
}

func ParseEnvAssignments(values []string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "[]" {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", raw)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("env key is required")
		}
		out[key] = value
	}
	return out, nil
}

func ParseBuildArgAssignments(values []string) (map[string]string, error) {
	return ParseEnvAssignments(values)
}

func ParseBindSpecs(values []string) ([]BindMount, error) {
	out := make([]BindMount, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "[]" {
			continue
		}
		parts := strings.Split(value, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("bind spec %q must be SOURCE:TARGET[:ro]", value)
		}
		mount := BindMount{
			Source: strings.TrimSpace(parts[0]),
			Target: strings.TrimSpace(parts[1]),
		}
		if mount.Source == "" || mount.Target == "" {
			return nil, fmt.Errorf("bind spec %q must include source and target", value)
		}
		if len(parts) == 3 {
			if strings.TrimSpace(parts[2]) != "ro" {
				return nil, fmt.Errorf("bind spec %q only supports optional ro mode", value)
			}
			mount.ReadOnly = true
		}
		out = append(out, mount)
	}
	return out, nil
}

func ParseVolumeSpecs(values []string) ([]VolumeMount, error) {
	out := make([]VolumeMount, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "[]" {
			continue
		}
		parts := strings.Split(value, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("volume spec %q must be NAME:TARGET[:ro]", value)
		}
		mount := VolumeMount{
			Source: strings.TrimSpace(parts[0]),
			Target: strings.TrimSpace(parts[1]),
		}
		if mount.Source == "" || mount.Target == "" {
			return nil, fmt.Errorf("volume spec %q must include source and target", value)
		}
		if looksLikeHostPath(mount.Source) {
			return nil, fmt.Errorf("volume spec %q must use a named volume source", value)
		}
		if len(parts) == 3 {
			if strings.TrimSpace(parts[2]) != "ro" {
				return nil, fmt.Errorf("volume spec %q only supports optional ro mode", value)
			}
			mount.ReadOnly = true
		}
		out = append(out, mount)
	}
	return out, nil
}

func ResolveBindSources(cwd string, binds []BindMount) ([]BindMount, error) {
	out := make([]BindMount, 0, len(binds))
	for _, bind := range binds {
		source, err := resolveScopedPath(cwd, bind.Source)
		if err != nil {
			return nil, err
		}
		bind.Source = source
		out = append(out, bind)
	}
	return out, nil
}

func ResolvePath(cwd, rawPath string) (string, error) {
	return resolveScopedPath(cwd, rawPath)
}

func NormalizeCommandArgs(values []string, raw string) []string {
	if len(values) > 0 {
		return append([]string(nil), values...)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts, err := shlexSplit(raw)
	if err != nil {
		return []string{"sh", "-lc", raw}
	}
	return parts
}

func shlexSplit(raw string) ([]string, error) {
	return tokenizeDockerArgs(raw)
}

func DockerCredentialMounts(cfgHostPrivateKeyPath, cfgKnownHostsPath string) []BindMount {
	return []BindMount{
		{
			Source:   cfgHostPrivateKeyPath,
			Target:   containerGitKeyPath,
			ReadOnly: true,
		},
		{
			Source:   cfgKnownHostsPath,
			Target:   containerKnownHostsPath,
			ReadOnly: true,
		},
	}
}

type dockerInspectBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type dockerInspectContainer struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	NetworkSettings struct {
		Ports map[string][]dockerInspectBinding `json:"Ports"`
	} `json:"NetworkSettings"`
}

type containerEndpoints struct {
	PublishedPorts []PublishedPort
	SSHEndpoint    string
	CodeServerURL  string
}

func (r *Runner) enrichContainerSummaries(ctx context.Context, cwd, host string, items []ContainerSummary) []ContainerSummary {
	if len(items) == 0 {
		return items
	}

	refs := make([]string, 0, len(items))
	for _, item := range items {
		if id := strings.TrimSpace(item.ID); id != "" {
			refs = append(refs, id)
		}
	}
	if len(refs) == 0 {
		return items
	}

	inspected, err := r.inspectContainers(ctx, cwd, host, refs)
	if err != nil {
		return items
	}

	for index := range items {
		endpoints, ok := inspected[strings.TrimSpace(items[index].ID)]
		if !ok {
			continue
		}
		items[index].PublishedPorts = endpoints.PublishedPorts
		items[index].SSHEndpoint = endpoints.SSHEndpoint
		items[index].CodeServerURL = endpoints.CodeServerURL
	}

	return items
}

func (r *Runner) inspectContainerEndpoints(ctx context.Context, cwd, host, ref string) (containerEndpoints, error) {
	inspected, err := r.inspectContainers(ctx, cwd, host, []string{ref})
	if err != nil {
		return containerEndpoints{}, err
	}
	return inspected[strings.TrimSpace(ref)], nil
}

func (r *Runner) inspectContainers(ctx context.Context, cwd, host string, refs []string) (map[string]containerEndpoints, error) {
	cleanRefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			cleanRefs = append(cleanRefs, ref)
		}
	}
	if len(cleanRefs) == 0 {
		return nil, nil
	}

	args := append([]string{"inspect"}, cleanRefs...)
	result, err := r.runArgv(ctx, cwd, host, nil, args)
	if err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("%s", strings.TrimSpace(result.Stderr))
	}

	var inspected []dockerInspectContainer
	if err := json.Unmarshal([]byte(result.Stdout), &inspected); err != nil {
		return nil, fmt.Errorf("parse docker inspect output: %w", err)
	}

	out := make(map[string]containerEndpoints, len(inspected))
	for _, item := range inspected {
		endpoints := containerEndpointsFromInspect(item)
		fullID := strings.TrimSpace(item.ID)
		if fullID != "" {
			out[fullID] = endpoints
			if len(fullID) >= 12 {
				out[fullID[:12]] = endpoints
			}
		}
		name := strings.TrimPrefix(strings.TrimSpace(item.Name), "/")
		if name != "" {
			out[name] = endpoints
		}
	}
	return out, nil
}

func containerEndpointsFromInspect(inspected dockerInspectContainer) containerEndpoints {
	ports := make([]PublishedPort, 0)
	for containerPort, bindings := range inspected.NetworkSettings.Ports {
		for _, binding := range bindings {
			hostPort := strings.TrimSpace(binding.HostPort)
			if hostPort == "" {
				continue
			}
			ports = append(ports, PublishedPort{
				ContainerPort: containerPort,
				HostIP:        normalizePublishedHostIP(binding.HostIP),
				HostPort:      hostPort,
			})
		}
	}

	sort.Slice(ports, func(i, j int) bool {
		left := publishedPortSortKey(ports[i].ContainerPort)
		right := publishedPortSortKey(ports[j].ContainerPort)
		if left != right {
			return left < right
		}
		if ports[i].HostIP != ports[j].HostIP {
			return ports[i].HostIP < ports[j].HostIP
		}
		return ports[i].HostPort < ports[j].HostPort
	})

	return containerEndpoints{
		PublishedPorts: ports,
		SSHEndpoint:    endpointForPublishedPort(ports, devSSHContainerPort, true),
		CodeServerURL:  endpointForPublishedPort(ports, devCodeServerPort, false),
	}
}

func publishedPortSortKey(containerPort string) int {
	port, _, ok := strings.Cut(containerPort, "/")
	if !ok {
		port = containerPort
	}
	value, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil {
		return 1 << 30
	}
	return value
}

func endpointForPublishedPort(ports []PublishedPort, containerPort string, ssh bool) string {
	for _, item := range ports {
		if item.ContainerPort != containerPort {
			continue
		}
		host := normalizePublishedHostIP(item.HostIP)
		if ssh {
			return fmt.Sprintf("ssh://%s@%s:%s", devContainerUser, host, item.HostPort)
		}
		return fmt.Sprintf("http://%s:%s/", host, item.HostPort)
	}
	return ""
}

func normalizePublishedHostIP(hostIP string) string {
	hostIP = strings.TrimSpace(hostIP)
	switch hostIP {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return hostIP
	}
}

func devPublishedPortArgs() []string {
	return []string{"127.0.0.1::22", "127.0.0.1::8080"}
}

func devAuthVolumeMounts(containerName string) []VolumeMount {
	containerName = strings.TrimSpace(containerName)
	mounts := []VolumeMount{
		{Target: devClaudeConfigDir},
		{Target: devCodexConfigDir},
	}
	if containerName == "" {
		return mounts
	}
	mounts[0].Source = containerName + "-claude-auth"
	mounts[1].Source = containerName + "-codex-auth"
	return mounts
}

func isDevImage(image string) bool {
	name := normalizeImageReference(image)
	return name == defaultDevImage || strings.HasSuffix(name, "/"+defaultDevImage)
}

func normalizeImageReference(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if base, _, ok := strings.Cut(image, "@"); ok {
		image = base
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		image = image[:lastColon]
	}
	return image
}

func ensureDevAuthorizedKeysFile(cfg configpkg.Config) (string, error) {
	keys, err := dockerAuthorizedKeys(cfg)
	if err != nil {
		return "", err
	}

	baseDir := filepath.Join(filepath.Dir(strings.TrimSpace(cfg.Git.SSH.HostKeyPath)), "docker")
	if strings.TrimSpace(baseDir) == "" || baseDir == "docker" {
		baseDir = filepath.Join(os.TempDir(), "nalvin-docker")
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", fmt.Errorf("create docker auth dir: %w", err)
	}

	path := filepath.Join(baseDir, "authorized_keys")
	contents := strings.Join(keys, "\n")
	if contents != "" {
		contents += "\n"
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return "", fmt.Errorf("write docker authorized_keys: %w", err)
	}
	return path, nil
}

func dockerAuthorizedKeys(cfg configpkg.Config) ([]string, error) {
	seen := map[string]struct{}{}
	keys := make([]string, 0)

	userNames := make([]string, 0, len(cfg.Git.Users))
	for user := range cfg.Git.Users {
		userNames = append(userNames, user)
	}
	sort.Strings(userNames)
	for _, user := range userNames {
		for _, raw := range cfg.Git.Users[user].PublicKeys {
			canonical, err := canonicalAuthorizedKey(raw)
			if err != nil {
				return nil, fmt.Errorf("parse git.users.%s public key: %w", user, err)
			}
			if canonical == "" {
				continue
			}
			if _, ok := seen[canonical]; ok {
				continue
			}
			seen[canonical] = struct{}{}
			keys = append(keys, canonical)
		}
	}

	privateKeyPath := strings.TrimSpace(cfg.Git.DefaultClient.PrivateKeyPath)
	if privateKeyPath != "" {
		privateKey, err := os.ReadFile(privateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read docker default client private key: %w", err)
		}
		signer, err := xssh.ParsePrivateKey(privateKey)
		if err != nil {
			return nil, fmt.Errorf("parse docker default client private key: %w", err)
		}
		canonical := strings.TrimSpace(string(xssh.MarshalAuthorizedKey(signer.PublicKey())))
		if _, ok := seen[canonical]; !ok {
			seen[canonical] = struct{}{}
			keys = append(keys, canonical)
		}
	}

	return keys, nil
}

func canonicalAuthorizedKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	key, _, _, _, err := xssh.ParseAuthorizedKey([]byte(raw))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(xssh.MarshalAuthorizedKey(key))), nil
}
