package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	dockerpkg "github.com/versionlens/OpenNalvin/internal/docker"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	xssh "golang.org/x/crypto/ssh"
)

func TestDockerToolsEnabledByDefaultButHidden(t *testing.T) {
	rt, _, _, cleanup := newDockerToolRuntime(t, ToolSelection{})
	defer cleanup()

	for _, id := range []string{
		"docker",
		"docker_list_containers",
		"docker_list_images",
		"docker_list_networks",
		"docker_list_volumes",
		"docker_pull_image",
		"docker_build_image",
		"docker_create_container",
		"docker_start_container",
		"docker_stop_container",
		"docker_remove_container",
		"docker_exec_foreground",
		"docker_exec_background",
		"docker_exec_tail",
		"docker_exec_signal",
		"docker_exec_list_processes",
	} {
		tool := requireTool(t, rt.catalogResult().Tools, id)
		if !tool.Enabled || tool.Pinned || tool.Visible {
			t.Fatalf("expected %s to be enabled, hidden, and unpinned by default: %#v", id, tool)
		}
	}
}

func TestSearchToolsQueryDockerRevealsDockerToolFamily(t *testing.T) {
	enabled := []string{
		"search_tools",
		"docker",
		"docker_list_containers",
		"docker_list_images",
		"docker_list_networks",
		"docker_list_volumes",
		"docker_pull_image",
		"docker_build_image",
		"docker_create_container",
		"docker_start_container",
		"docker_stop_container",
		"docker_remove_container",
		"docker_exec_foreground",
		"docker_exec_background",
		"docker_exec_tail",
		"docker_exec_signal",
		"docker_exec_list_processes",
	}
	rt, ctx, _, cleanup := newDockerToolRuntime(t, ToolSelection{
		EnabledToolIDs: enabled,
		PinnedToolIDs:  []string{"search_tools"},
	})
	defer cleanup()

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "docker", Limit: 20}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	got := append([]string(nil), payload.RevealedToolIDs...)
	sort.Strings(got)
	want := append([]string(nil), enabled[1:]...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected revealed docker tools: got %v want %v", got, want)
	}
}

