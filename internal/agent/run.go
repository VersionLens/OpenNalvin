package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/google/uuid"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

var errPlanExitComplete = errors.New("plan_exit requested run completion")

// baseSystemPrompt is prepended to every agent run's system prompt (before any
// user-supplied system prompt). It explains core capabilities and conventions
// that apply to all runs.
const baseSystemPrompt = `You are a capable AI assistant with access to a set of tools.

TOOL COMPOSITION WITH THE SHELL
When a task requires loops, pipelines, or multiple tool calls that feed into
each other, prefer composing them in a single shell tool call rather than
making many individual tool calls. This reduces round-trips, keeps operations
atomic, and lets you use the full power of POSIX shell composition.

Use the shell tool when you need to:
- Loop over a set of files, results, or values
- Pipe one tool's output into another tool or a text-processing utility
- Perform a multi-step workflow atomically (all changes committed only on success)
- Combine web fetching, file reading, grepping, and writing in one operation

Examples:
  # Count occurrences of a pattern in every .go file
  glob --pattern "**/*.go" | jq -r '.paths[]' | while read f; do
    echo "$f: $(grep --pattern "TODO" --path "$f" | jq '.matches | length')"
  done

  # Fetch a JSON API and write a derived file
  data=$(web_fetch_get --url "https://api.example.com/items" | jq '.body_json.items')
  echo "$data" | jq '[.[] | select(.active) | .name]' > active.json
  write --path "reports/active.json" --content "$(cat active.json)"

  # Find large files, search each for a keyword, write a report
  find . -size +10k -type f | while read f; do
    count=$(grep --pattern "error" --path "$f" | jq '.matches | length')
    [ "$count" -gt 0 ] && echo "$f: $count errors"
  done | write --path "reports/errors.txt" --content "$(cat /dev/stdin)"

AVAILABLE TOOLS IN THE SHELL
All currently pinned tools and any tools you have revealed via search_tools are
available as commands inside the shell. Use search_tools first if you are
unsure whether a particular capability is available, then use the revealed tool
inside shell scripts for any looping or pipeline work.

SEARCH STRATEGY
For any non-trivial query, run search_knowledge and web search (e.g. web_search_exa)
in parallel as your first step — before reasoning or responding. Skip this only for
simple single-step tasks where no external knowledge is needed.

Before reaching for broad web search, child-agent web research, or generic
workarounds, explicitly consider whether hidden domain-specific tools may exist
for the task and use search_tools to reveal them first.

This is especially important for requests about news, headlines, RSS feeds,
Reddit posts, subreddit activity, comments, or other feed-like/current-events
data. For those requests, search_tools should usually be your first discovery
step with concise queries such as: news, rss, headlines, reddit, subreddit,
top posts, comments, or permalink.

If a revealed domain-specific tool can answer the request directly, prefer it
over broad web search.

	If asked to do something you cannot accomplish with the tools currently visible,
	immediately call search_tools to find the right tool before attempting any workaround.

PYTHON PACKAGE MANAGEMENT IN CONTAINERS
The nalvin/dev container image does not have pip on PATH. Use uv for all Python
package management inside containers:
- Library installs: uv venv .venv && uv pip install -r requirements.txt
- CLI tools:        uvx <tool> or uv tool install <tool>
- Scripts:          uv run script.py
Never call pip directly inside a container; it will not be found.
`

const parentDelegationSystemPrompt = `
DELEGATION STRATEGY
Before starting any substantial multi-step task, you MUST explicitly consider
whether child agents would improve the result. Think through delegation before
you commit to doing the work locally.

Use child agents when the final answer matters more than the intermediate work.
Prefer delegating bounded side work to child agents and then synthesizing the
results yourself. Use child agents for independent tasks that can run in
parallel and whose detailed steps are not important to keep in your own context.

Ask yourself:
- Are there 2 or more separable subproblems that can be researched or explored independently?
- Is the task mostly gathering facts, evidence, or intermediate summaries that I will later combine?
- Would parallel web research, file exploration, or evidence collection improve speed or answer quality?
- Is the work important, but the exact sequence of steps taken to get the result not important to preserve in my own context?

If the answer to any of those is yes, strongly prefer spawning child agents.
If there are 3 clearly separable research threads, default to using 3 child
agents unless there is a concrete reason not to.

Good fits with the available tools include:
- web research on separate subtopics or competing claims
- collecting and comparing information from multiple URLs or APIs
- exploring separate files, modules, or code paths before you synthesize
- gathering evidence from the knowledge base and the web in parallel
- repetitive fetch, read, or search tasks whose outputs you will summarize
- comparing multiple products, frameworks, libraries, or approaches where each item can be researched independently
- investigating separate hypotheses or root causes before deciding which one best explains the evidence

Keep the final synthesis, decisions, and user-facing answer in the parent run.
Avoid delegating work that blocks your immediate next step or requires tight
coupling with the reasoning you are doing locally.
`

