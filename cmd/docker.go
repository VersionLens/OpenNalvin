package cmd

import (
	"fmt"
	"strings"

	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	"github.com/versionlens/OpenNalvin/internal/output"
	"github.com/spf13/cobra"
)

var (
	dockerHost             string
	dockerCreateImage      string
	dockerCreateName       string
	dockerCreateMountWS    bool
	dockerCreateRepoPath   string
	dockerCreateBind       []string
	dockerCreateVolume     []string
	dockerCreateNetwork    string
	dockerCreateEnv        []string
	dockerCreateWorkdir    string
	dockerCreateCommandRaw string
	dockerBuildDockerfile  string
	dockerBuildTag         string
	dockerBuildArgs        []string
	dockerBuildTarget      string
)

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Manage Docker images and containers from nalvin",
}

var dockerContainersCmd = &cobra.Command{
	Use:   "containers",
	Short: "List Docker containers",
}

var dockerContainersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Docker containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		items, result, err := dockerpkg.NewRunner(cfg).ListContainers(cmd.Context(), paths.FilesPath, dockerHost)
		if err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("%s", strings.TrimSpace(result.Stderr))
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}
		if len(items) == 0 {
			w.Line("No containers found.")
			return nil
		}
		for _, item := range items {
			parts := []string{fallback(item.Names, item.ID), item.Status, item.Image}
			if item.SSHEndpoint != "" {
				parts = append(parts, "ssh="+item.SSHEndpoint)
			}
			if item.CodeServerURL != "" {
				parts = append(parts, "code-server="+item.CodeServerURL)
			}
			w.Line("%s", strings.Join(parts, "  "))
		}
		return nil
	},
}

var dockerImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List Docker images",
}

var dockerImagesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Docker images",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		items, result, err := dockerpkg.NewRunner(cfg).ListImages(cmd.Context(), paths.FilesPath, dockerHost)
		if err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("%s", strings.TrimSpace(result.Stderr))
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}
		if len(items) == 0 {
			w.Line("No images found.")
			return nil
		}
		for _, item := range items {
			w.Line("%s:%s  %s", item.Repository, item.Tag, item.Size)
		}
		return nil
	},
}

var dockerNetworksCmd = &cobra.Command{
	Use:   "networks",
	Short: "List Docker networks",
}

var dockerNetworksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Docker networks",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		items, result, err := dockerpkg.NewRunner(cfg).ListNetworks(cmd.Context(), paths.FilesPath, dockerHost)
		if err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("%s", strings.TrimSpace(result.Stderr))
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}
		if len(items) == 0 {
			w.Line("No networks found.")
			return nil
		}
		for _, item := range items {
			w.Line("%s  %s  %s", item.Name, item.Driver, item.Scope)
		}
		return nil
	},
}

var dockerVolumesCmd = &cobra.Command{
	Use:   "volumes",
	Short: "List Docker volumes",
}

var dockerVolumesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Docker volumes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		items, result, err := dockerpkg.NewRunner(cfg).ListVolumes(cmd.Context(), paths.FilesPath, dockerHost)
		if err != nil {
			return err
		}
		if !result.OK {
			return fmt.Errorf("%s", strings.TrimSpace(result.Stderr))
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(items)
		}
		if len(items) == 0 {
			w.Line("No volumes found.")
			return nil
		}
		for _, item := range items {
			w.Line("%s  %s", item.Name, item.Driver)
		}
		return nil
	},
}

