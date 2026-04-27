package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"charm.land/fantasy"
	"github.com/google/uuid"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const (
	sourceInternal = "internal"
	sourceMCP      = "mcp"
	sourceCustom   = "custom"
	sourceSkill    = "skill"
)

const (
	revealSourceSkillActivation = "skill"
)

var childRunRestrictedToolIDs = []string{
	"spawn_agent",
	"send_input",
	"wait_agent",
	"get_agent_result",
	"close_agent",
	"todowrite",
	"todoread",
	"bash",
}

type runtimeTool struct {
	id             string
	tool           fantasy.AgentTool
	source         string
	serverName     string
	keywords       []string
	defaultEnabled bool
	defaultPinned  bool
}

type resolvedToolState struct {
	enabled  map[string]struct{}
	pinned   map[string]struct{}
	revealed map[string]struct{}
}

type agentRuntime struct {
	cfg      configpkg.Config
	store    *knowledge.Store
	session  *runSessionState
	warnings []string
	model    string
	mode     string
	isChild  bool
	todos    []StoredTodoItem

	tools      map[string]runtimeTool
	state      resolvedToolState
	mcpManager *mcpServerManager

	skills        *skillManager
	skillExec     *skillExecutor
	skillExecOnce sync.Once

	toolOutputTokenLimit int
	compaction           *compactionManager
	compactionTrace      *StoredTrace // pointer to builder's trace for compaction reads
}

func ListTools(ctx context.Context, store *knowledge.Store, runID string, selection ToolSelection) (ToolCatalogResult, error) {
	requestedRunID := strings.TrimSpace(runID)
	if requestedRunID == "" {
		runID = "tool_session_" + uuid.NewString()
	} else {
		runID = requestedRunID
	}
	trace := StoredTrace{}
	session := newEphemeralSessionState(ctx, store, strings.TrimSpace(runID))
	if requestedRunID != "" && store != nil {
		run, err := store.GetAgentRun(ctx, requestedRunID)
		if err != nil {
			return ToolCatalogResult{}, fmt.Errorf("load run %q: %w", runID, err)
		}
		trace, err = parseStoredTrace(run.Trace)
		if err != nil {
			return ToolCatalogResult{}, err
		}
		session = globalRunSessions.getOrCreate(strings.TrimSpace(runID))
		session.applyContext(ctx, store)
		session.setMetadata(run.ParentRunID, run.RootRunID, run.TaskName, run.RunKind)
		session.setMode(trace.Metadata.Mode)
		session.setPlanRef(trace.Metadata.PlanRef)
	}

	rt, err := newAgentRuntime(ctx, store, session, trace, selection)
	if err != nil {
		return ToolCatalogResult{}, err
	}
	defer rt.close()
	return rt.catalogResult(), nil
}

func InvokeTool(ctx context.Context, store *knowledge.Store, runID, toolID string, input any, selection ToolSelection) (ToolRunResult, error) {
	requestedRunID := strings.TrimSpace(runID)
	if requestedRunID == "" {
		runID = "tool_session_" + uuid.NewString()
	} else {
		runID = requestedRunID
	}
	trace := StoredTrace{}
	session := newEphemeralSessionState(ctx, store, strings.TrimSpace(runID))
	if requestedRunID != "" && store != nil {
		run, err := store.GetAgentRun(ctx, requestedRunID)
		if err != nil {
			return ToolRunResult{}, fmt.Errorf("load run %q: %w", runID, err)
		}
		trace, err = parseStoredTrace(run.Trace)
		if err != nil {
			return ToolRunResult{}, err
		}
		session = globalRunSessions.getOrCreate(strings.TrimSpace(runID))
		session.applyContext(ctx, store)
		session.setMetadata(run.ParentRunID, run.RootRunID, run.TaskName, run.RunKind)
		session.setMode(trace.Metadata.Mode)
		session.setPlanRef(trace.Metadata.PlanRef)
	}

	rt, err := newAgentRuntime(ctx, store, session, trace, selection)
	if err != nil {
		return ToolRunResult{}, err
	}
	defer rt.close()

	toolID = strings.TrimSpace(toolID)
	tool, ok := rt.tools[toolID]
	if !ok {
		return ToolRunResult{}, fmt.Errorf("tool %q not found", toolID)
	}

	payload, err := marshalToolInput(input)
	if err != nil {
		return ToolRunResult{}, err
	}

	resp, err := tool.tool.Run(ctx, fantasy.ToolCall{
		ID:    "cli_tool_call",
		Name:  toolID,
		Input: payload,
	})
	if err != nil {
		return ToolRunResult{}, err
	}

	return ToolRunResult{
		ToolID:  toolID,
		Output:  formatToolResponse(resp),
		IsError: resp.IsError,
	}, nil
}

