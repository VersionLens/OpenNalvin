package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func (s *Server) handleListAgentRuns(w http.ResponseWriter, r *http.Request) {
	store, err := s.runtime.currentStore(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	runs, err := store.ListAgentRuns(r.Context(), q.Get("status"), limit, offset)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []knowledge.AgentRunSummary{}
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetAgentRun(w http.ResponseWriter, r *http.Request) {
	store, err := s.runtime.currentStore(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	run, err := store.GetAgentRun(r.Context(), chi.URLParam(r, "runID"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, knowledge.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, err.Error())
		return
	}
	if enrichedTrace, err := agentpkg.EnrichTraceJSON(run.Trace); err == nil {
		run.Trace = enrichedTrace
	}
	s.writeJSON(w, r, http.StatusOK, run)
}

type chatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Messages     []chatCompletionMessage `json:"messages"`
	Stream       bool                    `json:"stream"`
	RunID        string                  `json:"run_id"`
	Title        string                  `json:"title"`
	Model        string                  `json:"model"`
	SystemPrompt string                  `json:"system_prompt"`
	Mode         string                  `json:"mode"`
	EnabledTools []string                `json:"enabled_tool_ids"`
	PinnedTools  []string                `json:"pinned_tool_ids"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	runService, err := s.runtime.currentRunService(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	var req chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	var message string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			message = req.Messages[i].Content
			break
		}
	}
	if message == "" {
		s.writeError(w, http.StatusBadRequest, "messages must contain at least one user message")
		return
	}

	prepared, err := runService.PrepareAttachedTurn(r.Context(), agentrun.Request{
		RunID:        req.RunID,
		Message:      message,
		Title:        req.Title,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
		Mode:         req.Mode,
		Tools: agentpkg.ToolSelection{
			EnabledToolIDs: req.EnabledTools,
			PinnedToolIDs:  req.PinnedTools,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, agentrun.ErrRunBusy):
			s.writeError(w, http.StatusConflict, err.Error())
		default:
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	writeChunk := func(chunk agentpkg.TraceChunk) error {
		payload, err := agentpkg.MarshalChunk(chunk)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	runErr := runService.ExecuteTurn(r.Context(), prepared.Turn.TurnID, agentrun.ExecutionOptions{
		EventSink: agentrun.EventSinkFunc(func(_ context.Context, evt agentrun.Event) error {
			if evt.Kind != agentrun.EventKindTraceChunk || evt.Chunk == nil {
				return nil
			}
			return writeChunk(*evt.Chunk)
		}),
	})
	if runErr != nil {
		s.logger.Error("chat completions stream failed", "run_id", prepared.Run.ID, "error", runErr)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}