const parentTodoSystemPrompt = `
TODO TRACKING
Before starting substantial multi-step work, you MUST explicitly decide whether
todo tracking is required for this run. For any task where todo tracking is
required, you MUST use todoread and todowrite to maintain a short structured
todo list for the current run.

Todo tracking is required when:
- the task has 3 or more meaningful steps
- you are coordinating parallel child agents
- you are doing longer research, investigation, or multi-file implementation
- you are doing medium or large research/comparison work that will require multiple searches, fetches, or evidence-gathering steps before the final answer
- you need to track progress, update the plan, or resume work reliably

If any of those is true, do not keep the task list only in your reasoning or in
free-form text: record it with the todo tools.

When todo tracking is required, your first substantive tool action should
usually be todoread or todowrite, not web search, file exploration, or child
spawning. On a fresh run, create the todo list early with todowrite. On a
resumed run, read the existing list first with todoread.

How to use them:
- Call todoread before resuming substantial work on an existing run.
- Call todowrite before starting complex work to create a short, concrete list.
- Keep exactly one item in_progress while actively executing work.
- Update the list after finishing a meaningful step or when the plan changes.
- Before waiting on multiple child agents, use todowrite to record the parent's
  coordination plan and expected synthesis steps.
- After child agents finish or a research thread completes, update the todo
  list before moving on to synthesis.
- Do not use the todo tools for trivial one-step tasks.

For tasks with 3 or more meaningful steps, and for tasks using child agents,
the default behavior should be to create and maintain a todo list unless there
is a concrete reason not to.

For medium or large research, comparison, or investigation tasks, default to
creating and maintaining a todo list even if you are not using child agents.
`

const newsToolAwarenessSystemPrompt = `
NEWS TOOL AWARENESS
When the user asks for news, headlines, current events, what's happening right
now, topic digests, or source roundups, treat that as a domain-specific
collection task rather than a generic web-research task.

For news collection:
- Use search_tools early with concise queries like news, rss, headlines,
  reddit, subreddit, top posts, comments, or permalink.
- Prefer the built-in news tools when available, especially
  news_rss_headlines, news_reddit_top_posts, and news_reddit_post_details.
- Use both RSS and Reddit tools when the user asks broadly about "the news" or
  wants a cross-source digest, unless they explicitly ask for only one source.
- Treat user topic buckets such as ai, tech, politics, world, cyber, and
  finance as collection targets that may need to be mapped onto the available
  tool categories instead of passed through literally.
- If a news tool returns available categories, use that response to correct the
  plan instead of repeatedly retrying invalid categories.
- Use web search only as a fallback when the built-in news tools cannot cover
  the requested topic, source, or detail.
`

const parentNewsCollectionSystemPrompt = `
NEWS COLLECTION PLAYBOOK
For comprehensive news collection, follow this workflow by default:
- Use todowrite early to track the categories or sources you need to collect.

When the request spans multiple categories or needs broad coverage, prefer
spawning child agents to collect categories in parallel after you have revealed
the news tools. A good default is one child per category or per source family,
then synthesize the combined digest in the parent run.

When collecting comprehensive news:
- The parent should use todowrite to record the category/source plan.
- Child agents should usually be given the revealed news tools they need,
  rather than broad web-search tools, unless the user explicitly asks for web-wide research.
- If a news tool returns available categories, use that response to correct the
  plan instead of repeatedly retrying invalid categories.
- Use web search only as a fallback when the built-in news tools cannot cover
  the requested topic, source, or detail.
`

func currentDateTimePromptLine(now time.Time) string {
	return "It is " + now.Format("Monday 2 January 2006 15:04 MST")
}

// effectiveSystemPrompt returns baseSystemPrompt combined with any
// user-supplied system prompt for a run.
func effectiveSystemPrompt(userSupplied string) string {
	return effectiveSystemPromptAt(time.Now(), userSupplied)
}

