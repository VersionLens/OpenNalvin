package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func defaultStartSubAgentLoop(child *subAgentHandle) {
	go child.loop()
}

var startSubAgentLoop func(*subAgentHandle)

func init() {
	startSubAgentLoop = defaultStartSubAgentLoop
}

type subAgentManager struct {
	parent *runSessionState
	store  *knowledge.Store
	cfg    configpkg.Config

	mu       sync.Mutex
	seq      int
	children map[string]*subAgentHandle
}

type subAgentSpawnRequest struct {
	Message         string
	Title           string
	SystemPrompt    string
	TaskName        string
	Tools           ToolSelection
	ParentToolState StoredToolState
	WorkspacePaths  *workspacepkg.Paths
}

type subAgentMessage struct {
	Message      string
	Title        string
	SystemPrompt string
	Interrupt    bool
}

type SubAgentStatus struct {
	RunID                    string `json:"run_id"`
	TaskName                 string `json:"task_name"`
	ParentRunID              string `json:"parent_run_id,omitempty"`
	RootRunID                string `json:"root_run_id,omitempty"`
	Status                   string `json:"status"`
	Error                    string `json:"error,omitempty"`
	PendingMessages          int    `json:"pending_messages"`
	LatestAssistantOutput    string `json:"latest_assistant_output"`
	HasLatestAssistantOutput bool   `json:"has_latest_assistant_output"`
}

type subAgentHandle struct {
	manager         *subAgentManager
	session         *runSessionState
	runID           string
	taskName        string
	parentRunID     string
	rootRunID       string
	tools           ToolSelection
	parentToolState StoredToolState
	workspacePaths  *workspacepkg.Paths
	initialTitle    string

	mu            sync.Mutex
	queue         []subAgentMessage
	status        string
	lastError     string
	currentCancel context.CancelFunc
	closed        bool
	wake          chan struct{}
}

func newSubAgentManager(parent *runSessionState, store *knowledge.Store, cfg configpkg.Config) *subAgentManager {
	return &subAgentManager{
		parent:   parent,
		store:    store,
		cfg:      cfg,
		children: map[string]*subAgentHandle{},
	}
}

func (m *subAgentManager) spawn(ctx context.Context, req subAgentSpawnRequest) (*subAgentHandle, error) {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}

	providerName, providerCfg, err := m.resolveChildProvider()
	if err != nil {
		return nil, err
	}

	parentMeta := m.parent.metadata()
	rootRunID := parentMeta.RootRunID
	if rootRunID == "" {
		rootRunID = m.parent.runID
	}

	// Capture workspace paths from context so the child loop can re-embed them.
	var workspacePaths *workspacepkg.Paths
	if req.WorkspacePaths != nil {
		wp := *req.WorkspacePaths
		workspacePaths = &wp
	} else if paths, ok := workspacepkg.PathsFromContext(ctx); ok {
		workspacePaths = &paths
	}

	m.mu.Lock()
	taskName := m.uniqueTaskNameLocked(req.TaskName)
	runID := uuid.NewString()
	childSession := globalRunSessions.getOrCreate(runID)
	childSession.applyContext(configpkg.WithContext(ctx, m.cfg), m.store)
	childSession.setDebugWriter(m.parent.debugWriter())
	childSession.setMetadata(m.parent.runID, rootRunID, taskName, RunKindChild)
	childSession.setMode(parentMeta.Mode)
	childSession.setPlanRef(parentMeta.PlanRef)
	childSession.setProviderName(providerName)

	child := &subAgentHandle{
		manager:         m,
		session:         childSession,
		runID:           runID,
		taskName:        taskName,
		parentRunID:     m.parent.runID,
		rootRunID:       rootRunID,
		tools:           req.Tools,
		parentToolState: req.ParentToolState,
		workspacePaths:  workspacePaths,
		initialTitle:    strings.TrimSpace(req.Title),
		queue: []subAgentMessage{{
			Message:      message,
			Title:        strings.TrimSpace(req.Title),
			SystemPrompt: strings.TrimSpace(req.SystemPrompt),
		}},
		status: AgentStatusQueued,
		wake:   make(chan struct{}, 1),
	}
	m.children[runID] = child
	m.mu.Unlock()

	if err := m.persistQueuedChildRun(ctx, providerName, providerCfg.Model, child, message); err != nil {
		m.mu.Lock()
		delete(m.children, runID)
		m.mu.Unlock()
		return nil, err
	}

	m.parent.debugf("spawned child run_id=%s task_name=%s", runID, taskName)
	child.signal()
	startSubAgentLoop(child)

	m.parent.enqueueNotification(fmt.Sprintf("Spawned sub-agent %q (%s).", taskName, runID))
	return child, nil
}

