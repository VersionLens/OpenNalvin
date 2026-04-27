package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/versionlens/OpenNalvin/internal/agenttrace"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/output"
)

var (
	agentTracesListStatus   string
	agentTracesListRunKind  string
	agentTracesListContains string
	agentTracesListSince    string
	agentTracesListLimit    int
	agentTracesListOffset   int
	agentTracesCurated      bool

	agentTracesCandidateStatus            string
	agentTracesCandidateRunKind           string
	agentTracesCandidateContains          string
	agentTracesCandidateSince             string
	agentTracesCandidateLimit             int
	agentTracesCandidateOffset            int
	agentTracesCandidateMinMessages       int
	agentTracesCandidateMinAssistantTurns int
	agentTracesCandidateMinToolCalls      int
	agentTracesCandidateMaxToolErrors     int

	agentTracesView string

	agentTracesOutputOffset int
	agentTracesOutputLimit  int
	agentTracesGrepPattern  string
	agentTracesGrepLiteral  bool
	agentTracesGrepLimit    int

	agentTraceTitle string
	agentTraceTags  []string
	agentTraceNote  string

	agentTraceCurateQuality string
	agentTraceCurateReward  float64
	agentTraceCurateSplit   string
	agentTraceCurateTags    []string
	agentTraceCurateNote    string
	agentTraceCurateApprove bool

	agentTraceEditorCommand string
	agentTraceEditor        bool

	agentTraceMsgContent       string
	agentTraceMsgContentFile   string
	agentTraceMsgReasoning     string
	agentTraceMsgReasoningFile string
	agentTraceMsgRole          string
	agentTraceMsgToolCallID    string
	agentTraceMsgToolName      string

	agentTraceRedactPattern     string
	agentTraceRedactReplacement string

	agentTraceLabelQuality string
	agentTraceLabelReward  float64
	agentTraceLabelSplit   string
	agentTraceLabelTags    []string
	agentTraceLabelNote    string

	agentTraceExportFormat             string
	agentTraceExportOut                string
	agentTraceExportCurated            bool
	agentTraceExportStatus             string
	agentTraceExportContains           string
	agentTraceExportSplit              string
	agentTraceExportTags               []string
	agentTraceExportSystem             string
	agentTraceExportReasoning          string
	agentTraceExportMaterializeOutputs bool
	agentTraceExportToolOutputMaxBytes int

	agentTraceBaselineCategory    string
	agentTraceBaselineContains    string
	agentTraceBaselineLimit       int
	agentTraceBaselineProvider    string
	agentTraceBaselineWorkspace   string
	agentTraceBaselineServedModel string
	agentTraceBaselineCommands    bool
)

var agentTracesCmd = &cobra.Command{
	Use:   "traces",
	Short: "Review, curate, and export historical agent traces",
}

var agentTracesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List historical agent traces or curated derivatives",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		w := output.FromContext(cmd.Context())
		if agentTracesCurated {
			curations, err := store.ListAgentTraceCurations(cmd.Context(), knowledge.AgentTraceCurationFilter{
				Status: agentTracesListStatus,
				Limit:  agentTracesListLimit,
				Offset: agentTracesListOffset,
			})
			if err != nil {
				return err
			}
			curations = filterCurations(curations, agentTracesListContains, agentTracesListSince)
			if w.IsJSON() {
				return w.JSON(map[string]any{"curations": summarizeTraceCurations(curations)})
			}
			if len(curations) == 0 {
				w.Line("No curated traces found.")
				return nil
			}
			for _, curation := range curations {
				w.Line("%s  %s  %s  %s", curation.ID, curation.Status, curation.UpdatedAt.Format(time.RFC3339), fallbackText(curation.Title, "(untitled)"))
				w.Line("   source: %s  tags: %s", curation.SourceRunID, strings.Join(curation.Tags, ","))
			}
			return nil
		}

		runs, err := store.ListAgentRuns(cmd.Context(), agentTracesListStatus, agentTracesListLimit, agentTracesListOffset)
		if err != nil {
			return err
		}
		runs = filterRuns(runs, agentTracesListRunKind, agentTracesListContains, agentTracesListSince)
		if w.IsJSON() {
			return w.JSON(map[string]any{"runs": runs})
		}
		if len(runs) == 0 {
			w.Line("No agent traces found.")
			return nil
		}
		for _, run := range runs {
			w.Line("%s  %s  %s  %s  msgs=%d  tokens=%d", run.ID, run.Status, run.RunKind, run.UpdatedAt.Format(time.RFC3339), run.MessageCount, run.TotalTokens)
			w.Line("   %s", fallbackText(run.Title, previewCLI(run.Prompt, 120)))
		}
		return nil
	},
}

var agentTracesCandidatesCmd = &cobra.Command{
	Use:   "candidates",
	Short: "List likely trace review candidates with triage hints",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		runs, err := store.ListAgentRuns(cmd.Context(), agentTracesCandidateStatus, agentTracesCandidateLimit, agentTracesCandidateOffset)
		if err != nil {
			return err
		}
		runs = filterRuns(runs, agentTracesCandidateRunKind, agentTracesCandidateContains, agentTracesCandidateSince)

		candidates := make([]agentTraceCandidateSummary, 0, len(runs))
		for _, run := range runs {
			loaded, err := agenttrace.LoadRun(cmd.Context(), store, run.ID)
			if err != nil {
				continue
			}
			review := agenttrace.BuildReview(cmd.Context(), store, loaded)
			candidate := buildTraceCandidateSummary(run, review)
			if agentTracesCandidateMinMessages > 0 && candidate.Stats.Messages < agentTracesCandidateMinMessages {
				continue
			}
			if agentTracesCandidateMinAssistantTurns > 0 && candidate.Stats.AssistantTurns < agentTracesCandidateMinAssistantTurns {
				continue
			}
			if agentTracesCandidateMinToolCalls > 0 && candidate.Stats.ToolCalls < agentTracesCandidateMinToolCalls {
				continue
			}
			if agentTracesCandidateMaxToolErrors >= 0 && candidate.Stats.ToolErrors > agentTracesCandidateMaxToolErrors {
				continue
			}
			candidates = append(candidates, candidate)
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].Score == candidates[j].Score {
				return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
			}
			return candidates[i].Score > candidates[j].Score
		})

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{"candidates": candidates})
		}
		if len(candidates) == 0 {
			w.Line("No candidate traces found.")
			return nil
		}
		for _, candidate := range candidates {
			w.Line("%s  score=%d  %s  %s  msgs=%d  tools=%d/%d  thinking=%d",
				candidate.ID,
				candidate.Score,
				candidate.Status,
				candidate.RunKind,
				candidate.Stats.Messages,
				candidate.Stats.ToolCalls,
				candidate.Stats.ToolErrors,
				candidate.Stats.ReasoningChars,
			)
			w.Line("   %s", fallbackText(candidate.Title, candidate.FinalAnswerPreview))
			if len(candidate.ReviewHints) > 0 {
				w.Line("   hints: %s", strings.Join(candidate.ReviewHints, "; "))
			}
		}
		return nil
	},
}

