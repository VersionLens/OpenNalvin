package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type searchToolsInput struct {
	Query string `json:"query" jsonschema_description:"Search hidden enabled tools by id, description, keywords, and schema terms. Examples: fetch, web fetch, http request, download url, node, knowledge base, create note, edge. If a broad query returns nothing, retry with a concise 1-3 word query."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Optional max number of hidden enabled tools to return."`
}

type webFetchInput struct {
	URL                   string            `json:"url" jsonschema_description:"Absolute URL to fetch with an HTTP GET request."`
	Headers               map[string]string `json:"headers,omitempty" jsonschema_description:"Optional request headers."`
	SaveToFilepath        string            `json:"save_to_filepath,omitempty" jsonschema_description:"Optional workspace-relative file path to save the fetched response body to."`
	ExtractArticleContent *bool             `json:"extract_article_content,omitempty" jsonschema_description:"For HTML pages, extract readable article text by default. Set false to return raw HTML instead."`
	Selector              string            `json:"selector,omitempty" jsonschema_description:"Optional CSS selector to extract matching outerHTML elements from an HTML response. Only valid when extract_article_content is false."`
}

type viewToolOutputInput struct {
	OutputID string `json:"output_id" jsonschema_description:"Output identifier returned by a spilled tool result summary."`
	Offset   int    `json:"offset,omitempty" jsonschema_description:"Optional 0-based line offset to start from."`
	Limit    int    `json:"limit,omitempty" jsonschema_description:"Optional max number of lines to return. Defaults to the configured tool-output page size."`
}

type grepToolOutputInput struct {
	OutputID    string `json:"output_id" jsonschema_description:"Output identifier returned by a spilled tool result summary."`
	Pattern     string `json:"pattern" jsonschema_description:"Text or regex pattern to search for in the stored output."`
	LiteralText bool   `json:"literal_text,omitempty" jsonschema_description:"If true, treat pattern as literal text instead of a regex."`
	Limit       int    `json:"limit,omitempty" jsonschema_description:"Optional max number of matches to return. Defaults to the configured tool-output grep limit."`
}

type kbListNodesInput struct {
	Query string `json:"query,omitempty" jsonschema_description:"Full-text query to search node content."`
	Kind  string `json:"kind,omitempty" jsonschema_description:"Optional node kind filter."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Optional max number of nodes to return."`
}

type kbGetInput struct {
	ID string `json:"id" jsonschema_description:"Node or edge identifier."`
}

type kbNodeInput struct {
	Kind       string         `json:"kind,omitempty" jsonschema_description:"Node kind."`
	Name       string         `json:"name,omitempty" jsonschema_description:"Node name."`
	Content    string         `json:"content,omitempty" jsonschema_description:"Node content."`
	Attributes map[string]any `json:"attributes,omitempty" jsonschema_description:"Optional JSON attributes."`
}

type kbUpdateNodeInput struct {
	ID         string         `json:"id" jsonschema_description:"Node identifier."`
	Kind       string         `json:"kind,omitempty" jsonschema_description:"Updated node kind."`
	Name       string         `json:"name,omitempty" jsonschema_description:"Updated node name."`
	Content    string         `json:"content,omitempty" jsonschema_description:"Updated node content."`
	Attributes map[string]any `json:"attributes,omitempty" jsonschema_description:"Updated JSON attributes."`
}

type kbListEdgesInput struct {
	NodeID   string `json:"node_id,omitempty" jsonschema_description:"Optional source or target node id filter."`
	Relation string `json:"relation,omitempty" jsonschema_description:"Optional relation filter."`
	Limit    int    `json:"limit,omitempty" jsonschema_description:"Optional max number of edges to return."`
}

type kbEdgeInput struct {
	SourceNodeID string         `json:"source_node_id,omitempty" jsonschema_description:"Source node identifier."`
	TargetNodeID string         `json:"target_node_id,omitempty" jsonschema_description:"Target node identifier."`
	Relation     string         `json:"relation,omitempty" jsonschema_description:"Edge relation name."`
	Attributes   map[string]any `json:"attributes,omitempty" jsonschema_description:"Optional JSON attributes."`
}

type kbUpdateEdgeInput struct {
	ID           string         `json:"id" jsonschema_description:"Edge identifier."`
	SourceNodeID string         `json:"source_node_id,omitempty" jsonschema_description:"Updated source node identifier."`
	TargetNodeID string         `json:"target_node_id,omitempty" jsonschema_description:"Updated target node identifier."`
	Relation     string         `json:"relation,omitempty" jsonschema_description:"Updated relation name."`
	Attributes   map[string]any `json:"attributes,omitempty" jsonschema_description:"Updated JSON attributes."`
}