func (m *subAgentManager) resolveChildProvider() (string, ProviderConfig, error) {
	providerName := strings.TrimSpace(m.cfg.Agent.Subagents.ProviderName)
	if providerName == "" {
		providerName = m.parent.providerName()
	}
	providerName = normalizeProviderName(providerName)
	providerCfg, err := LoadProviderConfig(providerName)
	if err != nil {
		return "", ProviderConfig{}, err
	}
	return providerName, providerCfg, nil
}

func (m *subAgentManager) persistQueuedChildRun(ctx context.Context, providerName, model string, child *subAgentHandle, prompt string) error {
	now := time.Now().UTC()
	trace := StoredTrace{
		SchemaVersion: 2,
		RunID:         child.runID,
		Title:         deriveRunTitle(child.initialTitle, prompt),
		Model:         strings.TrimSpace(model),
		Provider:      normalizeProviderName(providerName),
		Todos:         []StoredTodoItem{},
		Metadata: StoredRunMeta{
			ParentRunID: child.parentRunID,
			RootRunID:   child.rootRunID,
			TaskName:    child.taskName,
			RunKind:     RunKindChild,
			Mode:        normalizeRunMode(m.parent.metadata().Mode),
			PlanRef:     normalizePlanRef(m.parent.metadata().PlanRef),
			Tools: child.parentToolState,
		},
		Messages:  []StoredMessage{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	rawTrace, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("marshal queued child trace: %w", err)
	}

	if err := m.store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
		ID:           child.runID,
		Title:        trace.Title,
		Model:        trace.Model,
		Provider:     trace.Provider,
		Prompt:       prompt,
		ParentRunID:  child.parentRunID,
		RootRunID:    child.rootRunID,
		TaskName:     child.taskName,
		RunKind:      RunKindChild,
		Status:       AgentStatusQueued,
		MessageCount: len(trace.Messages),
		Trace:        rawTrace,
	}); err != nil {
		return fmt.Errorf("create queued child run: %w", err)
	}

	return nil
}

func (m *subAgentManager) resolve(target string) (*subAgentHandle, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("target is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if child, ok := m.children[target]; ok {
		return child, nil
	}
	for _, child := range m.children {
		if child.taskName == target {
			return child, nil
		}
	}
	return nil, fmt.Errorf("sub-agent %q not found", target)
}