func newAgentRuntime(
	ctx context.Context,
	store *knowledge.Store,
	session *runSessionState,
	trace StoredTrace,
	selection ToolSelection,
) (*agentRuntime, error) {
	cfg, _ := configpkg.FromContext(ctx)
	if session == nil {
		session = newEphemeralSessionState(ctx, store, trace.RunID)
	}
	session.applyContext(ctx, store)
	if trace.RunID != "" {
		session.setMetadata(trace.Metadata.ParentRunID, trace.Metadata.RootRunID, trace.Metadata.TaskName, trace.Metadata.RunKind)
		session.setMode(trace.Metadata.Mode)
		session.setPlanRef(trace.Metadata.PlanRef)
	}
	isChildRun := session.metadata().RunKind == RunKindChild
	if !isChildRun {
		isChildRun = trace.Metadata.RunKind == RunKindChild
	}

	rt := &agentRuntime{
		cfg:     cfg,
		store:   store,
		session: session,
		model:   strings.TrimSpace(trace.Model),
		mode:    normalizeRunMode(session.metadata().Mode),
		isChild: isChildRun,
		todos:   append([]StoredTodoItem{}, trace.Todos...),
		tools:   map[string]runtimeTool{},
		state: resolvedToolState{
			enabled:  map[string]struct{}{},
			pinned:   map[string]struct{}{},
			revealed: map[string]struct{}{},
		},
		toolOutputTokenLimit: 4000,
	}

	for _, tool := range rt.internalTools() {
		rt.tools[tool.id] = tool
	}

	customTools, customWarnings := rt.customTools()
	rt.warnings = append(rt.warnings, customWarnings...)
	for _, tool := range customTools {
		if _, exists := rt.tools[tool.id]; exists {
			rt.warnings = append(rt.warnings, fmt.Sprintf("custom tool %q skipped: conflicts with existing tool", tool.id))
			continue
		}
		rt.tools[tool.id] = tool
	}

	mcpTools, warnings, err := rt.mcpTools(ctx)
	if err != nil {
		return nil, err
	}
	rt.warnings = append(rt.warnings, warnings...)
	for _, tool := range mcpTools {
		rt.tools[tool.id] = tool
	}
	if rt.isChild {
		rt.removeRestrictedSubAgentTools()
	}

	// Wire skills runtime when enabled. Skills load from embedded assets,
	// the user dir, and any workspace skill dir; system-activation skills are
	// activated immediately. Skill-declared command tools are registered now
	// so they show up in the catalog before tool-state resolution.
	if rt.cfg.Agent.Skills.Enabled {
		runKind := session.metadata().RunKind
		if runKind == "" {
			if rt.isChild {
				runKind = RunKindChild
			} else {
				runKind = RunKindRoot
			}
		}
		rt.skills = newSkillManager(ctx, cfg, runKind, rt.tools, parseStoredSkillState(trace))
		rt.skills.initialize()
		// Activate any default-active skills configured by the user.
		for _, name := range rt.cfg.Agent.Skills.DefaultActive {
			_, _ = rt.skills.activateManual(name)
		}
		rt.warnings = append(rt.warnings, rt.skills.catalogWarnings()...)
		rt.registerSkillCommandTools()
	}

	rt.state = rt.resolveToolState(trace, selection)
	rt.applyActiveSkillAutoreveals()
	return rt, nil
}

