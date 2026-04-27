package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type searchToolsResponse struct {
	Query           string           `json:"query"`
	RevealedToolIDs []string         `json:"revealed_tool_ids"`
	Tools           []ToolDescriptor `json:"tools"`
}

func decodeSubAgentStatusResponse(t *testing.T, resp fantasy.ToolResponse) SubAgentStatus {
	t.Helper()

	var payload SubAgentStatus
	if err := json.Unmarshal([]byte(resp.Content), &payload); err != nil {
		t.Fatalf("parse sub-agent response: %v", err)
	}
	return payload
}

func decodeSearchToolsResponse(t *testing.T, resp fantasy.ToolResponse) searchToolsResponse {
	t.Helper()

	var payload searchToolsResponse
	if err := json.Unmarshal([]byte(resp.Content), &payload); err != nil {
		t.Fatalf("parse search response: %v", err)
	}
	return payload
}

func decodeJSONToolResponse[T any](t *testing.T, resp fantasy.ToolResponse) T {
	t.Helper()

	if resp.IsError {
		t.Fatalf("expected successful tool response, got error: %s", resp.Content)
	}

	var payload T
	if err := json.Unmarshal([]byte(resp.Content), &payload); err != nil {
		t.Fatalf("parse tool response: %v", err)
	}
	return payload
}

func boolPtr(value bool) *bool {
	return &value
}

func requireTool(t *testing.T, tools []ToolDescriptor, id string) ToolDescriptor {
	t.Helper()

	for _, tool := range tools {
		if tool.ID == id {
			return tool
		}
	}
	t.Fatalf("tool %q not found in %#v", id, tools)
	return ToolDescriptor{}
}

func TestResolveToolStateMergesDefaultsAndOverrides(t *testing.T) {
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		Agent: configpkg.AgentConfig{
			Tools: configpkg.AgentToolsConfig{
				DefaultEnabled:  []string{"kb_create_node"},
				DefaultDisabled: []string{"web_fetch_get"},
				DefaultPinned:   []string{"kb_get_node"},
			},
		},
	})

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnableToolIDs:  []string{"web_fetch_get"},
		DisableToolIDs: []string{"kb_create_node"},
		PinToolIDs:     []string{"web_fetch_get"},
		UnpinToolIDs:   []string{"spawn_agent"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	if _, ok := rt.state.enabled["web_fetch_get"]; !ok {
		t.Fatalf("expected web_fetch_get to be enabled")
	}
	if _, ok := rt.state.enabled["kb_create_node"]; ok {
		t.Fatalf("expected kb_create_node to stay disabled after explicit override")
	}
	if _, ok := rt.state.pinned["web_fetch_get"]; !ok {
		t.Fatalf("expected web_fetch_get to be pinned")
	}
	if _, ok := rt.state.pinned["kb_get_node"]; !ok {
		t.Fatalf("expected kb_get_node to remain pinned from config defaults")
	}
	if _, ok := rt.state.pinned["spawn_agent"]; ok {
		t.Fatalf("expected spawn_agent to be unpinned by override")
	}
}

func TestChildRunsOmitSubAgentControlTools(t *testing.T) {
	ctx := context.Background()
	trace := StoredTrace{
		RunID: "child_run",
		Metadata: StoredRunMeta{
			ParentRunID: "parent_run",
			RootRunID:   "parent_run",
			TaskName:    "child_task",
			RunKind:     RunKindChild,
		},
	}

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "child_run"), trace, ToolSelection{
		EnabledToolIDs: append([]string{"search_tools"}, childRunRestrictedToolIDs...),
		PinnedToolIDs:  append([]string{"search_tools"}, childRunRestrictedToolIDs...),
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	for _, id := range childRunRestrictedToolIDs {
		if _, ok := rt.tools[id]; ok {
			t.Fatalf("expected %s to be unavailable for child runs", id)
		}
	}
	for _, tool := range rt.catalogResult().Tools {
		if slices.Contains(childRunRestrictedToolIDs, tool.ID) {
			t.Fatalf("expected %s to be omitted from child catalog", tool.ID)
		}
	}
	if _, ok := rt.tools["search_tools"]; !ok {
		t.Fatalf("expected non-subagent tools to remain available")
	}
}

func TestChildRunsRejectDirectSpawnAgentInvocation(t *testing.T) {
	ctx := context.Background()
	trace := StoredTrace{
		RunID: "child_run",
		Metadata: StoredRunMeta{
			ParentRunID: "parent_run",
			RootRunID:   "parent_run",
			TaskName:    "child_task",
			RunKind:     RunKindChild,
		},
	}

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "child_run"), trace, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.spawnAgent(ctx, spawnAgentInput{Message: "hello"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("spawn agent: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected child spawn_agent invocation to fail")
	}
	if resp.Content != "child runs cannot spawn or manage sub-agents" {
		t.Fatalf("unexpected child spawn_agent error: %q", resp.Content)
	}
}

func TestRootRunsKeepSubAgentControlTools(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "root_run"), StoredTrace{RunID: "root_run"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	for _, id := range childRunRestrictedToolIDs {
		if _, ok := rt.tools[id]; !ok {
			t.Fatalf("expected %s to remain available for root runs", id)
		}
	}
}

func TestRootRunsPinTodoToolsByDefault(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "root_run"), StoredTrace{RunID: "root_run"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	todoWrite := requireTool(t, rt.catalogResult().Tools, "todowrite")
	if !todoWrite.Enabled || !todoWrite.Pinned || !todoWrite.Visible {
		t.Fatalf("expected todowrite to be enabled, pinned, and visible by default: %#v", todoWrite)
	}
	if !todoWrite.DefaultEnabled || !todoWrite.DefaultPinned {
		t.Fatalf("expected todowrite defaults to be enabled and pinned: %#v", todoWrite)
	}

	todoRead := requireTool(t, rt.catalogResult().Tools, "todoread")
	if !todoRead.Enabled || !todoRead.Pinned || !todoRead.Visible {
		t.Fatalf("expected todoread to be enabled, pinned, and visible by default: %#v", todoRead)
	}
	if !todoRead.DefaultEnabled || !todoRead.DefaultPinned {
		t.Fatalf("expected todoread defaults to be enabled and pinned: %#v", todoRead)
	}
}

func TestPlanRunsExposePlanExitTool(t *testing.T) {
	ctx := context.Background()
	trace := StoredTrace{
		RunID: "plan_run",
		Metadata: StoredRunMeta{
			RunKind: RunKindRoot,
			Mode:    RunModePlan,
			PlanRef: StoredPlanRef{Path: ".plans/example.md", SourceRunID: "plan_run", Status: PlanStatusActive},
		},
	}

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "plan_run"), trace, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	tool := requireTool(t, rt.catalogResult().Tools, "plan_exit")
	if !tool.Enabled || !tool.Pinned || !tool.Visible {
		t.Fatalf("expected plan_exit to be enabled, pinned, and visible: %#v", tool)
	}
}