type spawnAgentInput struct {
	Message        string   `json:"message" jsonschema_description:"Initial user message for the child agent."`
	Title          string   `json:"title,omitempty" jsonschema_description:"Optional child run title."`
	SystemPrompt   string   `json:"system_prompt,omitempty" jsonschema_description:"Optional child system prompt."`
	TaskName       string   `json:"task_name,omitempty" jsonschema_description:"Optional stable child task name."`
	EnabledToolIDs []string `json:"enabled_tool_ids,omitempty" jsonschema_description:"Optional absolute enabled tool ids for the child."`
	PinnedToolIDs  []string `json:"pinned_tool_ids,omitempty" jsonschema_description:"Optional absolute pinned tool ids for the child."`
	EnableToolIDs  []string `json:"enable_tool_ids,omitempty" jsonschema_description:"Optional tool ids to additionally enable for the child."`
	DisableToolIDs []string `json:"disable_tool_ids,omitempty" jsonschema_description:"Optional tool ids to disable for the child."`
	PinToolIDs     []string `json:"pin_tool_ids,omitempty" jsonschema_description:"Optional tool ids to pin for the child."`
	UnpinToolIDs   []string `json:"unpin_tool_ids,omitempty" jsonschema_description:"Optional tool ids to unpin for the child."`
}

type sendInputInput struct {
	Target       string `json:"target" jsonschema_description:"Child task name or run id."`
	Message      string `json:"message" jsonschema_description:"Message to send to the child agent."`
	Interrupt    bool   `json:"interrupt,omitempty" jsonschema_description:"Cancel current child work before delivering the new message."`
	Title        string `json:"title,omitempty" jsonschema_description:"Optional title override for the continued child run."`
	SystemPrompt string `json:"system_prompt,omitempty" jsonschema_description:"Optional system prompt override for the continued child run."`
}

type waitAgentInput struct {
	Target         string `json:"target" jsonschema_description:"Child task name or run id."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema_description:"Optional timeout in seconds."`
}

type getAgentResultInput struct {
	Target string `json:"target" jsonschema_description:"Child task name or run id."`
}

type closeAgentInput struct {
	Target string `json:"target" jsonschema_description:"Child task name or run id."`
}

type todoItemInput struct {
	Content string `json:"content" jsonschema_description:"Short concrete task description."`
	Status  string `json:"status" jsonschema_description:"Task status. Must be one of: pending, in_progress, completed."`
}

type todoWriteInput struct {
	Items []todoItemInput `json:"items" jsonschema_description:"Ordered full replacement todo list for this run."`
}

type planExitInput struct{}

