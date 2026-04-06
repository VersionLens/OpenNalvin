package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

// compactionManager tracks compaction state for a single agent run and decides
// when to compact old conversation history into a checkpoint summary.
type compactionManager struct {
	cfg              configpkg.AgentCompactionConfig
	fullCfg          configpkg.Config
	contextWindow    int // from provider config; 0 = proactive compaction disabled
	providerOptions  fantasy.ProviderOptions
	counter          textTokenCounter
	checkpoint       *compactionCheckpoint
	compactionCount  int
	lastCompactedIdx int // index in Messages up to which compaction has been applied
	model            fantasy.LanguageModel
	originalSystem   string
	debug            func(string, ...any)
}

// compactionCheckpoint represents the current compacted state of old messages.
type compactionCheckpoint struct {
	Summary       CompactionSummary
	CoveredCount  int   // how many messages from the start are covered
	SummaryTokens int64 // estimated tokens in the rendered checkpoint text
	ToolOutputIDs []string
}

func newCompactionManager(
	cfg configpkg.AgentCompactionConfig,
	fullCfg configpkg.Config,
	contextWindow int,
	model fantasy.LanguageModel,
	systemPrompt string,
	providerOptions fantasy.ProviderOptions,
	counter textTokenCounter,
	debugFn func(string, ...any),
) *compactionManager {
	if debugFn == nil {
		debugFn = func(string, ...any) {}
	}
	return &compactionManager{
		cfg:             cfg,
		fullCfg:         fullCfg,
		contextWindow:   contextWindow,
		providerOptions: providerOptions,
		counter:         counter,
		model:           model,
		originalSystem:  systemPrompt,
		debug:           debugFn,
	}
}

// restoreCheckpoint restores compaction state from a persisted trace, so that
// resumed runs reuse the existing checkpoint instead of re-summarizing.
func (cm *compactionManager) restoreCheckpoint(trace StoredTrace) {
	if len(trace.Compactions) == 0 {
		return
	}
	last := trace.Compactions[len(trace.Compactions)-1]
	cm.checkpoint = &compactionCheckpoint{
		Summary:      last.Summary,
		CoveredCount: last.CoveredMessageCount,
	}
	cm.lastCompactedIdx = last.CoveredMessageCount
	cm.compactionCount = len(trace.Compactions)

	// Collect referenced tool output IDs.
	for _, ref := range last.Summary.ToolOutputs {
		cm.checkpoint.ToolOutputIDs = append(cm.checkpoint.ToolOutputIDs, ref.OutputID)
	}

	rendered := cm.renderCheckpointBlock(last.Summary)
	cm.checkpoint.SummaryTokens = cm.countTokens(rendered)
	cm.debug("restored compaction checkpoint covered_messages=%d compaction_count=%d", last.CoveredMessageCount, cm.compactionCount)
}

// needsCompaction checks whether compaction should be triggered proactively
// based on estimated token usage versus the context window.
func (cm *compactionManager) needsCompaction(messages []StoredMessage) bool {
	if !cm.cfg.Enabled || cm.contextWindow <= 0 || cm.counter == nil {
		return false
	}
	if cm.compactionCount >= cm.cfg.MaxCompactionsPerRun {
		return false
	}
	if len(messages) <= cm.cfg.KeepRecentMessages {
		return false
	}

	estimated := cm.estimatePromptTokens(messages)
	threshold := int64(cm.contextWindow) * int64(cm.cfg.TriggerPct) / 100
	needs := estimated > threshold
	if needs {
		cm.debug("compaction needed: estimated=%d threshold=%d context_window=%d", estimated, threshold, cm.contextWindow)
	}
	return needs
}