func TestPlanExitMarksPlanReady(t *testing.T) {
	ctx := context.Background()
	trace := StoredTrace{
		RunID: "plan_run",
		Metadata: StoredRunMeta{
			RunKind: RunKindRoot,
			Mode:    RunModePlan,
			PlanRef: StoredPlanRef{Path: ".plans/example.md", SourceRunID: "plan_run", Status: PlanStatusActive},
		},
	}

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "plan_run"), trace, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.planExit(ctx, planExitInput{}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("plan exit: %v", err)
	}
	ref := decodeJSONToolResponse[StoredPlanRef](t, resp)
	if ref.Status != PlanStatusReady {
		t.Fatalf("expected ready status, got %#v", ref)
	}
	metadata := parseToolResultClientMetadata(resp.Metadata)
	if metadata.PlanRef == nil || metadata.PlanRef.Status != PlanStatusReady {
		t.Fatalf("expected plan ref metadata, got %#v", metadata)
	}
}

func TestPlanModePinsCanonicalPlanEditingTools(t *testing.T) {
	ctx := context.Background()
	trace := StoredTrace{
		RunID: "plan_run",
		Metadata: StoredRunMeta{
			RunKind: RunKindRoot,
			Mode:    RunModePlan,
			PlanRef: StoredPlanRef{Path: ".plans/example.md", SourceRunID: "plan_run", Status: PlanStatusActive},
		},
	}

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "plan_run"), trace, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	for _, id := range []string{"view", "write", "edit", "multiedit", "plan_exit"} {
		tool := requireTool(t, rt.catalogResult().Tools, id)
		if !tool.Enabled || !tool.Pinned || !tool.Visible {
			t.Fatalf("expected %s to be enabled, pinned, and visible in plan mode: %#v", id, tool)
		}
	}
}

func TestChildRunsRejectDirectTodoInvocation(t *testing.T) {
	ctx := context.Background()
	trace := StoredTrace{
		RunID: "child_run",
		Metadata: StoredRunMeta{
			ParentRunID: "parent_run",
			RootRunID:   "parent_run",
			TaskName:    "child_task",
			RunKind:     RunKindChild,
		},
	}

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "child_run"), trace, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	writeResp, err := rt.todoWrite(ctx, todoWriteInput{Items: []todoItemInput{{Content: "Task", Status: "pending"}}}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo write: %v", err)
	}
	if !writeResp.IsError {
		t.Fatalf("expected child todowrite invocation to fail")
	}
	if writeResp.Content != "child runs cannot use root-only todo tools" {
		t.Fatalf("unexpected child todowrite error: %q", writeResp.Content)
	}

	readResp, err := rt.todoRead(ctx, struct{}{}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo read: %v", err)
	}
	if !readResp.IsError {
		t.Fatalf("expected child todoread invocation to fail")
	}
	if readResp.Content != "child runs cannot use root-only todo tools" {
		t.Fatalf("unexpected child todoread error: %q", readResp.Content)
	}
}

func TestTodoReadReturnsEmptyListForNewRun(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{RunID: "run_test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.todoRead(ctx, struct{}{}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo read: %v", err)
	}
	summary := decodeJSONToolResponse[todoSummary](t, resp)
	if len(summary.Items) != 0 || summary.Total != 0 || summary.Pending != 0 || summary.InProgress != 0 || summary.Completed != 0 {
		t.Fatalf("expected empty todo summary, got %#v", summary)
	}
}

func TestTodoWriteReplacesListAndReturnsSummary(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{RunID: "run_test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.todoWrite(ctx, todoWriteInput{
		Items: []todoItemInput{
			{Content: "  Research APIs  ", Status: "pending"},
			{Content: "Implement handler", Status: "in_progress"},
			{Content: "Write tests", Status: "completed"},
		},
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo write: %v", err)
	}
	summary := decodeJSONToolResponse[todoSummary](t, resp)
	if summary.Total != 3 || summary.Pending != 1 || summary.InProgress != 1 || summary.Completed != 1 {
		t.Fatalf("unexpected todo counts: %#v", summary)
	}
	if !slices.Equal(summary.Items, []StoredTodoItem{
		{Content: "Research APIs", Status: "pending"},
		{Content: "Implement handler", Status: "in_progress"},
		{Content: "Write tests", Status: "completed"},
	}) {
		t.Fatalf("unexpected normalized todo items: %#v", summary.Items)
	}

	replaceResp, err := rt.todoWrite(ctx, todoWriteInput{
		Items: []todoItemInput{
			{Content: "Ship it", Status: "in_progress"},
		},
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo write replace: %v", err)
	}
	replaced := decodeJSONToolResponse[todoSummary](t, replaceResp)
	if replaced.Total != 1 || len(replaced.Items) != 1 || replaced.Items[0].Content != "Ship it" {
		t.Fatalf("expected todo list replacement, got %#v", replaced)
	}
}

func TestTodoWriteRejectsInvalidItems(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{RunID: "run_test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	cases := []struct {
		name  string
		input todoWriteInput
		want  string
	}{
		{
			name:  "blank content",
			input: todoWriteInput{Items: []todoItemInput{{Content: "   ", Status: "pending"}}},
			want:  "todo item content is required",
		},
		{
			name:  "invalid status",
			input: todoWriteInput{Items: []todoItemInput{{Content: "Task", Status: "doing"}}},
			want:  `todo item status "doing" is invalid`,
		},
		{
			name: "duplicate content",
			input: todoWriteInput{Items: []todoItemInput{
				{Content: "Task", Status: "pending"},
				{Content: " Task ", Status: "completed"},
			}},
			want: `todo item content "Task" is duplicated`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := rt.todoWrite(ctx, tc.input, fantasy.ToolCall{})
			if err != nil {
				t.Fatalf("todo write: %v", err)
			}
			if !resp.IsError {
				t.Fatalf("expected todowrite to fail for %s", tc.name)
			}
			if resp.Content != tc.want {
				t.Fatalf("unexpected error: got %q want %q", resp.Content, tc.want)
			}
		})
	}
}

func TestTodoStatePersistsAcrossTraceSnapshotsAndResumedRuns(t *testing.T) {
	ctx := context.Background()
	runID := "run_test"
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, runID), StoredTrace{RunID: runID, Model: "gpt-test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.todoWrite(ctx, todoWriteInput{
		Items: []todoItemInput{
			{Content: "Research", Status: "completed"},
			{Content: "Implement", Status: "in_progress"},
		},
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo write: %v", err)
	}
	decodeJSONToolResponse[todoSummary](t, resp)

	builder := newTraceBuilder(StoredTrace{RunID: runID, Model: "gpt-test"})
	builder.trace.Todos = rt.todoState()
	snapshot, raw, err := builder.Snapshot(time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot trace: %v", err)
	}
	if !slices.Equal(snapshot.Todos, []StoredTodoItem{
		{Content: "Research", Status: "completed"},
		{Content: "Implement", Status: "in_progress"},
	}) {
		t.Fatalf("unexpected snapshot todos: %#v", snapshot.Todos)
	}

	parsed, err := parseStoredTrace(raw)
	if err != nil {
		t.Fatalf("parse stored trace: %v", err)
	}
	resumed, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, runID), parsed, ToolSelection{})
	if err != nil {
		t.Fatalf("new resumed runtime: %v", err)
	}

	readResp, err := resumed.todoRead(ctx, struct{}{}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("todo read: %v", err)
	}
	summary := decodeJSONToolResponse[todoSummary](t, readResp)
	if !slices.Equal(summary.Items, snapshot.Todos) {
		t.Fatalf("expected resumed todo state to match snapshot, got %#v want %#v", summary.Items, snapshot.Todos)
	}
}