type skillToolInput struct {
	Action string `json:"action,omitempty" jsonschema_description:"One of: list, search, activate, deactivate. Defaults to list."`
	Query  string `json:"query,omitempty" jsonschema_description:"Skill name (for activate/deactivate) or free-text query (for search)."`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"Optional max results when action is search."`
}

type todoSummary struct {
	Items      []StoredTodoItem `json:"items"`
	Total      int              `json:"total"`
	Pending    int              `json:"pending"`
	InProgress int              `json:"in_progress"`
	Completed  int              `json:"completed"`
}

func (rt *agentRuntime) internalTools() []runtimeTool {
	tools := []runtimeTool{
		rt.makeTool(
			"search_tools",
			sourceInternal,
			true,
			true,
			newParallelAgentTool("search_tools", "Search hidden enabled tools by id, description, keywords, and schema terms, then reveal the best matches for later steps. Examples: fetch, web fetch, http request, or download url for web_fetch_get; node, knowledge base, create note, or edge for knowledge-base tools. If a broad query returns nothing, retry with a concise 1-3 word query.", rt.searchTools),
		),
		rt.makeTool(
			"search_knowledge",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("search_knowledge", "Search the knowledge base by full-text query and optional kind filter. Use in parallel with web search for most non-trivial questions.", rt.kbListNodes),
		),
		rt.makeTool(
			"todowrite",
			sourceInternal,
			true,
			true,
			newParallelAgentTool("todowrite", "Replace the current run's ordered todo list with a new structured list of tasks and statuses.", rt.todoWrite),
		),
		rt.makeTool(
			"todoread",
			sourceInternal,
			true,
			true,
			newParallelAgentTool("todoread", "Read the current run's structured todo list and summary counts.", rt.todoRead),
		),
	}
	if rt.mode == RunModePlan && !rt.isChild {
		tools = append(tools, rt.makeTool(
			"plan_exit",
			sourceInternal,
			true,
			true,
			newParallelAgentTool("plan_exit", "Mark the canonical plan as ready for approval and implementation handoff.", rt.planExit),
		))
	}
	tools = append(tools,
		rt.makeTool(
			"shell",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("shell", shellToolDescription, rt.runShell),
		),
		rt.makeTool(
			"bash",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("bash", bashToolDescription, rt.runBash),
		),
		rt.makeTool(
			"spawn_agent",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("spawn_agent", "Spawn a fresh child agent with its own tool selection.", rt.spawnAgent),
		),
		rt.makeTool(
			"send_input",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("send_input", "Send a message to a spawned child agent.", rt.sendInput),
		),
		rt.makeTool(
			"wait_agent",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("wait_agent", "Wait for a spawned child agent to leave the queued or running state, then return its latest assistant output when available. Default timeout is 300 seconds; safe to call repeatedly. When the timeout elapses the response includes timed_out: true (not an error) meaning the child is still running.", rt.waitAgent),
		),
		rt.makeTool(
			"get_agent_result",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("get_agent_result", "Get a child agent's current status and latest assistant output without waiting.", rt.getAgentResult),
		),
		rt.makeTool(
			"close_agent",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("close_agent", "Close a spawned child agent and cancel any active work. Waits up to 5 seconds for the child to terminate. Returns the child's final status and latest output when available.", rt.closeAgent),
		),
		rt.makeTool(
			"web_fetch_get",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("web_fetch_get", "Fetch a URL with a simple HTTP GET request. HTML pages use reader mode by default and return readable text in body; retry with extract_article_content=false for raw HTML, optionally with selector to return only matching outerHTML elements. Binary responses return a base64 body with encoding=base64. JSON responses still include body_json when the body is valid JSON.", rt.webFetchGet),
		),
		rt.makeTool(
			"news_rss_headlines",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("news_rss_headlines", rt.newsRSSHeadlinesDescription(), rt.newsRSSHeadlines),
		),
		rt.makeTool(
			"news_reddit_top_posts",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("news_reddit_top_posts", rt.newsRedditTopPostsDescription(), rt.newsRedditTopPosts),
		),
		rt.makeTool(
			"news_reddit_post_details",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("news_reddit_post_details", "Fetch a Reddit post by permalink or URL and return the post body, linked URL, metadata, and nested comments.", rt.newsRedditPostDetails),
		),
		rt.makeTool(
			"slack_search",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("slack_search", "Search Slack messages, files, channels, and users via Real-Time Search. Supports semantic (natural language questions) and keyword search.", rt.slackSearch),
		),
		rt.makeTool(
			"wikipedia_get_article",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("wikipedia_get_article", "Fetch a Wikipedia page summary plus its section index as JSON, and optionally fetch one section by section index for research and essay-writing workflows.", rt.wikipediaGetArticle),
		),
		rt.makeTool(
			"view_tool_output",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("view_tool_output", "Read a paginated slice of a previously spilled tool output by output_id.", rt.viewToolOutput),
		),
		rt.makeTool(
			"grep_tool_output",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("grep_tool_output", "Search within a previously spilled tool output by output_id.", rt.grepToolOutput),
		),
		rt.makeTool(
			"view",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("view", "Read a file from the active workspace root with optional line offsets and limits.", rt.viewFile),
		),
		rt.makeTool(
			"ls",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("ls", "List files and directories under the active workspace root.", rt.listFiles),
		),
		rt.makeTool(
			"glob",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("glob", "Find workspace files by glob pattern relative to the active workspace root.", rt.globFiles),
		),
		rt.makeTool(
			"grep",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("grep", "Search file contents under the active workspace root for text or regex matches.", rt.grepFiles),
		),
		rt.makeTool(
			"write",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("write", "Create or replace a file under the active workspace root.", rt.writeFile),
		),
		rt.makeTool(
			"edit",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("edit", "Apply an exact text replacement to a file under the active workspace root.", rt.editFile),
		),
		rt.makeTool(
			"multiedit",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("multiedit", "Apply multiple exact text replacements to one workspace file atomically.", rt.multiEditFile),
		),
		rt.makeTool(
			"get_workspace_file_url",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("get_workspace_file_url", "Build a full URL for a workspace file served by nalvin serve. Use this after creating or downloading a file when the user needs a streamable URL for VLC or another local media player.", rt.getWorkspaceFileURL),
		),
		rt.makeTool(
			"extract_epub",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("extract_epub", "Extract a workspace EPUB file into a directory of Markdown chapters and a TOC.md file under the active workspace root.", rt.extractEPUB),
		),
		rt.makeTool(
			"kb_list_nodes",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_list_nodes", "List knowledge base nodes by query and kind.", rt.kbListNodes),
		),
		rt.makeTool(
			"kb_get_node",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_get_node", "Fetch a knowledge base node by id.", rt.kbGetNode),
		),
		rt.makeTool(
			"kb_create_node",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_create_node", "Create a knowledge base node.", rt.kbCreateNode),
		),
		rt.makeTool(
			"kb_update_node",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_update_node", "Update a knowledge base node.", rt.kbUpdateNode),
		),
		rt.makeTool(
			"kb_delete_node",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_delete_node", "Delete a knowledge base node.", rt.kbDeleteNode),
		),
		rt.makeTool(
			"kb_list_edges",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_list_edges", "List knowledge base edges by node or relation.", rt.kbListEdges),
		),
		rt.makeTool(
			"kb_get_edge",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_get_edge", "Fetch a knowledge base edge by id.", rt.kbGetEdge),
		),
		rt.makeTool(
			"kb_create_edge",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_create_edge", "Create a knowledge base edge.", rt.kbCreateEdge),
		),
		rt.makeTool(
			"kb_update_edge",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_update_edge", "Update a knowledge base edge.", rt.kbUpdateEdge),
		),
		rt.makeTool(
			"kb_delete_edge",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("kb_delete_edge", "Delete a knowledge base edge.", rt.kbDeleteEdge),
		),
	)
	tools = append(tools, rt.gitTools()...)
	tools = append(tools, rt.dockerTools()...)
	tools = append(tools,
		rt.makeTool(
			"skill",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("skill", "Search or activate installed agent skills. This tool is the first place to look for reusable workflow guidance that is already available in the current run. Use action=search with a short natural-language task phrase to find relevant installed skills before you reach for search_tools. `skill` search is installed-only; it does not browse remote skills on the network. Use `browse_remote_skills` for remote discovery and `install_skill` before trying to activate a remote skill. Search results are metadata only; they do not load a skill body into context. If you want to actually use a skill and it is not already active, call action=activate with the exact skill name. Activating a skill returns its full body, makes it active for later steps, and may automatically reveal tools listed in that skill's autoreveal_tools metadata when those tools are already allowed in the current run. Managed skill activation also returns read hints for `skill://...` resources; read those paths with `view`, `list_files`, `glob`, or `grep`, not web/http/browser tools. Activation does not resurrect tools that were removed from the run during setup. Some skills are profile skills and can also shape the initial provider and tool defaults of a new root run or child run when explicitly attached. Profile-gated tools must be unlocked at run start or child spawn; activating a profile skill later does not retroactively unlock tools that are not already allowed in the run. Browser tasks are a special case in a root run: treat browser-tool visibility as a runtime fact, not an inference from skill metadata or `autoreveal_tools`. If browser tools are not already visible, do not activate `browser-automation` locally just to hunt for browser access. Spawn a child with that skill attached instead. Do not use spawn_agent to search for or activate skills; use spawn_agent only after you already know which skills to attach.", rt.skillTool),
		),
		rt.makeTool(
			"skill_exec",
			sourceInternal,
			true,
			false,
			newParallelAgentTool("skill_exec", "Run an ad-hoc argv inside an active skill's container. Prefer the auto-registered per-command tools when available.", rt.skillExecTool),
		),
	)
	return tools
}