var agentTracesShowCmd = &cobra.Command{
	Use:   "show <trace-id>",
	Short: "Show a trace in a review-oriented view",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		loaded, err := agenttrace.Resolve(cmd.Context(), store, args[0], agentTracesCurated)
		if err != nil {
			return err
		}
		review := agenttrace.BuildReview(cmd.Context(), store, loaded)
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(review)
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), agenttrace.FormatReview(review, loaded, agentTracesView))
		return err
	},
}

var agentTracesValidateCmd = &cobra.Command{
	Use:   "validate <trace-id>",
	Short: "Render a human review view for qualitative trace validation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		loaded, err := agenttrace.Resolve(cmd.Context(), store, args[0], agentTracesCurated)
		if err != nil {
			return err
		}
		review := agenttrace.BuildReview(cmd.Context(), store, loaded)
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(review)
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), agenttrace.FormatReview(review, loaded, "validate"))
		return err
	},
}

var agentTracesOutputCmd = &cobra.Command{
	Use:   "output <run-id> <output-id>",
	Short: "Page through a spilled tool output",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		page, err := store.GetAgentRunToolOutputPage(cmd.Context(), args[0], args[1], agentTracesOutputOffset, agentTracesOutputLimit)
		if err != nil {
			return err
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(page)
		}
		w.Line("Output %s (%s)", page.OutputID, fallbackText(page.ToolName, "tool"))
		w.Line("Lines %d-%d of %d, bytes=%d, tokens=%d", page.StartLine, page.EndLine, page.TotalLines, page.SizeBytes, page.EstimatedTokens)
		if page.Truncated {
			w.Line("More lines are available; increase --offset or --limit.")
		}
		w.Line("")
		_, err = fmt.Fprint(cmd.OutOrStdout(), page.Content)
		if err == nil && !strings.HasSuffix(page.Content, "\n") {
			_, err = fmt.Fprintln(cmd.OutOrStdout())
		}
		return err
	},
}

var agentTracesGrepOutputCmd = &cobra.Command{
	Use:   "grep-output <run-id> <output-id> --pattern <pattern>",
	Short: "Search within a spilled tool output",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(agentTracesGrepPattern) == "" {
			return fmt.Errorf("--pattern is required")
		}
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		result, err := store.SearchAgentRunToolOutput(cmd.Context(), args[0], args[1], agentTracesGrepPattern, agentTracesGrepLiteral, agentTracesGrepLimit)
		if err != nil {
			return err
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}
		w.Line("Matches in %s (%s):", result.OutputID, fallbackText(result.ToolName, "tool"))
		if len(result.Matches) == 0 {
			w.Line("  none")
			return nil
		}
		for _, match := range result.Matches {
			w.Line("  %d:%s", match.LineNumber, match.Preview)
		}
		if result.Truncated {
			w.Line("Results truncated; raise --limit or narrow --pattern.")
		}
		return nil
	},
}

var agentTracesStatsCmd = &cobra.Command{
	Use:   "stats <trace-id>",
	Short: "Compute quantitative helper stats for a trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		loaded, err := agenttrace.Resolve(cmd.Context(), store, args[0], agentTracesCurated)
		if err != nil {
			return err
		}
		stats := agenttrace.ComputeStats(loaded)
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(stats)
		}
		w.Line("Messages: %d (%d assistant, %d tool)", stats.Messages, stats.AssistantTurns, stats.ToolMessages)
		w.Line("Tool calls: %d (%d errors, %d spilled)", stats.ToolCalls, stats.ToolErrors, stats.SpilledOutputs)
		w.Line("Reasoning: %d chars, %d estimated tokens", stats.ReasoningChars, stats.ReasoningTokens)
		w.Line("Sub-agent notices: %d", stats.SubagentNotices)
		w.Line("Compactions: %d", stats.Compactions)
		w.Line("Duration: %d ms", stats.DurationMs)
		w.Line("Tokens: input=%d output=%d total=%d estimated_trace=%d", stats.InputTokens, stats.OutputTokens, stats.TotalTokens, stats.EstimatedTokens)
		w.Line("Final answer: %d chars", stats.FinalAnswerChars)
		if stats.FinalAnswerPreview != "" {
			w.Line("  %s", stats.FinalAnswerPreview)
		}
		return nil
	},
}

var agentTracesDeriveCmd = &cobra.Command{
	Use:   "derive <run-id>",
	Short: "Create an editable curated trace from an immutable raw run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := agenttrace.Derive(cmd.Context(), store, args[0], agentTraceTitle, agentTraceNote, agentTraceTags)
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesCurateCmd = &cobra.Command{
	Use:   "curate <run-id...>",
	Short: "Batch derive, label, and optionally approve curated traces",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curations := make([]knowledge.AgentTraceCuration, 0, len(args))
		var reward *float64
		if cmd.Flags().Changed("reward") {
			value := agentTraceCurateReward
			reward = &value
		}
		for _, runID := range args {
			curation, err := agenttrace.Derive(cmd.Context(), store, runID, "", agentTraceCurateNote, agentTraceCurateTags)
			if err != nil {
				return err
			}
			if agentTraceCurateQuality != "" || reward != nil || agentTraceCurateSplit != "" || len(agentTraceCurateTags) > 0 || agentTraceCurateNote != "" {
				curation, err = agenttrace.Label(cmd.Context(), store, curation.ID, agenttrace.LabelInput{
					Quality: agentTraceCurateQuality,
					Reward:  reward,
					Split:   agentTraceCurateSplit,
					Tags:    nil,
					Notes:   "",
				})
				if err != nil {
					return err
				}
			}
			if agentTraceCurateApprove {
				curation, err = agenttrace.SetStatus(cmd.Context(), store, curation.ID, "approved")
				if err != nil {
					return err
				}
			}
			curations = append(curations, *curation)
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{"curations": summarizeTraceCurations(curations)})
		}
		for _, curation := range curations {
			w.Line("%s  %s  source=%s  %s", curation.ID, curation.Status, curation.SourceRunID, fallbackText(curation.Title, "(untitled)"))
		}
		return nil
	},
}

