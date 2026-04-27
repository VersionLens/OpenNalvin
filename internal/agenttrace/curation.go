package agenttrace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

type LabelInput struct {
	Quality string
	Reward  *float64
	Split   string
	Tags    []string
	Notes   string
}

func Derive(ctx context.Context, store *knowledge.Store, runID, title, note string, tags []string) (*knowledge.AgentTraceCuration, error) {
	run, err := store.GetAgentRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSpace(run.Title)
	}
	curation, err := store.CreateAgentTraceCuration(ctx, knowledge.CreateAgentTraceCurationInput{
		SourceRunID:     run.ID,
		Title:           title,
		Status:          "draft",
		Tags:            tags,
		Notes:           note,
		SourceTraceHash: SourceTraceHash(run.Trace),
		Trace:           append(json.RawMessage(nil), run.Trace...),
	})
	if err != nil {
		return nil, err
	}
	_ = recordEvent(ctx, store, curation.ID, "derive", map[string]any{"source_run_id": run.ID})
	return curation, nil
}

func SetStatus(ctx context.Context, store *knowledge.Store, curationID, status string) (*knowledge.AgentTraceCuration, error) {
	curation, err := store.GetAgentTraceCuration(ctx, curationID)
	if err != nil {
		return nil, err
	}
	if err := store.UpdateAgentTraceCuration(ctx, knowledge.UpdateAgentTraceCurationInput{
		ID:                curation.ID,
		Title:             curation.Title,
		Status:            status,
		Tags:              curation.Tags,
		Notes:             curation.Notes,
		Quality:           curation.Quality,
		Reward:            curation.Reward,
		Split:             curation.Split,
		SourceTraceHash:   curation.SourceTraceHash,
		ValidationSummary: curation.ValidationSummary,
		Trace:             curation.Trace,
	}); err != nil {
		return nil, err
	}
	_ = recordEvent(ctx, store, curation.ID, status, map[string]any{"status": status})
	return store.GetAgentTraceCuration(ctx, curation.ID)
}

func Label(ctx context.Context, store *knowledge.Store, curationID string, input LabelInput) (*knowledge.AgentTraceCuration, error) {
	curation, err := store.GetAgentTraceCuration(ctx, curationID)
	if err != nil {
		return nil, err
	}
	tags := append([]string(nil), curation.Tags...)
	tags = append(tags, input.Tags...)
	notes := curation.Notes
	if strings.TrimSpace(input.Notes) != "" {
		if strings.TrimSpace(notes) != "" {
			notes += "\n"
		}
		notes += strings.TrimSpace(input.Notes)
	}
	quality := curation.Quality
	if strings.TrimSpace(input.Quality) != "" {
		quality = strings.TrimSpace(input.Quality)
	}
	reward := curation.Reward
	if input.Reward != nil {
		value := *input.Reward
		reward = &value
	}
	split := curation.Split
	if strings.TrimSpace(input.Split) != "" {
		split = strings.TrimSpace(input.Split)
	}
	if err := store.UpdateAgentTraceCuration(ctx, knowledge.UpdateAgentTraceCurationInput{
		ID:                curation.ID,
		Title:             curation.Title,
		Status:            curation.Status,
		Tags:              tags,
		Notes:             notes,
		Quality:           quality,
		Reward:            reward,
		Split:             split,
		SourceTraceHash:   curation.SourceTraceHash,
		ValidationSummary: curation.ValidationSummary,
		Trace:             curation.Trace,
	}); err != nil {
		return nil, err
	}
	_ = recordEvent(ctx, store, curation.ID, "label", input)
	return store.GetAgentTraceCuration(ctx, curation.ID)
}

func ReplaceMessage(ctx context.Context, store *knowledge.Store, curationID, messageRef, content, reasoning string, setReasoning bool) (*knowledge.AgentTraceCuration, error) {
	return mutateTrace(ctx, store, curationID, "replace_message", map[string]any{"message": messageRef}, func(trace *agentpkg.StoredTrace) error {
		idx, err := findMessageIndex(trace.Messages, messageRef)
		if err != nil {
			return err
		}
		switch trace.Messages[idx].Role {
		case "tool":
			trace.Messages[idx].ContentText = content
		default:
			trace.Messages[idx].Content = content
		}
		if setReasoning {
			trace.Messages[idx].Reasoning = reasoning
		}
		return nil
	})
}