func TestSearchToolsRevealsHiddenEnabledTools(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	visible := rt.visibleToolIDs()
	if len(visible) != 1 || visible[0] != "search_tools" {
		t.Fatalf("expected only search_tools to be initially visible, got %v", visible)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "fetch"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}
	if resp.IsError {
		t.Fatalf("expected successful search response, got error: %s", resp.Content)
	}

	visible = rt.visibleToolIDs()
	if len(visible) != 2 {
		t.Fatalf("expected revealed tool to become visible, got %v", visible)
	}
	if _, ok := rt.state.revealed["web_fetch_get"]; !ok {
		t.Fatalf("expected web_fetch_get to be revealed")
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.RevealedToolIDs) != 1 || payload.RevealedToolIDs[0] != "web_fetch_get" {
		t.Fatalf("unexpected revealed tool ids payload: %#v", payload.RevealedToolIDs)
	}
}

func TestSearchToolsBroadWebQueryUsesFTSAndFiltersNoise(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get", "kb_get_node", "kb_get_edge"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "web fetch url"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}
	if resp.IsError {
		t.Fatalf("expected successful search response, got error: %s", resp.Content)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) != 1 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected only web_fetch_get for broad web query, got %#v", payload.Tools)
	}
}

func TestSearchToolsDescriptionDrivenQueryFindsWebFetchTool(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get", "kb_get_node"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "simple request"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) == 0 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected description-driven query to rank web_fetch_get first, got %#v", payload.Tools)
	}
}

func TestSearchToolsKeywordQueryFindsWebFetchTool(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get", "kb_get_node"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "download url"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) == 0 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected keyword query to rank web_fetch_get first, got %#v", payload.Tools)
	}
}

func TestSearchToolsReaderModeKeywordQueryFindsWebFetchTool(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get", "kb_get_node"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "reader mode"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) == 0 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected reader mode query to rank web_fetch_get first, got %#v", payload.Tools)
	}
}

func TestSearchToolsBroadWebSearchQueryPrefersWebSearchTool(t *testing.T) {
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		Agent: configpkg.AgentConfig{
			Tools: configpkg.AgentToolsConfig{
				Metadata: map[string]configpkg.ToolMetadataConfig{
					"mcp__exa__web_search_exa": {Keywords: []string{"web search", "internet search"}},
				},
			},
		},
	})

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	rt.tools["mcp__exa__web_search_exa"] = runtimeTool{
		id:         "mcp__exa__web_search_exa",
		source:     sourceMCP,
		serverName: "exa",
		keywords:   rt.mergedToolKeywords("mcp__exa__web_search_exa"),
		tool: fantasy.NewAgentTool(
			"mcp__exa__web_search_exa",
			"Search the web for relevant pages and return ranked results.",
			func(context.Context, map[string]any, fantasy.ToolCall) (fantasy.ToolResponse, error) {
				return jsonToolResponse(map[string]any{"ok": true})
			},
		),
	}
	rt.tools["mcp__exa__crawling_exa"] = runtimeTool{
		id:         "mcp__exa__crawling_exa",
		source:     sourceMCP,
		serverName: "exa",
		tool: fantasy.NewAgentTool(
			"mcp__exa__crawling_exa",
			"Read and extract content from a known web page URL.",
			func(context.Context, map[string]any, fantasy.ToolCall) (fantasy.ToolResponse, error) {
				return jsonToolResponse(map[string]any{"ok": true})
			},
		),
	}
	rt.state.enabled["mcp__exa__web_search_exa"] = struct{}{}
	rt.state.enabled["mcp__exa__crawling_exa"] = struct{}{}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "web search"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) != 1 || payload.Tools[0].ID != "mcp__exa__web_search_exa" {
		t.Fatalf("expected broad web search query to return only the web search tool, got %#v", payload.Tools)
	}
}

func TestSearchToolsBroadKnowledgeBaseQueryRanksNodeToolsFirst(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "kb_create_node", "kb_get_node", "kb_get_edge", "web_fetch_get"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "knowledge base note node create fetch"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) < 2 {
		t.Fatalf("expected at least two tools for broad KB query, got %#v", payload.Tools)
	}
	if got := []string{payload.Tools[0].ID, payload.Tools[1].ID}; !slices.Equal(got, []string{"kb_create_node", "kb_get_node"}) {
		t.Fatalf("expected node tools to rank first, got %v", got)
	}
	for _, tool := range payload.Tools {
		if tool.ID == "web_fetch_get" {
			t.Fatalf("did not expect one-token fallback noise to include web_fetch_get: %#v", payload.Tools)
		}
	}
}

func TestWorkspaceFileToolsEnabledByDefaultButNotPinned(t *testing.T) {
	ctx, _ := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	for _, id := range []string{"view", "ls", "glob", "grep", "write", "edit", "multiedit"} {
		tool := requireTool(t, rt.catalogResult().Tools, id)
		if !tool.Enabled {
			t.Fatalf("expected %s to be enabled by default", id)
		}
		if tool.Pinned {
			t.Fatalf("expected %s to remain unpinned by default", id)
		}
		if tool.Visible {
			t.Fatalf("expected %s to remain hidden until revealed", id)
		}
	}
}

func TestSearchToolsFileQueriesFindWorkspaceFileTools(t *testing.T) {
	ctx, _ := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "view", "glob", "grep", "edit", "multiedit"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "read file"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools for read file: %v", err)
	}
	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) == 0 || payload.Tools[0].ID != "view" {
		t.Fatalf("expected view to rank first for read file query, got %#v", payload.Tools)
	}

	resp, err = rt.searchTools(ctx, searchToolsInput{Query: "edit file"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools for edit file: %v", err)
	}
	payload = decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) == 0 || payload.Tools[0].ID != "edit" {
		t.Fatalf("expected edit to rank first for edit file query, got %#v", payload.Tools)
	}
}

func TestWorkspaceFileToolsReadWriteAndSearch(t *testing.T) {
	ctx, paths := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"view", "ls", "glob", "grep", "write", "edit", "multiedit"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	if resp, err := rt.writeFile(ctx, writeInput{Path: "notes/todo.txt", Content: "hello\nworld\n"}, fantasy.ToolCall{}); err != nil || resp.IsError {
		t.Fatalf("write file: err=%v resp=%+v", err, resp)
	}

	resp, err := rt.viewFile(ctx, viewInput{Path: "notes/todo.txt", Offset: 1, Limit: 1}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("view file: err=%v resp=%+v", err, resp)
	}
	if !strings.Contains(resp.Content, `"content": "world"`) {
		t.Fatalf("expected view output to include world, got %s", resp.Content)
	}

	resp, err = rt.editFile(ctx, editInput{Path: "notes/todo.txt", OldString: "world", NewString: "workspace"}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("edit file: err=%v resp=%+v", err, resp)
	}

	resp, err = rt.multiEditFile(ctx, multiEditInput{
		Path: "notes/todo.txt",
		Edits: []multiEditElement{
			{OldString: "hello", NewString: "hi"},
			{OldString: "workspace", NewString: "team"},
		},
	}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("multiedit file: err=%v resp=%+v", err, resp)
	}

	data, err := os.ReadFile(filepath.Join(paths.FilesPath, "notes", "todo.txt"))
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if string(data) != "hi\nteam\n" {
		t.Fatalf("unexpected workspace file content: %q", string(data))
	}

	resp, err = rt.listFiles(ctx, lsInput{Path: ".", Depth: 2}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("ls: err=%v resp=%+v", err, resp)
	}
	if !strings.Contains(resp.Content, `"path": "notes/todo.txt"`) {
		t.Fatalf("expected ls output to include notes/todo.txt, got %s", resp.Content)
	}

	resp, err = rt.globFiles(ctx, globInput{Pattern: "**/*.txt"}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("glob: err=%v resp=%+v", err, resp)
	}
	if !strings.Contains(resp.Content, `"notes/todo.txt"`) {
		t.Fatalf("expected glob output to include notes/todo.txt, got %s", resp.Content)
	}

	resp, err = rt.grepFiles(ctx, grepInput{Pattern: "team"}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("grep: err=%v resp=%+v", err, resp)
	}
	if !strings.Contains(resp.Content, `"preview": "team"`) {
		t.Fatalf("expected grep output to include matching preview, got %s", resp.Content)
	}

	resp, err = rt.grepFiles(ctx, grepInput{Path: "notes/todo.txt", Pattern: "team"}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("grep single file: err=%v resp=%+v", err, resp)
	}
	if !strings.Contains(resp.Content, `"path": "notes/todo.txt"`) {
		t.Fatalf("expected single-file grep output to include notes/todo.txt, got %s", resp.Content)
	}
}

