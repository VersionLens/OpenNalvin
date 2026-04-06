package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	gitpkg "github.com/versionlens/OpenNalvin/internal/git"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	xssh "golang.org/x/crypto/ssh"
)

func TestGitToolsEnabledByDefaultButHidden(t *testing.T) {
	rt, _, cleanup := newGitToolRuntime(t, ToolSelection{})
	defer cleanup()

	for _, id := range []string{"git", "git_list_repos", "git_create_repo", "git_delete_repo", "git_repo_tree", "git_repo_commits"} {
		tool := requireTool(t, rt.catalogResult().Tools, id)
		if !tool.Enabled || tool.Pinned || tool.Visible {
			t.Fatalf("expected %s to be enabled, hidden, and unpinned by default: %#v", id, tool)
		}
		if !tool.DefaultEnabled || tool.DefaultPinned {
			t.Fatalf("expected %s defaults to be enabled and not pinned: %#v", id, tool)
		}
	}
}

func TestSearchToolsQueryGitRevealsGitToolFamily(t *testing.T) {
	rt, ctx, cleanup := newGitToolRuntime(t, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "git", "git_list_repos", "git_create_repo", "git_delete_repo", "git_repo_tree", "git_repo_commits"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	defer cleanup()

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "git"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	got := append([]string(nil), payload.RevealedToolIDs...)
	sort.Strings(got)
	want := []string{"git", "git_create_repo", "git_delete_repo", "git_list_repos", "git_repo_commits", "git_repo_tree"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected revealed git tools: got %v want %v", got, want)
	}
}

func TestSearchToolsSpecificGitQueriesRevealExpectedTools(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{query: "create git repo", want: "git_create_repo"},
		{query: "list repos", want: "git_list_repos"},
		{query: "delete git repo", want: "git_delete_repo"},
		{query: "repo tree", want: "git_repo_tree"},
		{query: "commit history", want: "git_repo_commits"},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			rt, ctx, cleanup := newGitToolRuntime(t, ToolSelection{
				EnabledToolIDs: []string{"search_tools", "git", "git_list_repos", "git_create_repo", "git_delete_repo", "git_repo_tree", "git_repo_commits"},
				PinnedToolIDs:  []string{"search_tools"},
			})
			defer cleanup()

			resp, err := rt.searchTools(ctx, searchToolsInput{Query: tc.query}, fantasy.ToolCall{})
			if err != nil {
				t.Fatalf("search tools: %v", err)
			}

			payload := decodeSearchToolsResponse(t, resp)
			if len(payload.Tools) == 0 || payload.Tools[0].ID != tc.want {
				t.Fatalf("unexpected top git tool for %q: %#v", tc.query, payload.Tools)
			}
		})
	}
}

func TestGitManagedRepoHelperTools(t *testing.T) {
	rt, ctx, cleanup := newGitToolRuntime(t, ToolSelection{})
	defer cleanup()

	createResp, err := rt.gitCreateRepo(ctx, gitManagedRepoInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git create repo: %v", err)
	}
	created := decodeJSONToolResponse[gitManagedRepoPayload](t, createResp)
	if created.Name != "demo" || created.SSHURL == "" || created.Deleted {
		t.Fatalf("unexpected created payload: %#v", created)
	}

	listResp, err := rt.gitListRepos(ctx, struct{}{}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git list repos: %v", err)
	}
	listed := decodeJSONToolResponse[[]gitManagedRepoPayload](t, listResp)
	if len(listed) != 1 || listed[0].Name != "demo" {
		t.Fatalf("unexpected listed repos payload: %#v", listed)
	}

	deleteResp, err := rt.gitDeleteRepo(ctx, gitManagedRepoInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git delete repo: %v", err)
	}
	deleted := decodeJSONToolResponse[gitManagedRepoPayload](t, deleteResp)
	if !deleted.Deleted || deleted.Name != "demo" {
		t.Fatalf("unexpected deleted payload: %#v", deleted)
	}

	missingResp, err := rt.gitDeleteRepo(ctx, gitManagedRepoInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git delete missing repo: %v", err)
	}
	if !missingResp.IsError || !strings.Contains(missingResp.Content, "was not found") {
		t.Fatalf("expected missing delete error, got %#v", missingResp)
	}
}