func (rt *agentRuntime) makeTool(id, source string, defaultEnabled, defaultPinned bool, tool fantasy.AgentTool) runtimeTool {
	return runtimeTool{
		id:             id,
		tool:           tool,
		source:         source,
		keywords:       rt.mergedToolKeywords(id),
		defaultEnabled: defaultEnabled,
		defaultPinned:  defaultPinned,
	}
}

func (rt *agentRuntime) searchTools(_ context.Context, input searchToolsInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool search_tools query=%q limit=%d", input.Query, input.Limit)
	limit := input.Limit
	if limit <= 0 {
		limit = 25
	}

	matches, _, err := rt.searchToolDescriptors(input.Query, limit)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	idsToReveal := make([]string, 0, len(matches))
	for _, descriptor := range matches {
		idsToReveal = append(idsToReveal, descriptor.ID)
	}
	revealed := rt.revealTools(idsToReveal...)
	result := map[string]any{
		"query":             input.Query,
		"revealed_tool_ids": revealed,
		"tools":             matches,
	}
	if len(matches) == 0 && len(rt.warnings) > 0 {
		result["warnings"] = rt.warnings
	}
	if len(revealed) > 0 {
		result["next_step"] = "These tools are now available. Use them to complete the task."
	}
	return jsonToolResponse(result)
}