func TestGetWorkspaceFileURLReturnsStableServeURL(t *testing.T) {
	ctx, paths := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"get_workspace_file_url"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	filePath := filepath.Join(paths.FilesPath, "downloads", "videos", "Ghosts S05E14.mp4")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir file dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resp, err := rt.getWorkspaceFileURL(ctx, fileURLInput{Path: "downloads/videos/Ghosts S05E14.mp4"}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("get workspace file url: err=%v resp=%+v", err, resp)
	}

	payload := decodeJSONToolResponse[fileURLResult](t, resp)
	if payload.Workspace != "alpha" || payload.Path != "downloads/videos/Ghosts S05E14.mp4" {
		t.Fatalf("unexpected payload metadata: %#v", payload)
	}
	if payload.BaseURL != "http://127.0.0.1:4210" {
		t.Fatalf("base url = %q", payload.BaseURL)
	}
	if payload.URL != "http://127.0.0.1:4210/files/alpha/downloads/videos/Ghosts%20S05E14.mp4" {
		t.Fatalf("url = %q", payload.URL)
	}
}

func TestGetAgentResultReturnsNoOutputBeforeAssistantReply(t *testing.T) {
	ctx, rt, manager, child, store := subAgentToolTestRuntime(t)

	if err := manager.persistQueuedChildRun(ctx, "default", "gpt-test", child, "fetch something"); err != nil {
		t.Fatalf("persist queued child run: %v", err)
	}
	child.status = AgentStatusQueued

	resp, err := rt.getAgentResult(ctx, getAgentResultInput{Target: child.taskName}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("get agent result: %v", err)
	}
	if resp.IsError {
		t.Fatalf("expected successful response, got error: %s", resp.Content)
	}

	payload := decodeSubAgentStatusResponse(t, resp)
	if payload.Status != AgentStatusQueued {
		t.Fatalf("expected queued status, got %q", payload.Status)
	}
	if payload.HasLatestAssistantOutput {
		t.Fatalf("expected no assistant output yet, got %#v", payload)
	}
	if payload.LatestAssistantOutput != "" {
		t.Fatalf("expected empty assistant output, got %q", payload.LatestAssistantOutput)
	}

	run, err := store.GetAgentRun(ctx, child.runID)
	if err != nil {
		t.Fatalf("get child run: %v", err)
	}
	if len(run.Trace) == 0 {
		t.Fatalf("expected queued child trace to exist")
	}
}

func TestWaitAgentAndGetAgentResultReturnLatestAssistantOutput(t *testing.T) {
	ctx, rt, manager, child, _ := subAgentToolTestRuntime(t)

	if err := manager.persistQueuedChildRun(ctx, "default", "gpt-test", child, "fetch something"); err != nil {
		t.Fatalf("persist queued child run: %v", err)
	}
	child.status = AgentStatusCompleted

	updateChildRunTrace(t, manager.store, child, []StoredMessage{
		{Role: "assistant", ToolCalls: []StoredToolCall{{ID: "call_1", Function: &StoredToolFunction{Name: "web_fetch_get", Arguments: `{"url":"https://example.com"}`}}}},
		{Role: "assistant", Content: "child ok"},
	})

	waitResp, err := rt.waitAgent(ctx, waitAgentInput{Target: child.taskName}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("wait agent: %v", err)
	}
	if waitResp.IsError {
		t.Fatalf("expected successful wait response, got error: %s", waitResp.Content)
	}
	waitPayload := decodeSubAgentStatusResponse(t, waitResp)
	if waitPayload.Status != AgentStatusCompleted {
		t.Fatalf("expected completed status, got %q", waitPayload.Status)
	}
	if !waitPayload.HasLatestAssistantOutput || waitPayload.LatestAssistantOutput != "child ok" {
		t.Fatalf("unexpected wait payload: %#v", waitPayload)
	}

	resultResp, err := rt.getAgentResult(ctx, getAgentResultInput{Target: child.runID}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("get agent result: %v", err)
	}
	if resultResp.IsError {
		t.Fatalf("expected successful get result response, got error: %s", resultResp.Content)
	}
	resultPayload := decodeSubAgentStatusResponse(t, resultResp)
	if !resultPayload.HasLatestAssistantOutput || resultPayload.LatestAssistantOutput != "child ok" {
		t.Fatalf("unexpected get result payload: %#v", resultPayload)
	}
}

