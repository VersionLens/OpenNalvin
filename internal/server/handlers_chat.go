package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/agentrun"
)

// chatRequest is the request body sent by the AI SDK useChat hook.
type chatRequest struct {
	Messages     []chatUIMessage `json:"messages"`
	RunID        string          `json:"run_id"`
	Title        string          `json:"title"`
	Model        string          `json:"model"`
	SystemPrompt string          `json:"system_prompt"`
	Mode         string          `json:"mode"`
	EnabledTools []string        `json:"enabled_tool_ids"`
	PinnedTools  []string        `json:"pinned_tool_ids"`
}

// chatUIMessage is the message format sent by the AI SDK useChat hook.
type chatUIMessage struct {
	Role  string           `json:"role"`
	Parts []chatUITextPart `json:"parts"`
}

// chatUITextPart is a text part in a UI message.
type chatUITextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// handleChat implements POST /api/chat, emitting the AI SDK UI Message Stream
// Protocol so the frontend can use the @ai-sdk/react useChat hook directly.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	runService, err := s.runtime.currentRunService(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	// Extract the last user message (text part) from the UI message array.
	var message string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		for _, part := range req.Messages[i].Parts {
			if part.Type == "text" && part.Text != "" {
				message = part.Text
				break
			}
		}
		if message != "" {
			break
		}
	}
	if message == "" {
		s.writeError(w, http.StatusBadRequest, "messages must contain at least one user text message")
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
	w.Header().Set("x-vercel-ai-ui-message-stream", "v1")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	writePart := func(v any) error {
		payload, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	// State machine: track open text/reasoning parts so we can emit start/end.
	var (
		textOpen      bool
		textID        string
		reasoningOpen bool
		reasoningID   string
		messageID     string
	)
	textPartCounter := 0
	reasoningPartCounter := 0

	closeText := func() error {
		if !textOpen {
			return nil
		}
		textOpen = false
		return writePart(map[string]any{"type": "text-end", "id": textID})
	}
	closeReasoning := func() error {
		if !reasoningOpen {
			return nil
		}
		reasoningOpen = false
		return writePart(map[string]any{"type": "reasoning-end", "id": reasoningID})
	}

	onChunk := func(chunk agentpkg.TraceChunk) error {
		// Initial chunk: captures run ID and emits start.
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil && chunk.Choices[0].Delta.Role == "assistant" {
			messageID = chunk.ID
			return writePart(map[string]any{"type": "start", "messageId": messageID})
		}

		// Error chunk.
		if chunk.Error != nil {
			return writePart(map[string]any{"type": "error", "errorText": chunk.Error.Message})
		}

		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]

		// Finish chunk.
		if choice.FinishReason != nil {
			if err := closeText(); err != nil {
				return err
			}
			if err := closeReasoning(); err != nil {
				return err
			}
			return writePart(map[string]any{"type": "finish"})
		}

		delta := choice.Delta
		if delta == nil {
			return nil
		}

		// Text content delta.
		if delta.Content != "" {
			if err := closeReasoning(); err != nil {
				return err
			}
			if !textOpen {
				textPartCounter++
				textID = fmt.Sprintf("text-%d", textPartCounter)
				textOpen = true
				if err := writePart(map[string]any{"type": "text-start", "id": textID}); err != nil {
					return err
				}
			}
			if err := writePart(map[string]any{"type": "text-delta", "id": textID, "delta": delta.Content}); err != nil {
				return err
			}
		}

		// Reasoning delta.
		if delta.Reasoning != "" {
			if err := closeText(); err != nil {
				return err
			}
			if !reasoningOpen {
				reasoningPartCounter++
				reasoningID = fmt.Sprintf("reasoning-%d", reasoningPartCounter)
				reasoningOpen = true
				if err := writePart(map[string]any{"type": "reasoning-start", "id": reasoningID}); err != nil {
					return err
				}
			}
			if err := writePart(map[string]any{"type": "reasoning-delta", "id": reasoningID, "delta": delta.Reasoning}); err != nil {
				return err
			}
		}

		// Tool call chunk: args arrive complete from the agent.
		for _, call := range delta.ToolCalls {
			if call.ID == "" || call.Function == nil {
				continue
			}
			if err := closeText(); err != nil {
				return err
			}
			if err := closeReasoning(); err != nil {
				return err
			}
			toolName := call.Function.Name
			args := call.Function.Arguments
			// Parse arguments JSON for the tool-input-available part.
			var parsedArgs any
			if json.Unmarshal([]byte(args), &parsedArgs) != nil {
				parsedArgs = args // fallback to raw string
			}
			if err := writePart(map[string]any{
				"type":             "tool-input-start",
				"toolCallId":       call.ID,
				"toolName":         toolName,
				"dynamic":          true,
				"providerExecuted": true,
			}); err != nil {
				return err
			}
			if err := writePart(map[string]any{
				"type":             "tool-input-available",
				"toolCallId":       call.ID,
				"toolName":         toolName,
				"input":            parsedArgs,
				"dynamic":          true,
				"providerExecuted": true,
			}); err != nil {
				return err
			}
		}

		return nil
	}

	onToolResult := func(evt agentpkg.ToolResultEvent) error {
		if err := writePart(map[string]any{
			"type":             "tool-output-available",
			"toolCallId":       evt.ToolCallID,
			"output":           evt.Output,
			"dynamic":          true,
			"providerExecuted": true,
		}); err != nil {
			return err
		}
		if evt.PlanRef != nil && evt.PlanRef.Path != "" && evt.PlanRef.Status == agentpkg.PlanStatusReady {
			sourceRunID := evt.PlanRef.SourceRunID
			if sourceRunID == "" {
				sourceRunID = prepared.Run.ID
			}
			return writePart(map[string]any{
				"type": "data-plan-ready",
				"data": map[string]any{
					"plan_path":     evt.PlanRef.Path,
					"source_run_id": sourceRunID,
					"status":        evt.PlanRef.Status,
				},
			})
		}
		return nil
	}

	runErr := runService.ExecuteTurn(r.Context(), prepared.Turn.TurnID, agentrun.ExecutionOptions{
		EventSink: agentrun.EventSinkFunc(func(_ context.Context, evt agentrun.Event) error {
			switch evt.Kind {
			case agentrun.EventKindTraceChunk:
				if evt.Chunk == nil {
					return nil
				}
				return onChunk(*evt.Chunk)
			case agentrun.EventKindToolResult:
				if evt.ToolResult == nil {
					return nil
				}
				return onToolResult(*evt.ToolResult)
			default:
				return nil
			}
		}),
	})
	if runErr != nil {
		s.logger.Error("chat stream failed", "run_id", prepared.Run.ID, "error", runErr)
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}
