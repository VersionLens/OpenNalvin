package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	queuepkg "github.com/versionlens/OpenNalvin/internal/queue"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func TestWorkspaceSwitchPersistsAndFlagOverrideDoesNot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := executeRoot(t, "workspace", "switch", "alpha"); err != nil {
		t.Fatalf("switch workspace: %v", err)
	}

	out, err := executeRoot(t, "workspace", "current", "--json")
	if err != nil {
		t.Fatalf("current workspace: %v", err)
	}
	var current workspaceCurrentPayload
	if err := json.Unmarshal([]byte(out), &current); err != nil {
		t.Fatalf("unmarshal current workspace payload: %v", err)
	}
	if current.Name != "alpha" || current.Persisted != "alpha" || current.Overridden {
		t.Fatalf("unexpected current payload: %#v", current)
	}

	out, err = executeRoot(t, "--workspace", "beta", "workspace", "current", "--json")
	if err != nil {
		t.Fatalf("current workspace with override: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &current); err != nil {
		t.Fatalf("unmarshal override current payload: %v", err)
	}
	if current.Name != "beta" || current.Persisted != "alpha" || !current.Overridden {
		t.Fatalf("unexpected override current payload: %#v", current)
	}
	if current.DBExists || current.FilesExists {
		t.Fatalf("expected override-only workspace to remain unmaterialized, got %#v", current)
	}

	configBytes, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if !bytes.Contains(configBytes, []byte("current: alpha")) {
		t.Fatalf("expected config file to persist alpha, got %q", string(configBytes))
	}
}

func TestJobsEnqueueAndWorkStatusUseSelectedWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create alpha workspace: %v", err)
	}
	if _, err := executeRoot(t, "workspace", "create", "beta"); err != nil {
		t.Fatalf("create beta workspace: %v", err)
	}

	if _, err := executeRoot(t, "--workspace", "alpha", "jobs", "enqueue", "hello", "--message", "hi", "--run-key", "alpha-1"); err != nil {
		t.Fatalf("enqueue alpha job: %v", err)
	}

	alphaDB, err := database.Open(context.Background(), filepath.Join(home, ".nalvin", "workspace-db", "alpha.sqlite"))
	if err != nil {
		t.Fatalf("open alpha database: %v", err)
	}
	defer alphaDB.Close()

	var alphaJobs int
	if err := alphaDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM river_job").Scan(&alphaJobs); err != nil {
		t.Fatalf("query alpha jobs: %v", err)
	}
	if alphaJobs != 1 {
		t.Fatalf("expected 1 alpha job, got %d", alphaJobs)
	}

	betaDB, err := database.Open(context.Background(), filepath.Join(home, ".nalvin", "workspace-db", "beta.sqlite"))
	if err != nil {
		t.Fatalf("open beta database: %v", err)
	}
	defer betaDB.Close()

	var betaJobs int
	if err := betaDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM river_job").Scan(&betaJobs); err != nil {
		t.Fatalf("query beta jobs: %v", err)
	}
	if betaJobs != 0 {
		t.Fatalf("expected 0 beta jobs, got %d", betaJobs)
	}

	out, err := executeRoot(t, "--workspace", "alpha", "work", "status", "--json")
	if err != nil {
		t.Fatalf("work status alpha: %v", err)
	}
	var status queuepkg.Status
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if len(status.HelloJobs) != 1 {
		t.Fatalf("expected 1 alpha hello job in status, got %d", len(status.HelloJobs))
	}
}

func TestWorkspacePutGetAndOverrideSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := executeRoot(t, "workspace", "create", "alpha"); err != nil {
		t.Fatalf("create alpha workspace: %v", err)
	}
	if _, err := executeRoot(t, "workspace", "create", "beta"); err != nil {
		t.Fatalf("create beta workspace: %v", err)
	}
	if _, err := executeRoot(t, "workspace", "switch", "alpha"); err != nil {
		t.Fatalf("switch alpha workspace: %v", err)
	}

	hostDir := t.TempDir()
	hostFile := filepath.Join(hostDir, "note.txt")
	if err := os.WriteFile(hostFile, []byte("hello workspace"), 0o644); err != nil {
		t.Fatalf("write host file: %v", err)
	}

	if _, err := executeRoot(t, "--workspace", "beta", "workspace", "put", hostFile); err != nil {
		t.Fatalf("workspace put: %v", err)
	}

	betaFile := filepath.Join(home, ".nalvin", "workspaces", "beta", "note.txt")
	content, err := os.ReadFile(betaFile)
	if err != nil {
		t.Fatalf("read beta workspace file: %v", err)
	}
	if string(content) != "hello workspace" {
		t.Fatalf("unexpected beta workspace file content: %q", string(content))
	}

	alphaFile := filepath.Join(home, ".nalvin", "workspaces", "alpha", "note.txt")
	if _, err := os.Stat(alphaFile); !os.IsNotExist(err) {
		t.Fatalf("expected alpha workspace file to stay absent, got err=%v", err)
	}

	if _, err := executeRoot(t, "--workspace", "beta", "workspace", "put", hostFile); err == nil || !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("expected overwrite protection error, got %v", err)
	}
	if _, err := executeRoot(t, "--workspace", "beta", "workspace", "put", "--overwrite", hostFile); err != nil {
		t.Fatalf("workspace put overwrite: %v", err)
	}

	downloadDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(downloadDir); err != nil {
		t.Fatalf("chdir download dir: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if _, err := executeRoot(t, "--workspace", "beta", "workspace", "get", "note.txt"); err != nil {
		t.Fatalf("workspace get: %v", err)
	}
	downloadedBytes, err := os.ReadFile(filepath.Join(downloadDir, "note.txt"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(downloadedBytes) != "hello workspace" {
		t.Fatalf("unexpected downloaded content: %q", string(downloadedBytes))
	}
}

func TestWorkspacePutGetDirectoryAndJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	hostDir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(filepath.Join(hostDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir host bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "nested", "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	out, err := executeRoot(t, "--json", "--workspace", "alpha", "workspace", "put", hostDir, "imports/bundle")
	if err != nil {
		t.Fatalf("workspace put json: %v", err)
	}

	var payload workspaceTransferPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal transfer payload: %v", err)
	}
	if payload.Workspace != "alpha" || payload.Kind != "directory" || payload.Destination != "imports/bundle" {
		t.Fatalf("unexpected transfer payload: %#v", payload)
	}

	exportDir := t.TempDir()
	if _, err := executeRoot(t, "--workspace", "alpha", "workspace", "get", "imports/bundle", filepath.Join(exportDir, "bundle-copy")); err != nil {
		t.Fatalf("workspace get directory: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(exportDir, "bundle-copy", "nested", "a.txt")); err != nil || string(data) != "A" {
		t.Fatalf("unexpected exported file: data=%q err=%v", string(data), err)
	}
}

func executeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return executeRootWithInput(t, bytes.NewBuffer(nil), args...)
}

func executeRootWithInput(t *testing.T, input io.Reader, args ...string) (string, error) {
	t.Helper()

	resetViperBindings()
	resetCommandFlags(rootCmd)

	cfgFile = ""
	outputJSON = false
	workspaceName = ""
filesURLBase = ""
	filesExtractEPUBOut = ""
	filesExtractEPUBForce = false
	wikipediaLanguage = ""
	wikipediaSectionIndex = ""

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(bytes.NewBuffer(nil))
	rootCmd.SetIn(input)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	return out.String(), err
}

func resetViperBindings() {
	viper.Reset()
	_ = viper.BindPFlag("app.env", rootCmd.PersistentFlags().Lookup("env"))
	_ = viper.BindPFlag("server.addr", serveCmd.Flags().Lookup("addr"))
}

func resetCommandFlags(cmd *cobra.Command) {
	resetFlagSet(cmd.Flags())
	resetFlagSet(cmd.PersistentFlags())
	for _, child := range cmd.Commands() {
		resetCommandFlags(child)
	}
}

func resetFlagSet(set *pflag.FlagSet) {
	if set == nil {
		return
	}
	set.VisitAll(func(flag *pflag.Flag) {
		_ = flag.Value.Set(flag.DefValue)
		flag.Changed = false
	})
}