// parseStoredSkillState extracts persisted skill state from a stored trace.
// Skills aren't stored in OpenNalvin's StoredTrace today; this returns the
// zero value so the manager hydrates from the live catalog.
func parseStoredSkillState(_ StoredTrace) storedSkillState {
	return storedSkillState{}
}

// applyActiveSkillAutoreveals reveals each active skill's autoreveal_tools
// when the tool exists, is enabled, is not already pinned, and has not yet
// been revealed. Mirrors the gate from the source project.
func (rt *agentRuntime) applyActiveSkillAutoreveals() {
	if rt.skills == nil {
		return
	}
	for _, active := range rt.skills.activeMetadata() {
		ids := active.AutorevealTools
		if len(ids) == 0 {
			if meta, ok := rt.lookupSkillMetadata(active.Name); ok {
				ids = append(ids, meta.AutorevealTools...)
				ids = append(ids, skillCommandToolIDs(meta)...)
			}
		}
		for _, id := range ids {
			if _, ok := rt.tools[id]; !ok {
				continue
			}
			if _, enabled := rt.state.enabled[id]; !enabled {
				continue
			}
			if _, pinned := rt.state.pinned[id]; pinned {
				continue
			}
			if _, revealed := rt.state.revealed[id]; revealed {
				continue
			}
			rt.state.revealed[id] = struct{}{}
		}
	}
}

func (rt *agentRuntime) resolveToolState(trace StoredTrace, selection ToolSelection) resolvedToolState {
	enabled := make(map[string]struct{})
	pinned := make(map[string]struct{})
	revealed := make(map[string]struct{})

	for id, tool := range rt.tools {
		if tool.defaultEnabled {
			enabled[id] = struct{}{}
		}
		if tool.defaultPinned {
			pinned[id] = struct{}{}
		}
	}
	for _, id := range rt.cfg.Agent.Tools.DefaultEnabled {
		if _, ok := rt.tools[id]; ok {
			enabled[id] = struct{}{}
		}
	}
	for _, id := range rt.cfg.Agent.Tools.DefaultDisabled {
		delete(enabled, id)
		delete(pinned, id)
	}
	for _, id := range rt.cfg.Agent.Tools.DefaultPinned {
		if _, ok := rt.tools[id]; ok {
			pinned[id] = struct{}{}
		}
	}

	if trace.Metadata.Tools.EnabledIDs != nil {
		enabled = toSet(trace.Metadata.Tools.EnabledIDs, rt.tools)
	}
	if trace.Metadata.Tools.PinnedIDs != nil {
		pinned = toSet(trace.Metadata.Tools.PinnedIDs, rt.tools)
	}
	if trace.Metadata.Tools.RevealedIDs != nil {
		revealed = toSet(trace.Metadata.Tools.RevealedIDs, rt.tools)
	}

	if selection.EnabledToolIDs != nil {
		enabled = toSet(selection.EnabledToolIDs, rt.tools)
	}
	if selection.PinnedToolIDs != nil {
		pinned = toSet(selection.PinnedToolIDs, rt.tools)
	}

	mergeIntoSet(enabled, selection.EnableToolIDs, rt.tools)
	removeFromSet(enabled, selection.DisableToolIDs)
	mergeIntoSet(pinned, selection.PinToolIDs, rt.tools)
	removeFromSet(pinned, selection.UnpinToolIDs)

	if rt.mode == RunModePlan {
		planPinned := []string{"view", "write", "edit", "multiedit", "plan_exit"}
		mergeIntoSet(enabled, planPinned, rt.tools)
		mergeIntoSet(pinned, planPinned, rt.tools)
	}

	for id := range pinned {
		if _, ok := enabled[id]; !ok {
			delete(pinned, id)
		}
	}
	for id := range revealed {
		if _, ok := enabled[id]; !ok {
			delete(revealed, id)
			continue
		}
		if _, ok := pinned[id]; ok {
			delete(revealed, id)
		}
	}

	if rt.hasHiddenEnabledTools(enabled, pinned, revealed) {
		if _, ok := rt.tools["search_tools"]; ok {
			enabled["search_tools"] = struct{}{}
			pinned["search_tools"] = struct{}{}
		}
	}

	for id := range pinned {
		if _, ok := enabled[id]; !ok {
			delete(pinned, id)
		}
	}

	return resolvedToolState{
		enabled:  enabled,
		pinned:   pinned,
		revealed: revealed,
	}
}

