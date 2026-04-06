package agent

import (
	"context"
	"errors"
	"fmt"

	"charm.land/fantasy"
	gogit "github.com/go-git/go-git/v5"
	gitpkg "github.com/versionlens/OpenNalvin/internal/git"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type gitToolInput struct {
	Args string `json:"args" jsonschema_description:"Raw git CLI arguments after the word git. Examples: status, -C demo diff, add hello.txt, commit -m 'Initial commit', push origin master, or clone ssh://user@host:port/repo.git demo."`
}

type gitManagedRepoInput struct {
	Name string `json:"name" jsonschema_description:"Managed git repo name, for example demo or team/demo."`
}

type gitManagedRepoBranchInput struct {
	Name   string `json:"name" jsonschema_description:"Managed git repo name, for example demo or team/demo."`
	Branch string `json:"branch,omitempty" jsonschema_description:"Optional branch name. Omit it to inspect the repo default branch."`
}

type gitManagedRepoPayload struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	SSHURL  string `json:"ssh_url"`
	Deleted bool   `json:"deleted,omitempty"`
}

func (rt *agentRuntime) gitTools() []runtimeTool {
	return []runtimeTool{
		rt.makeTool(
			"git",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("git", "Run the git CLI against the active workspace or managed repo root using raw CLI-style args. Use for git status, diff, add, commit, branch, checkout, clone, fetch, pull, push, log, show, remote, rev-parse, merge-base, and ls-remote.", rt.runGitTool),
		),
		rt.makeTool(
			"git_list_repos",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("git_list_repos", "List managed git repos available on the nalvin git server, including clone-ready SSH URLs.", rt.gitListRepos),
		),
		rt.makeTool(
			"git_create_repo",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("git_create_repo", "Create a managed git repo on the nalvin git server and return its clone-ready SSH URL.", rt.gitCreateRepo),
		),
		rt.makeTool(
			"git_delete_repo",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("git_delete_repo", "Delete a managed git repo from the nalvin git server by repo name and return the deleted repo record.", rt.gitDeleteRepo),
		),
		rt.makeTool(
			"git_repo_tree",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("git_repo_tree", "Show the committed file tree for a managed git repo branch. Omitting branch uses the repo default branch and gitignored paths are filtered out.", rt.gitRepoTree),
		),
		rt.makeTool(
			"git_repo_commits",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("git_repo_commits", "List commits for a managed git repo branch. Omitting branch uses the repo default branch.", rt.gitRepoCommits),
		),
	}
}

func (rt *agentRuntime) runGitTool(ctx context.Context, input gitToolInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	runner := gitpkg.NewClientRunner(rt.cfg)
	result, err := runner.Run(ctx, gitpkg.ClientRunOptions{
		CWD:          paths.FilesPath,
		Args:         input.Args,
		AllowedRoots: []string{paths.FilesPath, rt.cfg.Git.RepoRoot},
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(result)
}

func (rt *agentRuntime) gitListRepos(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	repos, err := gitpkg.ListRepos(rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload := make([]gitManagedRepoPayload, 0, len(repos))
	for _, repo := range repos {
		item, err := rt.managedRepoPayload(repo.Name, false)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		payload = append(payload, item)
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) gitCreateRepo(ctx context.Context, input gitManagedRepoInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	repo, err := gitpkg.CreateBareRepo(rt.cfg, input.Name)
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryAlreadyExists) {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("git repo %q already exists", input.Name)), nil
		}
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := rt.managedRepoPayload(repo.Name, false)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) gitDeleteRepo(ctx context.Context, input gitManagedRepoInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	repo, err := gitpkg.DeleteBareRepo(rt.cfg, input.Name)
	if err != nil {
		if errors.Is(err, gitpkg.ErrRepoNotFound) {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("git repo %q was not found", input.Name)), nil
		}
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := rt.managedRepoPayload(repo.Name, true)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) gitRepoTree(ctx context.Context, input gitManagedRepoBranchInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	payload, err := gitpkg.InspectRepoTree(rt.cfg, input.Name, input.Branch)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) gitRepoCommits(ctx context.Context, input gitManagedRepoBranchInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	payload, err := gitpkg.ListRepoCommits(rt.cfg, input.Name, input.Branch)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(payload)
}

func (rt *agentRuntime) managedRepoPayload(repoName string, deleted bool) (gitManagedRepoPayload, error) {
	name, path, err := gitpkg.RepoPath(rt.cfg, repoName)
	if err != nil {
		return gitManagedRepoPayload{}, err
	}
	sshURL, err := gitpkg.RepoSSHURL(rt.cfg, name)
	if err != nil {
		return gitManagedRepoPayload{}, err
	}

	return gitManagedRepoPayload{
		Name:    name,
		Path:    path,
		SSHURL:  sshURL,
		Deleted: deleted,
	}, nil
}
