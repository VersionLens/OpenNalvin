package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/versionlens/OpenNalvin/internal/config"
	gitpkg "github.com/versionlens/OpenNalvin/internal/git"
	"github.com/versionlens/OpenNalvin/internal/output"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	gitStatusRepoPath string
	gitAddRepoPath    string
	gitCommitRepoPath string
	gitCommitMessage  string
	gitPushRepoPath   string
	gitPushRemote     string
	gitPushBranch     string
	gitRepoTreeBranch string
	gitRepoLogBranch  string
)

type gitRepoCreatePayload struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type gitRepoTreePayload = gitpkg.RepoTree

type gitRepoCommitsPayload = gitpkg.RepoCommitList

type gitCheckoutPayload struct {
	Repo        string `json:"repo"`
	Destination string `json:"destination"`
	RemoteURL   string `json:"remote_url"`
}

type gitStatusEntry struct {
	Path     string `json:"path"`
	Staging  string `json:"staging"`
	Worktree string `json:"worktree"`
	Extra    string `json:"extra,omitempty"`
}

type gitStatusPayload struct {
	RepoPath string           `json:"repo_path"`
	Clean    bool             `json:"clean"`
	Entries  []gitStatusEntry `json:"entries"`
}

type gitAddPayload struct {
	RepoPath string   `json:"repo_path"`
	Paths    []string `json:"paths"`
}

type gitCommitPayload struct {
	RepoPath string `json:"repo_path"`
	Message  string `json:"message"`
	Commit   string `json:"commit"`
}

type gitPushPayload struct {
	RepoPath string `json:"repo_path"`
	Remote   string `json:"remote"`
	Branch   string `json:"branch,omitempty"`
}

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "Manage nalvin Git repos and worktrees",
}

var gitRepoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage global nalvin bare repos",
}

var gitRepoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed bare repos",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		repos, err := gitpkg.ListRepos(cfg)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(repos)
		}
		if len(repos) == 0 {
			w.Line("No repos found.")
			return nil
		}
		for _, repo := range repos {
			w.Line("%s", repo.Name)
			w.Line("  path: %s", repo.Path)
		}
		return nil
	},
}

var gitRepoCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a managed bare repo",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		repo, err := gitpkg.CreateBareRepo(cfg, args[0])
		if err != nil {
			if errors.Is(err, gogit.ErrRepositoryAlreadyExists) {
				return fmt.Errorf("repo %q already exists", args[0])
			}
			return err
		}

		payload := gitRepoCreatePayload{Name: repo.Name, Path: repo.Path}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Created repo %s", payload.Name)
		w.Line("Path: %s", payload.Path)
		return nil
	},
}

var gitRepoTreeCmd = &cobra.Command{
	Use:   "tree <name>",
	Short: "Show the latest committed file tree for a managed repo branch",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		payload, err := gitpkg.InspectRepoTree(cfg, args[0], gitRepoTreeBranch)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("%s @ %s", payload.RepoName, payload.Branch)
		w.Line("Commit: %s", payload.Commit)
		if len(payload.Lines) == 0 {
			w.Line("(empty tree)")
			return nil
		}
		for _, line := range payload.Lines {
			w.Line("%s", line)
		}
		return nil
	},
}

var gitRepoCommitsCmd = &cobra.Command{
	Use:   "commits <name>",
	Short: "List commits for a managed repo branch",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		payload, err := gitpkg.ListRepoCommits(cfg, args[0], gitRepoLogBranch)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("%s @ %s", payload.RepoName, payload.Branch)
		w.Line("Head: %s", payload.Head)
		if len(payload.Commits) == 0 {
			w.Line("(no commits)")
			return nil
		}
		for _, commit := range payload.Commits {
			w.Line("%s %s", commit.ShortHash, commit.Subject)
		}
		return nil
	},
}

var gitCheckoutCmd = &cobra.Command{
	Use:   "checkout <repo> [dest]",
	Short: "Clone a managed repo into the active workspace",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, ok := config.FromContext(cmd.Context())
		if !ok {
			return fmt.Errorf("config not found in command context")
		}

		destination := ""
		if len(args) > 1 {
			destination = args[1]
		} else {
			name, err := gitpkg.NormalizeRepoName(args[0])
			if err != nil {
				return err
			}
			destination = name
		}

		_, relativeDestination, absoluteDestination, err := workspacepkg.ResolveFilePath(cmd.Context(), cfg, destination, false)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(absoluteDestination), 0o755); err != nil {
			return err
		}

		remoteURL, err := gitpkg.CheckoutRepo(cfg, args[0], absoluteDestination)
		if err != nil {
			return err
		}

		payload := gitCheckoutPayload{
			Repo:        args[0],
			Destination: relativeDestination,
			RemoteURL:   remoteURL,
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Checked out %s to %s", payload.Repo, payload.Destination)
		w.Line("Remote: %s", payload.RemoteURL)
		return nil
	},
}

var gitStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show worktree status for a checked-out repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, repoPath, relativeRepoPath, err := resolveGitRepoPath(cmd.Context(), gitStatusRepoPath)
		if err != nil {
			return err
		}

		status, err := gitpkg.RepoStatus(repoPath)
		if err != nil {
			return err
		}

		payload := gitStatusPayload{
			RepoPath: relativeRepoPath,
			Clean:    status.IsClean(),
			Entries:  make([]gitStatusEntry, 0, len(status)),
		}

		paths := make([]string, 0, len(status))
		for path := range status {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			fileStatus := status[path]
			payload.Entries = append(payload.Entries, gitStatusEntry{
				Path:     path,
				Staging:  string(fileStatus.Staging),
				Worktree: string(fileStatus.Worktree),
				Extra:    fileStatus.Extra,
			})
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		if payload.Clean {
			w.Line("%s is clean", payload.RepoPath)
			return nil
		}
		for _, entry := range payload.Entries {
			w.Line("%s [%s%s]", entry.Path, entry.Staging, entry.Worktree)
		}
		_ = cfg
		return nil
	},
}

var gitAddCmd = &cobra.Command{
	Use:   "add <pathspec...>",
	Short: "Stage files in a checked-out repo",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, repoPath, relativeRepoPath, err := resolveGitRepoPath(cmd.Context(), gitAddRepoPath)
		if err != nil {
			return err
		}
		if err := gitpkg.AddPaths(repoPath, args); err != nil {
			return err
		}

		payload := gitAddPayload{RepoPath: relativeRepoPath, Paths: append([]string(nil), args...)}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Staged %d path(s) in %s", len(payload.Paths), payload.RepoPath)
		return nil
	},
}

var gitCommitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Commit staged changes in a checked-out repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, repoPath, relativeRepoPath, err := resolveGitRepoPath(cmd.Context(), gitCommitRepoPath)
		if err != nil {
			return err
		}
		message := strings.TrimSpace(gitCommitMessage)
		if message == "" {
			return fmt.Errorf("commit message is required")
		}

		hash, err := gitpkg.CommitRepo(cfg, repoPath, message)
		if err != nil {
			return err
		}

		payload := gitCommitPayload{
			RepoPath: relativeRepoPath,
			Message:  message,
			Commit:   hash,
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Committed %s", payload.Commit)
		return nil
	},
}

var gitPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push committed changes to the managed SSH Git server",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, repoPath, relativeRepoPath, err := resolveGitRepoPath(cmd.Context(), gitPushRepoPath)
		if err != nil {
			return err
		}
		remoteName := strings.TrimSpace(gitPushRemote)
		if remoteName == "" {
			remoteName = "origin"
		}

		if err := gitpkg.PushRepo(cfg, repoPath, remoteName, strings.TrimSpace(gitPushBranch)); err != nil {
			return err
		}

		payload := gitPushPayload{
			RepoPath: relativeRepoPath,
			Remote:   remoteName,
			Branch:   strings.TrimSpace(gitPushBranch),
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(payload)
		}
		w.Line("Pushed %s to %s", payload.RepoPath, payload.Remote)
		return nil
	},
}

func init() {
	gitStatusCmd.Flags().StringVar(&gitStatusRepoPath, "repo-path", "", "workspace-relative path to the checked-out repo")
	gitAddCmd.Flags().StringVar(&gitAddRepoPath, "repo-path", "", "workspace-relative path to the checked-out repo")
	gitCommitCmd.Flags().StringVar(&gitCommitRepoPath, "repo-path", "", "workspace-relative path to the checked-out repo")
	gitCommitCmd.Flags().StringVarP(&gitCommitMessage, "message", "m", "", "commit message")
	gitPushCmd.Flags().StringVar(&gitPushRepoPath, "repo-path", "", "workspace-relative path to the checked-out repo")
	gitPushCmd.Flags().StringVar(&gitPushRemote, "remote", "origin", "remote name to push to")
	gitPushCmd.Flags().StringVar(&gitPushBranch, "branch", "", "branch name to push (defaults to current branch)")
	gitRepoTreeCmd.Flags().StringVar(&gitRepoTreeBranch, "branch", "", "branch name to inspect (defaults to the repo default branch)")
	gitRepoCommitsCmd.Flags().StringVar(&gitRepoLogBranch, "branch", "", "branch name to inspect (defaults to the repo default branch)")

	gitRepoCmd.AddCommand(gitRepoListCmd)
	gitRepoCmd.AddCommand(gitRepoCreateCmd)
	gitRepoCmd.AddCommand(gitRepoTreeCmd)
	gitRepoCmd.AddCommand(gitRepoCommitsCmd)

	gitCmd.AddCommand(gitRepoCmd)
	gitCmd.AddCommand(gitCheckoutCmd)
	gitCmd.AddCommand(gitStatusCmd)
	gitCmd.AddCommand(gitAddCmd)
	gitCmd.AddCommand(gitCommitCmd)
	gitCmd.AddCommand(gitPushCmd)

	rootCmd.AddCommand(gitCmd)
}

func resolveGitRepoPath(ctx context.Context, rawPath string) (config.Config, string, string, error) {
	cfg, ok := config.FromContext(ctx)
	if !ok {
		return config.Config{}, "", "", fmt.Errorf("config not found in command context")
	}

	if strings.TrimSpace(rawPath) == "" {
		return config.Config{}, "", "", fmt.Errorf("--repo-path is required")
	}

	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, cfg, rawPath, false)
	if err != nil {
		return config.Config{}, "", "", err
	}
	return cfg, absolutePath, relativePath, nil
}
