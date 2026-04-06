package cmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/versionlens/OpenNalvin/internal/buildinfo"
	"github.com/versionlens/OpenNalvin/internal/config"
	gitpkg "github.com/versionlens/OpenNalvin/internal/git"
	serverpkg "github.com/versionlens/OpenNalvin/internal/server"
	xssh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestGitCLIEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, repoRoot := writeGitCLIConfig(t, home)
	srv := serverpkg.New(cfg, buildinfo.Current(), serverpkg.AssetSource{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			t.Fatalf("close server: %v", err)
		}
	}()

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	out, err := executeRoot(t, "--json", "git", "repo", "create", "demo")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	var created gitRepoCreatePayload
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("unmarshal create payload: %v", err)
	}
	if created.Name != "demo" || created.Path != filepath.Join(repoRoot, "demo.git") {
		t.Fatalf("unexpected repo create payload: %#v", created)
	}

	if _, err := executeRoot(t, "git", "repo", "create", "demo"); err == nil || !strings.Contains(err.Error(), `repo "demo" already exists`) {
		t.Fatalf("expected duplicate repo create error, got %v", err)
	}

	out, err = executeRoot(t, "--json", "git", "repo", "list")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	var repos []gitpkg.RepoInfo
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		t.Fatalf("unmarshal repo list payload: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "demo" {
		t.Fatalf("unexpected repo list payload: %#v", repos)
	}

	out, err = executeRoot(t, "--workspace", "alpha", "--json", "git", "checkout", "demo")
	if err != nil {
		t.Fatalf("checkout repo: %v", err)
	}
	var checkout gitCheckoutPayload
	if err := json.Unmarshal([]byte(out), &checkout); err != nil {
		t.Fatalf("unmarshal checkout payload: %v", err)
	}
	if checkout.Destination != "demo" {
		t.Fatalf("expected checkout destination demo, got %#v", checkout)
	}

	worktreeFile := filepath.Join(cfg.Workspace.FilesRoot, "alpha", "demo", "hello.txt")
	if err := os.WriteFile(worktreeFile, []byte("first tracked file\n"), 0o644); err != nil {
		t.Fatalf("write initial tracked file: %v", err)
	}

	out, err = executeRoot(t, "--workspace", "alpha", "--json", "git", "status", "--repo-path", "demo")
	if err != nil {
		t.Fatalf("status before add: %v", err)
	}
	var status gitStatusPayload
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("unmarshal status payload: %v", err)
	}
	if status.Clean {
		t.Fatalf("expected dirty worktree after file write, got %#v", status)
	}

	if _, err := executeRoot(t, "--workspace", "alpha", "git", "add", "--repo-path", "demo", "hello.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	out, err = executeRoot(t, "--workspace", "alpha", "--json", "git", "commit", "--repo-path", "demo", "-m", "initial commit")
	if err != nil {
		t.Fatalf("git commit: %v", err)
	}
	var firstCommit gitCommitPayload
	if err := json.Unmarshal([]byte(out), &firstCommit); err != nil {
		t.Fatalf("unmarshal first commit payload: %v", err)
	}
	if _, err := executeRoot(t, "--workspace", "alpha", "git", "push", "--repo-path", "demo"); err != nil {
		t.Fatalf("git push: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cfg.Workspace.FilesRoot, "alpha", "demo", ".gitignore"), []byte("hello.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Workspace.FilesRoot, "alpha", "demo", "keep.txt"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}
	if _, err := executeRoot(t, "--workspace", "alpha", "git", "add", "--repo-path", "demo", ".gitignore", "keep.txt"); err != nil {
		t.Fatalf("git add second commit files: %v", err)
	}
	out, err = executeRoot(t, "--workspace", "alpha", "--json", "git", "commit", "--repo-path", "demo", "-m", "add ignore rules")
	if err != nil {
		t.Fatalf("git second commit: %v", err)
	}
	var secondCommit gitCommitPayload
	if err := json.Unmarshal([]byte(out), &secondCommit); err != nil {
		t.Fatalf("unmarshal second commit payload: %v", err)
	}
	if _, err := executeRoot(t, "--workspace", "alpha", "git", "push", "--repo-path", "demo"); err != nil {
		t.Fatalf("git second push: %v", err)
	}

	out, err = executeRoot(t, "--json", "git", "repo", "tree", "demo")
	if err != nil {
		t.Fatalf("repo tree: %v", err)
	}
	var treePayload gitRepoTreePayload
	if err := json.Unmarshal([]byte(out), &treePayload); err != nil {
		t.Fatalf("unmarshal repo tree payload: %v", err)
	}
	if treePayload.Branch != "master" {
		t.Fatalf("expected default branch master, got %#v", treePayload)
	}
	if treePayload.Commit != secondCommit.Commit {
		t.Fatalf("expected tree head %q, got %#v", secondCommit.Commit, treePayload)
	}
	treePaths := make([]string, 0, len(treePayload.Entries))
	for _, entry := range treePayload.Entries {
		treePaths = append(treePaths, entry.Path)
	}
	if strings.Contains(strings.Join(treePaths, ","), "hello.txt") {
		t.Fatalf("expected tree to omit ignored tracked file, got %#v", treePayload.Entries)
	}
	if !strings.Contains(strings.Join(treePayload.Lines, "\n"), "keep.txt") {
		t.Fatalf("expected tree lines to mention keep.txt, got %#v", treePayload.Lines)
	}

	out, err = executeRoot(t, "--json", "git", "repo", "commits", "demo")
	if err != nil {
		t.Fatalf("repo commits: %v", err)
	}
	var commitsPayload gitRepoCommitsPayload
	if err := json.Unmarshal([]byte(out), &commitsPayload); err != nil {
		t.Fatalf("unmarshal repo commits payload: %v", err)
	}
	if commitsPayload.Branch != "master" || commitsPayload.Head != secondCommit.Commit {
		t.Fatalf("unexpected repo commits payload: %#v", commitsPayload)
	}
	if len(commitsPayload.Commits) != 2 {
		t.Fatalf("expected 2 commits, got %#v", commitsPayload.Commits)
	}
	if commitsPayload.Commits[0].Hash != secondCommit.Commit || commitsPayload.Commits[0].Subject != "add ignore rules" {
		t.Fatalf("unexpected latest commit entry: %#v", commitsPayload.Commits[0])
	}
	if commitsPayload.Commits[1].Hash != firstCommit.Commit || commitsPayload.Commits[1].Subject != "initial commit" {
		t.Fatalf("unexpected first commit entry: %#v", commitsPayload.Commits[1])
	}

	bareRepo, err := gogit.PlainOpen(filepath.Join(repoRoot, "demo.git"))
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
}

func writeGitCLIConfig(t *testing.T, home string) (config.Config, string) {
	t.Helper()

	repoRoot := filepath.Join(home, "git-repos")
	workspaceDBRoot := filepath.Join(home, "workspace-db")
	workspaceFilesRoot := filepath.Join(home, "workspaces")
	hostKeyPath := filepath.Join(home, "git-ssh", "host_ed25519")
	clientKeyPath, clientPublicKey := writeGitCLIKeyPair(t, home, "id_alice")
	addr := freeGitCLIAddr(t)

	cfgDoc := map[string]any{
		"workspace": map[string]any{
			"current":    "alpha",
			"db_root":    workspaceDBRoot,
			"files_root": workspaceFilesRoot,
		},
		"git": map[string]any{
			"repo_root": repoRoot,
			"ssh": map[string]any{
				"enabled":       true,
				"addr":          addr,
				"host_key_path": hostKeyPath,
			},
			"default_client": map[string]any{
				"user":             "alice",
				"private_key_path": clientKeyPath,
				"host":             "127.0.0.1",
				"author_name":      "Alice Example",
				"author_email":     "alice@example.com",
			},
			"repo_defaults": map[string]any{
				"public_read":     true,
				"default_writers": []string{"alice"},
			},
			"users": map[string]any{
				"alice": map[string]any{
					"public_keys": []string{clientPublicKey},
				},
			},
		},
	}

	configBytes, err := yaml.Marshal(cfgDoc)
	if err != nil {
		t.Fatalf("marshal config yaml: %v", err)
	}

	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), configBytes, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	resetViperBindings()
	config.SetDefaultConfig(bytes.NewReader(defaultConfig))
	if err := config.Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg, repoRoot
}

func writeGitCLIKeyPair(t *testing.T, dir, basename string) (string, string) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

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

func freeGitCLIAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free addr: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}