func normalizeTodoItems(items []todoItemInput) ([]StoredTodoItem, error) {
	normalized := make([]StoredTodoItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, fmt.Errorf("todo item content is required")
		}
		status := strings.TrimSpace(item.Status)
		switch status {
		case "pending", "in_progress", "completed":
		default:
			return nil, fmt.Errorf("todo item status %q is invalid", item.Status)
		}
		if _, exists := seen[content]; exists {
			return nil, fmt.Errorf("todo item content %q is duplicated", content)
		}
		seen[content] = struct{}{}
		normalized = append(normalized, StoredTodoItem{
			Content: content,
			Status:  status,
		})
	}
	return normalized, nil
}

func summarizeTodos(items []StoredTodoItem) todoSummary {
	summary := todoSummary{
		Items: append([]StoredTodoItem{}, items...),
		Total: len(items),
	}
	for _, item := range items {
		switch item.Status {
		case "pending":
			summary.Pending++
		case "in_progress":
			summary.InProgress++
		case "completed":
			summary.Completed++
		}
	}
	return summary
}

func (rt *agentRuntime) todoWrite(_ context.Context, input todoWriteInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool todowrite items=%d", len(input.Items))
	if err := rt.ensureRootOnlyTodoAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	items, err := normalizeTodoItems(input.Items)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	rt.replaceTodos(items)
	return jsonToolResponse(summarizeTodos(rt.todoState()))
}

func (rt *agentRuntime) todoRead(_ context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool todoread")
	if err := rt.ensureRootOnlyTodoAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(summarizeTodos(rt.todoState()))
}

func (rt *agentRuntime) planExit(_ context.Context, _ planExitInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool plan_exit")
	if normalizeRunMode(rt.mode) != RunModePlan {
		return fantasy.NewTextErrorResponse("plan_exit is only available in plan mode"), nil
	}
	if rt.isChild {
		return fantasy.NewTextErrorResponse("plan_exit cannot be used from a child run"), nil
	}
	ref := normalizePlanRef(rt.session.metadata().PlanRef)
	if ref.Path == "" {
		return fantasy.NewTextErrorResponse("plan mode is missing a canonical plan file"), nil
	}
	ref.Status = PlanStatusReady
	rt.session.setPlanRef(ref)
	rt.mode = RunModePlan

	resp, err := jsonToolResponse(ref)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	resp.Metadata = mergeToolResponseMetadata(resp.Metadata, toolResponseClientMetadata{
		PlanRef: &ref,
	})
	return resp, nil
}

