package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

// skillExec wires up a skill's declared commands so the agent sees them as
// regular tools. Each command runs inside a docker skill-runner container
// with the skill directory bind-mounted at /skills/<name>/, the workspace
// at /workspace, and network on by default. Setup steps declared in the
// skill's manifest.yaml run once per agentRuntime (roughly: per run) before
// the first command is exec'd.

// nonContainerNameChars is shared with shell_docker_fallback.go.

// skillExecResult carries back what a skill command produced.
type skillExecResult struct {
	Skill     string   `json:"skill"`
	Container string   `json:"container"`
	Argv      []string `json:"argv"`
	CWD       string   `json:"cwd"`
	ExitCode  int      `json:"exit_code"`
	OK        bool     `json:"ok"`
	Stdout    string   `json:"stdout,omitempty"`
	Stderr    string   `json:"stderr,omitempty"`
}

// skillExecutor is the per-agentRuntime coordinator for skill-command
// execution.
type skillExecutor struct {
	mu             sync.Mutex
	container      string
	setupCompleted map[string]bool
	boundMounts    map[string]string
	containers     map[string]struct{}
	workspaceHint  string
	leaseID        string
}

type skillCommandLock struct {
	file *os.File
}

func newSkillExecutor() *skillExecutor {
	return &skillExecutor{
		setupCompleted: map[string]bool{},
		boundMounts:    map[string]string{},
		containers:     map[string]struct{}{},
		leaseID:        fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()),
	}
}

// runSkillCommand is the top-level entry: ensure the container exists with the
// right mounts, run setup if needed, then exec the command.
func (rt *agentRuntime) runSkillCommand(ctx context.Context, skillName string, argv []string, timeoutSec int) (skillExecResult, error) {
	skill, ok := rt.lookupSkillMetadata(skillName)
	if !ok {
		return skillExecResult{}, fmt.Errorf("skill %q not found in catalog", skillName)
	}
	if len(skill.Commands) == 0 {
		return skillExecResult{}, fmt.Errorf("skill %q declares no commands", skillName)
	}
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return skillExecResult{}, err
	}
	lock, err := acquireSkillCommandLock(skillCommandLockPath(paths.FilesPath))
	if err != nil {
		return skillExecResult{}, err
	}
	defer releaseSkillCommandLock(lock)

	exec := rt.ensureSkillExecutor()
	exec.mu.Lock()
	containerName, err := rt.ensureSkillCommandsContainerLocked(ctx, exec, skill, paths)
	if err != nil {
		exec.mu.Unlock()
		return skillExecResult{}, err
	}

	if !exec.setupCompleted[skill.Name] {
		if err := rt.runSkillSetupLocked(ctx, skill, containerName, paths); err != nil {
			exec.mu.Unlock()
			return skillExecResult{}, fmt.Errorf("skill %q setup: %w", skill.Name, err)
		}
		exec.setupCompleted[skill.Name] = true
	}
	exec.mu.Unlock()

	cwd := skillContainerDir(skill)
	full := append([]string{"sh", "-c", fmt.Sprintf("cd %q && exec \"$@\"", cwd), "--"}, argv...)
	runner := dockerpkg.NewRunner(rt.cfg)
	res, err := runner.Exec(ctx, paths.FilesPath, "", containerName, full)
	if err != nil {
		return skillExecResult{}, err
	}
	return skillExecResult{
		Skill:     skill.Name,
		Container: containerName,
		Argv:      argv,
		CWD:       cwd,
		ExitCode:  res.ExitCode,
		OK:        res.OK,
		Stdout:    res.Stdout,
		Stderr:    res.Stderr,
	}, nil
}