func (m *subAgentManager) closeAll(ctx context.Context) error {
	m.mu.Lock()
	children := make([]*subAgentHandle, 0, len(m.children))
	for _, child := range m.children {
		children = append(children, child)
	}
	m.mu.Unlock()

	var errs []string
	for _, child := range children {
		if err := child.close(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (m *subAgentManager) uniqueTaskNameLocked(requested string) string {
	base := strings.TrimSpace(requested)
	if base == "" {
		m.seq++
		base = fmt.Sprintf("agent_%d", m.seq)
	}

	candidate := base
	index := 2
	for {
		conflict := false
		for _, child := range m.children {
			if child.taskName == candidate {
				conflict = true
				break
			}
		}
		if !conflict {
			return candidate
		}
		candidate = fmt.Sprintf("%s_%d", base, index)
		index++
	}
}

func (h *subAgentHandle) send(_ context.Context, msg subAgentMessage) error {
	message := strings.TrimSpace(msg.Message)
	if message == "" {
		return fmt.Errorf("message is required")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return fmt.Errorf("sub-agent %q is closed", h.taskName)
	}

	if msg.Interrupt {
		h.queue = nil
		if h.currentCancel != nil {
			h.currentCancel()
		}
	}

	msg.Message = message
	msg.Title = strings.TrimSpace(msg.Title)
	msg.SystemPrompt = strings.TrimSpace(msg.SystemPrompt)
	h.queue = append(h.queue, msg)
	if h.status != AgentStatusRunning {
		h.status = AgentStatusQueued
	}
	h.session.debugf("queued message for child task_name=%s interrupt=%t pending=%d", h.taskName, msg.Interrupt, len(h.queue))
	h.signal()
	return nil
}

func (h *subAgentHandle) wait(ctx context.Context) (SubAgentStatus, error) {
	for {
		status, err := h.result(ctx)
		if err != nil {
			return SubAgentStatus{}, err
		}
		if status.Status != AgentStatusQueued && status.Status != AgentStatusRunning {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (h *subAgentHandle) result(ctx context.Context) (SubAgentStatus, error) {
	status := h.snapshot()
	output, ok, err := h.latestAssistantOutput(ctx)
	if err != nil {
		return SubAgentStatus{}, err
	}
	status.LatestAssistantOutput = output
	status.HasLatestAssistantOutput = ok
	return status, nil
}

func (h *subAgentHandle) latestAssistantOutput(ctx context.Context) (string, bool, error) {
	if h.manager == nil || h.manager.store == nil {
		return "", false, fmt.Errorf("workspace-backed knowledge store is required")
	}

	run, err := h.manager.store.GetAgentRun(ctx, h.runID)
	if err != nil {
		return "", false, fmt.Errorf("get child run %q: %w", h.runID, err)
	}
	trace, err := parseStoredTrace(run.Trace)
	if err != nil {
		return "", false, err
	}
	return latestAssistantOutputFromTrace(trace)
}

func (h *subAgentHandle) close(ctx context.Context) error {
	if mgr := h.session.currentSubAgentManager(); mgr != nil {
		_ = mgr.closeAll(ctx)
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.queue = nil
	cancel := h.currentCancel
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	h.session.debugf("closed child task_name=%s run_id=%s", h.taskName, h.runID)
	h.signal()
	return nil
}

func (h *subAgentHandle) snapshot() SubAgentStatus {
	h.mu.Lock()
	defer h.mu.Unlock()

	return SubAgentStatus{
		RunID:           h.runID,
		TaskName:        h.taskName,
		ParentRunID:     h.parentRunID,
		RootRunID:       h.rootRunID,
		Status:          h.status,
		Error:           h.lastError,
		PendingMessages: len(h.queue),
	}
}

func (h *subAgentHandle) loop() {
	for {
		msg, exit := h.dequeue()
		if exit {
			h.setFinalClosed()
			return
		}
		if msg == nil {
			<-h.wake
			continue
		}

		baseCtx := configpkg.WithContext(context.Background(), h.manager.cfg)
		if h.workspacePaths != nil {
			baseCtx = workspacepkg.WithPaths(baseCtx, *h.workspacePaths)
		}
		runCtx, cancel := context.WithCancel(baseCtx)
		h.setRunning(cancel)
		h.session.debugf("child task_name=%s starting run message=%q", h.taskName, msg.Message)

		_, err := Run(runCtx, h.manager.store, RunRequest{
			Message:      msg.Message,
			RunID:        h.runID,
			Title:        chooseFirstNonEmpty(msg.Title, h.initialTitle, h.taskName),
			ProviderName: h.session.providerName(),
			SystemPrompt: msg.SystemPrompt,
			ParentRunID:  h.parentRunID,
			RootRunID:    h.rootRunID,
			TaskName:     h.taskName,
			RunKind:      RunKindChild,
			Mode:         h.session.metadata().Mode,
			PlanRef:      h.session.metadata().PlanRef,
			Tools:        h.tools,
		}, RunOptions{
			Debug:   h.session.debugWriter(),
			Verbose: h.session.debugWriter() != nil,
		})

		h.onRunFinished(err)
	}
}

func (h *subAgentHandle) dequeue() (*subAgentMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.queue) > 0 {
		msg := h.queue[0]
		h.queue = h.queue[1:]
		return &msg, false
	}
	if h.closed {
		return nil, true
	}
	return nil, false
}

func (h *subAgentHandle) setRunning(cancel context.CancelFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentCancel = cancel
	h.status = AgentStatusRunning
	h.lastError = ""
}

func (h *subAgentHandle) onRunFinished(err error) {
	h.mu.Lock()
	cancel := h.currentCancel
	h.currentCancel = nil
	closed := h.closed

	switch {
	case err == nil && closed:
		h.status = AgentStatusClosed
		h.lastError = ""
	case err == nil:
		h.status = AgentStatusCompleted
		h.lastError = ""
	case errors.Is(err, context.Canceled) && closed:
		h.status = AgentStatusClosed
		h.lastError = ""
	case errors.Is(err, context.Canceled):
		h.status = AgentStatusAborted
		h.lastError = ""
	default:
		h.status = AgentStatusFailed
		h.lastError = err.Error()
	}
	status := h.status
	taskName := h.taskName
	runID := h.runID
	lastError := h.lastError
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	switch status {
	case AgentStatusCompleted:
		h.session.debugf("child task_name=%s completed", taskName)
		h.manager.parent.enqueueNotification(fmt.Sprintf("Sub-agent %q (%s) completed.", taskName, runID))
	case AgentStatusFailed:
		h.session.debugf("child task_name=%s failed error=%s", taskName, lastError)
		h.manager.parent.enqueueNotification(fmt.Sprintf("Sub-agent %q (%s) failed: %s", taskName, runID, lastError))
	case AgentStatusAborted:
		h.session.debugf("child task_name=%s aborted", taskName)
		h.manager.parent.enqueueNotification(fmt.Sprintf("Sub-agent %q (%s) stopped.", taskName, runID))
	case AgentStatusClosed:
		h.session.debugf("child task_name=%s closed", taskName)
		h.manager.parent.enqueueNotification(fmt.Sprintf("Sub-agent %q (%s) closed.", taskName, runID))
	}
}

func (h *subAgentHandle) setFinalClosed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = AgentStatusClosed
	h.lastError = ""
}

func (h *subAgentHandle) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func chooseFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func latestAssistantOutputFromTrace(trace StoredTrace) (string, bool, error) {
	for index := len(trace.Messages) - 1; index >= 0; index-- {
		msg := trace.Messages[index]
		if msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		return content, true, nil
	}
	return "", false, nil
}