func effectiveSystemPromptAt(now time.Time, userSupplied string) string {
	return effectiveSystemPromptForRunContextAt(now, userSupplied, StoredRunMeta{RunKind: RunKindRoot})
}

func effectiveSystemPromptForRun(userSupplied, runKind string) string {
	return effectiveSystemPromptForRunContextAt(time.Now(), userSupplied, StoredRunMeta{RunKind: runKind})
}

func effectiveSystemPromptForRunAt(now time.Time, userSupplied, runKind string) string {
	return effectiveSystemPromptForRunContextAt(now, userSupplied, StoredRunMeta{RunKind: runKind})
}

const childWorkspaceSystemPrompt = `
WORKSPACE ACCESS
You are running as a child agent with access to a shared workspace. All file
operations (reading, writing, listing, searching) must use the file tools:
view, write, edit, multiedit, ls, glob, grep. Never use web_fetch_get to
access local files or workspace paths — it only works for http/https URLs.
`

func effectiveSystemPromptForRunContextAt(now time.Time, userSupplied string, meta StoredRunMeta) string {
	userSupplied = strings.TrimSpace(userSupplied)
	dateTimeLine := currentDateTimePromptLine(now)
	basePrompt := baseSystemPrompt + newsToolAwarenessSystemPrompt
	runKind := strings.TrimSpace(meta.RunKind)
	if strings.TrimSpace(runKind) != RunKindChild {
		basePrompt += parentDelegationSystemPrompt
		basePrompt += parentTodoSystemPrompt
		basePrompt += parentNewsCollectionSystemPrompt
	} else {
		basePrompt += childWorkspaceSystemPrompt
	}
	if normalizeRunMode(meta.Mode) == RunModePlan {
		basePrompt += planModePrompt(meta.PlanRef.Path, runKind == RunKindChild)
	}
	if userSupplied == "" {
		return basePrompt + "\n" + dateTimeLine
	}
	return basePrompt + "\n" + dateTimeLine + "\n" + userSupplied
}

func shouldTerminateAfterToolResult(evt ToolResultEvent) bool {
	if strings.TrimSpace(evt.ToolName) != "plan_exit" || evt.IsError || evt.PlanRef == nil {
		return false
	}
	return normalizePlanRef(*evt.PlanRef).Status == PlanStatusReady
}

