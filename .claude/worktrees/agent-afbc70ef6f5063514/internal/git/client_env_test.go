package git

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	xssh "golang.org/x/crypto/ssh"
)

func TestDockerClientEnvRewritesLoopbackSSHURLs(t *testing.T) {
	cfg := configpkg.Config{
		Git: configpkg.GitConfig{
			SSH: configpkg.GitSSHConfig{
				Addr: "127.0.0.1:4222",
			},
			DefaultClient: configpkg.GitDefaultClientConfig{
				User:       "nalvin",
				Host:       "127.0.0.1",
				AuthorName: "nalvin",
			},
		},
	}

	env := DockerClientEnv(cfg)
	got := strings.Join(env, "\n")
	if !strings.Contains(got, "GIT_CONFIG_COUNT=1") {
		t.Fatalf("expected docker git rewrite count, got %v", env)
	}
	if !strings.Contains(got, "GIT_CONFIG_KEY_0=url.ssh://nalvin@host.docker.internal:4222/.insteadof") {
		t.Fatalf("expected docker git rewrite key, got %v", env)
	}
	if !strings.Contains(got, "GIT_CONFIG_VALUE_0=ssh://nalvin@127.0.0.1:4222/") {
		t.Fatalf("expected docker git rewrite value, got %v", env)
	}
	if extraHosts := DockerExtraHosts(cfg); len(extraHosts) != 1 || extraHosts[0] != "host.docker.internal:host-gateway" {
		t.Fatalf("unexpected docker extra hosts: %v", extraHosts)
	}
}

func TestEnsureKnownHostsFileIncludesDockerAliasForLoopbackGitHost(t *testing.T) {
	root := t.TempDir()
	hostKeyPath := writeClientEnvTestKeyPair(t, root, "ssh_host_ed25519")
	cfg := configpkg.Config{
		Git: configpkg.GitConfig{
			SSH: configpkg.GitSSHConfig{
				Addr:        "127.0.0.1:4222",
				HostKeyPath: hostKeyPath,
			},
			DefaultClient: configpkg.GitDefaultClientConfig{
				Host: "127.0.0.1",
			},
		},
	}

	knownHostsPath, err := EnsureKnownHostsFile(cfg)
	if err != nil {
		t.Fatalf("ensure known_hosts: %v", err)
	}

	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "[127.0.0.1]:4222 ") {
		t.Fatalf("expected loopback known_hosts entry, got:\n%s", text)
	}
	if !strings.Contains(text, "[host.docker.internal]:4222 ") {
		t.Fatalf("expected docker alias known_hosts entry, got:\n%s", text)
	}
}

func writeClientEnvTestKeyPair(t *testing.T, dir, name string) string {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	block, err := xssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return path
}
