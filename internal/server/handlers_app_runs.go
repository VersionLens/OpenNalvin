package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/queue"
)

const appRunEventBatchSize = 200

type appRunRequest struct {
	ClientRequestID string   `json:"client_request_id"`
	Message         string   `json:"message"`
	Title           string   `json:"title"`
	Model           string   `json:"model"`
	SystemPrompt    string   `json:"system_prompt"`
	ProviderName    string   `json:"provider_name"`
	ParentRunID     string   `json:"parent_run_id"`
	RootRunID       string   `json:"root_run_id"`
	TaskName        string   `json:"task_name"`
	RunKind         string   `json:"run_kind"`
	Mode            string   `json:"mode"`
	EnabledToolIDs  []string `json:"enabled_tool_ids"`
	PinnedToolIDs   []string `json:"pinned_tool_ids"`
	EnableToolIDs   []string `json:"enable_tool_ids"`
	DisableToolIDs  []string `json:"disable_tool_ids"`
	PinToolIDs      []string `json:"pin_tool_ids"`
	UnpinToolIDs    []string `json:"unpin_tool_ids"`
}

func (s *Server) handleCreateAppRun(w http.ResponseWriter, r *http.Request) {
	s.handleQueueAppRun(w, r, "")
}

func (s *Server) handleCreateAppRunMessage(w http.ResponseWriter, r *http.Request) {
	s.handleQueueAppRun(w, r, chi.URLParam(r, "runID"))
}

