package agent

import (
	"context"
	"strings"

	"charm.land/fantasy"
	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type dockerToolInput struct {
	Args string `json:"args" jsonschema_description:"Raw docker CLI arguments after the word docker. Supported commands are ps, images, network ls, volume ls, pull, build, create, start, stop, rm, and exec."`
	Host string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override. When omitted, nalvin uses docker.host config or the local docker client default context behavior."`
}

type dockerHostInput struct {
	Host string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override. When omitted, nalvin uses docker.host config or the local docker client default context behavior."`
}

type dockerPullImageInput struct {
	Host  string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Image string `json:"image" jsonschema_description:"Docker image reference to pull."`
}

type dockerBuildImageInput struct {
	Host        string   `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	ContextPath string   `json:"context_path" jsonschema_description:"Workspace-relative build context path."`
	Dockerfile  string   `json:"dockerfile,omitempty" jsonschema_description:"Optional workspace-relative Dockerfile path."`
	Tag         string   `json:"tag,omitempty" jsonschema_description:"Optional image tag."`
	Target      string   `json:"target,omitempty" jsonschema_description:"Optional target stage."`
	BuildArgs   []string `json:"build_args,omitempty" jsonschema_description:"Optional repeated KEY=VALUE build args."`
}

type dockerCreateContainerInput struct {
	Host           string   `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Image          string   `json:"image,omitempty" jsonschema_description:"Optional image. Defaults to docker.default_image."`
	Name           string   `json:"name,omitempty" jsonschema_description:"Optional container name."`
	MountWorkspace bool     `json:"mount_workspace,omitempty" jsonschema_description:"If true, mount the active workspace root at /workspace and default the workdir there unless overridden."`
	RepoPath       string   `json:"repo_path,omitempty" jsonschema_description:"Optional workspace-relative repo path to mount at /workspace."`
	Binds          []string `json:"binds,omitempty" jsonschema_description:"Optional repeated bind mounts in SOURCE:TARGET[:ro] form where SOURCE is workspace-relative."`
	Volumes        []string `json:"volumes,omitempty" jsonschema_description:"Optional repeated named volume mounts in NAME:TARGET[:ro] form."`
	Env            []string `json:"env,omitempty" jsonschema_description:"Optional repeated environment variables in KEY=VALUE form."`
	Network        string   `json:"network,omitempty" jsonschema_description:"Optional network to attach."`
	Workdir        string   `json:"workdir,omitempty" jsonschema_description:"Optional working directory inside the container."`
	Ports          []string `json:"ports,omitempty" jsonschema_description:"Optional repeated port publish specs in HOST_PORT:CONTAINER_PORT form (e.g. 5173:5173, 127.0.0.1:8000:8000). Ports cannot be added after creation; plan all needed ports upfront. Dev servers inside the container must bind to 0.0.0.0, not 127.0.0.1."`
	Command        []string `json:"command,omitempty" jsonschema_description:"Optional explicit command and args to run in the container. When omitted, non-dev images use a keep-alive command; the default nalvin/dev image keeps its own startup command so SSH and code-server can start."`
}

type dockerContainerInput struct {
	Host      string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Container string `json:"container" jsonschema_description:"Container name or ID."`
}

type dockerExecInput struct {
	Host      string   `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Container string   `json:"container" jsonschema_description:"Container name or ID."`
	Command   []string `json:"command" jsonschema_description:"Command and args to run inside the container."`
}

type dockerExecBackgroundInput struct {
	Host      string   `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Container string   `json:"container" jsonschema_description:"Container name or ID."`
	ProcessID string   `json:"process_id" jsonschema_description:"A short identifier for this background process (e.g. backend, frontend, worker). Used to tail output and send signals later."`
	Command   []string `json:"command" jsonschema_description:"Command and args to run in the background. Dev servers must bind to 0.0.0.0, not 127.0.0.1 (e.g. vite --host 0.0.0.0, uvicorn main:app --host 0.0.0.0)."`
}

type dockerExecTailInput struct {
	Host      string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Container string `json:"container" jsonschema_description:"Container name or ID."`
	ProcessID string `json:"process_id" jsonschema_description:"The process_id given when the background process was started."`
	Lines     int    `json:"lines,omitempty" jsonschema_description:"Number of trailing lines to return. Defaults to 50."`
}

type dockerExecSignalInput struct {
	Host      string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Container string `json:"container" jsonschema_description:"Container name or ID."`
	ProcessID string `json:"process_id" jsonschema_description:"The process_id given when the background process was started."`
	Signal    string `json:"signal,omitempty" jsonschema_description:"Signal name (e.g. TERM, KILL, HUP, INT). Defaults to TERM."`
}

type dockerExecListProcessesInput struct {
	Host      string `json:"host,omitempty" jsonschema_description:"Optional Docker daemon host override."`
	Container string `json:"container" jsonschema_description:"Container name or ID."`
}

func (rt *agentRuntime) dockerTools() []runtimeTool {
	return []runtimeTool{
		rt.makeTool(
			"docker",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker", "Run a scoped docker CLI with raw CLI-style args. Supported commands are ps, images, network ls, volume ls, pull, build, create, start, stop, rm, exec, info, version, and inspect.", rt.runDockerTool),
		),
		rt.makeTool(
			"docker_list_containers",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_list_containers", "List Docker containers visible to the current daemon, including stopped containers and any detected published SSH/code-server endpoints.", rt.dockerListContainers),
		),
		rt.makeTool(
			"docker_list_images",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_list_images", "List Docker images visible to the current daemon.", rt.dockerListImages),
		),
		rt.makeTool(
			"docker_list_networks",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_list_networks", "List Docker networks visible to the current daemon.", rt.dockerListNetworks),
		),
		rt.makeTool(
			"docker_list_volumes",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_list_volumes", "List Docker volumes visible to the current daemon.", rt.dockerListVolumes),
		),
		rt.makeTool(
			"docker_pull_image",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_pull_image", "Pull a Docker image by reference.", rt.dockerPullImage),
		),
		rt.makeTool(
			"docker_build_image",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_build_image", "Build a Docker image from a workspace-relative build context.", rt.dockerBuildImage),
		),
		rt.makeTool(
			"docker_create_container",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_create_container", "Create a Docker container with optional workspace mounts, named volumes, env vars, published ports, and nalvin-managed Git credentials. Use ports to publish container ports to the host (e.g. 5173:5173 for Vite, 8000:8000 for a Python server); ports cannot be added after creation so plan all needed ports upfront. The default nalvin/dev image also keeps its own startup command, auto-publishes SSH and code-server, and uses uv-managed Python tooling, so prefer uv tool install or uvx for Python CLI apps and uv venv plus uv pip for library work instead of pip inside that container.", rt.dockerCreateContainer),
		),
		rt.makeTool(
			"docker_start_container",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_start_container", "Start a Docker container by name or ID.", rt.dockerStartContainer),
		),
		rt.makeTool(
			"docker_stop_container",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_stop_container", "Stop a Docker container by name or ID.", rt.dockerStopContainer),
		),
		rt.makeTool(
			"docker_remove_container",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_remove_container", "Remove a Docker container by name or ID.", rt.dockerRemoveContainer),
		),
		rt.makeTool(
			"docker_exec_foreground",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_exec_foreground", "Run a command inside a running Docker container and wait for it to complete. Returns stdout, stderr, exit code, and argv. Outputs are captured non-interactively; prefer machine-readable flags (--json, --format=json) where available. For long-running processes like dev servers, use docker_exec_background instead. In nalvin/dev containers, prefer uv tool install or uvx for Python CLI apps, and use uv venv plus uv pip for library work; uv-installed executables are placed on PATH.", rt.dockerExecForeground),
		),
		rt.makeTool(
			"docker_exec_background",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_exec_background", "Start a long-running command inside a container as a background process (e.g. dev servers, watchers, build processes). Returns immediately with a process_id that can be used with docker_exec_tail to read output and docker_exec_signal to stop the process. Dev servers must bind to 0.0.0.0 inside the container, not 127.0.0.1 or localhost (e.g. vite --host 0.0.0.0, uvicorn main:app --host 0.0.0.0). Ensure the container was created with the appropriate --ports for host access.", rt.dockerExecBackground),
		),
		rt.makeTool(
			"docker_exec_tail",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_exec_tail", "Read the last N lines of output from a background process started with docker_exec_background. Also reports whether the process is still running. Use this to check startup logs, monitor errors, or verify a dev server is ready.", rt.dockerExecTail),
		),
		rt.makeTool(
			"docker_exec_signal",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_exec_signal", "Send a signal to a background process started with docker_exec_background. Defaults to TERM. Use KILL for forceful termination.", rt.dockerExecSignal),
		),
		rt.makeTool(
			"docker_exec_list_processes",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("docker_exec_list_processes", "List all tracked background processes in a container, showing process_id, PID, running status, and original command.", rt.dockerExecListProcesses),
		),
	}
}

func (rt *agentRuntime) runDockerTool(ctx context.Context, input dockerToolInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	result, err := dockerpkg.NewRunner(rt.cfg).Run(ctx, dockerpkg.RawRunOptions{
		CWD:           paths.FilesPath,
		WorkspaceRoot: paths.FilesPath,
		Args:          input.Args,
		Host:          input.Host,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(result)
}

func (rt *agentRuntime) dockerListContainers(ctx context.Context, input dockerHostInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.jsonDockerList(ctx, input.Host, func(r *dockerpkg.Runner, cwd, host string) (any, dockerpkg.CommandResult, error) {
		return r.ListContainers(ctx, cwd, host)
	})
}

func (rt *agentRuntime) dockerListImages(ctx context.Context, input dockerHostInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.jsonDockerList(ctx, input.Host, func(r *dockerpkg.Runner, cwd, host string) (any, dockerpkg.CommandResult, error) {
		return r.ListImages(ctx, cwd, host)
	})
}

func (rt *agentRuntime) dockerListNetworks(ctx context.Context, input dockerHostInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.jsonDockerList(ctx, input.Host, func(r *dockerpkg.Runner, cwd, host string) (any, dockerpkg.CommandResult, error) {
		return r.ListNetworks(ctx, cwd, host)
	})
}

func (rt *agentRuntime) dockerListVolumes(ctx context.Context, input dockerHostInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.jsonDockerList(ctx, input.Host, func(r *dockerpkg.Runner, cwd, host string) (any, dockerpkg.CommandResult, error) {
		return r.ListVolumes(ctx, cwd, host)
	})
}

func (rt *agentRuntime) dockerPullImage(ctx context.Context, input dockerPullImageInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).PullImage(ctx, paths.FilesPath, input.Host, input.Image)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerBuildImage(ctx context.Context, input dockerBuildImageInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	contextPath, err := dockerpkg.ResolvePath(paths.FilesPath, input.ContextPath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	dockerfile := ""
	if strings.TrimSpace(input.Dockerfile) != "" {
		dockerfile, err = dockerpkg.ResolvePath(paths.FilesPath, input.Dockerfile)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
	}

	buildArgs, err := dockerpkg.ParseBuildArgAssignments(input.BuildArgs)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).BuildImage(ctx, dockerpkg.BuildOptions{
		Host:        input.Host,
		CWD:         paths.FilesPath,
		ContextPath: contextPath,
		Dockerfile:  dockerfile,
		Tag:         strings.TrimSpace(input.Tag),
		Target:      strings.TrimSpace(input.Target),
		BuildArgs:   buildArgs,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerCreateContainer(ctx context.Context, input dockerCreateContainerInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	binds, err := dockerpkg.ParseBindSpecs(input.Binds)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	binds, err = dockerpkg.ResolveBindSources(paths.FilesPath, binds)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	volumes, err := dockerpkg.ParseVolumeSpecs(input.Volumes)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	env, err := dockerpkg.ParseEnvAssignments(input.Env)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	repoPath := ""
	if strings.TrimSpace(input.RepoPath) != "" {
		repoPath, err = dockerpkg.ResolvePath(paths.FilesPath, input.RepoPath)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).CreateContainer(ctx, dockerpkg.CreateOptions{
		Host:           input.Host,
		CWD:            paths.FilesPath,
		Image:          strings.TrimSpace(input.Image),
		Name:           strings.TrimSpace(input.Name),
		WorkspacePath:  paths.FilesPath,
		RepoPath:       repoPath,
		MountWorkspace: input.MountWorkspace,
		Binds:          binds,
		Volumes:        volumes,
		Env:            env,
		Network:        strings.TrimSpace(input.Network),
		Workdir:        strings.TrimSpace(input.Workdir),
		Ports:          append([]string(nil), input.Ports...),
		Command:        append([]string(nil), input.Command...),
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerStartContainer(ctx context.Context, input dockerContainerInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.dockerContainerAction(ctx, input, func(r *dockerpkg.Runner, cwd, host, container string) (any, error) {
		return r.StartContainer(ctx, cwd, host, container)
	})
}

func (rt *agentRuntime) dockerStopContainer(ctx context.Context, input dockerContainerInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.dockerContainerAction(ctx, input, func(r *dockerpkg.Runner, cwd, host, container string) (any, error) {
		return r.StopContainer(ctx, cwd, host, container)
	})
}

func (rt *agentRuntime) dockerRemoveContainer(ctx context.Context, input dockerContainerInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return rt.dockerContainerAction(ctx, input, func(r *dockerpkg.Runner, cwd, host, container string) (any, error) {
		return r.RemoveContainer(ctx, cwd, host, container)
	})
}

func (rt *agentRuntime) dockerExecForeground(ctx context.Context, input dockerExecInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).Exec(ctx, paths.FilesPath, input.Host, input.Container, input.Command)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerExecBackground(ctx context.Context, input dockerExecBackgroundInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).ExecBackground(ctx, paths.FilesPath, input.Host, input.Container, input.ProcessID, input.Command)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerExecTail(ctx context.Context, input dockerExecTailInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).ExecTail(ctx, paths.FilesPath, input.Host, input.Container, input.ProcessID, input.Lines)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerExecSignal(ctx context.Context, input dockerExecSignalInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).ExecSignal(ctx, paths.FilesPath, input.Host, input.Container, input.ProcessID, input.Signal)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerExecListProcesses(ctx context.Context, input dockerExecListProcessesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := dockerpkg.NewRunner(rt.cfg).ExecListProcesses(ctx, paths.FilesPath, input.Host, input.Container)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) jsonDockerList(ctx context.Context, host string, fn func(r *dockerpkg.Runner, cwd, host string) (any, dockerpkg.CommandResult, error)) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, result, err := fn(dockerpkg.NewRunner(rt.cfg), paths.FilesPath, host)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if !result.OK {
		return fantasy.NewTextErrorResponse(strings.TrimSpace(result.Stderr)), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) dockerContainerAction(ctx context.Context, input dockerContainerInput, fn func(r *dockerpkg.Runner, cwd, host, container string) (any, error)) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := fn(dockerpkg.NewRunner(rt.cfg), paths.FilesPath, input.Host, input.Container)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}
