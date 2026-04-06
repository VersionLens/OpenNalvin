package docker

import (
	"context"
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

func TestRunnerRunBuildResolvesWorkspacePathsAndHostOverride(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	appDir := filepath.Join(workspaceRoot, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	result, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "build --file app/Dockerfile --tag demo:latest app",
		Host:          "tcp://docker.example:2375",
	})
	if err != nil {
		t.Fatalf("run docker build: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected successful build result, got %#v", result)
	}

	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=--host\narg=tcp://docker.example:2375\narg=build") {
		t.Fatalf("expected host override and build command in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--file\narg="+filepath.Join(appDir, "Dockerfile")) {
		t.Fatalf("expected resolved dockerfile path in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg="+appDir) {
		t.Fatalf("expected resolved build context in log, got:\n%s", log)
	}
}

func TestRunnerRunRejectsCreateBindEscapingWorkspace(t *testing.T) {
	cfg, workspaceRoot, _ := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	_, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "create -v ../secret:/secret alpine/git",
	})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected workspace escape error, got %v", err)
	}
}

func TestCreateContainerAppliesDevDefaultsWorkspaceMountAndGitCredentials(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	payload, err := runner.CreateContainer(context.Background(), CreateOptions{
		CWD:            workspaceRoot,
		WorkspacePath:  workspaceRoot,
		MountWorkspace: true,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if payload.Image != defaultDevImage {
		t.Fatalf("expected default image %s, got %#v", defaultDevImage, payload)
	}
	if payload.Workdir != DefaultWorkspaceMountTarget {
		t.Fatalf("expected default workdir %s, got %#v", DefaultWorkspaceMountTarget, payload)
	}
	if payload.ContainerID != "container123" {
		t.Fatalf("expected fake container id, got %#v", payload)
	}
	if payload.SSHEndpoint != "ssh://nalvin@127.0.0.1:41022" {
		t.Fatalf("expected ssh endpoint in payload, got %#v", payload)
	}
	if payload.CodeServerURL != "http://127.0.0.1:41080/" {
		t.Fatalf("expected code-server url in payload, got %#v", payload)
	}

	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=--volume\narg="+workspaceRoot+":"+DefaultWorkspaceMountTarget) {
		t.Fatalf("expected workspace mount in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--workdir\narg="+DefaultWorkspaceMountTarget) {
		t.Fatalf("expected workdir flag in log, got:\n%s", log)
	}
	if strings.Contains(log, "arg=--entrypoint\narg=sh") {
		t.Fatalf("did not expect keep-alive entrypoint override for dev image, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--publish\narg=127.0.0.1::22") || !strings.Contains(log, "arg=--publish\narg=127.0.0.1::8080") {
		t.Fatalf("expected dev service ports in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg="+defaultDevImage) {
		t.Fatalf("expected dev image in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--volume\narg="+devClaudeConfigDir) || !strings.Contains(log, "arg=--volume\narg="+devCodexConfigDir) {
		t.Fatalf("expected anonymous auth volume mounts in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=GIT_SSH_COMMAND=") {
		t.Fatalf("expected GIT_SSH_COMMAND docker env arg in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--env\narg=GIT_CONFIG_COUNT=1") {
		t.Fatalf("expected docker git URL rewrite env in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--env\narg=GIT_CONFIG_KEY_0=url.ssh://nalvin@host.docker.internal:4222/.insteadof") {
		t.Fatalf("expected docker git URL rewrite key in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--env\narg=GIT_CONFIG_VALUE_0=ssh://nalvin@127.0.0.1:4222/") {
		t.Fatalf("expected docker git URL rewrite value in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--add-host\narg=host.docker.internal:host-gateway") {
		t.Fatalf("expected docker host alias mapping in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--volume\narg="+cfg.Git.DefaultClient.PrivateKeyPath+":"+containerGitKeyPath+":ro") {
		t.Fatalf("expected git private key mount in log, got:\n%s", log)
	}

	knownHostsPath := filepath.Join(filepath.Dir(cfg.Git.SSH.HostKeyPath), "known_hosts")
	if _, err := os.Stat(knownHostsPath); err != nil {
		t.Fatalf("expected known_hosts file to exist: %v", err)
	}
	if !strings.Contains(log, "arg=--volume\narg="+knownHostsPath+":"+containerKnownHostsPath+":ro") {
		t.Fatalf("expected known_hosts mount in log, got:\n%s", log)
	}
	if !strings.Contains(log, ":"+devAuthorizedKeysPath+":ro") {
		t.Fatalf("expected authorized_keys mount in log, got:\n%s", log)
	}
	knownHostsData, err := os.ReadFile(knownHostsPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(knownHostsData), "[127.0.0.1]:4222 ") || !strings.Contains(string(knownHostsData), "[host.docker.internal]:4222 ") {
		t.Fatalf("expected known_hosts aliases for host and docker host, got:\n%s", string(knownHostsData))
	}

	authorizedKeysPath := filepath.Join(filepath.Dir(cfg.Git.SSH.HostKeyPath), "docker", "authorized_keys")
	authorizedKeysData, err := os.ReadFile(authorizedKeysPath)
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	expectedKey, err := authorizedKeyFromPrivateKeyPath(cfg.Git.DefaultClient.PrivateKeyPath)
	if err != nil {
		t.Fatalf("derive authorized key: %v", err)
	}
	if !strings.Contains(string(authorizedKeysData), expectedKey) {
		t.Fatalf("expected default client key in authorized_keys, got:\n%s", string(authorizedKeysData))
	}
	if !strings.Contains(string(authorizedKeysData), strings.TrimSpace(cfg.Git.Users["alice"].PublicKeys[0])) {
		t.Fatalf("expected configured git user key in authorized_keys, got:\n%s", string(authorizedKeysData))
	}
}

func TestCreateContainerUsesKeepAliveForNonDevImages(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	payload, err := runner.CreateContainer(context.Background(), CreateOptions{
		CWD:           workspaceRoot,
		WorkspacePath: workspaceRoot,
		Image:         "alpine/git",
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if payload.Image != "alpine/git" {
		t.Fatalf("expected explicit image to remain alpine/git, got %#v", payload)
	}

	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=--entrypoint\narg=sh\narg=alpine/git\narg=-lc\narg="+defaultKeepAliveCommand) {
		t.Fatalf("expected keep-alive entrypoint for non-dev image, got:\n%s", log)
	}
	if strings.Contains(log, "arg=--publish\narg=127.0.0.1::22") || strings.Contains(log, "arg=--publish\narg=127.0.0.1::8080") {
		t.Fatalf("did not expect dev ports for non-dev image, got:\n%s", log)
	}
	if strings.Contains(log, "arg=--volume\narg="+devClaudeConfigDir) || strings.Contains(log, "arg=--volume\narg="+devCodexConfigDir) {
		t.Fatalf("did not expect dev auth volumes for non-dev image, got:\n%s", log)
	}
}

func TestListContainersIncludesStructuredEndpoints(t *testing.T) {
	cfg, workspaceRoot, _ := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	items, result, err := runner.ListContainers(context.Background(), workspaceRoot, "")
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected successful list result, got %#v", result)
	}
	if len(items) != 1 {
		t.Fatalf("expected one container, got %#v", items)
	}
	if items[0].Image != defaultDevImage {
		t.Fatalf("expected dev image in list payload, got %#v", items[0])
	}
	if items[0].SSHEndpoint != "ssh://nalvin@127.0.0.1:41022" {
		t.Fatalf("expected ssh endpoint in list payload, got %#v", items[0])
	}
	if items[0].CodeServerURL != "http://127.0.0.1:41080/" {
		t.Fatalf("expected code-server url in list payload, got %#v", items[0])
	}
	if len(items[0].PublishedPorts) != 2 {
		t.Fatalf("expected published ports in list payload, got %#v", items[0])
	}
}

func TestBuildImageRequiresContextPath(t *testing.T) {
	cfg, workspaceRoot, _ := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	_, err := runner.BuildImage(context.Background(), BuildOptions{
		CWD: workspaceRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "context path is required") {
		t.Fatalf("expected context path error, got %v", err)
	}
}

func TestValidateDockerInfo(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	result, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "info",
	})
	if err != nil {
		t.Fatalf("run docker info: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected successful info result, got %#v", result)
	}
	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=info") {
		t.Fatalf("expected info in log, got:\n%s", log)
	}
}

func TestValidateDockerVersion(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	result, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "version",
	})
	if err != nil {
		t.Fatalf("run docker version: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected successful version result, got %#v", result)
	}
	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=version") {
		t.Fatalf("expected version in log, got:\n%s", log)
	}
}

func TestValidateDockerInspect(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	result, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "inspect --format '{{.Id}}' worker",
	})
	if err != nil {
		t.Fatalf("run docker inspect: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected successful inspect result, got %#v", result)
	}
	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=inspect") {
		t.Fatalf("expected inspect in log, got:\n%s", log)
	}
}

func TestValidateDockerInspectRejectsDangerousFlags(t *testing.T) {
	cfg, workspaceRoot, _ := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	_, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "inspect --env-file secrets.env worker",
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected rejection of --env-file for inspect, got %v", err)
	}
}

func TestDockerExecInjectsTermAndNoColor(t *testing.T) {
	cfg, workspaceRoot, logPath := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	_, err := runner.Exec(context.Background(), workspaceRoot, "", "worker", []string{"ls", "-la"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	log := readDockerLog(t, logPath)
	if !strings.Contains(log, "arg=-e\narg=TERM=dumb") {
		t.Fatalf("expected TERM=dumb in exec argv, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=-e\narg=NO_COLOR=1") {
		t.Fatalf("expected NO_COLOR=1 in exec argv, got:\n%s", log)
	}
}

func TestValidateDockerInspectRequiresRef(t *testing.T) {
	cfg, workspaceRoot, _ := newDockerTestConfig(t)
	runner := NewRunner(cfg)

	_, err := runner.Run(context.Background(), RawRunOptions{
		CWD:           workspaceRoot,
		WorkspaceRoot: workspaceRoot,
		Args:          "inspect --format '{{.Id}}'",
	})
	if err == nil || !strings.Contains(err.Error(), "requires at least one") {
		t.Fatalf("expected missing ref error, got %v", err)
	}
}

func newDockerTestConfig(t *testing.T) (configpkg.Config, string, string) {
	t.Helper()

	root := t.TempDir()
	logPath := filepath.Join(root, "docker.log")
	scriptPath := filepath.Join(root, "fake-docker.sh")
	workspaceRoot := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace root: %v", err)
	}

	privateKeyPath, _ := writeDockerTestKeyPair(t, root, "id_nalvin")
	hostKeyPath, _ := writeDockerTestKeyPair(t, root, "ssh_host_ed25519")
	_, alicePublicKey := writeDockerTestKeyPair(t, root, "id_alice")
	writeFakeDockerScript(t, scriptPath)
	t.Setenv("NALVIN_FAKE_DOCKER_LOG", logPath)

	return configpkg.Config{
		Docker: configpkg.DockerConfig{
			Binary:       scriptPath,
			DefaultImage: defaultDevImage,
		},
		Git: configpkg.GitConfig{
			SSH: configpkg.GitSSHConfig{
				Addr:        "127.0.0.1:4222",
				HostKeyPath: hostKeyPath,
			},
			DefaultClient: configpkg.GitDefaultClientConfig{
				User:           "nalvin",
				PrivateKeyPath: privateKeyPath,
				Host:           "127.0.0.1",
				AuthorName:     "nalvin",
				AuthorEmail:    "nalvin@example.com",
			},
			Users: map[string]configpkg.GitUserConfig{
				"alice": {
					PublicKeys: []string{alicePublicKey},
				},
			},
		},
	}, workspaceRoot, logPath
}

func writeDockerTestKeyPair(t *testing.T, dir, name string) (string, string) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
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
	sshPublicKey, err := xssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return path, strings.TrimSpace(string(xssh.MarshalAuthorizedKey(sshPublicKey)))
}

func writeFakeDockerScript(t *testing.T, path string) {
	t.Helper()

	script := `#!/bin/sh
LOG="$NALVIN_FAKE_DOCKER_LOG"
{
  printf '%s\n' '---'
  printf 'cwd=%s\n' "$PWD"
  for arg in "$@"; do
    printf 'arg=%s\n' "$arg"
  done
  env | LC_ALL=C sort | while IFS= read -r line; do
    case "$line" in
      GIT_*) printf 'env=%s\n' "$line" ;;
    esac
  done
} >> "$LOG"

if [ "$1" = "--host" ]; then
  shift
  shift
fi

case "$1" in
  info)
    printf '%s\n' '{"ServerVersion":"24.0.0","OperatingSystem":"Docker Desktop"}'
    ;;
  version)
    printf '%s\n' '{"Client":{"Version":"24.0.0"},"Server":{"Version":"24.0.0"}}'
    ;;
  ps)
    printf '%s\n' '{"ID":"abc123abc123","Image":"nalvin/dev","Status":"Up 1 hour","Names":"worker"}'
    ;;
  images)
    printf '%s\n' '{"ID":"img123","Repository":"nalvin/dev","Tag":"latest","Size":"55MB"}'
    ;;
  network)
    printf '%s\n' '{"ID":"net123","Name":"bridge","Driver":"bridge","Scope":"local"}'
    ;;
  volume)
    printf '%s\n' '{"Driver":"local","Name":"cache"}'
    ;;
  pull)
    printf '%s\n' "pulled $2"
    ;;
  build)
    printf '%s\n' 'built image'
    ;;
  create)
    printf '%s\n' 'container123'
    ;;
  inspect)
    printf '%s\n' '[{"Id":"abc123abc123def4567890","Name":"/worker","NetworkSettings":{"Ports":{"22/tcp":[{"HostIp":"127.0.0.1","HostPort":"41022"}],"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"41080"}]}}},{"Id":"container123","Name":"/container123","NetworkSettings":{"Ports":{"22/tcp":[{"HostIp":"127.0.0.1","HostPort":"41022"}],"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"41080"}]}}}]'
    ;;
  start|stop|rm)
    printf '%s\n' "$2"
    ;;
  exec)
    shift
    while [ $# -gt 0 ]; do
      case "$1" in
        -e|--env|-w|--workdir|-u|--user) shift; shift ;;
        -*) shift ;;
        *) break ;;
      esac
    done
    if [ "$1" = "fail" ]; then
      printf '%s\n' 'exec failed' >&2
      exit 7
    fi
    printf '%s\n' 'exec ok'
    ;;
esac
`

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker script: %v", err)
	}
}

func authorizedKeyFromPrivateKeyPath(path string) (string, error) {
	privateKey, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	signer, err := xssh.ParsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(xssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func readDockerLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	return string(data)
}
