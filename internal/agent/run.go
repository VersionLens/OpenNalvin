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
	"charm.land/fantasy/providers/google"
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
const baseSystemPrompt = `You are a capable AI assistant. You have access to many tools, but most are hidden until you discover them with search_tools.

HOW TO WORK
1. Break the task into steps. If there are 2+ steps, call todowrite first.
2. Discover ALL tools you will need before starting work. Call search_tools once for each category (e.g. "file write", "shell"). Do all discovery up front.
3. Do the work. Use the discovered tools to complete each step. Do not stop between steps.
4. Update your todo list after completing a group of related steps — not after every single tool call.
5. Before finishing, review the original request. Complete ALL steps. Do not stop early.

EXAMPLE WORKFLOW
User: "Create files a.txt and b.txt, then use shell to combine them into c.txt"
  todowrite → [{content: "Create files", status: "in_progress"}, {content: "Combine with shell", status: "pending"}]
  search_tools("file write") → reveals write
  search_tools("shell") → reveals shell
  write({path: "a.txt", content: "A"})
  write({path: "b.txt", content: "B"})
  shell({script: "cat a.txt b.txt > combined.txt && write --path c.txt --content \"$(cat combined.txt)\""})
  todowrite → [both completed]
Do NOT call todowrite between every tool call. Do the work first, update after.

SHELL TOOL
For loops, pipelines, or multi-step operations, use the shell tool. All revealed tools are available as commands inside it with --flag syntax. Example:
  glob --pattern "*.go" | jq -r '.paths[]' | while read f; do
    grep --pattern "TODO" --path "$f" | jq '.matches | length'
  done

SEARCH TIPS
- For knowledge or research questions, discover "knowledge" tools and web_search_exa.
- For news, headlines, or current events, search for "news" or "reddit" tools first — prefer them over generic web search.
- In containers, use uv instead of pip for Python packages.
`

const parentDelegationSystemPrompt = `
CHILD AGENTS
For tasks with 2+ independent subtasks (e.g. researching separate topics,
comparing alternatives, collecting from multiple sources), use search_tools
to find "child agent" tools and spawn child agents to work in parallel.
Keep synthesis and the final answer in the parent run.
`

const parentTodoSystemPrompt = `
TODO TRACKING
For any task with 2+ steps, call todowrite FIRST to plan your work.
- Each todo item = one concrete step from the user's request.
- Mark items in_progress → completed as you go.
- Update the list after each step.
- Before stopping, check that every item is completed.
On resumed runs, call todoread first to see your existing plan.
`

const newsToolAwarenessSystemPrompt = `
NEWS AND CURRENT EVENTS
For news, headlines, or current events: use search_tools with "news" or
"reddit" to find built-in news tools. Prefer these over generic web search.
If a news tool returns available categories, use those — do not retry with
invalid categories. For broad coverage across multiple categories, consider
using child agents to collect in parallel.
`