var agentTracesEditCmd = &cobra.Command{
	Use:   "edit <curation-id>",
	Short: "Edit a curated trace JSON payload in $VISUAL or $EDITOR",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = agentTraceEditor
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := agenttrace.EditInEditor(cmd.Context(), store, args[0], agentTraceEditorCommand)
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesMsgCmd = &cobra.Command{
	Use:   "msg",
	Short: "Modify messages in a curated trace",
}

var agentTracesMsgReplaceCmd = &cobra.Command{
	Use:   "replace <curation-id> <message-ref>",
	Short: "Replace a message's visible content and optionally reasoning",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := readFlagText(agentTraceMsgContent, agentTraceMsgContentFile)
		if err != nil {
			return err
		}
		if !cmd.Flags().Changed("content") && !cmd.Flags().Changed("content-file") {
			return fmt.Errorf("--content or --content-file is required")
		}
		reasoning, err := readFlagText(agentTraceMsgReasoning, agentTraceMsgReasoningFile)
		if err != nil {
			return err
		}
		setReasoning := cmd.Flags().Changed("reasoning") || cmd.Flags().Changed("reasoning-file")
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := agenttrace.ReplaceMessage(cmd.Context(), store, args[0], args[1], content, reasoning, setReasoning)
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesMsgDropCmd = &cobra.Command{
	Use:   "drop <curation-id> <message-ref>",
	Short: "Drop a message from a curated trace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := agenttrace.DropMessage(cmd.Context(), store, args[0], args[1])
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesMsgInsertCmd = &cobra.Command{
	Use:   "insert <curation-id> <position>",
	Short: "Insert a user, assistant, or tool message into a curated trace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := readFlagText(agentTraceMsgContent, agentTraceMsgContentFile)
		if err != nil {
			return err
		}
		if !cmd.Flags().Changed("content") && !cmd.Flags().Changed("content-file") {
			return fmt.Errorf("--content or --content-file is required")
		}
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := agenttrace.InsertMessage(cmd.Context(), store, args[0], args[1], agentTraceMsgRole, content, agentTraceMsgToolCallID, agentTraceMsgToolName)
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesRedactCmd = &cobra.Command{
	Use:   "redact <curation-id>",
	Short: "Apply a regex redaction across a curated trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(agentTraceRedactPattern) == "" {
			return fmt.Errorf("--pattern is required")
		}
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := agenttrace.Redact(cmd.Context(), store, args[0], agentTraceRedactPattern, agentTraceRedactReplacement)
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesLabelCmd = &cobra.Command{
	Use:   "label <curation-id>",
	Short: "Attach quality, reward, split, tags, and notes to a curated trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		var reward *float64
		if cmd.Flags().Changed("reward") {
			value := agentTraceLabelReward
			reward = &value
		}
		curation, err := agenttrace.Label(cmd.Context(), store, args[0], agenttrace.LabelInput{
			Quality: agentTraceLabelQuality,
			Reward:  reward,
			Split:   agentTraceLabelSplit,
			Tags:    agentTraceLabelTags,
			Notes:   agentTraceLabelNote,
		})
		if err != nil {
			return err
		}
		return renderCuration(cmd, curation)
	},
}

var agentTracesApproveCmd = &cobra.Command{
	Use:   "approve <curation-id>",
	Short: "Mark a curated trace as approved for export",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setTraceCurationStatus(cmd, args[0], "approved")
	},
}

var agentTracesRejectCmd = &cobra.Command{
	Use:   "reject <curation-id>",
	Short: "Mark a curated trace as rejected",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setTraceCurationStatus(cmd, args[0], "rejected")
	},
}

var agentTracesDiffCmd = &cobra.Command{
	Use:   "diff <curation-id>",
	Short: "Show a focused JSON diff between a curation and its source run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		curation, err := store.GetAgentTraceCuration(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		run, err := store.GetAgentRun(cmd.Context(), curation.SourceRunID)
		if err != nil {
			return err
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{
				"curation_id":       curation.ID,
				"source_run_id":     curation.SourceRunID,
				"source_trace_hash": curation.SourceTraceHash,
				"current_run_hash":  agenttrace.SourceTraceHash(run.Trace),
				"diff":              buildJSONLineDiff(run.Trace, curation.Trace, 400),
			})
		}
		w.Line("Diff for curation %s", curation.ID)
		w.Line("Source run: %s", curation.SourceRunID)
		if curation.SourceTraceHash != "" && curation.SourceTraceHash != agenttrace.SourceTraceHash(run.Trace) {
			w.Line("Source trace hash has changed since derive.")
		}
		diff := buildJSONLineDiff(run.Trace, curation.Trace, 400)
		if len(diff) == 0 {
			w.Line("No JSON-level differences.")
			return nil
		}
		for _, line := range diff {
			w.Line("%s", line)
		}
		return nil
	},
}

