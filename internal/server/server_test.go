package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/buildinfo"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/viper"
)

func TestAPIEndpoints(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.default.base_url", "https://api.z.ai/api/coding/paas/v4")
	viper.Set("providers.default.api_key", "configured")
	viper.Set("providers.default.model", "GLM-5.1")
	viper.Set("providers.default.type", "openai_compat")
	viper.Set("providers.anthropic.api_key", "configured")
	viper.Set("providers.anthropic.model", "claude-sonnet-4-6")
	viper.Set("providers.anthropic.type", "anthropic")
	viper.Set("providers.openai.api_key", "configured")
	viper.Set("providers.openai.model", "gpt-5.4")
	viper.Set("providers.openai.type", "openai")

	srv := New(
		config.Config{
			App: config.AppConfig{Env: "test"},
			Server: config.ServerConfig{
				Addr:           ":4210",
				AllowedOrigins: []string{"http://localhost:4211"},
			},
		},
		buildinfo.Current(),
		AssetSource{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", body)
	}

	metaRec := httptest.NewRecorder()
	metaReq := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	srv.Handler().ServeHTTP(metaRec, metaReq)
	if metaRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for meta, got %d", metaRec.Code)
	}
	if !strings.Contains(metaRec.Body.String(), `"environment":"test"`) {
		t.Fatalf("expected environment in meta: %s", metaRec.Body.String())
	}

	modelsRec := httptest.NewRecorder()
	modelsReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	srv.Handler().ServeHTTP(modelsRec, modelsReq)
	if modelsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for models list, got %d", modelsRec.Code)
	}
	if body := strings.TrimSpace(modelsRec.Body.String()); !strings.Contains(body, `"object":"list"`) {
		t.Fatalf("expected OpenAI list object, got %s", body)
	}
	if !strings.Contains(modelsRec.Body.String(), `"id":"GLM-5.1"`) {
		t.Fatalf("expected default model in models list: %s", modelsRec.Body.String())
	}
	if !strings.Contains(modelsRec.Body.String(), `"owned_by":"default"`) {
		t.Fatalf("expected default provider ownership in models list: %s", modelsRec.Body.String())
	}
	if !strings.Contains(modelsRec.Body.String(), `"id":"claude-sonnet-4-6"`) {
		t.Fatalf("expected anthropic model in models list: %s", modelsRec.Body.String())
	}
	if !strings.Contains(modelsRec.Body.String(), `"id":"gpt-5.4"`) {
		t.Fatalf("expected openai model in models list: %s", modelsRec.Body.String())
	}
}

func TestChatEndpointAcceptsModel(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.kimi.type", "openai_compat")
	viper.Set("providers.kimi.base_url", "https://api.kimi.test/v1")
	viper.Set("providers.kimi.api_key", "configured")
	viper.Set("providers.kimi.model", "kimi-for-coding")

	srv, _ := newWorkspaceTestServer(t)
	defer srv.Close()

	runID := ""
	srv.agentRunner = func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
		if req.ProviderName != "kimi" {
			t.Fatalf("expected provider kimi, got %q", req.ProviderName)
		}
		if req.Mode != agentpkg.RunModePlan {
			t.Fatalf("expected plan mode, got %q", req.Mode)
		}
		if opts.OnChunk != nil {
			if err := opts.OnChunk(agentpkg.TraceChunk{
				ID:      "chunk_1",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "kimi-for-coding",
				Choices: []agentpkg.TraceChoice{{
					Index: 0,
					Delta: &agentpkg.TraceDelta{Content: "hi"},
				}},
			}); err != nil {
				return "", err
			}
		}
		return runID, nil
	}

	body := bytes.NewBufferString(`{"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}],"model":"kimi-for-coding","mode":"plan"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", body)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"delta":"hi"`) {
		t.Fatalf("expected streamed assistant content, got %s", rec.Body.String())
	}
}