func (rt *agentRuntime) webFetchGet(ctx context.Context, input webFetchInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool web_fetch_get url=%q save_to_filepath=%q extract_article_content=%t selector=%q",
		input.URL,
		input.SaveToFilepath,
		webFetchShouldExtractArticleContent(input),
		input.Selector,
	)
	if strings.TrimSpace(input.URL) == "" {
		return fantasy.NewTextErrorResponse("url is required"), nil
	}
	if webFetchShouldExtractArticleContent(input) && strings.TrimSpace(input.Selector) != "" {
		return fantasy.NewTextErrorResponse("selector can only be used when extract_article_content is false"), nil
	}
	if err := validateWebFetchURL(input.URL); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	for key, value := range input.Headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	savedPath := ""
	if strings.TrimSpace(input.SaveToFilepath) != "" {
		_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, input.SaveToFilepath, false)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		if err := os.WriteFile(absolutePath, body, 0o644); err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		savedPath = relativePath
	}

	result := webFetchResponsePayload{
		URL:             input.URL,
		SavedToFilepath: savedPath,
	}

	contentType := resp.Header.Get("Content-Type")
	extractArticleContent := webFetchShouldExtractArticleContent(input)
	htmlLike := isHTMLLikeResponse(contentType, body)
	jsonPreview, jsonTruncated := webFetchPreviewBytes(body)
	jsonPreviewValid := json.Valid(jsonPreview)

	// Keep JSON behavior unchanged in this plan so existing jq/body_json flows
	// continue to work exactly as before.
	if isJSONLikeContentType(contentType) || jsonPreviewValid {
		result.Body = string(jsonPreview)
		result.Truncated = jsonTruncated
		var parsed any
		if jsonPreviewValid && json.Unmarshal(jsonPreview, &parsed) == nil {
			result.BodyJSON = parsed
		}
		return jsonToolResponse(result)
	}

	if htmlLike {
		if !extractArticleContent {
			result.Mode = "raw"
			if selector := strings.TrimSpace(input.Selector); selector != "" {
				selected, err := selectHTMLNodes(body, selector)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("select HTML nodes: %v", err)), nil
				}
				result.Selector = selector
				result.Body, result.Truncated = truncateWebFetchBodyForTokens(rt, selected)
				return jsonToolResponse(result)
			}

			result.Body, result.Truncated = truncateWebFetchBodyForTokens(rt, normalizeFetchBodyString(body))
			return jsonToolResponse(result)
		}

		article, err := buildReaderModeBody(input.URL, body)
		if err != nil {
			return fantasy.NewTextErrorResponse("article extraction failed or returned no readable content; retry with extract_article_content=false or use mcp__exa__crawling_exa"), nil
		}

		result.Mode = "reader"
		result.Title = article.Title
		result.Excerpt = article.Excerpt
		result.Body, result.Truncated = truncateWebFetchBodyForTokens(rt, article.Body)
		return jsonToolResponse(result)
	}

	if strings.TrimSpace(input.Selector) != "" {
		return fantasy.NewTextErrorResponse("selector can only be used with HTML responses in raw mode"), nil
	}

	if isTextLikeResponse(contentType, body) {
		result.Body, result.Truncated = truncateWebFetchBodyForTokens(rt, normalizeFetchBodyString(body))
		return jsonToolResponse(result)
	}

	result.Encoding = "base64"
	result.Body, result.Truncated = truncateWebFetchBodyForTokens(rt, encodeBinaryFetchBody(body))
	return jsonToolResponse(result)
}

func (rt *agentRuntime) viewToolOutput(ctx context.Context, input viewToolOutputInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool view_tool_output output_id=%q offset=%d limit=%d", input.OutputID, input.Offset, input.Limit)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if strings.TrimSpace(input.OutputID) == "" {
		return fantasy.NewTextErrorResponse("output_id is required"), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = rt.cfg.Agent.ToolOutput.DefaultPageLines
	}

	page, err := store.GetAgentRunToolOutputPage(ctx, rt.session.runID, input.OutputID, input.Offset, limit)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(page)
}

func (rt *agentRuntime) grepToolOutput(ctx context.Context, input grepToolOutputInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool grep_tool_output output_id=%q pattern=%q literal=%t limit=%d", input.OutputID, input.Pattern, input.LiteralText, input.Limit)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if strings.TrimSpace(input.OutputID) == "" {
		return fantasy.NewTextErrorResponse("output_id is required"), nil
	}
	if strings.TrimSpace(input.Pattern) == "" {
		return fantasy.NewTextErrorResponse("pattern is required"), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = rt.cfg.Agent.ToolOutput.GrepDefaultLimit
	}

	result, err := store.SearchAgentRunToolOutput(ctx, rt.session.runID, input.OutputID, input.Pattern, input.LiteralText, limit)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(result)
}

func (rt *agentRuntime) kbListNodes(ctx context.Context, input kbListNodesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_list_nodes query=%q kind=%q limit=%d", input.Query, input.Kind, input.Limit)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	nodes, err := store.ListNodes(ctx, input.Query, input.Kind, input.Limit)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(nodes)
}

func (rt *agentRuntime) kbGetNode(ctx context.Context, input kbGetInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_get_node id=%q", input.ID)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	node, err := store.GetNode(ctx, input.ID)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(node)
}

func (rt *agentRuntime) kbCreateNode(ctx context.Context, input kbNodeInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_create_node kind=%q name=%q", input.Kind, input.Name)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	node, err := store.CreateNode(ctx, knowledge.NodeInput{
		Kind:       input.Kind,
		Name:       input.Name,
		Content:    input.Content,
		Attributes: encodeAttributes(input.Attributes),
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(node)
}