func DropMessage(ctx context.Context, store *knowledge.Store, curationID, messageRef string) (*knowledge.AgentTraceCuration, error) {
	return mutateTrace(ctx, store, curationID, "drop_message", map[string]any{"message": messageRef}, func(trace *agentpkg.StoredTrace) error {
		idx, err := findMessageIndex(trace.Messages, messageRef)
		if err != nil {
			return err
		}
		trace.Messages = append(trace.Messages[:idx], trace.Messages[idx+1:]...)
		return nil
	})
}

func InsertMessage(ctx context.Context, store *knowledge.Store, curationID, position, role, content, toolCallID, toolName string) (*knowledge.AgentTraceCuration, error) {
	return mutateTrace(ctx, store, curationID, "insert_message", map[string]any{"position": position, "role": role}, func(trace *agentpkg.StoredTrace) error {
		role = strings.TrimSpace(role)
		switch role {
		case "user", "assistant", "tool":
		default:
			return fmt.Errorf("role must be user, assistant, or tool")
		}
		idx, err := insertionIndex(trace.Messages, position)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		message := agentpkg.StoredMessage{
			Role:      role,
			MessageID: nextCuratedMessageID(trace.Messages),
			StartedAt: &now,
			EndedAt:   &now,
		}
		if role == "tool" {
			message.ToolCallID = strings.TrimSpace(toolCallID)
			message.ToolName = strings.TrimSpace(toolName)
			message.ContentText = content
			message.Ok = true
		} else {
			message.Content = content
		}
		trace.Messages = append(trace.Messages, agentpkg.StoredMessage{})
		copy(trace.Messages[idx+1:], trace.Messages[idx:])
		trace.Messages[idx] = message
		return nil
	})
}

func Redact(ctx context.Context, store *knowledge.Store, curationID, pattern, replacement string) (*knowledge.AgentTraceCuration, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile redaction pattern: %w", err)
	}
	return mutateTrace(ctx, store, curationID, "redact", map[string]any{"pattern": pattern, "replacement": replacement}, func(trace *agentpkg.StoredTrace) error {
		trace.SystemPrompt = re.ReplaceAllString(trace.SystemPrompt, replacement)
		trace.EffectiveSystemPrompt = re.ReplaceAllString(trace.EffectiveSystemPrompt, replacement)
		for i := range trace.Messages {
			msg := &trace.Messages[i]
			msg.Content = re.ReplaceAllString(msg.Content, replacement)
			msg.Reasoning = re.ReplaceAllString(msg.Reasoning, replacement)
			msg.ContentText = re.ReplaceAllString(msg.ContentText, replacement)
			for j := range msg.ToolCalls {
				if msg.ToolCalls[j].Function != nil {
					msg.ToolCalls[j].Function.Arguments = re.ReplaceAllString(msg.ToolCalls[j].Function.Arguments, replacement)
				}
			}
		}
		return nil
	})
}