func TestChatCompletionsEndpointAcceptsModel(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("providers.openai.type", "openai")
	viper.Set("providers.openai.api_key", "configured")
	viper.Set("providers.openai.model", "gpt-5.4")

	srv, _ := newWorkspaceTestServer(t)
	defer srv.Close()

	runID := ""
	srv.agentRunner = func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
		if req.ProviderName != "openai" {
			t.Fatalf("expected provider openai, got %q", req.ProviderName)
		}
		if req.Mode != agentpkg.RunModePlan {
			t.Fatalf("expected plan mode, got %q", req.Mode)
		}
		if opts.OnChunk != nil {
			if err := opts.OnChunk(agentpkg.TraceChunk{
				ID:      "chunk_1",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "gpt-5.4",
				Choices: []agentpkg.TraceChoice{{
					Index: 0,
					Delta: &agentpkg.TraceDelta{Content: "hello"},
				}},
			}); err != nil {
				return "", err
			}
		}
		return runID, nil
	}

	body := bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}],"stream":true,"model":"gpt-5.4","mode":"plan"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"model":"gpt-5.4"`) {
		t.Fatalf("expected streamed chunk for gpt-5.4, got %s", rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<!doctype html><title>app</title>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('hi')")},
		"assets/style.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	handler := newSPAHandler(fs.FS(assets), []byte("<!doctype html><title>app</title>"))

	t.Run("serves embedded asset", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "console.log") {
			t.Fatalf("expected asset body, got %q", rec.Body.String())
		}
	})

	t.Run("falls back to index for client route", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>app</title>") {
			t.Fatalf("expected index html, got %q", rec.Body.String())
		}
	})
}

func TestWorkspaceAndAgentRunEndpoints(t *testing.T) {
	srv, home := newWorkspaceTestServer(t)
	defer srv.Close()

	ctx := context.Background()
	alphaStore, err := srv.runtime.storeForName(ctx, "alpha")
	if err != nil {
		t.Fatalf("open alpha store: %v", err)
	}
	betaStore, err := srv.runtime.storeForName(ctx, "beta")
	if err != nil {
		t.Fatalf("open beta store: %v", err)
	}

	createTestRun(t, alphaStore, "alpha-run", "alpha prompt")
	createTestRun(t, betaStore, "beta-run", "beta prompt")
	betaOutput, err := betaStore.CreateAgentRunToolOutput(ctx, knowledge.CreateAgentRunToolOutputInput{
		RunID:           "beta-run",
		ToolCallID:      "call_001",
		ToolName:        "web_fetch_get",
		Content:         "first line\nsecond line\nthird line\n",
		SizeBytes:       32,
		EstimatedTokens: 12,
		TotalLines:      3,
		InlineTruncated: true,
	})
	if err != nil {
		t.Fatalf("create beta spilled output: %v", err)
	}

	workspacesRec := httptest.NewRecorder()
	workspacesReq := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	srv.Handler().ServeHTTP(workspacesRec, workspacesReq)
	if workspacesRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for workspaces, got %d", workspacesRec.Code)
	}
	if !strings.Contains(workspacesRec.Body.String(), `"name":"alpha"`) {
		t.Fatalf("expected alpha workspace in list: %s", workspacesRec.Body.String())
	}
	if !strings.Contains(workspacesRec.Body.String(), `"current":true`) {
		t.Fatalf("expected a current workspace in list: %s", workspacesRec.Body.String())
	}

	switchBody := bytes.NewBufferString(`{"name":"beta"}`)
	switchRec := httptest.NewRecorder()
	switchReq := httptest.NewRequest(http.MethodPost, "/api/workspace/switch", switchBody)
	srv.Handler().ServeHTTP(switchRec, switchReq)
	if switchRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for workspace switch, got %d: %s", switchRec.Code, switchRec.Body.String())
	}
	if !strings.Contains(switchRec.Body.String(), `"name":"beta"`) {
		t.Fatalf("expected beta workspace after switch: %s", switchRec.Body.String())
	}

	configData, err := os.ReadFile(filepath.Join(home, ".nalvin", "config.yaml"))
	if err != nil {
		t.Fatalf("read updated config file: %v", err)
	}
	if !strings.Contains(string(configData), "current: beta") {
		t.Fatalf("expected persisted current workspace beta, got %s", string(configData))
	}

	runsRec := httptest.NewRecorder()
	runsReq := httptest.NewRequest(http.MethodGet, "/api/agent-runs", nil)
	srv.Handler().ServeHTTP(runsRec, runsReq)
	if runsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for runs list, got %d", runsRec.Code)
	}
	if !strings.Contains(runsRec.Body.String(), `"id":"beta-run"`) {
		t.Fatalf("expected beta run in list: %s", runsRec.Body.String())
	}
	if !strings.Contains(runsRec.Body.String(), `"total_tokens":2`) {
		t.Fatalf("expected total_tokens in runs list: %s", runsRec.Body.String())
	}
	if strings.Contains(runsRec.Body.String(), `"id":"alpha-run"`) {
		t.Fatalf("did not expect alpha run in beta workspace list: %s", runsRec.Body.String())
	}

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/agent-runs/beta-run", nil)
	srv.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for beta run detail, got %d", getRec.Code)
	}
	if !strings.Contains(getRec.Body.String(), `"id":"beta-run"`) {
		t.Fatalf("expected beta run detail payload, got %s", getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"total_tokens":2`) {
		t.Fatalf("expected total_tokens in run detail payload, got %s", getRec.Body.String())
	}

	missingRec := httptest.NewRecorder()
	missingReq := httptest.NewRequest(http.MethodGet, "/api/agent-runs/alpha-run", nil)
	srv.Handler().ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for alpha run in beta workspace, got %d", missingRec.Code)
	}

	currentRec := httptest.NewRecorder()
	currentReq := httptest.NewRequest(http.MethodGet, "/api/workspace/current", nil)
	srv.Handler().ServeHTTP(currentRec, currentReq)
	if currentRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for current workspace, got %d", currentRec.Code)
	}
	if !strings.Contains(currentRec.Body.String(), `"name":"beta"`) {
		t.Fatalf("expected beta current workspace payload, got %s", currentRec.Body.String())
	}

	outputRec := httptest.NewRecorder()
	outputReq := httptest.NewRequest(http.MethodGet, "/api/agent-runs/beta-run/tool-outputs/"+betaOutput.OutputID+"?offset=1&limit=1", nil)
	srv.Handler().ServeHTTP(outputRec, outputReq)
	if outputRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for spilled output page, got %d: %s", outputRec.Code, outputRec.Body.String())
	}
	if !strings.Contains(outputRec.Body.String(), `"content":"second line"`) {
		t.Fatalf("expected paginated tool output content, got %s", outputRec.Body.String())
	}

	searchRec := httptest.NewRecorder()
	searchReq := httptest.NewRequest(http.MethodGet, "/api/agent-runs/beta-run/tool-outputs/"+betaOutput.OutputID+"/grep?pattern=third&literal_text=true", nil)
	srv.Handler().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for spilled output search, got %d: %s", searchRec.Code, searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), `"line_number":3`) {
		t.Fatalf("expected matching line in tool output search, got %s", searchRec.Body.String())
	}
}

