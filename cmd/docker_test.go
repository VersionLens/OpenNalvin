package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/config"
	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	xssh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestDockerCLIEndToEndWithFakeBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, logPath := writeDockerCLIConfig(t, home)
	appDir := filepath.Join(cfg.Workspace.FilesRoot, "alpha", "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	out, err := executeRoot(t, "--json", "docker", "images", "list")
	if err != nil {
		t.Fatalf("docker images list: %v", err)
	}
	var images []dockerpkg.ImageSummary
	if err := json.Unmarshal([]byte(out), &images); err != nil {
		t.Fatalf("unmarshal images list: %v", err)
	}
	if len(images) != 1 || images[0].Repository != "nalvin/dev" {
		t.Fatalf("unexpected images payload: %#v", images)
	}

	out, err = executeRoot(t, "--json", "docker", "containers", "list")
	if err != nil {
		t.Fatalf("docker containers list json: %v", err)
	}
	var containers []dockerpkg.ContainerSummary
	if err := json.Unmarshal([]byte(out), &containers); err != nil {
		t.Fatalf("unmarshal container list payload: %v", err)
	}
	if len(containers) != 1 || containers[0].SSHEndpoint != "ssh://nalvin@127.0.0.1:41022" || containers[0].CodeServerURL != "http://127.0.0.1:41080/" {
		t.Fatalf("unexpected containers payload: %#v", containers)
	}

	out, err = executeRoot(t, "docker", "containers", "list")
	if err != nil {
		t.Fatalf("docker containers list text: %v", err)
	}
	if !strings.Contains(out, "ssh=ssh://nalvin@127.0.0.1:41022") || !strings.Contains(out, "code-server=http://127.0.0.1:41080/") {
		t.Fatalf("expected service endpoints in text output, got %q", out)
	}

	out, err = executeRoot(t, "--json", "docker", "build", "app", "--tag", "my-app:dev")
	if err != nil {
		t.Fatalf("docker build: %v", err)
	}
	var buildPayload dockerpkg.BuildImageResult
	if err := json.Unmarshal([]byte(out), &buildPayload); err != nil {
		t.Fatalf("unmarshal build payload: %v", err)
	}
	if buildPayload.Tag != "my-app:dev" || !buildPayload.Result.OK {
		t.Fatalf("unexpected build payload: %#v", buildPayload)
	}

	out, err = executeRoot(t, "--json", "docker", "create", "--name", "ws-worker", "--mount-workspace", "--image", "my-app:dev")
	if err != nil {
		t.Fatalf("docker create: %v", err)
	}
	var createPayload dockerpkg.CreateContainerResult
	if err := json.Unmarshal([]byte(out), &createPayload); err != nil {
		t.Fatalf("unmarshal create payload: %v", err)
	}
	if createPayload.Name != "ws-worker" || createPayload.Workdir != dockerpkg.DefaultWorkspaceMountTarget {
		t.Fatalf("unexpected create payload: %#v", createPayload)
	}

	out, err = executeRoot(t, "--json", "docker", "exec", "ws-worker", "--", "sh", "-lc", "cd /workspace && git status")
	if err != nil {
		t.Fatalf("docker exec: %v", err)
	}
	var execPayload dockerpkg.ExecResult
	if err := json.Unmarshal([]byte(out), &execPayload); err != nil {
		t.Fatalf("unmarshal exec payload: %v", err)
	}
	if !execPayload.OK || execPayload.Container != "ws-worker" {
		t.Fatalf("unexpected exec payload: %#v", execPayload)
	}

	log := readDockerCLILog(t, logPath)
	if !strings.Contains(log, "arg=build") || !strings.Contains(log, "arg=create") || !strings.Contains(log, "arg=exec") || !strings.Contains(log, "arg=inspect") || !strings.Contains(log, "arg=ps") {
		t.Fatalf("expected build/create/list/exec in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--volume\narg="+cfg.Workspace.FilesRoot+"/alpha:"+dockerpkg.DefaultWorkspaceMountTarget) {
		t.Fatalf("expected workspace mount in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--entrypoint\narg=sh") {
		t.Fatalf("expected default keep-alive entrypoint override in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--add-host\narg=host.docker.internal:host-gateway") {
		t.Fatalf("expected docker host alias mapping in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=GIT_CONFIG_VALUE_0=ssh://nalvin@127.0.0.1:") {
		t.Fatalf("expected git URL rewrite env in log, got:\n%s", log)
	}
}

func writeDockerCLIConfig(t *testing.T, home string) (config.Config, string) {
	t.Helper()

	logPath := filepath.Join(home, "docker.log")
	scriptPath := filepath.Join(home, "fake-docker.sh")
	writeDockerCLIScript(t, scriptPath)
	t.Setenv("NALVIN_FAKE_DOCKER_LOG", logPath)

	workspaceDBRoot := filepath.Join(home, "workspace-db")
	workspaceFilesRoot := filepath.Join(home, "workspaces")
	hostKeyPath := filepath.Join(home, "git-ssh", "host_ed25519")
	clientKeyPath, clientPublicKey := writeDockerCLIKeyPair(t, home, "id_nalvin")
	_, _ = writeDockerCLIKeyPair(t, filepath.Dir(hostKeyPath), filepath.Base(hostKeyPath))
	addr := freeGitCLIAddr(t)

	cfgDoc := map[string]any{
		"workspace": map[string]any{
			"current":    "alpha",
			"db_root":    workspaceDBRoot,
			"files_root": workspaceFilesRoot,
		},
		"docker": map[string]any{
			"binary":        scriptPath,
			"default_image": "nalvin/dev",
		},
		"git": map[string]any{
			"ssh": map[string]any{
				"enabled":       true,
				"addr":          addr,
				"host_key_path": hostKeyPath,
			},
			"default_client": map[string]any{
				"user":             "nalvin",
				"private_key_path": clientKeyPath,
				"host":             "127.0.0.1",
				"author_name":      "nalvin",
				"author_email":     "nalvin@example.com",
			},
			"users": map[string]any{
				"nalvin": map[string]any{
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
	return cfg, logPath
}

func writeDockerCLIKeyPair(t *testing.T, dir, basename string) (string, string) {
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

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir key dir: %v", err)
	}
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

func writeDockerCLIScript(t *testing.T, path string) {
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
  ps)
    printf '%s\n' '{"ID":"abc123","Image":"nalvin/dev","Status":"Up 1 hour","Names":"worker"}'
    ;;
  images)
    printf '%s\n' '{"ID":"img123","Repository":"nalvin/dev","Tag":"latest","Size":"55MB"}'
    ;;
  build)
    printf '%s\n' 'built image'
    ;;
  create)
    printf '%s\n' 'container123'
    ;;
  inspect)
    printf '%s\n' '[{"Id":"abc123","Name":"/worker","NetworkSettings":{"Ports":{"22/tcp":[{"HostIp":"127.0.0.1","HostPort":"41022"}],"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"41080"}]}}},{"Id":"container123","Name":"/ws-worker","NetworkSettings":{"Ports":{}}}]'
    ;;
  exec)
    printf '%s\n' 'exec ok'
    ;;
esac
`

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker script: %v", err)
	}
}

func readDockerCLILog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docker cli log: %v", err)
	}
	return string(data)
}