// compact performs compaction of old messages, returning the updated compaction
// record for the trace. It uses the same configured model with no tools to
// generate a structured summary.
func (cm *compactionManager) compact(ctx context.Context, messages []StoredMessage, aggressive bool) (*StoredCompaction, error) {
	if !cm.cfg.Enabled {
		return nil, fmt.Errorf("compaction is disabled")
	}
	if cm.compactionCount >= cm.cfg.MaxCompactionsPerRun {
		return nil, fmt.Errorf("max compactions per run reached (%d)", cm.cfg.MaxCompactionsPerRun)
	}

	keepRecent := cm.cfg.KeepRecentMessages
	if aggressive {
		keepRecent = cm.cfg.AggressiveKeepRecentMessages
	}
	if len(messages) <= keepRecent {
		return nil, fmt.Errorf("not enough messages to compact (have %d, keep %d)", len(messages), keepRecent)
	}

	splitIdx := findCompactionSplitPoint(messages, keepRecent)
	if splitIdx <= 0 {
		return nil, fmt.Errorf("no valid compaction split point found")
	}
	compactable := messages[:splitIdx]

	// Estimate pre-compaction tokens for the compactable prefix.
	preTokens := cm.estimateMessagesTokens(compactable)

	// Build the compaction prompt.
	prompt := cm.buildCompactionPrompt(compactable, aggressive)

	trigger := "proactive"
	if aggressive {
		trigger = "reactive"
	}
	cm.debug("compacting %s: compactable_messages=%d keep_recent=%d pre_tokens=%d", trigger, len(compactable), keepRecent, preTokens)

	// Call the model with no tools to generate the summary.
	summary, err := cm.generateSummary(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("generate compaction summary: %w", err)
	}

	// Filter the files list to only include paths that actually exist on disk.
	// This prevents the agent from "seeing" files that were mentioned in tool
	// output but never actually written (e.g. due to a failed shell script).
	summary.Files = cm.filterExistingFiles(ctx, summary.Files)

	// Collect tool output references from compacted messages.
	summary.ToolOutputs = cm.collectToolOutputRefs(compactable)

	// If we have a previous checkpoint, merge: the new summary subsumes it.
	if cm.checkpoint != nil {
		// Carry forward any tool output refs from the previous checkpoint that
		// aren't already in the new summary.
		existing := make(map[string]struct{})
		for _, ref := range summary.ToolOutputs {
			existing[ref.OutputID] = struct{}{}
		}
		for _, ref := range cm.checkpoint.Summary.ToolOutputs {
			if _, ok := existing[ref.OutputID]; !ok {
				summary.ToolOutputs = append(summary.ToolOutputs, ref)
			}
		}
	}

	rendered := cm.renderCheckpointBlock(*summary)
	postTokens := cm.countTokens(rendered)

	cm.checkpoint = &compactionCheckpoint{
		Summary:       *summary,
		CoveredCount:  splitIdx,
		SummaryTokens: postTokens,
	}
	for _, ref := range summary.ToolOutputs {
		cm.checkpoint.ToolOutputIDs = append(cm.checkpoint.ToolOutputIDs, ref.OutputID)
	}
	cm.lastCompactedIdx = splitIdx
	cm.compactionCount++

	record := &StoredCompaction{
		ID:                  uuid.NewString(),
		CreatedAt:           time.Now().UTC(),
		Trigger:             trigger,
		CoveredMessageCount: splitIdx,
		PreTokens:           preTokens,
		PostTokens:          postTokens,
		Summary:             *summary,
	}
	cm.debug("compaction complete: id=%s covered=%d pre=%d post=%d", record.ID, splitIdx, preTokens, postTokens)
	return record, nil
}

// applyToStep modifies the PrepareStepResult to reflect compaction state:
// replaces the system prompt with original + checkpoint, and rebuilds the
// message list from the stored trace's uncompacted tail. We rebuild from
// the stored trace rather than trimming fantasy messages because the
// StoredMessage count may not map 1:1 to the fantasy message list.
func (cm *compactionManager) applyToStep(storedMessages []StoredMessage) ([]fantasy.Message, *string) {
	if cm.checkpoint == nil || cm.checkpoint.CoveredCount == 0 {
		return nil, nil
	}

	// Rebuild tail from stored messages, skipping the compacted prefix.
	startIdx := cm.checkpoint.CoveredCount
	if startIdx >= len(storedMessages) {
		startIdx = len(storedMessages)
	}
	tailTrace := StoredTrace{Messages: storedMessages[startIdx:]}
	tail := traceToFantasyMessages(tailTrace)

	// The fantasy agent loop always replaces messages[0] with the system
	// message. We must account for this by placing a system placeholder at
	// index 0, followed by a user continuation message (providers require
	// conversations to start with a user message after the system prompt).
	augmented := cm.augmentedSystemPrompt()
	rebuilt := []fantasy.Message{
		fantasy.NewSystemMessage(augmented),
		fantasy.NewUserMessage("Continue the task described in the conversation checkpoint above."),
	}
	rebuilt = append(rebuilt, tail...)
	return rebuilt, &augmented
}