// Run executes an agent run against the selected configured provider and
// persists the run into the provided workspace-backed knowledge store.
func Run(ctx context.Context, store *knowledge.Store, req RunRequest, opts RunOptions) (string, error) {
	if store == nil {
		return "", fmt.Errorf("workspace-backed knowledge store is required")
	}
	if strings.TrimSpace(req.Message) == "" {
		return "", fmt.Errorf("message is required")
	}

	now := time.Now().UTC()
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = uuid.NewString()
	}

	var (
		trace       StoredTrace
		history     []fantasy.Message
		existingRun *knowledge.AgentRun
		err         error
	)

	if strings.TrimSpace(req.RunID) != "" {
		existingRun, err = store.GetAgentRun(ctx, runID)
		if err != nil {
			return "", fmt.Errorf("load agent run %q: %w", runID, err)
		}
		trace, err = parseStoredTrace(existingRun.Trace)
		if err != nil {
			return "", err
		}
		history = traceToFantasyMessages(trace)
	}

	requestedProviderName := strings.TrimSpace(req.ProviderName)
	providerName := normalizeProviderName(requestedProviderName)
	if requestedProviderName == "" && existingRun != nil {
		providerName = trace.Provider
	}

	providerCfg, err := LoadProviderConfig(providerName)
	if err != nil {
		return "", err
	}
	providerOptions, err := fantasyProviderOptions(providerCfg)
	if err != nil {
		return "", fmt.Errorf("build provider options: %w", err)
	}
	provider, err := newFantasyProvider(providerCfg)
	if err != nil {
		return "", fmt.Errorf("create provider: %w", err)
	}
	model, err := provider.LanguageModel(ctx, providerCfg.Model)
	if err != nil {
		return "", fmt.Errorf("load model %q: %w", providerCfg.Model, err)
	}

	session := globalRunSessions.getOrCreate(runID)
	session.applyContext(ctx, store)
	session.setProviderName(providerName)
	if trace.RunID != "" {
		session.setMode(trace.Metadata.Mode)
		session.setPlanRef(trace.Metadata.PlanRef)
	}
	if opts.Verbose && opts.Debug != nil {
		session.setDebugWriter(opts.Debug)
	} else {
		session.setDebugWriter(nil)
	}

	if req.ParentRunID != "" || req.RootRunID != "" || req.TaskName != "" || req.RunKind != "" {
		session.setMetadata(req.ParentRunID, req.RootRunID, req.TaskName, req.RunKind)
	}
	if req.Mode != "" {
		session.setMode(req.Mode)
	}
	if req.PlanRef.Path != "" || req.PlanRef.SourceRunID != "" || req.PlanRef.Status != "" {
		session.setPlanRef(req.PlanRef)
	}

	if strings.TrimSpace(req.RunID) == "" {
		history = append([]fantasy.Message(nil), req.History...)
		if session.metadata().RootRunID == "" {
			requestedRunKind := strings.TrimSpace(req.RunKind)
			if requestedRunKind == "" {
				if strings.TrimSpace(req.ParentRunID) != "" {
					requestedRunKind = RunKindChild
				} else {
					requestedRunKind = RunKindRoot
				}
			}
			session.setMetadata(req.ParentRunID, runID, req.TaskName, requestedRunKind)
		}
		trace = StoredTrace{
			SchemaVersion: 2,
			RunID:         runID,
			Title:         deriveRunTitle(req.Title, req.Message),
			Model:         providerCfg.Model,
			Provider:      providerName,
			SystemPrompt:  strings.TrimSpace(req.SystemPrompt),
			Metadata:      session.metadata(),
			Messages:      storedMessagesFromFantasyMessages(history),
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	} else {
		session.setMetadata(existingRun.ParentRunID, existingRun.RootRunID, existingRun.TaskName, existingRun.RunKind)
		session.setMode(trace.Metadata.Mode)
		session.setPlanRef(trace.Metadata.PlanRef)
		session.setMetadata(req.ParentRunID, req.RootRunID, req.TaskName, req.RunKind)
		if req.Mode != "" {
			session.setMode(req.Mode)
		}
		if req.PlanRef.Path != "" || req.PlanRef.SourceRunID != "" || req.PlanRef.Status != "" {
			session.setPlanRef(req.PlanRef)
		}
		// Preserve the persisted tool state across the metadata overwrite so that
		// propagated parent tool policies (written by persistQueuedChildRun) are
		// not clobbered before newAgentRuntime reads them.
		savedTools := trace.Metadata.Tools
		trace.Metadata = session.metadata()
		trace.Metadata.Tools = savedTools
		history = traceToFantasyMessages(trace)
		if title := strings.TrimSpace(req.Title); title != "" {
			trace.Title = title
		}
		if systemPrompt := strings.TrimSpace(req.SystemPrompt); systemPrompt != "" {
			trace.SystemPrompt = systemPrompt
		}
		trace.Model = providerCfg.Model
		trace.Provider = providerName
	}

	runtime, err := newAgentRuntime(ctx, store, session, trace, req.Tools)
	if err != nil {
		return runID, err
	}
	defer runtime.close()
	runtime.setProviderConfig(providerCfg)

	// Initialize compaction manager.
	cfg, _ := configpkg.FromContext(ctx)
	if cfg.Agent.Compaction.Enabled {
		contextWindow := providerCfg.ContextWindowTokens
		if opts.ContextWindowOverride > 0 {
			contextWindow = opts.ContextWindowOverride
		}
		counter := newTokenEstimator(providerCfg.Model)
		runtime.compaction = newCompactionManager(
			cfg.Agent.Compaction,
			cfg,
			contextWindow,
			model,
			effectiveSystemPromptForRunContextAt(time.Now(), req.SystemPrompt, session.metadata()),
			providerOptions,
			counter,
			session.debugf,
		)
		runtime.compaction.restoreCheckpoint(trace)
	}

	session.debugf("run_id=%s provider=%s provider_model=%s prompt=%q", runID, providerName, providerCfg.Model, req.Message)
	session.debugf("resolved enabled_tools=%v pinned_tools=%v revealed_tools=%v", runtime.enabledToolIDs(), runtime.pinnedToolIDs(), runtime.revealedToolIDs())

	trace.Metadata = session.metadata()
	trace.Metadata.Tools = runtime.toolState()
	trace.Todos = runtime.todoState()
	builder := newTraceBuilder(trace)
	builder.AddUserMessage(req.Message)
	builder.trace.Metadata = session.metadata()
	builder.trace.Metadata.Tools = runtime.toolState()
	builder.trace.Todos = runtime.todoState()
	runtime.compactionTrace = &builder.trace

	trace, rawTrace, err := builder.Build(now)
	if err != nil {
		return runID, err
	}

	if strings.TrimSpace(req.RunID) == "" {
		if err := store.CreateAgentRun(ctx, knowledge.CreateAgentRunInput{
			ID:           runID,
			Title:        trace.Title,
			Model:        trace.Model,
			Provider:     trace.Provider,
			Prompt:       req.Message,
			ParentRunID:  trace.Metadata.ParentRunID,
			RootRunID:    trace.Metadata.RootRunID,
			TaskName:     trace.Metadata.TaskName,
			RunKind:      trace.Metadata.RunKind,
			Status:       AgentStatusRunning,
			MessageCount: len(trace.Messages),
			Trace:        rawTrace,
		}); err != nil {
			return "", err
		}
	} else {
		if err := store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
			ID:                  runID,
			Title:               trace.Title,
			Model:               trace.Model,
			Provider:            trace.Provider,
			ParentRunID:         trace.Metadata.ParentRunID,
			RootRunID:           trace.Metadata.RootRunID,
			TaskName:            trace.Metadata.TaskName,
			RunKind:             trace.Metadata.RunKind,
			Status:              AgentStatusRunning,
			MessageCount:        len(trace.Messages),
			PreserveRuntimeMeta: true,
			Trace:               rawTrace,
		}); err != nil {
			return "", err
		}
	}
	session.debugf("persisted run status=%s run_id=%s", AgentStatusRunning, runID)

	lastProgressPersist := time.Now()
	persistProgress := func(force bool) {
		if !force && time.Since(lastProgressPersist) < 500*time.Millisecond {
			return
		}
		builder.trace.Metadata = session.metadata()
		builder.trace.Metadata.Tools = runtime.toolState()
		builder.trace.Todos = runtime.todoState()
		progressTrace, progressRawTrace, buildErr := builder.Snapshot(time.Now().UTC())
		if buildErr != nil {
			session.debugf("persist progress snapshot failed run_id=%s error=%v", runID, buildErr)
			return
		}
		updateErr := store.UpdateAgentRun(context.Background(), knowledge.UpdateAgentRunInput{
			ID:                  runID,
			Title:               progressTrace.Title,
			Model:               providerCfg.Model,
			Provider:            providerName,
			ParentRunID:         progressTrace.Metadata.ParentRunID,
			RootRunID:           progressTrace.Metadata.RootRunID,
			TaskName:            progressTrace.Metadata.TaskName,
			RunKind:             progressTrace.Metadata.RunKind,
			Status:              AgentStatusRunning,
			MessageCount:        len(progressTrace.Messages),
			InputTokens:         progressTrace.Usage.InputTokens,
			OutputTokens:        progressTrace.Usage.OutputTokens,
			PreserveRuntimeMeta: true,
			Trace:               progressRawTrace,
		})
		if updateErr != nil {
			session.debugf("persist progress update failed run_id=%s error=%v", runID, updateErr)
			return
		}
		lastProgressPersist = time.Now()
	}

	if opts.OnChunk != nil {
		if err := opts.OnChunk(initialChunk(runID, providerCfg.Model)); err != nil {
			return runID, err
		}
	}

	var agentOpts []fantasy.AgentOption
	agentOpts = append(agentOpts, fantasy.WithSystemPrompt(effectiveSystemPromptForRunContextAt(time.Now(), trace.SystemPrompt, session.metadata())))
	agentOpts = append(agentOpts, fantasy.WithTools(runtime.activeTools()...))

	start := time.Now()
	toolCallTimes := map[string]time.Time{}

	// Loop-guard: detect consecutive identical tool errors.
	const toolFailureEscalationThreshold = 3
	var lastFailureTool string
	var lastFailureFingerprint string
	var lastFailureCount int
	streamCall := fantasy.AgentStreamCall{
		Prompt:          req.Message,
		Messages:        history,
		ProviderOptions: providerOptions,
		PrepareStep:     runtime.prepareStep,
		OnTextDelta: func(id, text string) error {
			builder.OnTextDelta(text)
			persistProgress(false)
			if opts.Out != nil {
				if _, err := io.WriteString(opts.Out, text); err != nil {
					return err
				}
			}
			if opts.OnChunk != nil {
				return opts.OnChunk(contentChunk(runID, providerCfg.Model, text))
			}
			return nil
		},
		OnReasoningDelta: func(id, text string) error {
			builder.OnReasoningDelta(text)
			persistProgress(false)
			if opts.Status != nil {
				if _, err := io.WriteString(opts.Status, "\033[2m"+text+"\033[0m"); err != nil {
					return err
				}
			}
			if opts.OnChunk != nil {
				return opts.OnChunk(reasoningChunk(runID, providerCfg.Model, text))
			}
			return nil
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			callTime := time.Now()
			toolCallTimes[tc.ToolCallID] = callTime
			session.debugf("tool_call id=%s name=%s elapsed=%s at=%s input=%s",
				tc.ToolCallID, tc.ToolName,
				formatElapsed(time.Since(start)),
				callTime.Format("15:04:05.000"),
				tc.Input,
			)
			builder.OnToolCall(tc)
			persistProgress(true)
			if opts.OnChunk != nil {
				return opts.OnChunk(toolCallChunk(runID, providerCfg.Model, ChunkToolCall{
					ID:   tc.ToolCallID,
					Type: "function",
					Function: &StoredToolFunction{
						Name:      tc.ToolName,
						Arguments: tc.Input,
					},
				}))
			}
			return nil
		},
		OnToolResult: func(tr fantasy.ToolResultContent) error {
			resultTime := time.Now()
			if callTime, ok := toolCallTimes[tr.ToolCallID]; ok {
				session.debugf("tool_result id=%s name=%s duration=%s elapsed=%s",
					tr.ToolCallID, tr.ToolName,
					formatElapsed(resultTime.Sub(callTime)),
					formatElapsed(resultTime.Sub(start)),
				)
				delete(toolCallTimes, tr.ToolCallID)
			} else {
				session.debugf("tool_result id=%s name=%s elapsed=%s", tr.ToolCallID, tr.ToolName, formatElapsed(resultTime.Sub(start)))
			}
			// Build event first to check for errors.
			evt := ToolResultEvent{ToolCallID: tr.ToolCallID, ToolName: tr.ToolName}
			if text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](tr.Result); ok {
				evt.Output = text.Text
			} else if errText, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](tr.Result); ok {
				evt.IsError = true
				if errText.Error != nil {
					evt.Output = errText.Error.Error()
				}
			}

			// Loop-guard: track consecutive identical errors and append
			// an escalation message so the model sees it on the next turn.
			if evt.IsError {
				fp := toolFailureFingerprint(evt.Output)
				if evt.ToolName == lastFailureTool && fp == lastFailureFingerprint {
					lastFailureCount++
				} else {
					lastFailureTool = evt.ToolName
					lastFailureFingerprint = fp
					lastFailureCount = 1
				}
				if lastFailureCount >= toolFailureEscalationThreshold {
					escalation := "\n[nalvin] this tool returned the same error " +
						fmt.Sprintf("%d", lastFailureCount) +
						" times consecutively; pick a different tool, adjust inputs, or report back to the user."
					evt.Output += escalation
					tr.Result = fantasy.ToolResultOutputContentError{Error: fmt.Errorf("%s", evt.Output)}
				}
			} else {
				lastFailureCount = 0
			}

			builder.OnToolResult(tr)
			builder.trace.Metadata = session.metadata()
			builder.trace.Metadata.Tools = runtime.toolState()
			builder.trace.Todos = runtime.todoState()
			persistProgress(true)
			metadata := parseToolResultClientMetadata(tr.ClientMetadata)
			if metadata.ToolOutputRef != nil {
				ref := *metadata.ToolOutputRef
				evt.OutputRef = &ref
			}
			if metadata.PlanRef != nil {
				ref := *metadata.PlanRef
				evt.PlanRef = &ref
			}
			if opts.OnToolResult != nil {
				if err := opts.OnToolResult(evt); err != nil {
					return err
				}
			}
			if shouldTerminateAfterToolResult(evt) {
				return errPlanExitComplete
			}
			return nil
		},
	}

	result, err := fantasy.NewAgent(model, agentOpts...).Stream(ctx, streamCall)

	// Reactive overflow recovery: if context is too large, attempt aggressive
	// compaction once and retry.
	if err != nil && runtime.compaction != nil {
		if providerErr := asContextTooLargeError(err); providerErr != nil {
			session.debugf("context too large (used=%d max=%d), attempting reactive compaction",
				providerErr.ContextUsedTokens, providerErr.ContextMaxTokens)

			// Snapshot builder state before compaction.
			builder.FlushAssistant()
			storedMsgs := builder.trace.Messages
			record, compactErr := runtime.compaction.compact(ctx, storedMsgs, true)
			if compactErr == nil {
				builder.trace.Compactions = append(builder.trace.Compactions, *record)

				// Rebuild history from the full trace and retry.
				retryHistory := traceToFantasyMessages(builder.trace)
				streamCall.Prompt = ""
				streamCall.Messages = retryHistory

				session.debugf("retrying after reactive compaction")
				result, err = fantasy.NewAgent(model, agentOpts...).Stream(ctx, streamCall)
			} else {
				session.debugf("reactive compaction failed: %v", compactErr)
			}
		}
	}
	if errors.Is(err, errPlanExitComplete) {
		err = nil
	}
	if err != nil {
		status := AgentStatusFailed
		if ctx.Err() != nil {
			status = AgentStatusAborted
		}
		if result != nil {
			builder.SetUsage(result.TotalUsage)
		}
		builder.trace.Metadata = session.metadata()
		builder.trace.Metadata.Tools = runtime.toolState()
		builder.trace.Todos = runtime.todoState()
		trace, rawTrace, buildErr := builder.Build(time.Now().UTC())
		if buildErr == nil {
			updateErr := store.UpdateAgentRun(context.Background(), knowledge.UpdateAgentRunInput{
				ID:                  runID,
				Title:               trace.Title,
				Model:               providerCfg.Model,
				Provider:            providerName,
				ParentRunID:         trace.Metadata.ParentRunID,
				RootRunID:           trace.Metadata.RootRunID,
				TaskName:            trace.Metadata.TaskName,
				RunKind:             trace.Metadata.RunKind,
				Status:              status,
				Error:               err.Error(),
				DurationMs:          int(time.Since(start).Milliseconds()),
				MessageCount:        len(trace.Messages),
				InputTokens:         trace.Usage.InputTokens,
				OutputTokens:        trace.Usage.OutputTokens,
				PreserveRuntimeMeta: true,
				Trace:               rawTrace,
			})
			if updateErr != nil && opts.Status != nil {
				_, _ = io.WriteString(opts.Status, fmt.Sprintf("\nfailed to persist agent run: %v\n", updateErr))
			}
		}
		session.debugf("run finished status=%s duration=%s error=%v", status, formatElapsed(time.Since(start)), err)
		if opts.OnChunk != nil {
			_ = opts.OnChunk(errorChunk(runID, providerCfg.Model, err))
			_ = opts.OnChunk(finishChunk(runID, providerCfg.Model, status))
		}
		return runID, fmt.Errorf("run agent: %w", err)
	}
	if result != nil {
		builder.SetUsage(result.TotalUsage)
	}
	if opts.Out != nil {
		_, _ = io.WriteString(opts.Out, "\n")
	}

	builder.trace.Metadata = session.metadata()
	builder.trace.Metadata.Tools = runtime.toolState()
	builder.trace.Todos = runtime.todoState()
	trace, rawTrace, err = builder.Build(time.Now().UTC())
	if err != nil {
		return runID, err
	}
	if err := store.UpdateAgentRun(ctx, knowledge.UpdateAgentRunInput{
		ID:                  runID,
		Title:               trace.Title,
		Model:               providerCfg.Model,
		Provider:            providerName,
		ParentRunID:         trace.Metadata.ParentRunID,
		RootRunID:           trace.Metadata.RootRunID,
		TaskName:            trace.Metadata.TaskName,
		RunKind:             trace.Metadata.RunKind,
		Status:              AgentStatusCompleted,
		DurationMs:          int(time.Since(start).Milliseconds()),
		MessageCount:        len(trace.Messages),
		InputTokens:         trace.Usage.InputTokens,
		OutputTokens:        trace.Usage.OutputTokens,
		PreserveRuntimeMeta: true,
		Trace:               rawTrace,
	}); err != nil {
		return runID, err
	}
	session.debugf("run finished status=%s duration=%s input_tokens=%d output_tokens=%d", AgentStatusCompleted, formatElapsed(time.Since(start)), trace.Usage.InputTokens, trace.Usage.OutputTokens)
	if opts.OnChunk != nil {
		if err := opts.OnChunk(finishChunk(runID, providerCfg.Model, "stop")); err != nil {
			return runID, err
		}
	}

	return runID, nil
}