func EditInEditor(ctx context.Context, store *knowledge.Store, curationID, editor string) (*knowledge.AgentTraceCuration, error) {
	curation, err := store.GetAgentTraceCuration(ctx, curationID)
	if err != nil {
		return nil, err
	}
	editor = strings.TrimSpace(editor)
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		return nil, fmt.Errorf("set EDITOR or pass --editor-command")
	}
	tmp, err := os.CreateTemp("", "nalvin-trace-*.json")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, curation.Trace, "", "  "); err != nil {
		pretty.Reset()
		pretty.Write(curation.Trace)
	}
	if _, err := tmp.Write(pretty.Bytes()); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", editor+" "+shellQuote(tmpPath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	edited, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	if !json.Valid(edited) {
		return nil, fmt.Errorf("edited trace is not valid JSON")
	}
	if _, err := agentpkg.ParseStoredTrace(edited); err != nil {
		return nil, err
	}
	if err := store.UpdateAgentTraceCuration(ctx, knowledge.UpdateAgentTraceCurationInput{
		ID:                curation.ID,
		Title:             curation.Title,
		Status:            curation.Status,
		Tags:              curation.Tags,
		Notes:             curation.Notes,
		Quality:           curation.Quality,
		Reward:            curation.Reward,
		Split:             curation.Split,
		SourceTraceHash:   curation.SourceTraceHash,
		ValidationSummary: curation.ValidationSummary,
		Trace:             edited,
	}); err != nil {
		return nil, err
	}
	_ = recordEvent(ctx, store, curation.ID, "edit", map[string]any{"editor": editor})
	return store.GetAgentTraceCuration(ctx, curation.ID)
}

func mutateTrace(ctx context.Context, store *knowledge.Store, curationID, eventKind string, eventPayload any, mutate func(*agentpkg.StoredTrace) error) (*knowledge.AgentTraceCuration, error) {
	curation, err := store.GetAgentTraceCuration(ctx, curationID)
	if err != nil {
		return nil, err
	}
	trace, err := agentpkg.ParseStoredTrace(curation.Trace)
	if err != nil {
		return nil, err
	}
	if err := mutate(&trace); err != nil {
		return nil, err
	}
	trace.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(trace)
	if err != nil {
		return nil, err
	}
	if err := store.UpdateAgentTraceCuration(ctx, knowledge.UpdateAgentTraceCurationInput{
		ID:                curation.ID,
		Title:             curation.Title,
		Status:            curation.Status,
		Tags:              curation.Tags,
		Notes:             curation.Notes,
		Quality:           curation.Quality,
		Reward:            curation.Reward,
		Split:             curation.Split,
		SourceTraceHash:   curation.SourceTraceHash,
		ValidationSummary: curation.ValidationSummary,
		Trace:             raw,
	}); err != nil {
		return nil, err
	}
	_ = recordEvent(ctx, store, curation.ID, eventKind, eventPayload)
	return store.GetAgentTraceCuration(ctx, curation.ID)
}

func recordEvent(ctx context.Context, store *knowledge.Store, curationID, kind string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		data = json.RawMessage(`{}`)
	}
	_, err = store.CreateAgentTraceCurationEvent(ctx, knowledge.CreateAgentTraceCurationEventInput{
		CurationID: curationID,
		Kind:       kind,
		Payload:    data,
	})
	return err
}

func findMessageIndex(messages []agentpkg.StoredMessage, ref string) (int, error) {
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "#"))
	if ref == "" {
		return -1, fmt.Errorf("message reference is required")
	}
	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 || n > len(messages) {
			return -1, fmt.Errorf("message index %d out of range", n)
		}
		return n - 1, nil
	}
	for i, message := range messages {
		if message.MessageID == ref {
			return i, nil
		}
	}
	return -1, fmt.Errorf("message %q not found", ref)
}

func insertionIndex(messages []agentpkg.StoredMessage, position string) (int, error) {
	position = strings.TrimSpace(position)
	switch position {
	case "", "end":
		return len(messages), nil
	case "start":
		return 0, nil
	}
	if strings.HasPrefix(position, "after:") {
		idx, err := findMessageIndex(messages, strings.TrimPrefix(position, "after:"))
		if err != nil {
			return 0, err
		}
		return idx + 1, nil
	}
	if strings.HasPrefix(position, "before:") {
		return findMessageIndex(messages, strings.TrimPrefix(position, "before:"))
	}
	return findMessageIndex(messages, position)
}

func nextCuratedMessageID(messages []agentpkg.StoredMessage) string {
	seen := map[string]struct{}{}
	for _, message := range messages {
		seen[message.MessageID] = struct{}{}
	}
	for i := len(messages) + 1; ; i++ {
		id := fmt.Sprintf("curated_msg_%03d", i)
		if _, ok := seen[id]; !ok {
			return id
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