// augmentedSystemPrompt returns the original system prompt with the compaction
// checkpoint block appended.
func (cm *compactionManager) augmentedSystemPrompt() string {
	if cm.checkpoint == nil {
		return cm.originalSystem
	}
	block := cm.renderCheckpointBlock(cm.checkpoint.Summary)
	if cm.originalSystem == "" {
		return block
	}
	return cm.originalSystem + "\n\n" + block
}

// estimatePromptTokens estimates the total token count for the current prompt
// shape including system prompt + checkpoint + all messages. It uses the
// counter directly rather than relying on pre-computed EstimatedTokens fields,
// because the builder's trace messages may not be enriched yet.
func (cm *compactionManager) estimatePromptTokens(messages []StoredMessage) int64 {
	var total int64

	// System prompt.
	if cm.checkpoint != nil {
		total += cm.countTokens(cm.augmentedSystemPrompt())
	} else {
		total += cm.countTokens(cm.originalSystem)
	}

	// Messages (only the non-compacted tail if checkpoint exists).
	startIdx := 0
	if cm.checkpoint != nil {
		startIdx = cm.checkpoint.CoveredCount
	}
	for i := startIdx; i < len(messages); i++ {
		total += cm.estimateMessageTokens(&messages[i])
	}

	return total
}

// estimateMessagesTokens estimates tokens for a slice of stored messages.
func (cm *compactionManager) estimateMessagesTokens(messages []StoredMessage) int64 {
	var total int64
	for i := range messages {
		total += cm.estimateMessageTokens(&messages[i])
	}
	return total
}

// estimateMessageTokens computes the token count for a single message using
// the counter directly.
func (cm *compactionManager) estimateMessageTokens(msg *StoredMessage) int64 {
	switch msg.Role {
	case "user":
		return cm.countTokens(msg.Content)
	case "assistant":
		n := cm.countTokens(msg.Content) + cm.countTokens(msg.Reasoning)
		for _, tc := range msg.ToolCalls {
			if tc.Function != nil {
				n += cm.countTokens(tc.Function.Name) + cm.countTokens(tc.Function.Arguments)
			}
		}
		return n
	case "tool":
		text := msg.ContentText
		if text == "" && msg.ContentJSON != nil {
			if b, err := json.Marshal(msg.ContentJSON); err == nil {
				text = string(b)
			}
		}
		return cm.countTokens(text)
	}
	return 0
}

func (cm *compactionManager) countTokens(text string) int64 {
	if cm.counter == nil {
		return 0
	}
	return cm.counter.Count(text)
}

// generateSummary calls the model with a compaction prompt and parses the
// structured summary from the response.
func (cm *compactionManager) generateSummary(ctx context.Context, prompt string) (*CompactionSummary, error) {
	if cm.model == nil {
		return nil, fmt.Errorf("no language model configured for compaction")
	}

	maxTokens := int64(cm.cfg.MaxSummaryTokens)
	result, err := cm.model.Generate(ctx, fantasy.Call{
		Prompt: fantasy.Prompt{
			fantasy.NewSystemMessage(prompt),
			fantasy.NewUserMessage("Produce the compaction summary now as a single JSON object. Do not include any text outside the JSON."),
		},
		MaxOutputTokens: &maxTokens,
		ProviderOptions: cm.providerOptions,
	})
	if err != nil {
		return nil, err
	}

	responseText := result.Content.Text()

	// Extract JSON from response (may be wrapped in markdown code fences).
	jsonStr := extractJSON(responseText)
	var summary CompactionSummary
	if err := json.Unmarshal([]byte(jsonStr), &summary); err != nil {
		return nil, fmt.Errorf("parse compaction summary JSON: %w (response: %s)", err, truncateForLog(responseText, 200))
	}
	return &summary, nil
}