var dockerPullCmd = &cobra.Command{
	Use:   "pull <image>",
	Short: "Pull a Docker image",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		payload, err := dockerpkg.NewRunner(cfg).PullImage(cmd.Context(), paths.FilesPath, dockerHost, args[0])
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

var dockerBuildCmd = &cobra.Command{
	Use:   "build <context-path>",
	Short: "Build a Docker image from a workspace-relative context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		contextPath, err := dockerpkg.ResolvePath(paths.FilesPath, args[0])
		if err != nil {
			return err
		}

		dockerfile := ""
		if strings.TrimSpace(dockerBuildDockerfile) != "" {
			dockerfile, err = dockerpkg.ResolvePath(paths.FilesPath, dockerBuildDockerfile)
			if err != nil {
				return err
			}
		}

		buildArgs, err := dockerpkg.ParseBuildArgAssignments(dockerBuildArgs)
		if err != nil {
			return err
		}

		payload, err := dockerpkg.NewRunner(cfg).BuildImage(cmd.Context(), dockerpkg.BuildOptions{
			Host:        dockerHost,
			CWD:         paths.FilesPath,
			ContextPath: contextPath,
			Dockerfile:  dockerfile,
			Tag:         strings.TrimSpace(dockerBuildTag),
			Target:      strings.TrimSpace(dockerBuildTarget),
			BuildArgs:   buildArgs,
		})
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

var dockerCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a Docker container",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}

		binds, err := dockerpkg.ParseBindSpecs(dockerCreateBind)
		if err != nil {
			return err
		}
		binds, err = dockerpkg.ResolveBindSources(paths.FilesPath, binds)
		if err != nil {
			return err
		}

		volumes, err := dockerpkg.ParseVolumeSpecs(dockerCreateVolume)
		if err != nil {
			return err
		}
		env, err := dockerpkg.ParseEnvAssignments(dockerCreateEnv)
		if err != nil {
			return err
		}

		repoPath := ""
		if strings.TrimSpace(dockerCreateRepoPath) != "" {
			repoPath, err = dockerpkg.ResolvePath(paths.FilesPath, dockerCreateRepoPath)
			if err != nil {
				return err
			}
		}

		payload, err := dockerpkg.NewRunner(cfg).CreateContainer(cmd.Context(), dockerpkg.CreateOptions{
			Host:           dockerHost,
			CWD:            paths.FilesPath,
			Image:          strings.TrimSpace(dockerCreateImage),
			Name:           strings.TrimSpace(dockerCreateName),
			WorkspacePath:  paths.FilesPath,
			RepoPath:       repoPath,
			MountWorkspace: dockerCreateMountWS,
			Binds:          binds,
			Volumes:        volumes,
			Env:            env,
			Network:        strings.TrimSpace(dockerCreateNetwork),
			Workdir:        strings.TrimSpace(dockerCreateWorkdir),
			Command:        dockerpkg.NormalizeCommandArgs(nil, dockerCreateCommandRaw),
		})
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

var dockerStartCmd = &cobra.Command{
	Use:   "start <container>",
	Short: "Start a Docker container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		payload, err := dockerpkg.NewRunner(cfg).StartContainer(cmd.Context(), paths.FilesPath, dockerHost, args[0])
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

var dockerStopCmd = &cobra.Command{
	Use:   "stop <container>",
	Short: "Stop a Docker container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		payload, err := dockerpkg.NewRunner(cfg).StopContainer(cmd.Context(), paths.FilesPath, dockerHost, args[0])
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

var dockerRemoveCmd = &cobra.Command{
	Use:   "remove <container>",
	Short: "Remove a Docker container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		payload, err := dockerpkg.NewRunner(cfg).RemoveContainer(cmd.Context(), paths.FilesPath, dockerHost, args[0])
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

var dockerExecCmd = &cobra.Command{
	Use:   "exec <container> -- <command...>",
	Short: "Run a command in a running Docker container",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		payload, err := dockerpkg.NewRunner(cfg).Exec(cmd.Context(), paths.FilesPath, dockerHost, args[0], args[1:])
		if err != nil {
			return err
		}
		return renderDockerResult(cmd, payload)
	},
}

func init() {
	dockerCmd.PersistentFlags().StringVar(&dockerHost, "host", "", "Docker daemon host override")

	dockerBuildCmd.Flags().StringVar(&dockerBuildDockerfile, "dockerfile", "", "workspace-relative Dockerfile path")
	dockerBuildCmd.Flags().StringVar(&dockerBuildTag, "tag", "", "image tag")
	dockerBuildCmd.Flags().StringArrayVar(&dockerBuildArgs, "build-arg", nil, "build arg in KEY=VALUE form")
	dockerBuildCmd.Flags().StringVar(&dockerBuildTarget, "target", "", "build target stage")

	dockerCreateCmd.Flags().StringVar(&dockerCreateImage, "image", "", "image to use (defaults to docker.default_image)")
	dockerCreateCmd.Flags().StringVar(&dockerCreateName, "name", "", "container name")
	dockerCreateCmd.Flags().BoolVar(&dockerCreateMountWS, "mount-workspace", false, "mount the active workspace root at /workspace")
	dockerCreateCmd.Flags().StringVar(&dockerCreateRepoPath, "repo-path", "", "workspace-relative repo path to mount at /workspace")
	dockerCreateCmd.Flags().StringArrayVar(&dockerCreateBind, "bind", nil, "bind mount in SOURCE:TARGET[:ro] form with workspace-relative source")
	dockerCreateCmd.Flags().StringArrayVar(&dockerCreateVolume, "volume", nil, "named volume mount in NAME:TARGET[:ro] form")
	dockerCreateCmd.Flags().StringVar(&dockerCreateNetwork, "network", "", "network to attach")
	dockerCreateCmd.Flags().StringArrayVar(&dockerCreateEnv, "env", nil, "env var in KEY=VALUE form")
	dockerCreateCmd.Flags().StringVar(&dockerCreateWorkdir, "workdir", "", "working directory inside the container")
	dockerCreateCmd.Flags().StringVar(&dockerCreateCommandRaw, "cmd", "", "command to run inside the container")

	dockerContainersCmd.AddCommand(dockerContainersListCmd)
	dockerImagesCmd.AddCommand(dockerImagesListCmd)
	dockerNetworksCmd.AddCommand(dockerNetworksListCmd)
	dockerVolumesCmd.AddCommand(dockerVolumesListCmd)

	dockerCmd.AddCommand(dockerContainersCmd)
	dockerCmd.AddCommand(dockerImagesCmd)
	dockerCmd.AddCommand(dockerNetworksCmd)
	dockerCmd.AddCommand(dockerVolumesCmd)
	dockerCmd.AddCommand(dockerPullCmd)
	dockerCmd.AddCommand(dockerBuildCmd)
	dockerCmd.AddCommand(dockerCreateCmd)
	dockerCmd.AddCommand(dockerStartCmd)
	dockerCmd.AddCommand(dockerStopCmd)
	dockerCmd.AddCommand(dockerRemoveCmd)
	dockerCmd.AddCommand(dockerExecCmd)

	rootCmd.AddCommand(dockerCmd)
}

func renderDockerResult(cmd *cobra.Command, payload any) error {
	w := output.FromContext(cmd.Context())
	if w.IsJSON() {
		return w.JSON(payload)
	}

	switch typed := payload.(type) {
	case dockerpkg.PullImageResult:
		return renderDockerCommandResult(w, typed.Result)
	case dockerpkg.BuildImageResult:
		return renderDockerCommandResult(w, typed.Result)
	case dockerpkg.CreateContainerResult:
		if typed.ContainerID != "" {
			w.Line("%s", typed.ContainerID)
		}
		return renderDockerCommandResult(w, typed.Result)
	case dockerpkg.ContainerActionResult:
		return renderDockerCommandResult(w, typed.Result)
	case dockerpkg.ExecResult:
		if typed.Stdout != "" {
			w.Line("%s", strings.TrimRight(typed.Stdout, "\n"))
		}
		if typed.Stderr != "" {
			w.Line("%s", strings.TrimRight(typed.Stderr, "\n"))
		}
		if !typed.OK {
			return fmt.Errorf("docker exec failed with exit code %d", typed.ExitCode)
		}
	default:
		return w.JSON(payload)
	}
	return nil
}

func renderDockerCommandResult(w *output.Writer, result dockerpkg.CommandResult) error {
	if result.Stdout != "" {
		w.Line("%s", strings.TrimRight(result.Stdout, "\n"))
	}
	if result.Stderr != "" {
		w.Line("%s", strings.TrimRight(result.Stderr, "\n"))
	}
	if !result.OK {
		return fmt.Errorf("docker command failed with exit code %d", result.ExitCode)
	}
	return nil
}

func fallback(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