const parentNewsCollectionSystemPrompt = ``

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

	// Apply skill-profile resolution when the run has explicitly requested
	// skills (--skill <name>). Profile skills can shape the initial provider
	// and tool defaults of the run.
	if cfg, _ := configpkg.FromContext(ctx); len(req.RequestedSkillNames) > 0 {
		runKindHint := strings.TrimSpace(req.RunKind)
		if runKindHint == "" {
			if strings.TrimSpace(req.ParentRunID) != "" {
				runKindHint = RunKindChild
			} else {
				runKindHint = RunKindRoot
			}
		}
		nextProvider, _, nextTools, _, perr := ResolveSkillProfileRunConfig(ctx, cfg, runKindHint, req.RequestedSkillNames, providerName, req.Tools)
		if perr != nil {
			return "", perr
		}
		if nextProvider != "" {
			providerName = nextProvider
		}
		req.Tools = nextTools
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

	// Activate skills explicitly requested for this run so their bodies and
	// autoreveal_tools take effect from the first step.
	if runtime.skills != nil {
		for _, name := range req.RequestedSkillNames {
			if _, actErr := runtime.skills.activateManual(name); actErr != nil {
				session.debugf("activate requested skill %q: %v", name, actErr)
			}
		}
		runtime.applyActiveSkillAutoreveals()
	}

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
			effectiveSystemPromptForRunContextAt(time.Now(), req.SystemPrompt, session.metadata())+runtime.composeSystemPromptAdditions(),
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
	trace.Metadata.Skills = runtime.activeSkillsState()
	trace.Todos = runtime.todoState()
	builder := newTraceBuilder(trace)
	builder.AddUserMessage(req.Message)
	builder.trace.Metadata = session.metadata()
	builder.trace.Metadata.Tools = runtime.toolState()
	builder.trace.Metadata.Skills = runtime.activeSkillsState()
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
		builder.trace.Metadata.Skills = runtime.activeSkillsState()
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
	systemPrompt := effectiveSystemPromptForRunContextAt(time.Now(), trace.SystemPrompt, session.metadata())
	systemPrompt += runtime.composeSystemPromptAdditions()
	agentOpts = append(agentOpts, fantasy.WithSystemPrompt(systemPrompt))
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
			builder.trace.Metadata.Skills = runtime.activeSkillsState()
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

	// Incomplete-todo recovery: if the model stopped but there are pending or
	// in-progress todo items, nudge it to continue by injecting a user message
	// and re-streaming. This helps local models that emit premature stop tokens.
	const maxTodoContinuations = 5
	for todoRetry := 0; todoRetry < maxTodoContinuations && err == nil && ctx.Err() == nil; todoRetry++ {
		todos := runtime.todoState()
		if len(todos) == 0 {
			break
		}
		hasPending := false
		for _, item := range todos {
			if item.Status == "pending" || item.Status == "in_progress" {
				hasPending = true
				break
			}
		}
		if !hasPending {
			break
		}
		// Build a specific nudge that names the next pending item.
		var nextItem string
		for _, item := range todos {
			if item.Status == "in_progress" || item.Status == "pending" {
				nextItem = item.Content
				break
			}
		}
		nudge := fmt.Sprintf("You stopped before finishing. Your next incomplete todo is: %q. Use search_tools to find any tools you need, then do the work. Do not call todoread — act now.", nextItem)
		session.debugf("todo-continuation retry=%d: nudging with %q", todoRetry+1, nextItem)
		builder.FlushAssistant()
		builder.AddUserMessage(nudge)
		builder.trace.Metadata = session.metadata()
		builder.trace.Metadata.Tools = runtime.toolState()
		builder.trace.Metadata.Skills = runtime.activeSkillsState()
		builder.trace.Todos = runtime.todoState()
		retryHistory := traceToFantasyMessages(builder.trace)
		streamCall.Prompt = ""
		streamCall.Messages = retryHistory
		persistProgress(true)
		result, err = fantasy.NewAgent(model, agentOpts...).Stream(ctx, streamCall)
	}

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
		builder.trace.Metadata.Skills = runtime.activeSkillsState()
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
	builder.trace.Metadata.Skills = runtime.activeSkillsState()
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
	case ProviderTypeVertex:
		opts := []google.Option{
			google.WithVertex(cfg.Project, cfg.Location),
		}
		if strings.TrimSpace(cfg.UserAgentOverride) != "" {
			opts = append(opts, google.WithUserAgent(cfg.UserAgentOverride))
		}
		return google.New(opts...)
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
	case ProviderTypeVertex:
		if effort == "none" {
			return nil, nil
		}
		level := strings.ToUpper(effort)
		return fantasy.ProviderOptions{
			google.Name: &google.ProviderOptions{
				ThinkingConfig: &google.ThinkingConfig{
					ThinkingLevel: &level,
				},
			},
		}, nil
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