func newFantasyProvider(cfg ProviderConfig) (fantasy.Provider, error) {
	switch cfg.Type {
	case ProviderTypeAnthropic:
		opts := []anthropic.Option{
			anthropic.WithAPIKey(cfg.APIKey),
		}
		if strings.TrimSpace(cfg.BaseURL) != "" {
			opts = append(opts, anthropic.WithBaseURL(cfg.BaseURL))
		}
		if strings.TrimSpace(cfg.UserAgentOverride) != "" {
			opts = append(opts, anthropic.WithUserAgent(cfg.UserAgentOverride))
		}
		return anthropic.New(opts...)
	case ProviderTypeOpenAI:
		opts := []openai.Option{
			openai.WithAPIKey(cfg.APIKey),
			openai.WithUseResponsesAPI(),
		}
		if strings.TrimSpace(cfg.BaseURL) != "" {
			opts = append(opts, openai.WithBaseURL(cfg.BaseURL))
		}
		if strings.TrimSpace(cfg.UserAgentOverride) != "" {
			opts = append(opts, openai.WithUserAgent(cfg.UserAgentOverride))
		}
		return openai.New(opts...)
	case ProviderTypeOpenAICompat:
		opts := []openaicompat.Option{
			openaicompat.WithBaseURL(cfg.BaseURL),
			openaicompat.WithAPIKey(cfg.APIKey),
		}
		if strings.TrimSpace(cfg.UserAgentOverride) != "" {
			opts = append(opts, openaicompat.WithUserAgent(cfg.UserAgentOverride))
		}
		return openaicompat.New(opts...)
	default:
		return nil, fmt.Errorf("unsupported provider type %q", cfg.Type)
	}
}