// buildCompactionPrompt creates the system prompt for the compaction model call.
func (cm *compactionManager) buildCompactionPrompt(messages []StoredMessage, aggressive bool) string {
	var b strings.Builder
	b.WriteString("You are a conversation compaction agent. Your job is to read the conversation history below and produce a structured JSON summary that captures all important context.\n\n")

	if cm.checkpoint != nil {
		b.WriteString("## Previous Checkpoint\n")
		b.WriteString("A previous compaction checkpoint exists. Merge the new messages into this existing summary, preserving anything still relevant:\n\n")
		prev, _ := json.MarshalIndent(cm.checkpoint.Summary, "", "  ")
		b.WriteString("```json\n")
		b.Write(prev)
		b.WriteString("\n```\n\n")
	}

	b.WriteString("## Conversation History to Compact\n\n")
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			b.WriteString("**User**: ")
			b.WriteString(truncateForLog(msg.Content, 2000))
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("**Assistant**: ")
			b.WriteString(truncateForLog(msg.Content, 2000))
			if len(msg.ToolCalls) > 0 {
				b.WriteString("\n  Tool calls: ")
				for i, tc := range msg.ToolCalls {
					if i > 0 {
						b.WriteString(", ")
					}
					if tc.Function != nil {
						b.WriteString(tc.Function.Name)
					}
				}
			}
			b.WriteString("\n\n")
		case "tool":
			b.WriteString("**Tool Result** (")
			b.WriteString(msg.ToolName)
			b.WriteString("): ")
			content := msg.ContentText
			if msg.ToolOutputRef != nil {
				b.WriteString(fmt.Sprintf("[spilled output_id=%s, %d bytes] ", msg.ToolOutputRef.OutputID, msg.ToolOutputRef.SizeBytes))
				content = truncateForLog(content, 500)
			} else {
				content = truncateForLog(content, 1000)
			}
			b.WriteString(content)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Output Format\n\n")
	b.WriteString("Respond with a single JSON object with these fields:\n")
	b.WriteString("- `goal` (string): The user's primary goal or task.\n")
	b.WriteString("- `constraints` (string[]): Any constraints, requirements, or preferences stated.\n")
	b.WriteString("- `decisions` (string[]): Key decisions made during the conversation.\n")
	b.WriteString("- `completed` (string[]): Tasks or steps already completed.\n")
	b.WriteString("- `open` (string[]): Tasks or questions still open.\n")
	b.WriteString("- `files` (string[]): File paths that were read, created, or modified.\n")

	if aggressive {
		b.WriteString("\nBe concise. Keep each array to the most critical items only.\n")
	} else {
		b.WriteString("\nBe thorough but concise. Capture all important context without unnecessary verbosity.\n")
	}

	return b.String()
}

// collectToolOutputRefs finds spilled tool output references in the compacted
// message range so they can be carried forward in the checkpoint.
func (cm *compactionManager) collectToolOutputRefs(messages []StoredMessage) []CompactionToolOutputRef {
	var refs []CompactionToolOutputRef
	seen := make(map[string]struct{})
	for _, msg := range messages {
		if msg.ToolOutputRef == nil {
			continue
		}
		if _, ok := seen[msg.ToolOutputRef.OutputID]; ok {
			continue
		}
		seen[msg.ToolOutputRef.OutputID] = struct{}{}
		refs = append(refs, CompactionToolOutputRef{
			OutputID:     msg.ToolOutputRef.OutputID,
			ToolName:     msg.ToolName,
			WhyItMatters: fmt.Sprintf("Output from %s (tool call %s)", msg.ToolName, msg.ToolCallID),
		})
	}
	return refs
}