func (rt *agentRuntime) kbUpdateNode(ctx context.Context, input kbUpdateNodeInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_update_node id=%q", input.ID)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	node, err := store.UpdateNode(ctx, input.ID, knowledge.NodeInput{
		Kind:       input.Kind,
		Name:       input.Name,
		Content:    input.Content,
		Attributes: encodeAttributes(input.Attributes),
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(node)
}

func (rt *agentRuntime) kbDeleteNode(ctx context.Context, input kbGetInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_delete_node id=%q", input.ID)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if err := store.DeleteNode(ctx, input.ID); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(map[string]any{"deleted": true, "id": input.ID})
}

func (rt *agentRuntime) kbListEdges(ctx context.Context, input kbListEdgesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_list_edges node_id=%q relation=%q limit=%d", input.NodeID, input.Relation, input.Limit)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	edges, err := store.ListEdges(ctx, knowledge.EdgeFilter{
		NodeID:   input.NodeID,
		Relation: input.Relation,
		Limit:    input.Limit,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(edges)
}

func (rt *agentRuntime) kbGetEdge(ctx context.Context, input kbGetInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_get_edge id=%q", input.ID)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	edge, err := store.GetEdge(ctx, input.ID)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(edge)
}

func (rt *agentRuntime) kbCreateEdge(ctx context.Context, input kbEdgeInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_create_edge source=%q target=%q relation=%q", input.SourceNodeID, input.TargetNodeID, input.Relation)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	edge, err := store.CreateEdge(ctx, knowledge.EdgeInput{
		SourceNodeID: input.SourceNodeID,
		TargetNodeID: input.TargetNodeID,
		Relation:     input.Relation,
		Attributes:   encodeAttributes(input.Attributes),
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(edge)
}

func (rt *agentRuntime) kbUpdateEdge(ctx context.Context, input kbUpdateEdgeInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_update_edge id=%q", input.ID)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	edge, err := store.UpdateEdge(ctx, input.ID, knowledge.EdgeInput{
		SourceNodeID: input.SourceNodeID,
		TargetNodeID: input.TargetNodeID,
		Relation:     input.Relation,
		Attributes:   encodeAttributes(input.Attributes),
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(edge)
}

func (rt *agentRuntime) kbDeleteEdge(ctx context.Context, input kbGetInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool kb_delete_edge id=%q", input.ID)
	store, err := rt.requireStore()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if err := store.DeleteEdge(ctx, input.ID); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(map[string]any{"deleted": true, "id": input.ID})
}

func (rt *agentRuntime) spawnAgent(ctx context.Context, input spawnAgentInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool spawn_agent task_name=%q enabled=%v pinned=%v enable=%v disable=%v pin=%v unpin=%v",
		input.TaskName,
		input.EnabledToolIDs,
		input.PinnedToolIDs,
		input.EnableToolIDs,
		input.DisableToolIDs,
		input.PinToolIDs,
		input.UnpinToolIDs,
	)
	if err := rt.ensureSubAgentControlAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	manager, err := rt.session.ensureSubAgentManager()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	// Capture workspace paths from the current context so the child inherits
	// the same workspace root rather than falling back to config-based resolution.
	var workspacePaths *workspacepkg.Paths
	if paths, ok := workspacepkg.PathsFromContext(ctx); ok {
		workspacePaths = &paths
	}

	child, err := manager.spawn(ctx, subAgentSpawnRequest{
		Message:         input.Message,
		Title:           input.Title,
		SystemPrompt:    input.SystemPrompt,
		TaskName:        input.TaskName,
		ParentToolState: rt.toolState(),
		WorkspacePaths:  workspacePaths,
		Tools: ToolSelection{
			EnabledToolIDs: input.EnabledToolIDs,
			PinnedToolIDs:  input.PinnedToolIDs,
			EnableToolIDs:  input.EnableToolIDs,
			DisableToolIDs: input.DisableToolIDs,
			PinToolIDs:     input.PinToolIDs,
			UnpinToolIDs:   input.UnpinToolIDs,
		},
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"run_id":    child.runID,
		"task_name": child.taskName,
		"status":    child.snapshot().Status,
	})
}

func (rt *agentRuntime) sendInput(ctx context.Context, input sendInputInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool send_input target=%q interrupt=%t", input.Target, input.Interrupt)
	if err := rt.ensureSubAgentControlAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	manager, err := rt.session.ensureSubAgentManager()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	child, err := manager.resolve(input.Target)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if err := child.send(ctx, subAgentMessage{
		Message:      input.Message,
		Title:        input.Title,
		SystemPrompt: input.SystemPrompt,
		Interrupt:    input.Interrupt,
	}); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(child.snapshot())
}

