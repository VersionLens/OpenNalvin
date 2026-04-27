package workspace_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/workspace"
)

func TestCreateListDeleteAndValidateWorkspaces(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)

	if _, err := workspace.Create(context.Background(), cfg, "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := workspace.Create(context.Background(), cfg, "alpha"); err != workspace.ErrAlreadyExists {
		t.Fatalf("expected already exists error, got %v", err)
	}

	items, err := workspace.List(cfg)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(items))
	}
	if items[0].Name != "alpha" {
		t.Fatalf("expected alpha workspace, got %q", items[0].Name)
	}
	if !items[0].DBExists || !items[0].FilesExist {
		t.Fatalf("expected workspace resources to exist, got %#v", items[0])
	}

	for _, name := range []string{"", ".", "..", "bad/name", "../bad", "has space"} {
		if err := workspace.ValidateName(name); err == nil {
			t.Fatalf("expected invalid workspace name %q to be rejected", name)
		}
	}

	if err := workspace.Delete(cfg, "alpha"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if err := workspace.Delete(cfg, "alpha"); err != workspace.ErrNotFound {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestOpenDBIsolatesWorkspaceData(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)

	dbAlpha, pathsAlpha, err := workspace.OpenDB(context.Background(), cfg, "alpha")
	if err != nil {
		t.Fatalf("open alpha db: %v", err)
	}
	defer dbAlpha.Close()

	alphaStore := knowledge.New(dbAlpha)
	node, err := alphaStore.CreateNode(context.Background(), knowledge.NodeInput{
		Kind:    "Doc",
		Name:    "Alpha Only",
		Content: "workspace alpha data",
	})
	if err != nil {
		t.Fatalf("create alpha node: %v", err)
	}
	if node.Name != "Alpha Only" {
		t.Fatalf("unexpected alpha node: %#v", node)
	}

	dbBeta, pathsBeta, err := workspace.OpenDB(context.Background(), cfg, "beta")
	if err != nil {
		t.Fatalf("open beta db: %v", err)
	}
	defer dbBeta.Close()

	if pathsAlpha.DBPath == pathsBeta.DBPath {
		t.Fatalf("expected distinct database paths, got %q", pathsAlpha.DBPath)
	}
	if pathsAlpha.FilesPath == pathsBeta.FilesPath {
		t.Fatalf("expected distinct workspace file paths, got %q", pathsAlpha.FilesPath)
	}

	betaStore := knowledge.New(dbBeta)
	nodes, err := betaStore.ListNodes(context.Background(), "", "", 10)
	if err != nil {
		t.Fatalf("list beta nodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected beta workspace to be empty, got %d nodes", len(nodes))
	}

	currentInfo, err := workspace.CurrentInfo(cfg, "beta")
	if err != nil {
		t.Fatalf("current info: %v", err)
	}
	if currentInfo.Name != "beta" {
		t.Fatalf("expected beta current info, got %q", currentInfo.Name)
	}
	if !currentInfo.DBExists || !currentInfo.FilesExist {
		t.Fatalf("expected beta workspace resources to exist, got %#v", currentInfo)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()

	root := t.TempDir()
	return config.Config{
		App: config.AppConfig{Env: "test"},
		Server: config.ServerConfig{
			Addr:           ":4210",
			AllowedOrigins: []string{"http://localhost:4211"},
		},
		Workspace: config.WorkspaceConfig{
			DBRoot:    filepath.Join(root, "workspace-db"),
			FilesRoot: filepath.Join(root, "workspaces"),
			Current:   "default",
		},
		Work: config.WorkConfig{
			HelloWorkers:     1,
			SchedulerWorkers: 1,
		},
		Schedule: config.ScheduleConfig{
			Enabled:   true,
			HelloCron: "*/5 * * * *",
		},
	}
}
