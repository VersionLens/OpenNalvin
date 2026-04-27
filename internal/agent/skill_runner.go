package agent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type managedSkillRunnerInfo struct {
	Container string `json:"container"`
	Created   bool   `json:"created,omitempty"`
	Started   bool   `json:"started,omitempty"`
}

// CleanupSkillContainersResult is the wire-format result for the
// `agent skills cleanup-containers` command.
type CleanupSkillContainersResult struct {
	Workspace string   `json:"workspace,omitempty"`
	All       bool     `json:"all"`
	Matched   []string `json:"matched"`
	Removed   []string `json:"removed"`
}

// ensureManagedSkillRunner creates (or reuses) a default skill-runner
// container for the active workspace with no extra mounts. It is intended for
// read-only browse-style operations against managed-skill payloads.
func (rt *agentRuntime) ensureManagedSkillRunner(ctx context.Context) (managedSkillRunnerInfo, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return managedSkillRunnerInfo{}, err
	}
	skillsRoot, err := managedSkillsRoot()
	if err != nil {
		return managedSkillRunnerInfo{}, err
	}
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return managedSkillRunnerInfo{}, err
	}

	containerName := managedSkillRunnerContainerName(paths.Name)
	leaseLock, err := acquireSkillCommandLock(skillContainerLeaseLockPath(paths.FilesPath, containerName))
	if err != nil {
		return managedSkillRunnerInfo{}, err
	}
	defer releaseSkillCommandLock(leaseLock)

	runner := dockerpkg.NewRunner(rt.cfg)
	containers, _, err := runner.ListContainers(ctx, paths.FilesPath, "")
	if err != nil {
		return managedSkillRunnerInfo{}, err
	}
	exec := rt.ensureSkillExecutor()
	for _, container := range containers {
		if !dockerContainerHasName(container.Names, containerName) {
			continue
		}
		info := managedSkillRunnerInfo{Container: containerName}
		if !strings.EqualFold(strings.TrimSpace(container.State), "running") {
			if _, err := runner.StartContainer(ctx, paths.FilesPath, "", containerName); err != nil {
				return managedSkillRunnerInfo{}, err
			}
			info.Started = true
		}
		exec.mu.Lock()
		err := rt.registerSkillContainerLeaseLocked(exec, paths.FilesPath, containerName)
		exec.mu.Unlock()
		if err != nil {
			return managedSkillRunnerInfo{}, err
		}
		return info, nil
	}

	image := strings.TrimSpace(rt.cfg.Docker.DefaultImage)
	if image == "" {
		image = "nalvin/dev"
	}
	if _, err := runner.CreateContainer(ctx, dockerpkg.CreateOptions{
		CWD:            paths.FilesPath,
		Name:           containerName,
		Image:          image,
		MountWorkspace: true,
		WorkspacePath:  paths.FilesPath,
		Workdir:        dockerpkg.DefaultWorkspaceMountTarget,
		Binds: []dockerpkg.BindMount{
			{
				Source:   skillsRoot,
				Target:   skillContainerMountRoot,
				ReadOnly: true,
			},
		},
	}); err != nil {
		return managedSkillRunnerInfo{}, fmt.Errorf("create skill runner container: %w", err)
	}
	if _, err := runner.StartContainer(ctx, paths.FilesPath, "", containerName); err != nil {
		return managedSkillRunnerInfo{}, fmt.Errorf("start skill runner container: %w", err)
	}
	exec.mu.Lock()
	err = rt.registerSkillContainerLeaseLocked(exec, paths.FilesPath, containerName)
	exec.mu.Unlock()
	if err != nil {
		return managedSkillRunnerInfo{}, err
	}
	return managedSkillRunnerInfo{
		Container: containerName,
		Created:   true,
		Started:   true,
	}, nil
}

// managedSkillRunnerContainerName builds the bare per-workspace runner name
// (no command-shape suffix). The skill_exec layer suffixes "-cmd-<sha8>" for
// per-shape containers; those are also removed by CleanupSkillContainers.
func managedSkillRunnerContainerName(workspaceName string) string {
	return skillRunnerContainerNameBase(workspaceName)
}

// CleanupSkillContainers removes skill-runner containers for the given
// workspace (or all workspaces when all is true).
func CleanupSkillContainers(ctx context.Context, cfg configpkg.Config, workspaceName, filesPath string, all bool) (CleanupSkillContainersResult, error) {
	runner := dockerpkg.NewRunner(cfg)
	containers, _, err := runner.ListContainers(ctx, filesPath, "")
	if err != nil {
		return CleanupSkillContainersResult{}, err
	}

	prefix := "nalvin-skill-runner-"
	workspace := strings.TrimSpace(workspaceName)
	if !all && workspace != "" {
		prefix = managedSkillRunnerContainerName(workspace)
	}

	result := CleanupSkillContainersResult{
		Workspace: workspace,
		All:       all,
		Matched:   []string{},
		Removed:   []string{},
	}

	seen := map[string]struct{}{}
	for _, container := range containers {
		for _, name := range strings.Split(container.Names, ",") {
			name = strings.TrimSpace(name)
			if name == "" || !strings.HasPrefix(name, prefix) {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			result.Matched = append(result.Matched, name)
		}
	}
	sort.Strings(result.Matched)

	for _, name := range result.Matched {
		_, _ = runner.StopContainer(ctx, filesPath, "", name)
		if _, err := runner.RemoveContainer(ctx, filesPath, "", name); err != nil {
			return result, err
		}
		result.Removed = append(result.Removed, name)
	}

	return result, nil
}