// ensureSkillCommandsContainerLocked creates or reuses a skill-runner
// container whose mount set covers every currently-active command-skill.
//
// Caller must hold exec.mu.
func (rt *agentRuntime) ensureSkillCommandsContainerLocked(ctx context.Context, exec *skillExecutor, triggering skillMetadata, paths workspacepkg.Paths) (string, error) {
	needed := map[string]skillMetadata{triggering.Name: triggering}
	if rt.skills != nil {
		for _, active := range rt.skills.activeMetadata() {
			if meta, ok := rt.lookupSkillMetadata(active.Name); ok && len(meta.Commands) > 0 {
				needed[meta.Name] = meta
			}
		}
	}
	names := make([]string, 0, len(needed))
	for n := range needed {
		names = append(names, n)
	}
	sort.Strings(names)

	skillsRoot, err := userSkillsRoot(rt.cfg)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return "", err
	}
	binds := []dockerpkg.BindMount{
		{Source: skillsRoot, Target: skillContainerMountRoot, ReadOnly: true},
	}
	for _, n := range names {
		meta := needed[n]
		real, err := filepath.EvalSymlinks(meta.SkillDir)
		if err != nil {
			return "", fmt.Errorf("resolve skill dir %q: %w", meta.SkillDir, err)
		}
		binds = append(binds, dockerpkg.BindMount{
			Source:   real,
			Target:   skillContainerDir(meta),
			ReadOnly: !meta.ExecWritable,
		})
		exec.boundMounts[meta.Name] = real
	}
	sort.Slice(binds, func(i, j int) bool { return binds[i].Target < binds[j].Target })

	containerName := skillCommandsContainerName(paths.Name, binds)
	if exec.container == containerName {
		return containerName, nil
	}
	leaseLock, err := acquireSkillCommandLock(skillContainerLeaseLockPath(paths.FilesPath, containerName))
	if err != nil {
		return "", err
	}
	defer releaseSkillCommandLock(leaseLock)

	runner := dockerpkg.NewRunner(rt.cfg)
	containers, _, err := runner.ListContainers(ctx, paths.FilesPath, "")
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		if !dockerContainerHasName(c.Names, containerName) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(c.State), "running") {
			if _, err := runner.StartContainer(ctx, paths.FilesPath, "", containerName); err != nil {
				return "", err
			}
		}
		if err := rt.registerSkillContainerLeaseLocked(exec, paths.FilesPath, containerName); err != nil {
			return "", err
		}
		exec.container = containerName
		exec.workspaceHint = paths.FilesPath
		return containerName, nil
	}

	network := ""
	for _, n := range names {
		if !needed[n].ExecNetworkOn {
			network = "none"
			break
		}
	}

	image := "nalvin/dev"
	for _, n := range names {
		if img := strings.TrimSpace(needed[n].ExecImage); img != "" {
			image = img
			break
		}
	}

	if _, err := runner.CreateContainer(ctx, dockerpkg.CreateOptions{
		CWD:            paths.FilesPath,
		Name:           containerName,
		Image:          image,
		MountWorkspace: true,
		WorkspacePath:  paths.FilesPath,
		Workdir:        dockerpkg.DefaultWorkspaceMountTarget,
		Binds:          binds,
		Network:        network,
	}); err != nil {
		return "", fmt.Errorf("create skill-commands runner: %w", err)
	}
	if _, err := runner.StartContainer(ctx, paths.FilesPath, "", containerName); err != nil {
		return "", fmt.Errorf("start skill-commands runner: %w", err)
	}
	if err := rt.registerSkillContainerLeaseLocked(exec, paths.FilesPath, containerName); err != nil {
		return "", err
	}
	exec.container = containerName
	exec.workspaceHint = paths.FilesPath
	return containerName, nil
}

// runSkillSetupLocked runs each setup step serially. Caller holds exec.mu.
func (rt *agentRuntime) runSkillSetupLocked(ctx context.Context, skill skillMetadata, container string, paths workspacepkg.Paths) error {
	if len(skill.SetupSteps) == 0 {
		return nil
	}
	base := skillContainerDir(skill)
	runner := dockerpkg.NewRunner(rt.cfg)
	for i, step := range skill.SetupSteps {
		cwd := base
		if step.Cwd != "" {
			cwd = path.Join(base, step.Cwd)
		}
		argv := []string{"sh", "-c", fmt.Sprintf("cd %q && %s", cwd, step.Run)}
		res, err := runner.Exec(ctx, paths.FilesPath, "", container, argv)
		if err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if !res.OK {
			return fmt.Errorf("step %d exit=%d: %s%s", i+1, res.ExitCode, res.Stdout, res.Stderr)
		}
	}
	return nil
}

func (rt *agentRuntime) ensureSkillExecutor() *skillExecutor {
	rt.skillExecOnce.Do(func() {
		rt.skillExec = newSkillExecutor()
	})
	return rt.skillExec
}

// lookupSkillMetadata returns the catalog entry for a skill name.
func (rt *agentRuntime) lookupSkillMetadata(name string) (skillMetadata, bool) {
	if rt.skills == nil {
		return skillMetadata{}, false
	}
	return rt.skills.metadataByName(name)
}

// skillContainerDir returns the in-container path for a skill's bind mount.
func skillContainerDir(meta skillMetadata) string {
	if dir := strings.TrimSpace(meta.ContainerSkillDir); dir != "" {
		return dir
	}
	return path.Join(skillContainerMountRoot, filepath.Base(meta.SkillDir))
}