var agentTracesExportCmd = &cobra.Command{
	Use:   "export [trace-id...] --format tinker-sft --out <dir>",
	Short: "Export selected traces as Tinker conversation JSONL",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := agentTraceStore(cmd)
		if err != nil {
			return err
		}
		defer closeFn()

		loaded := make([]*agenttrace.LoadedTrace, 0)
		if len(args) == 0 {
			if !agentTraceExportCurated {
				return fmt.Errorf("raw trace collection export requires explicit trace ids; omit --curated=false or pass ids")
			}
			curations, err := store.ListAgentTraceCurations(cmd.Context(), knowledge.AgentTraceCurationFilter{
				Status: fallbackText(agentTraceExportStatus, "approved"),
				Limit:  10000,
			})
			if err != nil {
				return err
			}
			curations = filterCurationsForExport(curations, agentTraceExportContains, agentTraceExportSplit, agentTraceExportTags)
			for _, curation := range curations {
				item, err := agenttrace.LoadCuration(cmd.Context(), store, curation.ID)
				if err != nil {
					return err
				}
				loaded = append(loaded, item)
			}
		} else {
			for _, id := range args {
				item, err := agenttrace.Resolve(cmd.Context(), store, id, false)
				if err != nil {
					return err
				}
				loaded = append(loaded, item)
			}
		}
		result, err := agenttrace.Export(cmd.Context(), store, loaded, agenttrace.ExportOptions{
			Format:                agentTraceExportFormat,
			OutDir:                agentTraceExportOut,
			SystemMode:            agentTraceExportSystem,
			ReasoningMode:         agentTraceExportReasoning,
			MaterializeToolOutput: agentTraceExportMaterializeOutputs,
			ToolOutputMaxBytes:    agentTraceExportToolOutputMaxBytes,
		})
		if err != nil {
			return err
		}
		recordSummaries := buildExportRecordSummaries(cmd, store, loaded)
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{"export": result, "records": recordSummaries})
		}
		w.Line("Exported %d trace(s)", result.Records)
		w.Line("Conversations: %s", result.ConversationPath)
		w.Line("Manifest: %s", result.ManifestPath)
		w.Line("Records:")
		for _, record := range recordSummaries {
			w.Line("  %s  source=%s  messages=%d  tools=%d/%d  thinking_parts=%d  warnings=%d",
				record.ID,
				record.SourceRunID,
				record.Messages,
				record.ToolCalls,
				record.ToolErrors,
				record.ThinkingParts,
				len(record.Warnings),
			)
		}
		if len(result.Warnings) > 0 {
			w.Line("Warnings:")
			for _, warning := range result.Warnings {
				w.Line("  - %s", warning)
			}
		}
		return nil
	},
}

var agentTracesBaselineSuiteCmd = &cobra.Command{
	Use:   "baseline-suite",
	Short: "List harness-intuition baseline prompts for pre-training model probes",
	RunE: func(cmd *cobra.Command, args []string) error {
		prompts := agenttrace.BaselineSuite(agenttrace.BaselineSuiteOptions{
			Category: agentTraceBaselineCategory,
			Contains: agentTraceBaselineContains,
			Limit:    agentTraceBaselineLimit,
		})
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(map[string]any{
				"prompts":      prompts,
				"categories":   agenttrace.BaselineCategories(),
				"provider":     agentTraceBaselineProvider,
				"workspace":    agentTraceBaselineWorkspace,
				"served_model": agentTraceBaselineServedModel,
				"note":         "For llama.cpp providers, the served model is determined by the endpoint; the configured model string may be stale.",
			})
		}
		if strings.TrimSpace(agentTraceBaselineServedModel) != "" {
			w.Line("Served model: %s", agentTraceBaselineServedModel)
			w.Line("Note: for llama.cpp providers, the endpoint is authoritative; the configured model string does not need to match.")
			w.Line("")
		}
		if agentTraceBaselineCommands {
			provider := fallbackText(agentTraceBaselineProvider, "<provider>")
			workspace := fallbackText(agentTraceBaselineWorkspace, "<workspace>")
			for _, prompt := range prompts {
				w.Line("# %s  category=%s  tags=%s", prompt.ID, prompt.Category, strings.Join(prompt.Tags, ","))
				args := fmt.Sprintf("--workspace %s agent run --provider %s -p %s", workspace, provider, shellQuoteCLI(prompt.Prompt))
				w.Line("PATH=/opt/homebrew/bin:$PATH make cli ARGS=%s", shellQuoteCLI(args))
			}
			return nil
		}
		if len(prompts) == 0 {
			w.Line("No baseline prompts found.")
			return nil
		}
		for _, prompt := range prompts {
			w.Line("%s  category=%s  tags=%s", prompt.ID, prompt.Category, strings.Join(prompt.Tags, ","))
			w.Line("   %s", prompt.Prompt)
		}
		return nil
	},
}

var agentTracesVerifyExportCmd = &cobra.Command{
	Use:   "verify-export <path-or-dir>",
	Short: "Verify and summarize a Tinker conversations JSONL export",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := verifyTraceExportFile(args[0])
		if err != nil {
			return err
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(report)
		}
		w.Line("Export: %s", report.Path)
		w.Line("Records: %d", report.Records)
		w.Line("Messages: %d (assistant=%d tool=%d system=%d)", report.Messages, report.AssistantMessages, report.ToolMessages, report.SystemMessages)
		w.Line("Tool calls: %d", report.ToolCalls)
		w.Line("Thinking parts: %d", report.ThinkingParts)
		if len(report.Errors) == 0 {
			w.Line("Shape: ok")
		} else {
			w.Line("Shape errors:")
			for _, item := range report.Errors {
				w.Line("  - line %d: %s", item.Line, item.Message)
			}
		}
		if len(report.RecordsSummary) > 0 {
			w.Line("Records:")
			for _, record := range report.RecordsSummary {
				w.Line("  line=%d source=%s messages=%d assistant=%d tool=%d thinking=%d",
					record.Line,
					fallbackText(record.SourceRunID, record.ID),
					record.Messages,
					record.AssistantMessages,
					record.ToolMessages,
					record.ThinkingParts,
				)
			}
		}
		if len(report.Errors) > 0 {
			return fmt.Errorf("export verification found %d issue(s)", len(report.Errors))
		}
		return nil
	},
}