// filterExistingFiles filters a list of workspace-relative file paths to only
// those that actually exist on disk. This keeps the compaction checkpoint honest:
// if a tool call claimed to write a file but the write was lost (e.g. due to a
// failed shell script), the file is dropped so the agent does not act on phantom state.
// If workspace paths cannot be resolved the original list is returned unchanged.
func (cm *compactionManager) filterExistingFiles(ctx context.Context, files []string) []string {
	if len(files) == 0 {
		return files
	}
	paths, err := workspacepkg.ActivePaths(ctx, cm.fullCfg)
	if err != nil {
		return files
	}
	result := files[:0:0]
	for _, f := range files {
		abs := filepath.Join(paths.FilesPath, filepath.FromSlash(filepath.Clean(f)))
		if _, statErr := os.Stat(abs); statErr == nil {
			result = append(result, f)
		}
	}
	return result
}

// renderCheckpointBlock produces the text block injected into the system prompt
// after compaction.
func (cm *compactionManager) renderCheckpointBlock(summary CompactionSummary) string {
	var b strings.Builder
	b.WriteString("## Conversation Checkpoint\n\n")
	b.WriteString("The earlier portion of this conversation has been compacted into the summary below. The full transcript is preserved for audit but is not shown to you.\n\n")

	b.WriteString("**Goal**: ")
	b.WriteString(summary.Goal)
	b.WriteString("\n\n")

	if len(summary.Constraints) > 0 {
		b.WriteString("**Constraints**:\n")
		for _, c := range summary.Constraints {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Decisions) > 0 {
		b.WriteString("**Decisions Made**:\n")
		for _, d := range summary.Decisions {
			b.WriteString("- ")
			b.WriteString(d)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Completed) > 0 {
		b.WriteString("**Completed**:\n")
		for _, c := range summary.Completed {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Open) > 0 {
		b.WriteString("**Open Tasks**:\n")
		for _, o := range summary.Open {
			b.WriteString("- ")
			b.WriteString(o)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(summary.Files) > 0 {
		b.WriteString("**Files Touched**:\n")
		for _, f := range summary.Files {
			b.WriteString("- ")
			b.WriteString(f)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(summary.ToolOutputs) > 0 {
		b.WriteString("**Stored Tool Outputs** (use `view_tool_output` or `grep_tool_output` to inspect):\n")
		for _, ref := range summary.ToolOutputs {
			b.WriteString(fmt.Sprintf("- `%s` (%s): %s\n", ref.OutputID, ref.ToolName, ref.WhyItMatters))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// findCompactionSplitPoint finds the best index to split messages for compaction.
// It ensures the split does not land inside an assistant+tool-result group.
// The tail (messages[splitIdx:]) must start at a valid conversation boundary:
// either a user message or an assistant message (not a bare tool result).
func findCompactionSplitPoint(messages []StoredMessage, keepRecent int) int {
	if len(messages) <= keepRecent {
		return 0
	}
	target := len(messages) - keepRecent

	// Walk backward from target to find a clean boundary.
	for idx := target; idx > 0; idx-- {
		role := messages[idx].Role
		// A user message is always a valid split boundary.
		if role == "user" {
			return idx
		}
		// An assistant message is valid (it starts a new turn).
		if role == "assistant" {
			return idx
		}
	}

	// Walk forward from target if we couldn't find a good boundary behind.
	for idx := target + 1; idx < len(messages); idx++ {
		role := messages[idx].Role
		if role == "user" || role == "assistant" {
			return idx
		}
	}

	return 0
}

// extractJSON extracts a JSON object from text that may contain markdown fences.
func extractJSON(text string) string {
	text = strings.TrimSpace(text)

	// Try to find JSON within code fences.
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if len(candidate) > 0 && candidate[0] == '{' {
				return candidate
			}
		}
	}

	// Try to find raw JSON object.
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			return text[start : end+1]
		}
	}

	return text
}

// truncateForLog truncates a string to maxLen runes, appending "..." if truncated.
func truncateForLog(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
