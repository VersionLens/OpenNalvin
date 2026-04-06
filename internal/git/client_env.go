package git

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	xssh "golang.org/x/crypto/ssh"
)

const DockerHostAlias = "host.docker.internal"

func DefaultClientEnv(cfg configpkg.Config) []string {
	env := []string{"GIT_TERMINAL_PROMPT=0"}

	if name := strings.TrimSpace(cfg.Git.DefaultClient.AuthorName); name != "" {
		env = append(env,
			"GIT_AUTHOR_NAME="+name,
			"GIT_COMMITTER_NAME="+name,
		)
	}
	if email := strings.TrimSpace(cfg.Git.DefaultClient.AuthorEmail); email != "" {
		env = append(env,
			"GIT_AUTHOR_EMAIL="+email,
			"GIT_COMMITTER_EMAIL="+email,
		)
	}

	return env
}

func DockerClientEnv(cfg configpkg.Config) []string {
	env := append([]string(nil), DefaultClientEnv(cfg)...)
	env = append(env, DockerURLRewriteEnv(cfg)...)
	return env
}

func DefaultClientSSHCommand(privateKeyPath, knownHostsPath string) string {
	return strings.Join([]string{
		"ssh",
		"-i", shellQuote(privateKeyPath),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "LogLevel=ERROR",
		"-o", "UserKnownHostsFile=" + shellQuote(knownHostsPath),
	}, " ")
}

func EnsureKnownHostsFile(cfg configpkg.Config) (string, error) {
	privateKey, err := os.ReadFile(strings.TrimSpace(cfg.Git.SSH.HostKeyPath))
	if err != nil {
		return "", err
	}
	signer, err := xssh.ParsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}

	key := strings.TrimSpace(string(xssh.MarshalAuthorizedKey(signer.PublicKey())))
	lines := make([]string, 0, len(DefaultClientKnownHostsHosts(cfg)))
	for _, host := range DefaultClientKnownHostsHosts(cfg) {
		lines = append(lines, fmt.Sprintf("[%s]:%s %s", host, DefaultClientSSHPort(cfg), key))
	}
	path := filepath.Join(filepath.Dir(strings.TrimSpace(cfg.Git.SSH.HostKeyPath)), "known_hosts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func DefaultClientHost(cfg configpkg.Config) string {
	host := strings.TrimSpace(cfg.Git.DefaultClient.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	return host
}

func DefaultClientSSHPort(cfg configpkg.Config) string {
	port := "4222"
	if _, parsedPort, err := SplitSSHAddr(strings.TrimSpace(cfg.Git.SSH.Addr)); err == nil && parsedPort != "" {
		port = parsedPort
	}
	return port
}

func DefaultClientKnownHostsHosts(cfg configpkg.Config) []string {
	hosts := []string{DefaultClientHost(cfg)}
	if rewrite := DockerRewriteTargetHost(cfg); rewrite != "" {
		hosts = append(hosts, rewrite)
	}

	unique := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" || slices.Contains(unique, host) {
			continue
		}
		unique = append(unique, host)
	}
	return unique
}

func DockerRewriteTargetHost(cfg configpkg.Config) string {
	if !NeedsDockerGitHostRewrite(cfg) {
		return ""
	}
	return DockerHostAlias
}

func NeedsDockerGitHostRewrite(cfg configpkg.Config) bool {
	return isLoopbackGitHost(DefaultClientHost(cfg))
}

func DockerURLRewriteEnv(cfg configpkg.Config) []string {
	targetHost := DockerRewriteTargetHost(cfg)
	if targetHost == "" {
		return nil
	}

	user := strings.TrimSpace(cfg.Git.DefaultClient.User)
	if user == "" {
		return nil
	}

	targetPrefix := fmt.Sprintf("ssh://%s@%s:%s/", user, targetHost, DefaultClientSSHPort(cfg))
	sourcePrefix := fmt.Sprintf("ssh://%s@%s:%s/", user, DefaultClientHost(cfg), DefaultClientSSHPort(cfg))

	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=url." + targetPrefix + ".insteadof",
		"GIT_CONFIG_VALUE_0=" + sourcePrefix,
	}
}

func DockerExtraHosts(cfg configpkg.Config) []string {
	targetHost := DockerRewriteTargetHost(cfg)
	if targetHost == "" {
		return nil
	}
	return []string{targetHost + ":host-gateway"}
}

func isLoopbackGitHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
