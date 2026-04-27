package git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	xssh "golang.org/x/crypto/ssh"
)

func TestSSHServerForwardsGitProtocolEnv(t *testing.T) {
	cfg, clientKeyPath, _, addr := newSSHTestConfig(t)
	if _, err := CreateBareRepo(cfg, "demo"); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}

	runner := &recordingRunner{}
	server, err := NewSSHServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})

	output, err := runSSHCommand(addr, "alice", clientKeyPath, map[string]string{"GIT_PROTOCOL": "version=2"}, "git-upload-pack demo.git")
	if err != nil {
		t.Fatalf("run git-upload-pack: %v (output=%q)", err, string(output))
	}

	call := runner.lastCall()
	if call.service != "upload-pack" {
		t.Fatalf("expected upload-pack service, got %q", call.service)
	}
	if call.repoName != "demo" {
		t.Fatalf("expected normalized repo name demo, got %q", call.repoName)
	}
	if !strings.Contains(call.repoPath, filepath.Join("git-repos", "demo.git")) {
		t.Fatalf("expected repo path to target bare repo, got %q", call.repoPath)
	}
	if len(call.env) != 1 || call.env[0] != "GIT_PROTOCOL=version=2" {
		t.Fatalf("expected forwarded git protocol env, got %#v", call.env)
	}
}

func TestSSHServerRejectsUnknownKey(t *testing.T) {
	cfg, _, _, addr := newSSHTestConfig(t)
	if _, err := CreateBareRepo(cfg, "demo"); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}

	server, err := NewSSHServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})

	unknownKeyPath, _ := writeAuthorizedKeyPair(t, t.TempDir(), "id_unknown")
	if _, err := runSSHCommand(addr, "alice", unknownKeyPath, nil, "git-upload-pack demo.git"); err == nil {
		t.Fatal("expected authentication with unknown key to fail")
	}
}

func TestSSHServerRejectsUnsupportedCommandsAndUnauthorizedReads(t *testing.T) {
	cfg, clientKeyPath, bobKeyPath, addr := newSSHTestConfig(t)
	cfg.Git.RepoDefaults.PublicRead = false
	cfg.Git.RepoDefaults.DefaultWriters = []string{"alice"}
	if _, err := CreateBareRepo(cfg, "demo"); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}

	server, err := NewSSHServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})

	output, err := runSSHCommand(addr, "alice", clientKeyPath, nil, "echo nope")
	if err == nil {
		t.Fatal("expected unsupported command to fail")
	}
	if !strings.Contains(string(output), "only git-upload-pack and git-receive-pack are supported") {
		t.Fatalf("expected unsupported command error, got %q", string(output))
	}

	output, err = runSSHCommand(addr, "bob", bobKeyPath, nil, "git-upload-pack demo.git")
	if err == nil {
		t.Fatal("expected unauthorized read to fail")
	}
	if !strings.Contains(string(output), "permission denied") {
		t.Fatalf("expected permission denied error, got %q", string(output))
	}

	output, err = runSSHCommand(addr, "alice", clientKeyPath, nil, "git-upload-pack ../demo")
	if err == nil {
		t.Fatal("expected path traversal to fail")
	}
	if !strings.Contains(string(output), "must stay within the repo root") {
		t.Fatalf("expected repo traversal rejection, got %q", string(output))
	}
}

type recordingRunner struct {
	mu    sync.Mutex
	calls []runnerCall
}

type runnerCall struct {
	service  string
	repoPath string
	repoName string
	env      []string
}

func (r *recordingRunner) RunUploadPack(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return r.record("upload-pack", repoPath, env)
}

func (r *recordingRunner) RunReceivePack(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return r.record("receive-pack", repoPath, env)
}

func (r *recordingRunner) record(service, repoPath string, env []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, runnerCall{
		service:  service,
		repoPath: repoPath,
		repoName: strings.TrimSuffix(filepath.Base(repoPath), ".git"),
		env:      append([]string(nil), env...),
	})
	return nil
}

func (r *recordingRunner) lastCall() runnerCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return runnerCall{}
	}
	return r.calls[len(r.calls)-1]
}

func newSSHTestConfig(t *testing.T) (configpkg.Config, string, string, string) {
	t.Helper()

	home := t.TempDir()
	repoRoot := filepath.Join(home, "git-repos")
	hostKeyPath := filepath.Join(home, "ssh", "host_ed25519")
	addr := mustFreeLocalAddr(t)

	aliceKeyPath, alicePublicKey := writeAuthorizedKeyPair(t, home, "id_alice")
	bobKeyPath, bobPublicKey := writeAuthorizedKeyPair(t, home, "id_bob")

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
				PrivateKeyPath: aliceKeyPath,
				Host:           "127.0.0.1",
				AuthorName:     "Alice Example",
				AuthorEmail:    "alice@example.com",
			},
			RepoDefaults: configpkg.GitRepoDefaultsConfig{
				PublicRead:     true,
				DefaultWriters: []string{"alice"},
			},
			Users: map[string]configpkg.GitUserConfig{
				"alice": {PublicKeys: []string{alicePublicKey}},
				"bob":   {PublicKeys: []string{bobPublicKey}},
			},
		},
	}

	return cfg, aliceKeyPath, bobKeyPath, addr
}

func writeAuthorizedKeyPair(t *testing.T, dir, basename string) (string, string) {
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

func runSSHCommand(addr, user, privateKeyPath string, env map[string]string, command string) ([]byte, error) {
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, err
	}
	signer, err := xssh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, err
	}

	client, err := xssh.Dial("tcp", addr, &xssh.ClientConfig{
		User:            user,
		Auth:            []xssh.AuthMethod{xssh.PublicKeys(signer)},
		HostKeyCallback: xssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	for key, value := range env {
		if err := session.Setenv(key, value); err != nil {
			return nil, err
		}
	}

	return session.CombinedOutput(command)
}

func mustFreeLocalAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free addr: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}