func TestImplementPlanEndpointQueuesFreshImplementationRun(t *testing.T) {
	srv, _ := newWorkspaceTestServer(t)
	defer srv.Close()

	ctx := context.Background()
	handle, err := srv.runtime.currentHandle(ctx)
	if err != nil {
		t.Fatalf("current handle: %v", err)
	}
	paths, err := srv.runtime.currentPaths()
	if err != nil {
		t.Fatalf("current paths: %v", err)
	}
	ctx = config.WithContext(ctx, srv.cfg)
	ctx = workspacepkg.WithPaths(ctx, paths)

	prepared, err := handle.runService.PrepareQueuedTurn(ctx, agentrun.Request{
		ClientRequestID: "req-plan",
		Message:         "Plan the feature",
		Mode:            agentpkg.RunModePlan,
	})
	if err != nil {
		t.Fatalf("prepare plan run: %v", err)
	}
	trace, err := agentpkg.ParseStoredTrace(prepared.Run.Trace)
	if err != nil {
		t.Fatalf("parse plan trace: %v", err)
	}
	planPath := filepath.Join(srv.cfg.Workspace.FilesRoot, "alpha", filepath.FromSlash(trace.Metadata.PlanRef.Path))
	if err := os.WriteFile(planPath, []byte("# Approved plan\n\n- Implement it\n"), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/agent-runs/%s/implement-plan", prepared.Run.ID), bytes.NewBufferString(`{}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Run knowledge.AgentRun `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Run.ID == prepared.Run.ID {
		t.Fatal("expected a fresh implementation run")
	}
	traceOut, err := agentpkg.ParseStoredTrace(payload.Run.Trace)
	if err != nil {
		t.Fatalf("parse implementation trace: %v", err)
	}
	if traceOut.Metadata.PlanRef.SourceRunID != prepared.Run.ID {
		t.Fatalf("expected source run id %q, got %q", prepared.Run.ID, traceOut.Metadata.PlanRef.SourceRunID)
	}
}