func (rt *agentRuntime) hasHiddenEnabledTools(enabled, pinned, revealed map[string]struct{}) bool {
	for id := range enabled {
		if id == "search_tools" {
			continue
		}
		if _, ok := pinned[id]; ok {
			continue
		}
		if _, ok := revealed[id]; ok {
			continue
		}
		return true
	}
	return false
}

func (rt *agentRuntime) catalogResult() ToolCatalogResult {
	tools := make([]ToolDescriptor, 0, len(rt.tools))
	for _, id := range rt.sortedIDs() {
		tool := rt.tools[id]
		info := tool.tool.Info()
		_, enabled := rt.state.enabled[id]
		_, pinned := rt.state.pinned[id]
		_, revealed := rt.state.revealed[id]

		tools = append(tools, ToolDescriptor{
			ID:             id,
			Description:    info.Description,
			Keywords:       append([]string(nil), tool.keywords...),
			Source:         tool.source,
			ServerName:     tool.serverName,
			Enabled:        enabled,
			Pinned:         pinned,
			Visible:        enabled && (pinned || revealed),
			DefaultEnabled: tool.defaultEnabled,
			DefaultPinned:  tool.defaultPinned,
			Schema: ToolSchema{
				Type:       "object",
				Properties: cloneMap(info.Parameters),
				Required:   append([]string(nil), info.Required...),
			},
		})
	}
	return ToolCatalogResult{
		Tools:    tools,
		Warnings: append([]string(nil), rt.warnings...),
	}
}

func (rt *agentRuntime) prepareStep(ctx context.Context, options fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
	messages := append([]fantasy.Message(nil), options.Messages...)
	notifications := rt.session.consumeNotifications()
	for _, notification := range notifications {
		messages = append(messages, fantasy.NewUserMessage("Sub-agent notification: "+notification))
	}

	// Proactive compaction: check if we need to compact before this step.
	var systemOverride *string
	if rt.compaction != nil && rt.compactionTrace != nil {
		storedMsgs := rt.compactionTrace.Messages
		if rt.compaction.needsCompaction(storedMsgs) {
			record, err := rt.compaction.compact(ctx, storedMsgs, false)
			if err != nil {
				rt.session.debugf("proactive compaction failed: %v", err)
			} else {
				rt.compactionTrace.Compactions = append(rt.compactionTrace.Compactions, *record)
			}
		}
		if rebuilt, sys := rt.compaction.applyToStep(storedMsgs); sys != nil {
			messages = rebuilt
			systemOverride = sys
		}
	}

	active := rt.visibleToolIDs()
	rt.session.debugf(
		"prepare step=%d active_tools=%v enabled=%v pinned=%v revealed=%v notifications=%d",
		options.StepNumber,
		active,
		rt.enabledToolIDs(),
		rt.pinnedToolIDs(),
		rt.revealedToolIDs(),
		len(notifications),
	)
	prepared := fantasy.PrepareStepResult{
		Messages: messages,
		System:   systemOverride,
	}
	if len(active) == 0 {
		prepared.DisableAllTools = true
	} else {
		prepared.ActiveTools = active
	}
	return ctx, prepared, nil
}

func (rt *agentRuntime) activeTools() []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, 0, len(rt.tools))
	for _, id := range rt.sortedIDs() {
		out = append(out, rt.wrapToolForAgent(rt.tools[id]))
	}
	return out
}

func (rt *agentRuntime) visibleToolIDs() []string {
	visible := make([]string, 0, len(rt.state.enabled))
	for _, id := range rt.sortedIDs() {
		if _, ok := rt.state.enabled[id]; !ok {
			continue
		}
		if _, ok := rt.state.pinned[id]; ok {
			visible = append(visible, id)
			continue
		}
		if _, ok := rt.state.revealed[id]; ok {
			visible = append(visible, id)
		}
	}
	return visible
}