func (s *Server) handleQueueAppRun(w http.ResponseWriter, r *http.Request, runID string) {
	requestStartedAt := time.Now().UTC()
	runService, err := s.runtime.currentRunService(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	insertClient, err := s.runtime.currentInsertClient(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	var req appRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		s.writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	decodedAt := time.Now().UTC()

	prepared, err := runService.PrepareQueuedTurn(r.Context(), agentrun.Request{
		RunID:           strings.TrimSpace(runID),
		ClientRequestID: strings.TrimSpace(req.ClientRequestID),
		Message:         req.Message,
		Title:           strings.TrimSpace(req.Title),
		Model:           strings.TrimSpace(req.Model),
		SystemPrompt:    strings.TrimSpace(req.SystemPrompt),
		ProviderName:    strings.TrimSpace(req.ProviderName),
		ParentRunID:     strings.TrimSpace(req.ParentRunID),
		RootRunID:       strings.TrimSpace(req.RootRunID),
		TaskName:        strings.TrimSpace(req.TaskName),
		RunKind:         strings.TrimSpace(req.RunKind),
		Mode:            strings.TrimSpace(req.Mode),
		Tools: agentpkg.ToolSelection{
			EnabledToolIDs: append([]string(nil), req.EnabledToolIDs...),
			PinnedToolIDs:  append([]string(nil), req.PinnedToolIDs...),
			EnableToolIDs:  append([]string(nil), req.EnableToolIDs...),
			DisableToolIDs: append([]string(nil), req.DisableToolIDs...),
			PinToolIDs:     append([]string(nil), req.PinToolIDs...),
			UnpinToolIDs:   append([]string(nil), req.UnpinToolIDs...),
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
	preparedAt := time.Now().UTC()

	duplicate := prepared.ExistingTurn && prepared.Turn.QueueJobID > 0
	jobID := prepared.Turn.QueueJobID
	run := prepared.Run
	turn := prepared.Turn
	var enqueuedAt time.Time
	var markedQueuedAt time.Time
	if !duplicate {
		var skipped bool
		jobID, skipped, err = queue.EnqueueAgentRun(r.Context(), insertClient, prepared.Turn.TurnID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		enqueuedAt = time.Now().UTC()
		run, turn, err = runService.MarkTurnQueued(r.Context(), prepared, jobID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		markedQueuedAt = time.Now().UTC()
		duplicate = prepared.ExistingTurn || skipped
	}

	enrichAgentRun(run)
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	s.writeJSON(w, r, status, map[string]any{
		"run":          run,
		"turn":         turn,
		"duplicate":    duplicate,
		"queue_job_id": jobID,
	})

	attrs := []any{
		"run_id", run.ID,
		"turn_id", turn.TurnID,
		"queue_job_id", jobID,
		"duplicate", duplicate,
		"decode_ms", decodedAt.Sub(requestStartedAt).Milliseconds(),
		"prepare_ms", preparedAt.Sub(decodedAt).Milliseconds(),
		"total_ms", time.Since(requestStartedAt).Milliseconds(),
	}
	if !enqueuedAt.IsZero() {
		attrs = append(
			attrs,
			"enqueue_ms", enqueuedAt.Sub(preparedAt).Milliseconds(),
			"mark_queued_ms", markedQueuedAt.Sub(enqueuedAt).Milliseconds(),
		)
	}
	s.logger.Info("app run queue timing", attrs...)
}

func (s *Server) handleImplementPlan(w http.ResponseWriter, r *http.Request) {
	requestStartedAt := time.Now().UTC()
	runService, err := s.runtime.currentRunService(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	insertClient, err := s.runtime.currentInsertClient(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	var req appRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	decodedAt := time.Now().UTC()

	prepared, err := runService.PrepareQueuedImplementationTurn(r.Context(), chi.URLParam(r, "runID"), agentrun.Request{
		ClientRequestID: strings.TrimSpace(req.ClientRequestID),
		Title:           strings.TrimSpace(req.Title),
		Model:           strings.TrimSpace(req.Model),
		SystemPrompt:    strings.TrimSpace(req.SystemPrompt),
		ProviderName:    strings.TrimSpace(req.ProviderName),
		Tools: agentpkg.ToolSelection{
			EnabledToolIDs: append([]string(nil), req.EnabledToolIDs...),
			PinnedToolIDs:  append([]string(nil), req.PinnedToolIDs...),
			EnableToolIDs:  append([]string(nil), req.EnableToolIDs...),
			DisableToolIDs: append([]string(nil), req.DisableToolIDs...),
			PinToolIDs:     append([]string(nil), req.PinToolIDs...),
			UnpinToolIDs:   append([]string(nil), req.UnpinToolIDs...),
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, agentrun.ErrRunBusy):
			s.writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, knowledge.ErrNotFound):
			s.writeError(w, http.StatusNotFound, err.Error())
		default:
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	preparedAt := time.Now().UTC()

	jobID, skipped, err := queue.EnqueueAgentRun(r.Context(), insertClient, prepared.Turn.TurnID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enqueuedAt := time.Now().UTC()

	run, turn, err := runService.MarkTurnQueued(r.Context(), prepared, jobID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	markedQueuedAt := time.Now().UTC()

	enrichAgentRun(run)
	status := http.StatusAccepted
	if skipped {
		status = http.StatusOK
	}
	s.writeJSON(w, r, status, map[string]any{
		"run":          run,
		"turn":         turn,
		"duplicate":    skipped,
		"queue_job_id": jobID,
	})

	s.logger.Info("implement plan queue timing",
		"source_run_id", chi.URLParam(r, "runID"),
		"run_id", run.ID,
		"turn_id", turn.TurnID,
		"queue_job_id", jobID,
		"decode_ms", decodedAt.Sub(requestStartedAt).Milliseconds(),
		"prepare_ms", preparedAt.Sub(decodedAt).Milliseconds(),
		"enqueue_ms", enqueuedAt.Sub(preparedAt).Milliseconds(),
		"mark_queued_ms", markedQueuedAt.Sub(enqueuedAt).Milliseconds(),
		"total_ms", time.Since(requestStartedAt).Milliseconds(),
	)
}

func (s *Server) handleStreamAppRunEvents(w http.ResponseWriter, r *http.Request) {
	handle, err := s.runtime.currentHandle(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	store := handle.store
	runService := handle.runService
	runID := chi.URLParam(r, "runID")
	if _, err := store.GetAgentRun(r.Context(), runID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, knowledge.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, err.Error())
		return
	}

	afterEventID, err := parseAfterEventID(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
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

	ctx := r.Context()
	liveEvents := runService.Subscribe(ctx, runID)
	pollTicker := time.NewTicker(350 * time.Millisecond)
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

	writeEvent := func(evt knowledge.AgentRunEvent) error {
		payload, err := json.Marshal(evt)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", evt.EventID, payload); err != nil {
			return err
		}
		flusher.Flush()
		afterEventID = evt.EventID
		return nil
	}

	flushBacklog := func() error {
		for {
			events, err := store.ListAgentRunEventsAfter(ctx, runID, afterEventID, appRunEventBatchSize)
			if err != nil {
				return err
			}
			if len(events) == 0 {
				return nil
			}
			for _, evt := range events {
				if evt.EventID <= afterEventID {
					continue
				}
				if err := writeEvent(evt); err != nil {
					return err
				}
			}
			if len(events) < appRunEventBatchSize {
				return nil
			}
		}
	}

	shouldClose := func() (bool, error) {
		run, err := store.GetAgentRun(ctx, runID)
		if err != nil {
			if errors.Is(err, knowledge.ErrNotFound) {
				return true, nil
			}
			return false, err
		}
		return isTerminalRunStatus(run.Status) && afterEventID >= run.LastEventID, nil
	}

	if err := flushBacklog(); err != nil {
		s.logger.Error("flush app run backlog failed", "run_id", runID, "error", err)
		return
	}
	if done, err := shouldClose(); err == nil && done {
		return
	} else if err != nil {
		s.logger.Error("load app run status failed", "run_id", runID, "error", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-liveEvents:
			if !ok {
				return
			}
			if evt.EventID <= afterEventID {
				continue
			}
			if err := writeEvent(evt); err != nil {
				return
			}
			if done, err := shouldClose(); err == nil && done {
				return
			}
		case <-pollTicker.C:
			if err := flushBacklog(); err != nil {
				s.logger.Error("poll app run events failed", "run_id", runID, "error", err)
				return
			}
			if done, err := shouldClose(); err == nil && done {
				return
			}
		case <-heartbeatTicker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) handleAbortAppRun(w http.ResponseWriter, r *http.Request) {
	runService, err := s.runtime.currentRunService(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	run, err := runService.AbortRun(r.Context(), chi.URLParam(r, "runID"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, knowledge.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, err.Error())
		return
	}
	enrichAgentRun(run)
	s.writeJSON(w, r, http.StatusAccepted, map[string]any{"run": run})
}

func (s *Server) handleDeleteMessagesFromIndex(w http.ResponseWriter, r *http.Request) {
	handle, err := s.runtime.currentHandle(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	store := handle.store
	runID := chi.URLParam(r, "runID")

	fromIndexStr := strings.TrimSpace(r.URL.Query().Get("from_user_index"))
	if fromIndexStr == "" {
		s.writeError(w, http.StatusBadRequest, "from_user_index query parameter is required")
		return
	}
	fromIndex, err := strconv.Atoi(fromIndexStr)
	if err != nil || fromIndex < 0 {
		s.writeError(w, http.StatusBadRequest, "from_user_index must be a non-negative integer")
		return
	}

	if err := store.DeleteMessagesFromUserIndex(r.Context(), runID, fromIndex); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, knowledge.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, err.Error())
		return
	}

	run, err := store.GetAgentRun(r.Context(), runID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrichAgentRun(run)
	s.writeJSON(w, r, http.StatusOK, map[string]any{"run": run})
}

func parseAfterEventID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("after_event_id"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("after_event_id must be a non-negative integer")
	}
	return value, nil
}

func enrichAgentRun(run *knowledge.AgentRun) {
	if run == nil {
		return
	}
	if enrichedTrace, err := agentpkg.EnrichTraceJSON(run.Trace); err == nil {
		run.Trace = enrichedTrace
	}
}

func isTerminalRunStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "completed", "failed", "aborted":
		return true
	default:
		return false
	}
}