func (rt *agentRuntime) registerSkillContainerLeaseLocked(exec *skillExecutor, workspaceHint, containerName string) error {
	if exec == nil {
		return fmt.Errorf("skill executor is nil")
	}
	containerName = strings.TrimSpace(containerName)
	workspaceHint = strings.TrimSpace(workspaceHint)
	if containerName == "" || workspaceHint == "" {
		return nil
	}
	if err := os.MkdirAll(skillContainerLeaseDir(workspaceHint, containerName), 0o755); err != nil {
		return fmt.Errorf("ensure skill lease dir: %w", err)
	}
	leasePath := skillContainerLeasePath(workspaceHint, containerName, exec.leaseID)
	leaseBody := fmt.Sprintf("pid=%d\nlease_id=%s\ncreated_at=%s\n", os.Getpid(), exec.leaseID, time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(leasePath, []byte(leaseBody), 0o644); err != nil {
		return fmt.Errorf("write skill lease: %w", err)
	}
	exec.containers[containerName] = struct{}{}
	exec.workspaceHint = workspaceHint
	return nil
}

// teardownSkillContainers releases every skill-runner lease held by this
// runtime and removes containers only when this runtime is the last lease
// holder.
func (rt *agentRuntime) teardownSkillContainers(ctx context.Context) {
	if rt.skillExec == nil {
		return
	}
	exec := rt.skillExec
	exec.mu.Lock()
	names := make([]string, 0, len(exec.containers))
	for n := range exec.containers {
		names = append(names, n)
	}
	workspaceHint := exec.workspaceHint
	leaseID := exec.leaseID
	exec.containers = map[string]struct{}{}
	exec.container = ""
	exec.mu.Unlock()

	if len(names) == 0 {
		return
	}
	for _, name := range names {
		if err := rt.releaseSkillContainerLease(ctx, workspaceHint, name, leaseID); err != nil && rt.session != nil {
			rt.session.debugf("skill-runner teardown: release %s: %v", name, err)
		}
	}
}

func (rt *agentRuntime) releaseSkillContainerLease(ctx context.Context, workspaceHint, containerName, leaseID string) error {
	workspaceHint = strings.TrimSpace(workspaceHint)
	containerName = strings.TrimSpace(containerName)
	leaseID = strings.TrimSpace(leaseID)
	if workspaceHint == "" || containerName == "" || leaseID == "" {
		return nil
	}

	lock, err := acquireSkillCommandLock(skillContainerLeaseLockPath(workspaceHint, containerName))
	if err != nil {
		return err
	}
	defer releaseSkillCommandLock(lock)

	leasePath := skillContainerLeasePath(workspaceHint, containerName, leaseID)
	if err := os.Remove(leasePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove skill lease: %w", err)
	}
	remaining, err := skillContainerLeaseEntries(workspaceHint, containerName)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return nil
	}

	runner := dockerpkg.NewRunner(rt.cfg)
	_, _ = runner.StopContainer(ctx, workspaceHint, "", containerName)
	if _, err := runner.RemoveContainer(ctx, workspaceHint, "", containerName); err != nil {
		return err
	}
	_ = os.Remove(skillContainerLeaseLockPath(workspaceHint, containerName))
	_ = os.Remove(skillContainerLeaseDir(workspaceHint, containerName))
	return nil
}

// skillCommandsContainerName builds a per-workspace, per-mount-shape
// container name so mount-set changes don't reuse an out-of-date container.
func skillCommandsContainerName(workspace string, binds []dockerpkg.BindMount) string {
	hasher := sha256.New()
	for _, b := range binds {
		ro := "rw"
		if b.ReadOnly {
			ro = "ro"
		}
		fmt.Fprintf(hasher, "%s|%s|%s\n", b.Source, b.Target, ro)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))[:8]
	base := skillRunnerContainerNameBase(workspace)
	return base + "-cmd-" + digest
}

func skillRunnerContainerNameBase(workspaceName string) string {
	workspaceName = strings.ToLower(strings.TrimSpace(workspaceName))
	workspaceName = nonContainerNameChars.ReplaceAllString(workspaceName, "-")
	workspaceName = strings.Trim(workspaceName, "-")
	if workspaceName == "" {
		workspaceName = "workspace"
	}
	return "nalvin-skill-runner-" + workspaceName
}

func skillCommandLockPath(filesPath string) string {
	return filepath.Join(filesPath, ".nalvin-skill-runner", "commands.lock")
}

func skillContainerLeaseDir(filesPath, containerName string) string {
	return filepath.Join(filesPath, ".nalvin-skill-runner", "leases", containerName)
}

func skillContainerLeaseLockPath(filesPath, containerName string) string {
	return filepath.Join(skillContainerLeaseDir(filesPath, containerName), ".lock")
}

func skillContainerLeasePath(filesPath, containerName, leaseID string) string {
	return filepath.Join(skillContainerLeaseDir(filesPath, containerName), leaseID+".lease")
}

func skillContainerLeaseEntries(filesPath, containerName string) ([]string, error) {
	dir := skillContainerLeaseDir(filesPath, containerName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skill lease dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == ".lock" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func acquireSkillCommandLock(path string) (*skillCommandLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("ensure skill-runner lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open skill-runner lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire skill-runner lock: %w", err)
	}
	return &skillCommandLock{file: file}, nil
}

func releaseSkillCommandLock(lock *skillCommandLock) {
	if lock == nil || lock.file == nil {
		return
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	_ = lock.file.Close()
}

func dockerContainerHasName(rawNames, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, name := range strings.Split(rawNames, ",") {
		if strings.TrimSpace(name) == target {
			return true
		}
	}
	return false
}

// userSkillsRoot returns the top-level directory containing managed user
// skills (used as the read-only `/skills` mount root for skill-runner
// containers).
func userSkillsRoot(cfg configpkg.Config) (string, error) {
	dirs := userSkillDirs(cfg)
	if len(dirs) > 0 {
		return dirs[0], nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nalvin", "skills"), nil
}