func (rt *agentRuntime) toolState() StoredToolState {
	return StoredToolState{
		EnabledIDs:  rt.enabledToolIDs(),
		PinnedIDs:   rt.pinnedToolIDs(),
		RevealedIDs: rt.revealedToolIDs(),
	}
}

func (rt *agentRuntime) close() error {
	if rt == nil {
		return nil
	}
	rt.teardownSkillContainers(context.Background())
	if rt.mcpManager == nil {
		return nil
	}
	return rt.mcpManager.Close()
}

func (rt *agentRuntime) removeRestrictedSubAgentTools() {
	for _, id := range childRunRestrictedToolIDs {
		delete(rt.tools, id)
	}
}

func (rt *agentRuntime) ensureSubAgentControlAllowed() error {
	if rt != nil && rt.isChild {
		return fmt.Errorf("child runs cannot spawn or manage sub-agents")
	}
	return nil
}

func (rt *agentRuntime) ensureRootOnlyTodoAllowed() error {
	if rt != nil && rt.isChild {
		return fmt.Errorf("child runs cannot use root-only todo tools")
	}
	return nil
}

func (rt *agentRuntime) enabledToolIDs() []string {
	return setToSortedSlice(rt.state.enabled)
}

func (rt *agentRuntime) pinnedToolIDs() []string {
	return setToSortedSlice(rt.state.pinned)
}

func (rt *agentRuntime) revealedToolIDs() []string {
	return setToSortedSlice(rt.state.revealed)
}

func (rt *agentRuntime) revealTools(ids ...string) []string {
	revealed := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := rt.state.enabled[id]; !ok {
			continue
		}
		if _, ok := rt.state.pinned[id]; ok {
			continue
		}
		if _, ok := rt.tools[id]; !ok {
			continue
		}
		if _, ok := rt.state.revealed[id]; ok {
			continue
		}
		rt.state.revealed[id] = struct{}{}
		revealed = append(revealed, id)
	}
	sort.Strings(revealed)
	if len(revealed) > 0 {
		rt.session.debugf("revealed tools=%v", revealed)
	}
	return revealed
}

func (rt *agentRuntime) todoState() []StoredTodoItem {
	return append([]StoredTodoItem{}, rt.todos...)
}

func (rt *agentRuntime) replaceTodos(items []StoredTodoItem) {
	rt.todos = append([]StoredTodoItem{}, items...)
}

func (rt *agentRuntime) sortedIDs() []string {
	ids := make([]string, 0, len(rt.tools))
	for id := range rt.tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func toSet(ids []string, catalog map[string]runtimeTool) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if catalog != nil {
			if _, ok := catalog[id]; !ok {
				continue
			}
		}
		out[id] = struct{}{}
	}
	return out
}

func mergeIntoSet(target map[string]struct{}, ids []string, catalog map[string]runtimeTool) {
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if catalog != nil {
			if _, ok := catalog[id]; !ok {
				continue
			}
		}
		target[id] = struct{}{}
	}
}

func removeFromSet(target map[string]struct{}, ids []string) {
	for _, id := range ids {
		delete(target, strings.TrimSpace(id))
	}
}

func setToSortedSlice(items map[string]struct{}) []string {
	out := make([]string, 0, len(items))
	for id := range items {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func marshalToolInput(input any) (string, error) {
	switch value := input.(type) {
	case nil:
		return "{}", nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "{}", nil
		}
		if !json.Valid([]byte(trimmed)) {
			return "", fmt.Errorf("tool input must be valid JSON")
		}
		return trimmed, nil
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("marshal tool input: %w", err)
		}
		return string(payload), nil
	}
}

func formatToolResponse(resp fantasy.ToolResponse) string {
	if strings.TrimSpace(resp.Metadata) == "" {
		return resp.Content
	}
	return resp.Content + "\n\nmetadata:\n" + resp.Metadata
}

func (rt *agentRuntime) setProviderConfig(cfg ProviderConfig) {
	if strings.TrimSpace(cfg.Model) != "" {
		rt.model = strings.TrimSpace(cfg.Model)
	}
	if cfg.ToolOutputTokenLimit > 0 {
		rt.toolOutputTokenLimit = cfg.ToolOutputTokenLimit
	}
}
