package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/buildinfo"
	"github.com/versionlens/OpenNalvin/internal/config"
	gitpkg "github.com/versionlens/OpenNalvin/internal/git"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type AssetSource struct {
	Files    fs.FS
	Index    []byte
	Embedded bool
}

type Server struct {
	cfg         config.Config
	info        buildinfo.Info
	assets      AssetSource
	logger      *slog.Logger
	runtime     *workspaceRuntime
	gitSSH      *gitpkg.SSHServer
	agentRunner func(context.Context, *knowledge.Store, agentpkg.RunRequest, agentpkg.RunOptions) (string, error)
}

func New(cfg config.Config, info buildinfo.Info, assets AssetSource, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	srv := &Server{
		cfg:         cfg,
		info:        info,
		assets:      assets,
		logger:      logger,
		agentRunner: agentpkg.Run,
	}
	srv.runtime = newWorkspaceRuntime(cfg, logger, func(ctx context.Context, store *knowledge.Store, req agentpkg.RunRequest, opts agentpkg.RunOptions) (string, error) {
		return srv.agentRunner(ctx, store, req, opts)
	})
	return srv
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := config.WithContext(r.Context(), s.cfg)
			if paths, err := s.runtime.currentPaths(); err == nil {
				ctx = workspacepkg.WithPaths(ctx, paths)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.cfg.Server.AllowedOrigins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Streaming endpoints — no request timeout; SSE connections are long-lived.
	r.Post("/v1/chat/completions", s.handleChatCompletions)
	r.Get("/v1/models", s.handleListModels)
	r.Post("/api/chat", s.handleChat)
	r.Get("/api/agent-runs/{runID}/events", s.handleStreamAppRunEvents)
	r.Get("/files/{workspace}/*", s.handleWorkspaceFile)
	r.Head("/files/{workspace}/*", s.handleWorkspaceFile)

	// Regular API endpoints with a 60s timeout.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(60 * time.Second))
		r.Route("/api", func(api chi.Router) {
			api.Get("/health", s.handleHealth)
			api.Get("/meta", s.handleMeta)
			api.Get("/workspaces", s.handleListWorkspaces)
			api.Get("/workspace/current", s.handleCurrentWorkspace)
			api.Post("/workspace/switch", s.handleSwitchWorkspace)
			api.Get("/agent-tools", s.handleListAgentTools)
			api.Route("/agent-runs", func(ar chi.Router) {
				ar.Post("/", s.handleCreateAppRun)
				ar.Get("/", s.handleListAgentRuns)
				ar.Get("/{runID}", s.handleGetAgentRun)
				ar.Post("/{runID}/messages", s.handleCreateAppRunMessage)
				ar.Post("/{runID}/implement-plan", s.handleImplementPlan)
				ar.Post("/{runID}/abort", s.handleAbortAppRun)
				ar.Delete("/{runID}/messages", s.handleDeleteMessagesFromIndex)
				ar.Get("/{runID}/tool-outputs/{outputID}", s.handleGetAgentRunToolOutput)
				ar.Get("/{runID}/tool-outputs/{outputID}/grep", s.handleSearchAgentRunToolOutput)
			})
		})
	})

	if s.assets.Embedded && s.assets.Files != nil && len(s.assets.Index) > 0 {
		r.Handle("/*", newSPAHandler(s.assets.Files, s.assets.Index))
	}

	return r
}

func (s *Server) Close() error {
	var firstErr error
	if s.gitSSH != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.gitSSH.Close(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.runtime == nil {
		return firstErr
	}
	if err := s.runtime.close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Server) Start(ctx context.Context) error {
	_ = ctx
	if !s.cfg.Git.SSH.Enabled {
		return nil
	}
	if s.gitSSH != nil {
		return nil
	}

	gitSSH, err := gitpkg.NewSSHServer(s.cfg, s.logger, nil)
	if err != nil {
		return err
	}
	if err := gitSSH.Start(); err != nil {
		return err
	}
	s.gitSSH = gitSSH
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"name":              s.info.Name,
		"module":            s.info.Module,
		"environment":       s.cfg.App.Env,
		"version":           s.info.Version,
		"commit":            s.info.Commit,
		"date":              s.info.Date,
		"frontend_embedded": s.assets.Embedded,
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, _ *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data == nil {
		return
	}

	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Error("encode json", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, nil, status, map[string]string{
		"error":   http.StatusText(status),
		"message": message,
	})
}

func newSPAHandler(staticFS fs.FS, indexHTML []byte) http.Handler {
	fileServer := http.FileServerFS(staticFS)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			writeIndex(w, indexHTML)
			return
		}

		if path == "api" || strings.HasPrefix(path, "api/") {
			http.NotFound(w, r)
			return
		}

		if _, err := fs.Stat(staticFS, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		writeIndex(w, indexHTML)
	})
}

func writeIndex(w http.ResponseWriter, indexHTML []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}