func TestDockerHelperToolsInvocation(t *testing.T) {
	_, ctx, logPath, cleanup := newDockerToolRuntime(t, ToolSelection{})
	defer cleanup()

	buildRun, err := InvokeTool(ctx, nil, "", "docker_build_image", map[string]any{
		"context_path": "app",
		"dockerfile":   "app/Dockerfile",
		"tag":          "demo:latest",
		"build_args":   []string{"FOO=bar"},
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke docker_build_image: %v", err)
	}
	if buildRun.IsError {
		t.Fatalf("expected successful docker build tool run, got %s", buildRun.Output)
	}
	var buildPayload dockerpkg.BuildImageResult
	if err := json.Unmarshal([]byte(buildRun.Output), &buildPayload); err != nil {
		t.Fatalf("parse docker build output: %v", err)
	}
	if !buildPayload.Result.OK || buildPayload.Tag != "demo:latest" {
		t.Fatalf("unexpected build payload: %#v", buildPayload)
	}

	createRun, err := InvokeTool(ctx, nil, "", "docker_create_container", map[string]any{
		"name":            "worker",
		"mount_workspace": true,
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke docker_create_container: %v", err)
	}
	if createRun.IsError {
		t.Fatalf("expected successful docker create tool run, got %s", createRun.Output)
	}
	var createPayload dockerpkg.CreateContainerResult
	if err := json.Unmarshal([]byte(createRun.Output), &createPayload); err != nil {
		t.Fatalf("parse docker create output: %v", err)
	}
	if createPayload.ContainerID != "container123" || createPayload.Workdir != dockerpkg.DefaultWorkspaceMountTarget {
		t.Fatalf("unexpected create payload: %#v", createPayload)
	}
	if createPayload.SSHEndpoint != "ssh://nalvin@127.0.0.1:41022" || createPayload.CodeServerURL != "http://127.0.0.1:41080/" {
		t.Fatalf("expected structured endpoints in create payload, got %#v", createPayload)
	}

	listRun, err := InvokeTool(ctx, nil, "", "docker_list_containers", map[string]any{}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke docker_list_containers: %v", err)
	}
	if listRun.IsError {
		t.Fatalf("expected successful docker list tool run, got %s", listRun.Output)
	}
	var listPayload []dockerpkg.ContainerSummary
	if err := json.Unmarshal([]byte(listRun.Output), &listPayload); err != nil {
		t.Fatalf("parse docker list output: %v", err)
	}
	if len(listPayload) != 1 || listPayload[0].SSHEndpoint != "ssh://nalvin@127.0.0.1:41022" || listPayload[0].CodeServerURL != "http://127.0.0.1:41080/" {
		t.Fatalf("unexpected docker list payload: %#v", listPayload)
	}

	execRun, err := InvokeTool(ctx, nil, "", "docker_exec_foreground", map[string]any{
		"container": "fail",
		"command":   []string{"sh", "-lc", "exit 7"},
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke docker_exec: %v", err)
	}
	if execRun.IsError {
		t.Fatalf("expected structured docker exec failure, got %s", execRun.Output)
	}
	var execPayload dockerpkg.ExecResult
	if err := json.Unmarshal([]byte(execRun.Output), &execPayload); err != nil {
		t.Fatalf("parse docker exec output: %v", err)
	}
	if execPayload.OK || execPayload.ExitCode != 7 {
		t.Fatalf("unexpected exec payload: %#v", execPayload)
	}

	log := readDockerToolLog(t, logPath)
	if !strings.Contains(log, "arg=build") || !strings.Contains(log, "arg=create") || !strings.Contains(log, "arg=exec") || !strings.Contains(log, "arg=inspect") {
		t.Fatalf("expected build/create/list/exec calls in log, got:\n%s", log)
	}
	if strings.Contains(log, "arg=--entrypoint\narg=sh") {
		t.Fatalf("did not expect keep-alive entrypoint override for dev image, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--publish\narg=127.0.0.1::22") || !strings.Contains(log, "arg=--publish\narg=127.0.0.1::8080") {
		t.Fatalf("expected dev service ports in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--add-host\narg=host.docker.internal:host-gateway") {
		t.Fatalf("expected docker host alias mapping in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=GIT_CONFIG_KEY_0=url.ssh://nalvin@host.docker.internal:4222/.insteadof") {
		t.Fatalf("expected git URL rewrite env in log, got:\n%s", log)
	}
	if !strings.Contains(log, "arg=--volume\narg=worker-claude-auth:"+dockerpkgPathDevClaudeConfigDir()) || !strings.Contains(log, "arg=--volume\narg=worker-codex-auth:"+dockerpkgPathDevCodexConfigDir()) {
		t.Fatalf("expected named auth volume mounts in log, got:\n%s", log)
	}
}

func newDockerToolRuntime(t *testing.T, selection ToolSelection) (*agentRuntime, context.Context, string, func()) {
	t.Helper()

	cfg, paths, logPath := newDockerToolConfig(t)
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
	return rt, ctx, logPath, cleanup
}

func newDockerToolConfig(t *testing.T) (configpkg.Config, workspacepkg.Paths, string) {
	t.Helper()

	home := t.TempDir()
	workspaceRoot := filepath.Join(home, "workspaces", "alpha")
	appDir := filepath.Join(workspaceRoot, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	logPath := filepath.Join(home, "docker.log")
	scriptPath := filepath.Join(home, "fake-docker.sh")
	writeDockerToolScript(t, scriptPath)
	t.Setenv("NALVIN_FAKE_DOCKER_LOG", logPath)

	clientKeyPath := writeDockerToolKeyPair(t, home, "id_nalvin")
	hostKeyPath := writeDockerToolKeyPair(t, home, "ssh_host_ed25519")

	cfg := configpkg.Config{
		Docker: configpkg.DockerConfig{
			Binary:       scriptPath,
			DefaultImage: "nalvin/dev",
		},
		Git: configpkg.GitConfig{
			SSH: configpkg.GitSSHConfig{
				Addr:        "127.0.0.1:4222",
				HostKeyPath: hostKeyPath,
			},
			DefaultClient: configpkg.GitDefaultClientConfig{
				User:           "nalvin",
				PrivateKeyPath: clientKeyPath,
				Host:           "127.0.0.1",
				AuthorName:     "nalvin",
				AuthorEmail:    "nalvin@example.com",
			},
		},
		Workspace: configpkg.WorkspaceConfig{
			Current:   "alpha",
			DBRoot:    filepath.Join(home, "workspace-db"),
			FilesRoot: filepath.Join(home, "workspaces"),
		},
	}

	return cfg, workspacepkg.Paths{
		Name:      "alpha",
		DBPath:    filepath.Join(home, "workspace-db", "alpha.sqlite"),
		FilesPath: workspaceRoot,
	}, logPath
}

func writeDockerToolKeyPair(t *testing.T, dir, basename string) string {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	block, err := xssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privateKeyPath := filepath.Join(dir, basename)
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return privateKeyPath
}

func writeDockerToolScript(t *testing.T, path string) {
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
  network)
    printf '%s\n' '{"ID":"net123","Name":"bridge","Driver":"bridge","Scope":"local"}'
    ;;
  volume)
    printf '%s\n' '{"Driver":"local","Name":"cache"}'
    ;;
  pull)
    printf '%s\n' 'pulled image'
    ;;
  build)
    printf '%s\n' 'built image'
    ;;
  create)
    printf '%s\n' 'container123'
    ;;
  inspect)
    printf '%s\n' '[{"Id":"abc123","Name":"/worker","NetworkSettings":{"Ports":{"22/tcp":[{"HostIp":"127.0.0.1","HostPort":"41022"}],"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"41080"}]}}},{"Id":"container123","Name":"/worker","NetworkSettings":{"Ports":{"22/tcp":[{"HostIp":"127.0.0.1","HostPort":"41022"}],"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"41080"}]}}}]'
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
		t.Fatalf("write docker tool script: %v", err)
	}
}

func readDockerToolLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docker tool log: %v", err)
	}
	return string(data)
}

func dockerpkgPathDevClaudeConfigDir() string {
	return "/home/nalvin/.claude"
}

func dockerpkgPathDevCodexConfigDir() string {
	return "/home/nalvin/.codex"
}