func TestWorkspaceFileEndpointSupportsRangeRequests(t *testing.T) {
	srv, home := newWorkspaceTestServer(t)
	defer srv.Close()

	filePath := filepath.Join(home, "workspaces", "alpha", "downloads", "videos", "Ghosts S05E14.mp4")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir file dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("abcdefghij"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/files/alpha/downloads/videos/Ghosts%20S05E14.mp4", nil)
	req.Header.Set("Range", "bytes=2-5")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", rec.Code)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("accept-ranges = %q, want bytes", got)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("content-range = %q", got)
	}
	if body := rec.Body.String(); body != "cdef" {
		t.Fatalf("body = %q, want cdef", body)
	}
}

func TestWorkspaceFileEndpointRejectsTraversal(t *testing.T) {
	srv, _ := newWorkspaceTestServer(t)
	defer srv.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/files/alpha/../beta/secret.txt", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestCurrentStoreDoesNotAbortRunningRunsWithoutEmbeddedAgentWorkers(t *testing.T) {
	srv, _ := newWorkspaceTestServer(t)
	defer srv.Close()

	ctx := context.Background()
	db, _, err := workspacepkg.OpenDB(ctx, srv.cfg, "alpha")
	if err != nil {
		t.Fatalf("open alpha workspace DB: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           "run-live",
		Title:        "live run",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "hello",
		Status:       "running",
		ActiveTurnID: "turn-live",
		Trace:        json.RawMessage(`{"schema_version":2}`),
	}); err != nil {
		t.Fatalf("create live run: %v", err)
	}
	if _, err := store.CreateAgentRunTurn(ctx, knowledge.CreateAgentRunTurnInput{
		TurnID:          "turn-live",
		RunID:           "run-live",
		ClientRequestID: "req-live",
		Message:         "hello",
		Status:          "running",
	}); err != nil {
		t.Fatalf("create live turn: %v", err)
	}

	if _, err := srv.runtime.currentStore(ctx); err != nil {
		t.Fatalf("open current store through runtime: %v", err)
	}

	run, err := store.GetAgentRun(ctx, "run-live")
	if err != nil {
		t.Fatalf("get live run: %v", err)
	}
	if run.Status != "running" {
		t.Fatalf("expected live run to remain running without embedded workers, got %q", run.Status)
	}
}

func TestEmbeddedWorkerRuntimeReusesWorkerClientForInserts(t *testing.T) {
	srv, _ := newWorkspaceTestServerWithConfig(t, func(cfg *config.Config) {
		cfg.Work.AgentWorkers = 1
	})
	defer srv.Close()

	handle, err := srv.runtime.currentHandle(context.Background())
	if err != nil {
		t.Fatalf("open current handle through runtime: %v", err)
	}
	if handle.workerClient == nil {
		t.Fatalf("expected embedded worker client to be initialized")
	}
	if handle.insertClient == nil {
		t.Fatalf("expected insert client to be initialized")
	}
	if handle.insertClient != handle.workerClient {
		t.Fatalf("expected embedded runtime to reuse worker client for inserts")
	}
}

func newWorkspaceTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	return newWorkspaceTestServerWithConfig(t, nil)
}

func newWorkspaceTestServerWithConfig(t *testing.T, mutate func(*config.Config)) (*Server, string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Config{
		App: config.AppConfig{Env: "test"},
		Server: config.ServerConfig{
			Addr:           ":4210",
			AllowedOrigins: []string{"http://localhost:4211"},
		},
		Workspace: config.WorkspaceConfig{
			DBRoot:    filepath.Join(home, "workspace-db"),
			FilesRoot: filepath.Join(home, "workspaces"),
			Current:   "alpha",
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}

	ctx := context.Background()
	for _, name := range []string{"alpha", "beta"} {
		if _, err := workspacepkg.Create(ctx, cfg, name); err != nil {
			t.Fatalf("create workspace %s: %v", name, err)
		}
	}

	srv := New(
		cfg,
		buildinfo.Current(),
		AssetSource{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	return srv, home
}

func createTestRun(t *testing.T, store *knowledge.Store, id, prompt string) {
	t.Helper()

	trace := map[string]any{
		"schema_version": 1,
		"run_id":         id,
		"title":          prompt,
		"model":          "gpt-test",
		"usage": map[string]any{
			"input_tokens":  1,
			"output_tokens": 1,
			"total_tokens":  2,
		},
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"created_at": "2026-03-28T00:00:00Z",
		"updated_at": "2026-03-28T00:00:00Z",
	}

	payload, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}

	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           id,
		Title:        prompt,
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       prompt,
		Status:       "completed",
		MessageCount: 1,
		InputTokens:  1,
		OutputTokens: 1,
		Trace:        payload,
	}); err != nil {
		t.Fatalf("create test run: %v", err)
	}
}