func init() {
	agentTracesListCmd.Flags().StringVar(&agentTracesListStatus, "status", "", "filter by raw run status or curation status")
	agentTracesListCmd.Flags().StringVar(&agentTracesListRunKind, "run-kind", "", "filter raw runs by run kind")
	agentTracesListCmd.Flags().StringVar(&agentTracesListContains, "contains", "", "filter title, prompt, id, notes, or tags")
	agentTracesListCmd.Flags().StringVar(&agentTracesListSince, "since", "", "filter by updated time (RFC3339, YYYY-MM-DD, or duration like 24h)")
	agentTracesListCmd.Flags().IntVar(&agentTracesListLimit, "limit", 50, "maximum traces to list")
	agentTracesListCmd.Flags().IntVar(&agentTracesListOffset, "offset", 0, "offset into the trace list")
	agentTracesListCmd.Flags().BoolVar(&agentTracesCurated, "curated", false, "list curated traces instead of raw runs")

	agentTracesCandidatesCmd.Flags().StringVar(&agentTracesCandidateStatus, "status", "completed", "filter by raw run status")
	agentTracesCandidatesCmd.Flags().StringVar(&agentTracesCandidateRunKind, "run-kind", "", "filter by run kind")
	agentTracesCandidatesCmd.Flags().StringVar(&agentTracesCandidateContains, "contains", "", "filter title, prompt, or id")
	agentTracesCandidatesCmd.Flags().StringVar(&agentTracesCandidateSince, "since", "", "filter by updated time (RFC3339, YYYY-MM-DD, or duration like 24h)")
	agentTracesCandidatesCmd.Flags().IntVar(&agentTracesCandidateLimit, "limit", 50, "maximum runs to scan")
	agentTracesCandidatesCmd.Flags().IntVar(&agentTracesCandidateOffset, "offset", 0, "offset into the run list")
	agentTracesCandidatesCmd.Flags().IntVar(&agentTracesCandidateMinMessages, "min-messages", 0, "minimum message count")
	agentTracesCandidatesCmd.Flags().IntVar(&agentTracesCandidateMinAssistantTurns, "min-assistant-turns", 0, "minimum assistant turns")
	agentTracesCandidatesCmd.Flags().IntVar(&agentTracesCandidateMinToolCalls, "min-tool-calls", 0, "minimum tool calls")
	agentTracesCandidatesCmd.Flags().IntVar(&agentTracesCandidateMaxToolErrors, "max-tool-errors", -1, "maximum tool errors; negative disables")

	agentTracesShowCmd.Flags().StringVar(&agentTracesView, "view", "summary", "view: summary, timeline, messages, tools, context, events")
	agentTracesShowCmd.Flags().BoolVar(&agentTracesCurated, "curated", false, "resolve id as a curated trace")
	agentTracesValidateCmd.Flags().BoolVar(&agentTracesCurated, "curated", false, "resolve id as a curated trace")
	agentTracesStatsCmd.Flags().BoolVar(&agentTracesCurated, "curated", false, "resolve id as a curated trace")

	agentTracesOutputCmd.Flags().IntVar(&agentTracesOutputOffset, "offset", 0, "zero-based line offset")
	agentTracesOutputCmd.Flags().IntVar(&agentTracesOutputLimit, "limit", 200, "maximum lines to print")

	agentTracesGrepOutputCmd.Flags().StringVar(&agentTracesGrepPattern, "pattern", "", "regular expression or literal search pattern")
	agentTracesGrepOutputCmd.Flags().BoolVar(&agentTracesGrepLiteral, "literal-text", false, "treat pattern as literal text")
	agentTracesGrepOutputCmd.Flags().IntVar(&agentTracesGrepLimit, "limit", 100, "maximum matches to return")

	agentTracesDeriveCmd.Flags().StringVar(&agentTraceTitle, "title", "", "curation title")
	agentTracesDeriveCmd.Flags().StringSliceVar(&agentTraceTags, "tag", nil, "curation tag (repeatable)")
	agentTracesDeriveCmd.Flags().StringVar(&agentTraceNote, "note", "", "curation note")

	agentTracesCurateCmd.Flags().StringSliceVar(&agentTraceCurateTags, "tag", nil, "curation tag (repeatable)")
	agentTracesCurateCmd.Flags().StringVar(&agentTraceCurateNote, "note", "", "curation note")
	agentTracesCurateCmd.Flags().StringVar(&agentTraceCurateQuality, "quality", "", "qualitative label")
	agentTracesCurateCmd.Flags().Float64Var(&agentTraceCurateReward, "reward", 0, "numeric reward label")
	agentTracesCurateCmd.Flags().StringVar(&agentTraceCurateSplit, "split", "", "dataset split label")
	agentTracesCurateCmd.Flags().BoolVar(&agentTraceCurateApprove, "approve", false, "mark derived curations approved")

	agentTracesEditCmd.Flags().BoolVar(&agentTraceEditor, "editor", false, "open the curation JSON in $VISUAL or $EDITOR")
	agentTracesEditCmd.Flags().StringVar(&agentTraceEditorCommand, "editor-command", "", "editor command to use instead of $VISUAL or $EDITOR")

	agentTracesMsgReplaceCmd.Flags().StringVar(&agentTraceMsgContent, "content", "", "replacement visible content")
	agentTracesMsgReplaceCmd.Flags().StringVar(&agentTraceMsgContentFile, "content-file", "", "file containing replacement visible content")
	agentTracesMsgReplaceCmd.Flags().StringVar(&agentTraceMsgReasoning, "reasoning", "", "replacement assistant reasoning")
	agentTracesMsgReplaceCmd.Flags().StringVar(&agentTraceMsgReasoningFile, "reasoning-file", "", "file containing replacement assistant reasoning")

	agentTracesMsgInsertCmd.Flags().StringVar(&agentTraceMsgRole, "role", "user", "inserted message role: user, assistant, or tool")
	agentTracesMsgInsertCmd.Flags().StringVar(&agentTraceMsgContent, "content", "", "inserted message content")
	agentTracesMsgInsertCmd.Flags().StringVar(&agentTraceMsgContentFile, "content-file", "", "file containing inserted message content")
	agentTracesMsgInsertCmd.Flags().StringVar(&agentTraceMsgToolCallID, "tool-call-id", "", "tool call id for inserted tool result")
	agentTracesMsgInsertCmd.Flags().StringVar(&agentTraceMsgToolName, "tool-name", "", "tool name for inserted tool result")

	agentTracesRedactCmd.Flags().StringVar(&agentTraceRedactPattern, "pattern", "", "regular expression to redact")
	agentTracesRedactCmd.Flags().StringVar(&agentTraceRedactReplacement, "replacement", "[REDACTED]", "replacement text")

	agentTracesLabelCmd.Flags().StringVar(&agentTraceLabelQuality, "quality", "", "qualitative label")
	agentTracesLabelCmd.Flags().Float64Var(&agentTraceLabelReward, "reward", 0, "numeric reward label")
	agentTracesLabelCmd.Flags().StringVar(&agentTraceLabelSplit, "split", "", "dataset split label")
	agentTracesLabelCmd.Flags().StringSliceVar(&agentTraceLabelTags, "tag", nil, "tag to append (repeatable)")
	agentTracesLabelCmd.Flags().StringVar(&agentTraceLabelNote, "note", "", "note to append")

	agentTracesExportCmd.Flags().StringVar(&agentTraceExportFormat, "format", "tinker-sft", "export format")
	agentTracesExportCmd.Flags().StringVar(&agentTraceExportOut, "out", "", "export output directory")
	agentTracesExportCmd.Flags().BoolVar(&agentTraceExportCurated, "curated", true, "export curated traces when no ids are passed")
	agentTracesExportCmd.Flags().StringVar(&agentTraceExportStatus, "status", "approved", "curation status to export when no ids are passed")
	agentTracesExportCmd.Flags().StringVar(&agentTraceExportContains, "contains", "", "filter selected curations by title, notes, tags, or id when no ids are passed")
	agentTracesExportCmd.Flags().StringVar(&agentTraceExportSplit, "split", "", "filter selected curations by split when no ids are passed")
	agentTracesExportCmd.Flags().StringSliceVar(&agentTraceExportTags, "tag", nil, "require curation tag when no ids are passed (repeatable)")
	agentTracesExportCmd.Flags().StringVar(&agentTraceExportSystem, "system", "effective", "system prompt mode: raw, effective, none")
	agentTracesExportCmd.Flags().StringVar(&agentTraceExportReasoning, "reasoning", agenttrace.ReasoningInclude, "reasoning mode: include, omit, metadata-only")
	agentTracesExportCmd.Flags().BoolVar(&agentTraceExportMaterializeOutputs, "materialize-tool-outputs", false, "replace spilled output summaries with full stored output")
	agentTracesExportCmd.Flags().IntVar(&agentTraceExportToolOutputMaxBytes, "tool-output-max-bytes", 1024*1024, "cap for materialized tool output bytes")

	agentTracesBaselineSuiteCmd.Flags().StringVar(&agentTraceBaselineCategory, "category", "", "filter by baseline category")
	agentTracesBaselineSuiteCmd.Flags().StringVar(&agentTraceBaselineContains, "contains", "", "filter baseline prompts by id, category, tags, or prompt text")
	agentTracesBaselineSuiteCmd.Flags().IntVar(&agentTraceBaselineLimit, "limit", 0, "maximum baseline prompts to print")
	agentTracesBaselineSuiteCmd.Flags().StringVar(&agentTraceBaselineProvider, "provider", "", "provider name to include in generated run commands")
	agentTracesBaselineSuiteCmd.Flags().StringVar(&agentTraceBaselineWorkspace, "workspace", "", "workspace name to include in generated run commands")
	agentTracesBaselineSuiteCmd.Flags().StringVar(&agentTraceBaselineServedModel, "served-model", "", "actual manually served model name to record in command output")
	agentTracesBaselineSuiteCmd.Flags().BoolVar(&agentTraceBaselineCommands, "commands", false, "print runnable make cli commands instead of prompt text")

	agentTracesMsgCmd.AddCommand(agentTracesMsgReplaceCmd)
	agentTracesMsgCmd.AddCommand(agentTracesMsgDropCmd)
	agentTracesMsgCmd.AddCommand(agentTracesMsgInsertCmd)

	agentTracesCmd.AddCommand(agentTracesListCmd)
	agentTracesCmd.AddCommand(agentTracesCandidatesCmd)
	agentTracesCmd.AddCommand(agentTracesShowCmd)
	agentTracesCmd.AddCommand(agentTracesValidateCmd)
	agentTracesCmd.AddCommand(agentTracesOutputCmd)
	agentTracesCmd.AddCommand(agentTracesGrepOutputCmd)
	agentTracesCmd.AddCommand(agentTracesStatsCmd)
	agentTracesCmd.AddCommand(agentTracesDeriveCmd)
	agentTracesCmd.AddCommand(agentTracesCurateCmd)
	agentTracesCmd.AddCommand(agentTracesEditCmd)
	agentTracesCmd.AddCommand(agentTracesMsgCmd)
	agentTracesCmd.AddCommand(agentTracesRedactCmd)
	agentTracesCmd.AddCommand(agentTracesLabelCmd)
	agentTracesCmd.AddCommand(agentTracesApproveCmd)
	agentTracesCmd.AddCommand(agentTracesRejectCmd)
	agentTracesCmd.AddCommand(agentTracesDiffCmd)
	agentTracesCmd.AddCommand(agentTracesExportCmd)
	agentTracesCmd.AddCommand(agentTracesVerifyExportCmd)
	agentTracesCmd.AddCommand(agentTracesBaselineSuiteCmd)
	agentCmd.AddCommand(agentTracesCmd)
}