func (rt *agentRuntime) waitAgent(ctx context.Context, input waitAgentInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool wait_agent target=%q timeout_seconds=%d", input.Target, input.TimeoutSeconds)
	if err := rt.ensureSubAgentControlAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	manager, err := rt.session.ensureSubAgentManager()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	child, err := manager.resolve(input.Target)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	// Apply defaults: 0 → 300s; < 5 → 5s.
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	} else if timeout < 5 {
		timeout = 5
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	status, err := child.wait(waitCtx)
	if err != nil {
		// If our internal timeout fired (not the parent ctx being canceled),
		// return a success response indicating the child is still running.
		if ctx.Err() == nil {
			snap := child.snapshot()
			return jsonToolResponse(map[string]any{
				"run_id":    snap.RunID,
				"task_name": snap.TaskName,
				"status":    snap.Status,
				"timed_out": true,
				"message":   "child still running; call wait_agent again or get_agent_result",
			})
		}
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(status)
}

func (rt *agentRuntime) getAgentResult(ctx context.Context, input getAgentResultInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if err := rt.ensureSubAgentControlAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	manager, err := rt.session.ensureSubAgentManager()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	child, err := manager.resolve(input.Target)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	status, err := child.result(ctx)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(status)
}

func (rt *agentRuntime) closeAgent(ctx context.Context, input closeAgentInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool close_agent target=%q", input.Target)
	if err := rt.ensureSubAgentControlAllowed(); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	manager, err := rt.session.ensureSubAgentManager()
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	child, err := manager.resolve(input.Target)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if err := child.close(ctx); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	// Wait briefly for the child to reach a terminal status so the caller
	// sees "closed"/"completed"/"failed" instead of "running".
	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, waitErr := child.wait(closeCtx)
	if waitErr != nil {
		// Deadline elapsed; child is still winding down.
		snap := child.snapshot()
		snap.Status = "stopping"
		return jsonToolResponse(snap)
	}
	return jsonToolResponse(status)
}

func (rt *agentRuntime) requireStore() (*knowledge.Store, error) {
	if rt.store == nil {
		return nil, fmt.Errorf("workspace-backed knowledge store is required")
	}
	return rt.store, nil
}

func toolMatchesQuery(tool ToolDescriptor, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(tool.ID), query) ||
		strings.Contains(strings.ToLower(tool.Description), query) ||
		strings.Contains(strings.ToLower(tool.ServerName), query)
}

func encodeAttributes(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(payload)
}

func (rt *agentRuntime) skillTool(_ context.Context, input skillToolInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if rt.skills == nil {
		return fantasy.NewTextErrorResponse("skills are not enabled in this run"), nil
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list":
		return jsonToolResponse(SkillCatalogResult{
			Skills:   rt.skills.skillSummaries(),
			Warnings: rt.skills.catalogWarnings(),
		})
	case "search":
		return jsonToolResponse(SkillCatalogResult{
			Skills: rt.skills.search(input.Query, input.Limit),
		})
	case "activate":
		name := strings.TrimSpace(input.Query)
		if name == "" {
			return fantasy.NewTextErrorResponse("query (skill name) is required for activate"), nil
		}
		meta, err := rt.skills.activateManual(name)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		// Reveal autoreveal_tools and any declared command tools.
		ids := append([]string(nil), meta.AutorevealTools...)
		ids = append(ids, skillCommandToolIDs(meta)...)
		revealed := rt.revealTools(ids...)
		return jsonToolResponse(map[string]any{
			"activated":         meta.Name,
			"reason":            skillActiveReasonManual,
			"revealed_tool_ids": revealed,
		})
	case "deactivate":
		name := strings.TrimSpace(input.Query)
		if name == "" {
			return fantasy.NewTextErrorResponse("query (skill name) is required for deactivate"), nil
		}
		removed := rt.skills.deactivate(name)
		return jsonToolResponse(map[string]any{
			"deactivated": name,
			"removed":     removed,
		})
	default:
		return fantasy.NewTextErrorResponse(fmt.Sprintf("unsupported skill action %q", input.Action)), nil
	}
}

func jsonToolResponse(value any) (fantasy.ToolResponse, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	return fantasy.NewTextResponse(string(payload)), nil
}