func TestGitToolIntegrationAgainstEmbeddedSSHServer(t *testing.T) {
	rt, ctx, cleanup := newGitToolRuntime(t, ToolSelection{})
	defer cleanup()

	server, cfg := startGitToolSSHServer(t, rt.cfg)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(shutdownCtx)
	}()

	createResp, err := rt.gitCreateRepo(ctx, gitManagedRepoInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git create repo: %v", err)
	}
	created := decodeJSONToolResponse[gitManagedRepoPayload](t, createResp)

	cloneResp, err := rt.runGitTool(ctx, gitToolInput{Args: "clone " + created.SSHURL + " demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git clone: %v", err)
	}
	cloneResult := decodeJSONToolResponse[gitpkg.ClientCommandResult](t, cloneResp)
	if !cloneResult.OK || cloneResult.ExitCode != 0 {
		t.Fatalf("expected successful clone result, got %#v", cloneResult)
	}

	worktreeFile := filepath.Join(cfg.Workspace.FilesRoot, "alpha", "demo", "hello.txt")
	if err := os.WriteFile(worktreeFile, []byte("hello from git tool\n"), 0o644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}

	statusResp, err := rt.runGitTool(ctx, gitToolInput{Args: "-C demo status --short"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	statusResult := decodeJSONToolResponse[gitpkg.ClientCommandResult](t, statusResp)
	if !strings.Contains(statusResult.Stdout, "hello.txt") {
		t.Fatalf("expected git status to mention hello.txt, got %#v", statusResult)
	}

	for _, args := range []string{
		"-C demo add hello.txt",
		`-C demo commit -m "init"`,
		"-C demo push origin master",
	} {
		resp, err := rt.runGitTool(ctx, gitToolInput{Args: args}, fantasy.ToolCall{})
		if err != nil {
			t.Fatalf("run git tool %q: %v", args, err)
		}
		result := decodeJSONToolResponse[gitpkg.ClientCommandResult](t, resp)
		if !result.OK || result.ExitCode != 0 {
			t.Fatalf("expected successful git result for %q, got %#v", args, result)
		}
	}

	if err := os.WriteFile(filepath.Join(cfg.Workspace.FilesRoot, "alpha", "demo", ".gitignore"), []byte("hello.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Workspace.FilesRoot, "alpha", "demo", "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}

	for _, args := range []string{
		"-C demo add .gitignore keep.txt",
		`-C demo commit -m "hide hello"`,
		"-C demo push origin master",
	} {
		resp, err := rt.runGitTool(ctx, gitToolInput{Args: args}, fantasy.ToolCall{})
		if err != nil {
			t.Fatalf("run git tool %q: %v", args, err)
		}
		result := decodeJSONToolResponse[gitpkg.ClientCommandResult](t, resp)
		if !result.OK || result.ExitCode != 0 {
			t.Fatalf("expected successful git result for %q, got %#v", args, result)
		}
	}

	treeResp, err := rt.gitRepoTree(ctx, gitManagedRepoBranchInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git repo tree: %v", err)
	}
	treePayload := decodeJSONToolResponse[gitpkg.RepoTree](t, treeResp)
	if treePayload.Branch != "master" {
		t.Fatalf("expected master tree payload, got %#v", treePayload)
	}
	var sawKeep, sawIgnored bool
	for _, entry := range treePayload.Entries {
		if entry.Path == "keep.txt" {
			sawKeep = true
		}
		if entry.Path == "hello.txt" {
			sawIgnored = true
		}
	}
	if !sawKeep || sawIgnored {
		t.Fatalf("unexpected tree payload entries: %#v", treePayload.Entries)
	}

	commitsResp, err := rt.gitRepoCommits(ctx, gitManagedRepoBranchInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git repo commits: %v", err)
	}
	commitsPayload := decodeJSONToolResponse[gitpkg.RepoCommitList](t, commitsResp)
	if commitsPayload.Branch != "master" || len(commitsPayload.Commits) != 2 {
		t.Fatalf("unexpected commits payload: %#v", commitsPayload)
	}
	if commitsPayload.Commits[0].Subject != "hide hello" || commitsPayload.Commits[1].Subject != "init" {
		t.Fatalf("unexpected commit subjects: %#v", commitsPayload.Commits)
	}

	bareRepo, err := gogit.PlainOpen(filepath.Join(cfg.Git.RepoRoot, "demo.git"))
	if err != nil {
		t.Fatalf("open bare repo: %v", err)
	}
	refs, err := bareRepo.References()
	if err != nil {
		t.Fatalf("iterate bare refs: %v", err)
	}
	defer refs.Close()

	foundBranch := false
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() && !ref.Hash().IsZero() {
			foundBranch = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan bare refs: %v", err)
	}
	if !foundBranch {
		t.Fatal("expected pushed branch ref to exist in bare repository")
	}

	deleteResp, err := rt.gitDeleteRepo(ctx, gitManagedRepoInput{Name: "demo"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git delete repo: %v", err)
	}
	deleted := decodeJSONToolResponse[gitManagedRepoPayload](t, deleteResp)
	if !deleted.Deleted {
		t.Fatalf("expected delete marker, got %#v", deleted)
	}

	listResp, err := rt.gitListRepos(ctx, struct{}{}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("git list repos after delete: %v", err)
	}
	listed := decodeJSONToolResponse[[]gitManagedRepoPayload](t, listResp)
	if len(listed) != 0 {
		t.Fatalf("expected repo list to be empty after delete, got %#v", listed)
	}
}

func newGitToolRuntime(t *testing.T, selection ToolSelection) (*agentRuntime, context.Context, func()) {
	t.Helper()

	cfg, paths := newGitToolConfig(t)
	ctx := configpkg.WithContext(context.Background(), cfg)
	ctx = workspacepkg.WithPaths(ctx, paths)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, selection)
	if err != nil {
		t.Fatalf("new agent runtime: %v", err)
	}

	cleanup := func() {
		if rt.mcpManager != nil {
			_ = rt.mcpManager.Close()
		}
	}
	return rt, ctx, cleanup
}

func newGitToolConfig(t *testing.T) (configpkg.Config, workspacepkg.Paths) {
	t.Helper()

	home := t.TempDir()
	repoRoot := filepath.Join(home, "git-repos")
	workspaceFilesRoot := filepath.Join(home, "workspaces")
	workspacePath := filepath.Join(workspaceFilesRoot, "alpha")
	hostKeyPath := filepath.Join(home, "git-ssh", "host_ed25519")
	addr := mustFreeGitToolAddr(t)
	clientKeyPath, clientPublicKey := writeGitToolKeyPair(t, home, "id_alice")

	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("mkdir workspace path: %v", err)
	}

	cfg := configpkg.Config{
		Git: configpkg.GitConfig{
			RepoRoot: repoRoot,
			SSH: configpkg.GitSSHConfig{
				Enabled:     true,
				Addr:        addr,
				HostKeyPath: hostKeyPath,
			},
			DefaultClient: configpkg.GitDefaultClientConfig{
				User:           "alice",
				PrivateKeyPath: clientKeyPath,
				Host:           "127.0.0.1",
				AuthorName:     "Alice Example",
				AuthorEmail:    "alice@example.com",
			},
			RepoDefaults: configpkg.GitRepoDefaultsConfig{
				PublicRead:     true,
				DefaultWriters: []string{"alice"},
			},
			Users: map[string]configpkg.GitUserConfig{
				"alice": {PublicKeys: []string{clientPublicKey}},
			},
		},
		Workspace: configpkg.WorkspaceConfig{
			Current:   "alpha",
			DBRoot:    filepath.Join(home, "workspace-db"),
			FilesRoot: workspaceFilesRoot,
		},
	}

	return cfg, workspacepkg.Paths{
		Name:      "alpha",
		DBPath:    filepath.Join(home, "workspace-db", "alpha.sqlite"),
		FilesPath: workspacePath,
	}
}

func startGitToolSSHServer(t *testing.T, cfg configpkg.Config) (*gitpkg.SSHServer, configpkg.Config) {
	t.Helper()

	server, err := gitpkg.NewSSHServer(cfg, nil, nil)
	if err != nil {
		t.Fatalf("new git ssh server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start git ssh server: %v", err)
	}
	return server, cfg
}

func writeGitToolKeyPair(t *testing.T, dir, basename string) (string, string) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	block, err := xssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privateKeyPEM := pem.EncodeToMemory(block)

	privateKeyPath := filepath.Join(dir, basename)
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	sshPublicKey, err := xssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatalf("create public key: %v", err)
	}
	return privateKeyPath, strings.TrimSpace(string(xssh.MarshalAuthorizedKey(sshPublicKey)))
}

func mustFreeGitToolAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free addr: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}