func agentTraceStore(cmd *cobra.Command) (*knowledge.Store, func(), error) {
	db, _, _, err := openWorkspaceDB(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return knowledge.New(db), func() { _ = db.Close() }, nil
}

func renderCuration(cmd *cobra.Command, curation *knowledge.AgentTraceCuration) error {
	w := output.FromContext(cmd.Context())
	if w.IsJSON() {
		return w.JSON(curation)
	}
	w.Line("Curation: %s", curation.ID)
	w.Line("Status: %s", curation.Status)
	w.Line("Source run: %s", curation.SourceRunID)
	if curation.Title != "" {
		w.Line("Title: %s", curation.Title)
	}
	if len(curation.Tags) > 0 {
		w.Line("Tags: %s", strings.Join(curation.Tags, ", "))
	}
	if curation.Quality != "" {
		w.Line("Quality: %s", curation.Quality)
	}
	if curation.Reward != nil {
		w.Line("Reward: %g", *curation.Reward)
	}
	if curation.Split != "" {
		w.Line("Split: %s", curation.Split)
	}
	return nil
}

type agentTraceCurationSummary struct {
	ID              string    `json:"id"`
	SourceRunID     string    `json:"source_run_id"`
	Title           string    `json:"title"`
	Status          string    `json:"status"`
	Tags            []string  `json:"tags"`
	Notes           string    `json:"notes,omitempty"`
	Quality         string    `json:"quality,omitempty"`
	Reward          *float64  `json:"reward,omitempty"`
	Split           string    `json:"split,omitempty"`
	SourceTraceHash string    `json:"source_trace_hash,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type agentTraceCandidateSummary struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Model              string           `json:"model"`
	Provider           string           `json:"provider,omitempty"`
	RunKind            string           `json:"run_kind"`
	Status             string           `json:"status"`
	Score              int              `json:"score"`
	Stats              agenttrace.Stats `json:"stats"`
	Warnings           []string         `json:"warnings,omitempty"`
	ReviewHints        []string         `json:"review_hints,omitempty"`
	FinalAnswerPreview string           `json:"final_answer_preview,omitempty"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type agentTraceExportRecordSummary struct {
	ID            string   `json:"id"`
	SourceKind    string   `json:"source_kind"`
	SourceRunID   string   `json:"source_run_id,omitempty"`
	Title         string   `json:"title,omitempty"`
	Messages      int      `json:"messages"`
	Assistant     int      `json:"assistant_messages"`
	ToolMessages  int      `json:"tool_messages"`
	ToolCalls     int      `json:"tool_calls"`
	ToolErrors    int      `json:"tool_errors"`
	ThinkingParts int      `json:"thinking_parts"`
	Warnings      []string `json:"warnings,omitempty"`
}

type traceExportVerifyReport struct {
	Path              string                         `json:"path"`
	Records           int                            `json:"records"`
	Messages          int                            `json:"messages"`
	SystemMessages    int                            `json:"system_messages"`
	UserMessages      int                            `json:"user_messages"`
	AssistantMessages int                            `json:"assistant_messages"`
	ToolMessages      int                            `json:"tool_messages"`
	ToolCalls         int                            `json:"tool_calls"`
	ThinkingParts     int                            `json:"thinking_parts"`
	Errors            []traceExportVerifyIssue       `json:"errors,omitempty"`
	RecordsSummary    []traceExportVerifyRecordStats `json:"records_summary,omitempty"`
}

type traceExportVerifyIssue struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type traceExportVerifyRecordStats struct {
	Line              int    `json:"line"`
	ID                string `json:"id,omitempty"`
	SourceRunID       string `json:"source_run_id,omitempty"`
	Messages          int    `json:"messages"`
	SystemMessages    int    `json:"system_messages"`
	UserMessages      int    `json:"user_messages"`
	AssistantMessages int    `json:"assistant_messages"`
	ToolMessages      int    `json:"tool_messages"`
	ToolCalls         int    `json:"tool_calls"`
	ThinkingParts     int    `json:"thinking_parts"`
}

func summarizeTraceCurations(curations []knowledge.AgentTraceCuration) []agentTraceCurationSummary {
	summaries := make([]agentTraceCurationSummary, 0, len(curations))
	for _, curation := range curations {
		summaries = append(summaries, agentTraceCurationSummary{
			ID:              curation.ID,
			SourceRunID:     curation.SourceRunID,
			Title:           curation.Title,
			Status:          curation.Status,
			Tags:            append([]string(nil), curation.Tags...),
			Notes:           curation.Notes,
			Quality:         curation.Quality,
			Reward:          curation.Reward,
			Split:           curation.Split,
			SourceTraceHash: curation.SourceTraceHash,
			CreatedAt:       curation.CreatedAt,
			UpdatedAt:       curation.UpdatedAt,
		})
	}
	return summaries
}

func buildTraceCandidateSummary(run knowledge.AgentRunSummary, review agenttrace.Review) agentTraceCandidateSummary {
	stats := review.Stats
	score := 0
	hints := make([]string, 0, 6)
	if run.Status == "completed" {
		score += 20
		hints = append(hints, "completed")
	}
	if stats.FinalAnswerChars > 0 {
		score += 20
		hints = append(hints, "has final answer")
	}
	if stats.ToolCalls > 0 {
		score += minInt(20, stats.ToolCalls*3)
		hints = append(hints, fmt.Sprintf("%d tool call(s)", stats.ToolCalls))
	}
	if stats.ToolErrors == 0 {
		score += 15
		if stats.ToolCalls > 0 {
			hints = append(hints, "no tool errors")
		}
	} else {
		score -= stats.ToolErrors * 5
		hints = append(hints, fmt.Sprintf("%d tool error(s)", stats.ToolErrors))
	}
	if stats.ReasoningChars > 0 {
		score += 10
		hints = append(hints, "has reasoning")
	}
	if stats.SubagentNotices > 0 {
		score += 5
		hints = append(hints, "has subagent notification")
	}
	if stats.SpilledOutputs > 0 {
		score += 3
		hints = append(hints, "has spilled output")
	}
	if len(review.Warnings) > 0 {
		score -= len(review.Warnings) * 2
	}
	return agentTraceCandidateSummary{
		ID:                 run.ID,
		Title:              run.Title,
		Model:              run.Model,
		Provider:           run.Provider,
		RunKind:            run.RunKind,
		Status:             run.Status,
		Score:              score,
		Stats:              stats,
		Warnings:           append([]string(nil), review.Warnings...),
		ReviewHints:        hints,
		FinalAnswerPreview: stats.FinalAnswerPreview,
		UpdatedAt:          run.UpdatedAt,
	}
}

func buildExportRecordSummaries(cmd *cobra.Command, store *knowledge.Store, loaded []*agenttrace.LoadedTrace) []agentTraceExportRecordSummary {
	summaries := make([]agentTraceExportRecordSummary, 0, len(loaded))
	for _, item := range loaded {
		review := agenttrace.BuildReview(cmd.Context(), store, item)
		stats := review.Stats
		summaries = append(summaries, agentTraceExportRecordSummary{
			ID:            item.ID,
			SourceKind:    item.Kind,
			SourceRunID:   review.SourceRunID,
			Title:         review.Title,
			Messages:      stats.Messages,
			Assistant:     stats.AssistantTurns,
			ToolMessages:  stats.ToolMessages,
			ToolCalls:     stats.ToolCalls,
			ToolErrors:    stats.ToolErrors,
			ThinkingParts: countTraceThinkingParts(item),
			Warnings:      append([]string(nil), review.Warnings...),
		})
	}
	return summaries
}

func countTraceThinkingParts(loaded *agenttrace.LoadedTrace) int {
	if loaded == nil {
		return 0
	}
	count := 0
	for _, message := range loaded.Trace.Messages {
		if message.Role == "assistant" && strings.TrimSpace(message.Reasoning) != "" {
			count++
		}
	}
	return count
}

func setTraceCurationStatus(cmd *cobra.Command, curationID, status string) error {
	store, closeFn, err := agentTraceStore(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	curation, err := agenttrace.SetStatus(cmd.Context(), store, curationID, status)
	if err != nil {
		return err
	}
	return renderCuration(cmd, curation)
}

func readFlagText(value, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return value, nil
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func filterRuns(runs []knowledge.AgentRunSummary, runKind, contains, sinceRaw string) []knowledge.AgentRunSummary {
	since := parseSinceTime(sinceRaw)
	needle := strings.ToLower(strings.TrimSpace(contains))
	out := runs[:0]
	for _, run := range runs {
		if strings.TrimSpace(runKind) != "" && run.RunKind != strings.TrimSpace(runKind) {
			continue
		}
		if !since.IsZero() && run.UpdatedAt.Before(since) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(run.ID+" "+run.Title+" "+run.Prompt+" "+run.TaskName), needle) {
			continue
		}
		out = append(out, run)
	}
	return out
}

func filterCurations(curations []knowledge.AgentTraceCuration, contains, sinceRaw string) []knowledge.AgentTraceCuration {
	since := parseSinceTime(sinceRaw)
	needle := strings.ToLower(strings.TrimSpace(contains))
	out := curations[:0]
	for _, curation := range curations {
		if !since.IsZero() && curation.UpdatedAt.Before(since) {
			continue
		}
		haystack := strings.ToLower(curation.ID + " " + curation.SourceRunID + " " + curation.Title + " " + curation.Notes + " " + strings.Join(curation.Tags, " "))
		if needle != "" && !strings.Contains(haystack, needle) {
			continue
		}
		out = append(out, curation)
	}
	return out
}

func filterCurationsForExport(curations []knowledge.AgentTraceCuration, contains, split string, tags []string) []knowledge.AgentTraceCuration {
	needle := strings.ToLower(strings.TrimSpace(contains))
	split = strings.TrimSpace(split)
	requiredTags := compactCLIStrings(tags)
	out := curations[:0]
	for _, curation := range curations {
		if split != "" && curation.Split != split {
			continue
		}
		if needle != "" {
			haystack := strings.ToLower(curation.ID + " " + curation.SourceRunID + " " + curation.Title + " " + curation.Notes + " " + strings.Join(curation.Tags, " "))
			if !strings.Contains(haystack, needle) {
				continue
			}
		}
		if len(requiredTags) > 0 && !curationHasAllTags(curation, requiredTags) {
			continue
		}
		out = append(out, curation)
	}
	return out
}

func curationHasAllTags(curation knowledge.AgentTraceCuration, tags []string) bool {
	have := make(map[string]struct{}, len(curation.Tags))
	for _, tag := range curation.Tags {
		have[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
	}
	for _, tag := range tags {
		if _, ok := have[strings.ToLower(strings.TrimSpace(tag))]; !ok {
			return false
		}
	}
	return true
}

func compactCLIStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func shellQuoteCLI(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func parseSinceTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(-d)
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse("2006-01-02", raw); err == nil {
		return ts
	}
	return time.Time{}
}

func buildJSONLineDiff(left, right json.RawMessage, maxLines int) []string {
	leftLines := prettyJSONLines(left)
	rightLines := prettyJSONLines(right)
	if maxLines <= 0 {
		maxLines = 400
	}
	lines := make([]string, 0)
	limit := len(leftLines)
	if len(rightLines) > limit {
		limit = len(rightLines)
	}
	for i := 0; i < limit; i++ {
		leftLine, rightLine := "", ""
		if i < len(leftLines) {
			leftLine = leftLines[i]
		}
		if i < len(rightLines) {
			rightLine = rightLines[i]
		}
		if leftLine == rightLine {
			continue
		}
		if leftLine != "" {
			lines = append(lines, "-"+leftLine)
		}
		if rightLine != "" {
			lines = append(lines, "+"+rightLine)
		}
		if len(lines) >= maxLines {
			lines = append(lines, "...diff truncated after "+strconv.Itoa(maxLines)+" lines")
			break
		}
	}
	return lines
}

func prettyJSONLines(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.Split(string(raw), "\n")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return strings.Split(string(data), "\n")
	}
	return strings.Split(string(data), "\n")
}

func verifyTraceExportFile(rawPath string) (traceExportVerifyReport, error) {
	path := filepath.Clean(rawPath)
	info, err := os.Stat(path)
	if err != nil {
		return traceExportVerifyReport{}, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "conversations.jsonl")
	}
	file, err := os.Open(path)
	if err != nil {
		return traceExportVerifyReport{}, err
	}
	defer file.Close()

	report := traceExportVerifyReport{Path: path}
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 32*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "invalid JSON: " + err.Error()})
			continue
		}
		stats := traceExportVerifyRecordStats{Line: lineNumber}
		if metadata, ok := record["agent_metadata"].(map[string]any); ok {
			stats.ID, _ = metadata["id"].(string)
			stats.SourceRunID, _ = metadata["source_run_id"].(string)
		}
		messages, ok := record["messages"].([]any)
		if !ok {
			report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "missing messages array"})
			continue
		}
		report.Records++
		stats.Messages = len(messages)
		report.Messages += len(messages)
		for _, rawMessage := range messages {
			message, ok := rawMessage.(map[string]any)
			if !ok {
				report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "message is not an object"})
				continue
			}
			role, _ := message["role"].(string)
			switch role {
			case "system":
				report.SystemMessages++
				stats.SystemMessages++
			case "user":
				report.UserMessages++
				stats.UserMessages++
			case "assistant":
				report.AssistantMessages++
				stats.AssistantMessages++
			case "tool":
				report.ToolMessages++
				stats.ToolMessages++
				if strings.TrimSpace(stringValue(message["tool_call_id"])) == "" {
					report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "tool message missing tool_call_id"})
				}
			default:
				report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "unsupported message role " + strconv.Quote(role)})
			}
			if _, ok := message["content"]; !ok {
				report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "message missing content"})
			}
			if calls, ok := message["tool_calls"].([]any); ok {
				report.ToolCalls += len(calls)
				stats.ToolCalls += len(calls)
				for _, rawCall := range calls {
					call, ok := rawCall.(map[string]any)
					if !ok {
						report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "tool_call is not an object"})
						continue
					}
					function, _ := call["function"].(map[string]any)
					if strings.TrimSpace(stringValue(function["name"])) == "" {
						report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "tool_call missing function.name"})
					}
				}
			}
			if parts, ok := message["content"].([]any); ok {
				for _, rawPart := range parts {
					part, ok := rawPart.(map[string]any)
					if !ok {
						report.Errors = append(report.Errors, traceExportVerifyIssue{Line: lineNumber, Message: "content part is not an object"})
						continue
					}
					if part["type"] == "thinking" {
						report.ThinkingParts++
						stats.ThinkingParts++
					}
				}
			}
		}
		report.RecordsSummary = append(report.RecordsSummary, stats)
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func previewCLI(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "..."
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