func TestRepeatedSendInputUpdatesRetrievedLatestAssistantOutput(t *testing.T) {
	ctx, rt, manager, child, _ := subAgentToolTestRuntime(t)

	if err := manager.persistQueuedChildRun(ctx, "default", "gpt-test", child, "fetch something"); err != nil {
		t.Fatalf("persist queued child run: %v", err)
	}
	child.status = AgentStatusCompleted

	firstResp, err := rt.sendInput(ctx, sendInputInput{
		Target:  child.taskName,
		Message: "Summarize your findings.",
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("send input #1: %v", err)
	}
	if firstResp.IsError {
		t.Fatalf("expected successful send_input response, got error: %s", firstResp.Content)
	}

	updateChildRunTrace(t, manager.store, child, []StoredMessage{
		{Role: "assistant", Content: "first answer"},
	})

	resultResp, err := rt.getAgentResult(ctx, getAgentResultInput{Target: child.taskName}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("get agent result after first reply: %v", err)
	}
	resultPayload := decodeSubAgentStatusResponse(t, resultResp)
	if resultPayload.LatestAssistantOutput != "first answer" {
		t.Fatalf("expected first answer, got %#v", resultPayload)
	}

	secondResp, err := rt.sendInput(ctx, sendInputInput{
		Target:  child.taskName,
		Message: "Reply again with a shorter version.",
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("send input #2: %v", err)
	}
	if secondResp.IsError {
		t.Fatalf("expected successful second send_input response, got error: %s", secondResp.Content)
	}

	updateChildRunTrace(t, manager.store, child, []StoredMessage{
		{Role: "assistant", Content: "first answer"},
		{Role: "assistant", Content: "second answer"},
	})

	resultResp, err = rt.getAgentResult(ctx, getAgentResultInput{Target: child.taskName}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("get agent result after second reply: %v", err)
	}
	resultPayload = decodeSubAgentStatusResponse(t, resultResp)
	if resultPayload.LatestAssistantOutput != "second answer" {
		t.Fatalf("expected second answer, got %#v", resultPayload)
	}
}

func subAgentToolTestRuntime(t *testing.T) (context.Context, *agentRuntime, *subAgentManager, *subAgentHandle, *knowledge.Store) {
	t.Helper()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "subagent-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	store := knowledge.New(db)
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{})
	session := newEphemeralSessionState(ctx, store, "parent_run")
	session.setMetadata("", "parent_run", "parent_task", RunKindRoot)

	rt, err := newAgentRuntime(ctx, store, session, StoredTrace{RunID: "parent_run", Model: "gpt-test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	manager := newSubAgentManager(session, store, configpkg.Config{})
	session.subagents = manager

	child := &subAgentHandle{
		manager:      manager,
		session:      newEphemeralSessionState(ctx, store, "child_run"),
		runID:        "child_run",
		taskName:     "smoke_child",
		parentRunID:  "parent_run",
		rootRunID:    "parent_run",
		initialTitle: "Child title",
		status:       AgentStatusQueued,
		wake:         make(chan struct{}, 1),
	}
	child.session.setMetadata(child.parentRunID, child.rootRunID, child.taskName, RunKindChild)
	child.session.setProviderName("default")
	manager.children[child.runID] = child

	return ctx, rt, manager, child, store
}

func updateChildRunTrace(t *testing.T, store *knowledge.Store, child *subAgentHandle, messages []StoredMessage) {
	t.Helper()

	now := time.Now().UTC()
	trace := StoredTrace{
		SchemaVersion: 2,
		RunID:         child.runID,
		Title:         child.initialTitle,
		Model:         "gpt-test",
		Provider:      "default",
		Metadata: StoredRunMeta{
			ParentRunID: child.parentRunID,
			RootRunID:   child.rootRunID,
			TaskName:    child.taskName,
			RunKind:     RunKindChild,
			Tools: StoredToolState{
				EnabledIDs:  []string{},
				PinnedIDs:   []string{},
				RevealedIDs: []string{},
			},
		},
		Messages:  messages,
		CreatedAt: now,
		UpdatedAt: now,
	}
	rawTrace, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	if err := store.UpdateAgentRun(context.Background(), knowledge.UpdateAgentRunInput{
		ID:           child.runID,
		Title:        child.initialTitle,
		Model:        "gpt-test",
		Provider:     "default",
		ParentRunID:  child.parentRunID,
		RootRunID:    child.rootRunID,
		TaskName:     child.taskName,
		RunKind:      RunKindChild,
		Status:       child.status,
		MessageCount: len(messages),
		Trace:        rawTrace,
	}); err != nil {
		t.Fatalf("update child run: %v", err)
	}
}

func TestWorkspaceFileToolsRejectEscapesBinaryAndSymlinks(t *testing.T) {
	ctx, paths := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"view", "edit", "multiedit"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.viewFile(ctx, viewInput{Path: "../secret.txt"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("view escape: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected escape path to fail")
	}

	if err := os.WriteFile(filepath.Join(paths.FilesPath, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}
	resp, err = rt.viewFile(ctx, viewInput{Path: "binary.bin"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("view binary: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected binary file to fail")
	}

	if err := os.WriteFile(filepath.Join(paths.FilesPath, "target.txt"), []byte("real"), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.Symlink(filepath.Join(paths.FilesPath, "target.txt"), filepath.Join(paths.FilesPath, "link.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	resp, err = rt.viewFile(ctx, viewInput{Path: "link.txt"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("view symlink: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected symlink path to fail")
	}

	if err := os.WriteFile(filepath.Join(paths.FilesPath, "multi.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write multi file: %v", err)
	}
	resp, err = rt.multiEditFile(ctx, multiEditInput{
		Path: "multi.txt",
		Edits: []multiEditElement{
			{OldString: "alpha", NewString: "one"},
			{OldString: "missing", NewString: "two"},
		},
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("multiedit failure path: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected multiedit failure to return an error response")
	}

	data, err := os.ReadFile(filepath.Join(paths.FilesPath, "multi.txt"))
	if err != nil {
		t.Fatalf("read multi file after failed multiedit: %v", err)
	}
	if string(data) != "alpha\nbeta\n" {
		t.Fatalf("expected failed multiedit to leave file unchanged, got %q", string(data))
	}
}

func TestWebFetchGetCanSaveToWorkspaceFile(t *testing.T) {
	ctx, paths := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	largeBody := strings.Repeat("alpha beta gamma delta\n", 3000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(largeBody))
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{
		URL:            server.URL,
		SaveToFilepath: "downloads/large.txt",
	}, fantasy.ToolCall{})
	if err != nil || resp.IsError {
		t.Fatalf("web fetch get: err=%v resp=%+v", err, resp)
	}
	if !strings.Contains(resp.Content, `"saved_to_filepath": "downloads/large.txt"`) {
		t.Fatalf("expected response to report saved_to_filepath, got %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"truncated": true`) {
		t.Fatalf("expected response preview to be truncated for large body, got %s", resp.Content)
	}

	savedBytes, err := os.ReadFile(filepath.Join(paths.FilesPath, "downloads", "large.txt"))
	if err != nil {
		t.Fatalf("read saved download: %v", err)
	}
	if string(savedBytes) != largeBody {
		t.Fatalf("expected saved file to contain full body, got %d bytes want %d", len(savedBytes), len(largeBody))
	}
}

func TestWebFetchGetHTMLReaderModeReturnsReadableContent(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	htmlBody := `<!doctype html><html><head><title>Reader Mode Story</title></head><body><article><h1>Reader Mode Story</h1><p>` +
		strings.Repeat("Reader mode should keep the important paragraph text visible. ", 40) +
		`</p><p>Second paragraph keeps the article readable without raw HTML noise.</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{URL: server.URL}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}

	payload := decodeJSONToolResponse[webFetchResponsePayload](t, resp)
	if payload.Mode != "reader" {
		t.Fatalf("expected reader mode, got %#v", payload.Mode)
	}
	if payload.Title != "Reader Mode Story" {
		t.Fatalf("expected extracted title, got %#v", payload.Title)
	}
	if !strings.Contains(payload.Body, "Reader mode should keep the important paragraph text visible.") {
		t.Fatalf("expected extracted body text, got %q", payload.Body)
	}
	if strings.Contains(payload.Body, "<article") {
		t.Fatalf("expected extracted reader text, got raw HTML: %q", payload.Body)
	}

	rawPayload := decodeJSONToolResponse[map[string]any](t, resp)
	if _, ok := rawPayload["status_code"]; ok {
		t.Fatalf("did not expect status_code in web_fetch_get payload: %#v", rawPayload)
	}
	if _, ok := rawPayload["status"]; ok {
		t.Fatalf("did not expect status in web_fetch_get payload: %#v", rawPayload)
	}
	if _, ok := rawPayload["headers"]; ok {
		t.Fatalf("did not expect headers in web_fetch_get payload: %#v", rawPayload)
	}
	if _, ok := rawPayload["content_type"]; ok {
		t.Fatalf("did not expect content_type in web_fetch_get payload: %#v", rawPayload)
	}
}

func TestWebFetchGetHTMLReaderModeClipsBodyToTokenLimit(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{Model: "gpt-test"}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.setProviderConfig(ProviderConfig{Model: "gpt-test"})

	htmlBody := `<!doctype html><html><head><title>Clip Story</title></head><body><article><h1>Clip Story</h1><p>` +
		strings.Repeat("This is a deliberately long reader mode paragraph for token clipping. ", 5000) +
		`</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{URL: server.URL}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}

	payload := decodeJSONToolResponse[webFetchResponsePayload](t, resp)
	if payload.Mode != "reader" {
		t.Fatalf("expected reader mode, got %#v", payload.Mode)
	}
	if !payload.Truncated {
		t.Fatalf("expected reader-mode body to be token-truncated")
	}
	if got := newTokenEstimator("gpt-test").Count(payload.Body); got > webFetchBodyTokenLimit {
		t.Fatalf("expected body to be capped at %d tokens, got %d", webFetchBodyTokenLimit, got)
	}
}

func TestWebFetchGetRawHTMLModeSupportsSelector(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	htmlBody := `<!doctype html><html><body><article><p class="story">First selected paragraph.</p><div>Ignore this content.</div><p class="story">Second selected paragraph.</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{
		URL:                   server.URL,
		ExtractArticleContent: boolPtr(false),
		Selector:              "p.story",
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}

	payload := decodeJSONToolResponse[webFetchResponsePayload](t, resp)
	if payload.Mode != "raw" {
		t.Fatalf("expected raw mode, got %#v", payload.Mode)
	}
	if payload.Selector != "p.story" {
		t.Fatalf("expected selector to be echoed, got %#v", payload.Selector)
	}
	if !strings.Contains(payload.Body, `<p class="story">First selected paragraph.</p>`) {
		t.Fatalf("expected first selected node in body, got %q", payload.Body)
	}
	if !strings.Contains(payload.Body, `<p class="story">Second selected paragraph.</p>`) {
		t.Fatalf("expected second selected node in body, got %q", payload.Body)
	}
	if strings.Contains(payload.Body, "Ignore this content.") {
		t.Fatalf("expected selector output to exclude non-matching content, got %q", payload.Body)
	}
}

func TestWebFetchGetRejectsSelectorInReaderMode(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.webFetchGet(ctx, webFetchInput{
		URL:      "https://example.com",
		Selector: "article",
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}
	if !resp.IsError {
		t.Fatalf("expected selector in reader mode to fail")
	}
	if !strings.Contains(resp.Content, "extract_article_content") {
		t.Fatalf("expected error to explain raw mode requirement, got %q", resp.Content)
	}
}

func TestWebFetchGetReturnsBase64ForBinaryResponses(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	binaryBody := []byte{0x00, 0x01, 0x02, 0xff, 0x10, 0x20}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(binaryBody)
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{URL: server.URL}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}

	payload := decodeJSONToolResponse[webFetchResponsePayload](t, resp)
	if payload.Encoding != "base64" {
		t.Fatalf("expected base64 encoding marker, got %#v", payload.Encoding)
	}
	if payload.Body != base64.StdEncoding.EncodeToString(binaryBody) {
		t.Fatalf("unexpected base64 body: %q", payload.Body)
	}
}

func TestWebFetchGetJSONBodyJSONRemainsAvailable(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	jsonBody := `{"ok":true,"items":[1,2,3]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(jsonBody))
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{URL: server.URL}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}

	payload := decodeJSONToolResponse[webFetchResponsePayload](t, resp)
	if payload.Body != jsonBody {
		t.Fatalf("expected JSON body string to remain unchanged, got %q", payload.Body)
	}
	bodyJSON, ok := payload.BodyJSON.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed body_json object, got %#v", payload.BodyJSON)
	}
	if bodyJSON["ok"] != true {
		t.Fatalf("expected body_json.ok=true, got %#v", bodyJSON["ok"])
	}
}

func TestWebFetchGetReaderModeCanSaveRawHTMLToWorkspaceFile(t *testing.T) {
	ctx, paths := workspaceFileToolContext(t)

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"web_fetch_get"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	htmlBody := `<!doctype html><html><head><title>Saved Story</title></head><body><article><h1>Saved Story</h1><p>` +
		strings.Repeat("Saving raw HTML should not change the extracted reader text. ", 50) +
		`</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	resp, err := rt.webFetchGet(ctx, webFetchInput{
		URL:            server.URL,
		SaveToFilepath: "downloads/saved-story.html",
	}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("web fetch get: %v", err)
	}

	payload := decodeJSONToolResponse[webFetchResponsePayload](t, resp)
	if payload.SavedToFilepath != "downloads/saved-story.html" {
		t.Fatalf("expected saved_to_filepath to be reported, got %#v", payload.SavedToFilepath)
	}
	if strings.Contains(payload.Body, "<html") {
		t.Fatalf("expected returned body to be reader text, got %q", payload.Body)
	}

	savedBytes, err := os.ReadFile(filepath.Join(paths.FilesPath, "downloads", "saved-story.html"))
	if err != nil {
		t.Fatalf("read saved HTML: %v", err)
	}
	if string(savedBytes) != htmlBody {
		t.Fatalf("expected saved file to keep original HTML bytes")
	}
}

func workspaceFileToolContext(t *testing.T) (context.Context, workspacepkg.Paths) {
	t.Helper()

	root := t.TempDir()
	cfg := configpkg.Config{
		Server: configpkg.ServerConfig{
			Addr: ":4210",
		},
		Workspace: configpkg.WorkspaceConfig{
			DBRoot:    filepath.Join(root, "workspace-db"),
			FilesRoot: filepath.Join(root, "workspaces"),
			Current:   "alpha",
		},
	}
	paths, err := workspacepkg.PathsForName(cfg, "alpha")
	if err != nil {
		t.Fatalf("resolve workspace paths: %v", err)
	}
	if err := os.MkdirAll(paths.FilesPath, 0o755); err != nil {
		t.Fatalf("mkdir workspace files: %v", err)
	}

	ctx := configpkg.WithContext(context.Background(), cfg)
	ctx = workspacepkg.WithPaths(ctx, paths)
	return ctx, paths
}

func TestSearchToolsExactUnavailableToolIDReturnsNoMatches(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "kb_get_node", "kb_get_edge"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "web_fetch_get"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) != 0 {
		t.Fatalf("expected no matches for unavailable exact tool id query, got %#v", payload.Tools)
	}
}

func TestSearchToolsExactAvailableToolIDFindsOnlyThatTool(t *testing.T) {
	ctx := context.Background()
	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get", "kb_get_node", "kb_get_edge"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "web_fetch_get"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) != 1 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected exact tool id query to return only web_fetch_get, got %#v", payload.Tools)
	}
}

func TestToolKeywordsAppendConfigMetadata(t *testing.T) {
	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		Agent: configpkg.AgentConfig{
			Tools: configpkg.AgentToolsConfig{
				Metadata: map[string]configpkg.ToolMetadataConfig{
					"web_fetch_get": {Keywords: []string{"retrieve page", "  retrieve page  "}},
				},
			},
		},
	})

	rt, err := newAgentRuntime(ctx, nil, newEphemeralSessionState(ctx, nil, "run_test"), StoredTrace{}, ToolSelection{
		EnabledToolIDs: []string{"search_tools", "web_fetch_get"},
		PinnedToolIDs:  []string{"search_tools"},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	tool := requireTool(t, rt.catalogResult().Tools, "web_fetch_get")
	if !slices.Contains(tool.Keywords, "retrieve page") {
		t.Fatalf("expected config keyword to be appended, got %#v", tool.Keywords)
	}
	if !slices.Contains(tool.Keywords, "http request") {
		t.Fatalf("expected built-in keyword to remain after append, got %#v", tool.Keywords)
	}

	resp, err := rt.searchTools(ctx, searchToolsInput{Query: "retrieve page"}, fantasy.ToolCall{})
	if err != nil {
		t.Fatalf("search tools: %v", err)
	}

	payload := decodeSearchToolsResponse(t, resp)
	if len(payload.Tools) != 1 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected appended config keyword to be searchable, got %#v", payload.Tools)
	}
}

func TestListToolsAndInvokeToolWithoutRunID(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "agent-tools.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)

	result, err := ListTools(context.Background(), store, "", ToolSelection{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatalf("expected tools to be listed")
	}
	if len(requireTool(t, result.Tools, "web_fetch_get").Keywords) == 0 {
		t.Fatalf("expected web_fetch_get to expose keywords in tool catalog")
	}

	runResult, err := InvokeTool(context.Background(), store, "", "search_tools", map[string]any{
		"query": "web fetch url",
	}, ToolSelection{})
	if err != nil {
		t.Fatalf("invoke tool: %v", err)
	}
	if runResult.ToolID != "search_tools" {
		t.Fatalf("unexpected tool id: %q", runResult.ToolID)
	}
	if runResult.Output == "" {
		t.Fatalf("expected tool output")
	}
	var payload searchToolsResponse
	if err := json.Unmarshal([]byte(runResult.Output), &payload); err != nil {
		t.Fatalf("parse invoke output: %v", err)
	}
	if len(payload.Tools) == 0 || payload.Tools[0].ID != "web_fetch_get" {
		t.Fatalf("expected broad query to find web_fetch_get without a store, got %#v", payload.Tools)
	}
}

func TestInternalToolCatalogPropertiesHaveDescriptions(t *testing.T) {
	rt, err := newAgentRuntime(context.Background(), nil, newEphemeralSessionState(context.Background(), nil, "run_test"), StoredTrace{}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	for _, tool := range rt.catalogResult().Tools {
		if tool.Source != sourceInternal {
			continue
		}
		for name, rawProp := range tool.Schema.Properties {
			prop, ok := rawProp.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := prop["type"]; !ok {
				continue
			}
			desc, _ := prop["description"].(string)
			if strings.TrimSpace(desc) == "" {
				t.Fatalf("internal tool %s property %s is missing description: %#v", tool.ID, name, prop)
			}
		}
	}
}

func TestWrappedToolSpillsLargeOutputAndRevealsPaginationTools(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "spill-output.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	runID := "run_spill"
	trace := json.RawMessage(`{
		"schema_version": 2,
		"run_id": "run_spill",
		"title": "spill",
		"model": "gpt-test",
		"messages": [],
		"created_at": "2026-03-29T00:00:00Z",
		"updated_at": "2026-03-29T00:00:00Z"
	}`)
	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           runID,
		Title:        "spill",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "spill",
		Status:       AgentStatusRunning,
		MessageCount: 0,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		Agent: configpkg.AgentConfig{
			ToolOutput: configpkg.AgentToolOutputConfig{
				MaxStoredBytes:   1024,
				DefaultPageLines: 2,
				GrepDefaultLimit: 2,
			},
		},
	})
	session := newEphemeralSessionState(ctx, store, runID)
	rt, err := newAgentRuntime(ctx, store, session, StoredTrace{RunID: runID, Model: "gpt-test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.setProviderConfig(ProviderConfig{Model: "gpt-test", ToolOutputTokenLimit: 10})

	entry := runtimeTool{
		id: "test_big_output",
		tool: fantasy.NewAgentTool("test_big_output", "Return a large text payload.", func(context.Context, map[string]any, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(strings.Repeat("0123456789\n", 20)), nil
		}),
		defaultEnabled: true,
	}

	resp, err := rt.wrapToolForAgent(entry).Run(ctx, fantasy.ToolCall{ID: "call_spill", Name: "test_big_output", Input: "{}"})
	if err != nil {
		t.Fatalf("run wrapped tool: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &payload); err != nil {
		t.Fatalf("parse spill summary: %v", err)
	}
	outputID, _ := payload["output_id"].(string)
	if outputID == "" {
		t.Fatalf("expected output_id in spill summary: %#v", payload)
	}

	output, err := store.GetAgentRunToolOutput(ctx, runID, outputID)
	if err != nil {
		t.Fatalf("get spilled output: %v", err)
	}
	if output.ToolCallID != "call_spill" {
		t.Fatalf("unexpected tool call id: %q", output.ToolCallID)
	}
	if _, ok := rt.state.revealed["view_tool_output"]; !ok {
		t.Fatalf("expected view_tool_output to be revealed after spill")
	}
	if _, ok := rt.state.revealed["grep_tool_output"]; !ok {
		t.Fatalf("expected grep_tool_output to be revealed after spill")
	}
}

func TestTraceBuilderStoresToolOutputRefFromClientMetadata(t *testing.T) {
	tb := newTraceBuilder(StoredTrace{})
	tb.OnToolResult(fantasy.ToolResultContent{
		ToolCallID: "call_001",
		ToolName:   "web_fetch_get",
		Result: fantasy.ToolResultOutputContentText{
			Text: `{"spilled":true}`,
		},
		ClientMetadata: `{"tool_output_ref":{"output_id":"out_001","tool_call_id":"call_001","size_bytes":123,"estimated_tokens":456,"total_lines":10,"inline_truncated":true}}`,
	})

	if len(tb.trace.Messages) != 1 {
		t.Fatalf("expected one stored message, got %d", len(tb.trace.Messages))
	}
	ref := tb.trace.Messages[0].ToolOutputRef
	if ref == nil || ref.OutputID != "out_001" {
		t.Fatalf("expected tool output ref to be captured, got %#v", ref)
	}
}

func TestWrappedViewToolSpillsInnerContent(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "view-spill.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	runID := "run_view_spill"
	trace := json.RawMessage(`{
		"schema_version": 2,
		"run_id": "run_view_spill",
		"title": "View spill",
		"model": "gpt-test",
		"messages": [],
		"created_at": "2026-03-29T00:00:00Z",
		"updated_at": "2026-03-29T00:00:00Z"
	}`)
	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           runID,
		Title:        "view spill",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "spill",
		Status:       AgentStatusRunning,
		MessageCount: 0,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		Agent: configpkg.AgentConfig{
			ToolOutput: configpkg.AgentToolOutputConfig{
				MaxStoredBytes:   4096,
				DefaultPageLines: 2,
				GrepDefaultLimit: 2,
			},
		},
	})
	session := newEphemeralSessionState(ctx, store, runID)
	rt, err := newAgentRuntime(ctx, store, session, StoredTrace{RunID: runID, Model: "gpt-test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.setProviderConfig(ProviderConfig{Model: "gpt-test", ToolOutputTokenLimit: 10})

	viewContent := strings.Repeat("line one\nline two\nline three\n", 20)
	viewContent = strings.TrimSuffix(viewContent, "\n")
	viewPayload := map[string]any{
		"path":        "smoke/large.txt",
		"offset":      0,
		"limit":       200,
		"start_line":  1,
		"end_line":    60,
		"total_lines": 7000,
		"content":     viewContent,
		"truncated":   true,
	}
	viewJSON, err := json.Marshal(viewPayload)
	if err != nil {
		t.Fatalf("marshal view payload: %v", err)
	}

	entry := runtimeTool{
		id: "view",
		tool: fantasy.NewAgentTool("view", "Return a structured file view.", func(context.Context, map[string]any, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(string(viewJSON)), nil
		}),
		defaultEnabled: true,
	}

	resp, err := rt.wrapToolForAgent(entry).Run(ctx, fantasy.ToolCall{ID: "call_view_spill", Name: "view", Input: "{}"})
	if err != nil {
		t.Fatalf("run wrapped tool: %v", err)
	}

	var summary map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &summary); err != nil {
		t.Fatalf("parse spill summary: %v", err)
	}
	outputID, _ := summary["output_id"].(string)
	if outputID == "" {
		t.Fatalf("expected output_id in spill summary: %#v", summary)
	}
	if got, _ := summary["source_path"].(string); got != "smoke/large.txt" {
		t.Fatalf("expected source_path in spill summary, got %#v", summary["source_path"])
	}

	output, err := store.GetAgentRunToolOutput(ctx, runID, outputID)
	if err != nil {
		t.Fatalf("get spilled output: %v", err)
	}
	if output.Content != viewContent {
		t.Fatalf("expected stored inner view content, got %q", output.Content)
	}
	if output.TotalLines != 60 {
		t.Fatalf("expected stored line count 60, got %d", output.TotalLines)
	}

	page, err := store.GetAgentRunToolOutputPage(ctx, runID, outputID, 1, 1)
	if err != nil {
		t.Fatalf("page spilled output: %v", err)
	}
	if page.Content != "line two" {
		t.Fatalf("expected paged inner content, got %q", page.Content)
	}
}

func TestWrappedWebFetchToolSpillsInnerBody(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "web-fetch-spill.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	runID := "run_web_fetch_spill"
	trace := json.RawMessage(`{
		"schema_version": 2,
		"run_id": "run_web_fetch_spill",
		"title": "Web fetch spill",
		"model": "gpt-test",
		"messages": [],
		"created_at": "2026-03-29T00:00:00Z",
		"updated_at": "2026-03-29T00:00:00Z"
	}`)
	if err := store.CreateAgentRun(context.Background(), knowledge.CreateAgentRunInput{
		ID:           runID,
		Title:        "web fetch spill",
		Model:        "gpt-test",
		Provider:     "default",
		Prompt:       "spill",
		Status:       AgentStatusRunning,
		MessageCount: 0,
		Trace:        trace,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ctx := configpkg.WithContext(context.Background(), configpkg.Config{
		Agent: configpkg.AgentConfig{
			ToolOutput: configpkg.AgentToolOutputConfig{
				MaxStoredBytes:   4096,
				DefaultPageLines: 2,
				GrepDefaultLimit: 2,
			},
		},
	})
	session := newEphemeralSessionState(ctx, store, runID)
	rt, err := newAgentRuntime(ctx, store, session, StoredTrace{RunID: runID, Model: "gpt-test"}, ToolSelection{})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	rt.setProviderConfig(ProviderConfig{Model: "gpt-test", ToolOutputTokenLimit: 10})

	htmlBody := `<!doctype html><html><head><title>Spill Reader Story</title></head><body><article><h1>Spill Reader Story</h1><p>` +
		strings.Repeat("Spilled reader paragraph content should be stored without the outer JSON wrapper. ", 200) +
		`</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	inputPayload, err := json.Marshal(webFetchInput{URL: server.URL})
	if err != nil {
		t.Fatalf("marshal input payload: %v", err)
	}

	entry, ok := rt.tools["web_fetch_get"]
	if !ok {
		t.Fatalf("expected web_fetch_get to be registered")
	}

	resp, err := rt.wrapToolForAgent(entry).Run(ctx, fantasy.ToolCall{
		ID:    "call_web_fetch_spill",
		Name:  "web_fetch_get",
		Input: string(inputPayload),
	})
	if err != nil {
		t.Fatalf("run wrapped tool: %v", err)
	}

	var summary map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &summary); err != nil {
		t.Fatalf("parse spill summary: %v", err)
	}
	outputID, _ := summary["output_id"].(string)
	if outputID == "" {
		t.Fatalf("expected output_id in spill summary: %#v", summary)
	}
	if got, _ := summary["source_url"].(string); got != server.URL {
		t.Fatalf("expected source_url in spill summary, got %#v", summary["source_url"])
	}
	if got, _ := summary["source_mode"].(string); got != "reader" {
		t.Fatalf("expected source_mode=reader, got %#v", summary["source_mode"])
	}
	if got, _ := summary["source_title"].(string); got != "Spill Reader Story" {
		t.Fatalf("expected source_title in spill summary, got %#v", summary["source_title"])
	}
	if _, ok := summary["source_status_code"]; ok {
		t.Fatalf("did not expect source_status_code in spill summary: %#v", summary)
	}
	if _, ok := summary["source_status"]; ok {
		t.Fatalf("did not expect source_status in spill summary: %#v", summary)
	}
	if _, ok := summary["source_content_type"]; ok {
		t.Fatalf("did not expect source_content_type in spill summary: %#v", summary)
	}

	output, err := store.GetAgentRunToolOutput(ctx, runID, outputID)
	if err != nil {
		t.Fatalf("get spilled output: %v", err)
	}
	if !strings.Contains(output.Content, "Spilled reader paragraph content should be stored") {
		t.Fatalf("expected stored inner body text, got %q", output.Content)
	}
	if strings.Contains(output.Content, `"url"`) {
		t.Fatalf("expected stored content without outer JSON wrapper, got %q", output.Content)
	}
	if strings.Contains(output.Content, "<html") {
		t.Fatalf("expected stored content to be reader text, got raw HTML: %q", output.Content)
	}
}

func TestTraceBuilderSnapshotDoesNotFlushAssistantState(t *testing.T) {
	tb := newTraceBuilder(StoredTrace{})
	tb.AddUserMessage("hello")
	tb.OnTextDelta("partial ")
	tb.OnTextDelta("response")

	snapshot, _, err := tb.Snapshot(time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot trace: %v", err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("expected user and assistant messages in snapshot, got %d", len(snapshot.Messages))
	}
	if len(tb.trace.Messages) != 1 {
		t.Fatalf("expected builder state to remain unflushed, got %d messages", len(tb.trace.Messages))
	}

	tb.OnTextDelta(" continued")
	trace, _, err := tb.Build(time.Now().UTC())
	if err != nil {
		t.Fatalf("build trace: %v", err)
	}
	if len(trace.Messages) != 2 {
		t.Fatalf("expected final trace to contain one assistant message, got %d", len(trace.Messages))
	}
	if got := trace.Messages[1].Content; got != "partial response continued" {
		t.Fatalf("unexpected final assistant content: %q", got)
	}
}