func fantasyProviderOptions(cfg ProviderConfig) (fantasy.ProviderOptions, error) {
	effort := configpkg.NormalizeProviderReasoningEffort(cfg.ReasoningEffort)
	if effort == "" {
		return nil, nil
	}
	if err := configpkg.ValidateProviderReasoningEffort(cfg.Type, effort); err != nil {
		return nil, err
	}

	switch cfg.Type {
	case ProviderTypeAnthropic:
		anthropicEffort := anthropic.Effort(effort)
		return anthropic.NewProviderOptions(&anthropic.ProviderOptions{
			Effort: &anthropicEffort,
		}), nil
	case ProviderTypeOpenAI:
		openAIEffort := openai.ReasoningEffort(effort)
		if openai.IsResponsesModel(cfg.Model) {
			return openai.NewResponsesProviderOptions(&openai.ResponsesProviderOptions{
				ReasoningEffort: &openAIEffort,
			}), nil
		}
		return openai.NewProviderOptions(&openai.ProviderOptions{
			ReasoningEffort: &openAIEffort,
		}), nil
	case ProviderTypeOpenAICompat:
		openAIEffort := openai.ReasoningEffort(effort)
		return openaicompat.NewProviderOptions(&openaicompat.ProviderOptions{
			ReasoningEffort: &openAIEffort,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q", cfg.Type)
	}
}

func MarshalChunk(chunk TraceChunk) ([]byte, error) {
	return json.Marshal(chunk)
}

// toolFailureFingerprint computes a short fingerprint of a tool error output
// for loop-guard deduplication. It lowercases, collapses whitespace, and
// truncates to 200 bytes.
func toolFailureFingerprint(output string) string {
	fp := strings.ToLower(strings.TrimSpace(output))
	fp = strings.Join(strings.Fields(fp), " ")
	if len(fp) > 200 {
		fp = fp[:200]
	}
	return fp
}

// asContextTooLargeError extracts a fantasy.ProviderError from an error chain
// and returns it only if it signals a context-too-large condition.
func asContextTooLargeError(err error) *fantasy.ProviderError {
	var providerErr *fantasy.ProviderError
	if errors.As(err, &providerErr) && providerErr.IsContextTooLarge() {
		return providerErr
	}
	return nil
}
