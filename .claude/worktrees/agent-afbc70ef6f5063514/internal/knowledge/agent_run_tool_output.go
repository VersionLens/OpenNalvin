package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	toolOutputMatchPreviewChars      = 240
	toolOutputMatchPreviewBytesLimit = 8 * 1024
)

type AgentRunToolOutput struct {
	OutputID        string    `json:"output_id"`
	RunID           string    `json:"run_id"`
	ToolCallID      string    `json:"tool_call_id,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	Content         string    `json:"content"`
	SizeBytes       int       `json:"size_bytes"`
	EstimatedTokens int64     `json:"estimated_tokens"`
	TotalLines      int       `json:"total_lines"`
	StoredTruncated bool      `json:"stored_truncated"`
	InlineTruncated bool      `json:"inline_truncated"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateAgentRunToolOutputInput struct {
	OutputID        string
	RunID           string
	ToolCallID      string
	ToolName        string
	Content         string
	SizeBytes       int
	EstimatedTokens int64
	TotalLines      int
	StoredTruncated bool
	InlineTruncated bool
}

type AgentRunToolOutputPage struct {
	OutputID        string `json:"output_id"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	Offset          int    `json:"offset"`
	Limit           int    `json:"limit"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	TotalLines      int    `json:"total_lines"`
	SizeBytes       int    `json:"size_bytes"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	StoredTruncated bool   `json:"stored_truncated"`
	InlineTruncated bool   `json:"inline_truncated"`
	Content         string `json:"content"`
	Truncated       bool   `json:"truncated"`
}

type AgentRunToolOutputMatch struct {
	LineNumber int    `json:"line_number"`
	Preview    string `json:"preview"`
}

type AgentRunToolOutputSearchResult struct {
	OutputID        string                    `json:"output_id"`
	ToolCallID      string                    `json:"tool_call_id,omitempty"`
	ToolName        string                    `json:"tool_name,omitempty"`
	Pattern         string                    `json:"pattern"`
	LiteralText     bool                      `json:"literal_text"`
	Limit           int                       `json:"limit"`
	TotalLines      int                       `json:"total_lines"`
	SizeBytes       int                       `json:"size_bytes"`
	EstimatedTokens int64                     `json:"estimated_tokens"`
	StoredTruncated bool                      `json:"stored_truncated"`
	InlineTruncated bool                      `json:"inline_truncated"`
	Matches         []AgentRunToolOutputMatch `json:"matches"`
	Truncated       bool                      `json:"truncated"`
}

func (s *Store) CreateAgentRunToolOutput(ctx context.Context, input CreateAgentRunToolOutputInput) (*AgentRunToolOutput, error) {
	if strings.TrimSpace(input.RunID) == "" {
		return nil, fmt.Errorf("agent run id is required")
	}

	outputID := strings.TrimSpace(input.OutputID)
	if outputID == "" {
		outputID = uuid.NewString()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_run_tool_outputs (
			output_id, run_id, tool_call_id, tool_name, content, size_bytes, estimated_tokens,
			total_lines, stored_truncated, inline_truncated, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outputID,
		strings.TrimSpace(input.RunID),
		strings.TrimSpace(input.ToolCallID),
		strings.TrimSpace(input.ToolName),
		input.Content,
		input.SizeBytes,
		input.EstimatedTokens,
		input.TotalLines,
		boolToInt(input.StoredTruncated),
		boolToInt(input.InlineTruncated),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("create agent run tool output: %w", err)
	}

	return s.GetAgentRunToolOutput(ctx, input.RunID, outputID)
}

func (s *Store) GetAgentRunToolOutput(ctx context.Context, runID, outputID string) (*AgentRunToolOutput, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT output_id, run_id, tool_call_id, tool_name, content, size_bytes, estimated_tokens,
		       total_lines, stored_truncated, inline_truncated, created_at, updated_at
		FROM agent_run_tool_outputs
		WHERE run_id = ? AND output_id = ?`,
		strings.TrimSpace(runID),
		strings.TrimSpace(outputID),
	)

	output, err := scanAgentRunToolOutput(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run tool output: %w", err)
	}
	return output, nil
}

func (s *Store) GetAgentRunToolOutputPage(ctx context.Context, runID, outputID string, offset, limit int) (*AgentRunToolOutputPage, error) {
	output, err := s.GetAgentRunToolOutput(ctx, runID, outputID)
	if err != nil {
		return nil, err
	}

	lines := splitToolOutputLines(output.Content)
	if output.TotalLines > 0 {
		lines = lines[:min(output.TotalLines, len(lines))]
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 200
	}
	if offset > len(lines) {
		offset = len(lines)
	}
	end := offset + limit
	truncated := false
	if end < len(lines) {
		truncated = true
	} else {
		end = len(lines)
	}

	window := ""
	if offset < end {
		window = strings.Join(lines[offset:end], "\n")
	}

	startLine := 0
	if offset < end {
		startLine = offset + 1
	}

	return &AgentRunToolOutputPage{
		OutputID:        output.OutputID,
		ToolCallID:      output.ToolCallID,
		ToolName:        output.ToolName,
		Offset:          offset,
		Limit:           limit,
		StartLine:       startLine,
		EndLine:         end,
		TotalLines:      len(lines),
		SizeBytes:       output.SizeBytes,
		EstimatedTokens: output.EstimatedTokens,
		StoredTruncated: output.StoredTruncated,
		InlineTruncated: output.InlineTruncated,
		Content:         window,
		Truncated:       truncated,
	}, nil
}

func (s *Store) SearchAgentRunToolOutput(ctx context.Context, runID, outputID, pattern string, literalText bool, limit int) (*AgentRunToolOutputSearchResult, error) {
	output, err := s.GetAgentRunToolOutput(ctx, runID, outputID)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if limit <= 0 {
		limit = 100
	}

	var matcher *regexp.Regexp
	if literalText {
		matcher, err = regexp.Compile(regexp.QuoteMeta(pattern))
	} else {
		matcher, err = regexp.Compile(pattern)
	}
	if err != nil {
		return nil, fmt.Errorf("compile search pattern: %w", err)
	}

	lines := splitToolOutputLines(output.Content)
	matches := make([]AgentRunToolOutputMatch, 0)
	truncated := false
	previewBytesUsed := 0
	for index, line := range lines {
		matchIndex := matcher.FindStringIndex(line)
		if matchIndex == nil {
			continue
		}
		if len(matches) >= limit {
			truncated = true
			break
		}

		preview := buildToolOutputMatchPreview(line, matchIndex, toolOutputMatchPreviewChars)
		previewBytes := len([]byte(preview))
		if previewBytesUsed+previewBytes > toolOutputMatchPreviewBytesLimit {
			truncated = true
			break
		}
		previewBytesUsed += previewBytes

		matches = append(matches, AgentRunToolOutputMatch{
			LineNumber: index + 1,
			Preview:    preview,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].LineNumber < matches[j].LineNumber
	})

	return &AgentRunToolOutputSearchResult{
		OutputID:        output.OutputID,
		ToolCallID:      output.ToolCallID,
		ToolName:        output.ToolName,
		Pattern:         pattern,
		LiteralText:     literalText,
		Limit:           limit,
		TotalLines:      len(lines),
		SizeBytes:       output.SizeBytes,
		EstimatedTokens: output.EstimatedTokens,
		StoredTruncated: output.StoredTruncated,
		InlineTruncated: output.InlineTruncated,
		Matches:         matches,
		Truncated:       truncated,
	}, nil
}

func scanAgentRunToolOutput(scanner interface{ Scan(dest ...any) error }) (*AgentRunToolOutput, error) {
	var (
		output          AgentRunToolOutput
		storedTruncated int
		inlineTruncated int
		createdAt       string
		updatedAt       string
	)
	if err := scanner.Scan(
		&output.OutputID,
		&output.RunID,
		&output.ToolCallID,
		&output.ToolName,
		&output.Content,
		&output.SizeBytes,
		&output.EstimatedTokens,
		&output.TotalLines,
		&storedTruncated,
		&inlineTruncated,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	output.StoredTruncated = storedTruncated != 0
	output.InlineTruncated = inlineTruncated != 0
	output.CreatedAt = mustParseTime(createdAt)
	output.UpdatedAt = mustParseTime(updatedAt)
	return &output, nil
}

func splitToolOutputLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func buildToolOutputMatchPreview(line string, matchIndex []int, maxChars int) string {
	if maxChars <= 0 || line == "" {
		return ""
	}

	runes := []rune(line)
	if len(runes) <= maxChars || len(matchIndex) != 2 {
		return line
	}

	matchStart := utf8.RuneCountInString(line[:matchIndex[0]])
	matchLength := utf8.RuneCountInString(line[matchIndex[0]:matchIndex[1]])
	if matchLength <= 0 {
		matchLength = 1
	}
	matchEnd := min(len(runes), matchStart+matchLength)

	prefixChars := 0
	if matchStart > 0 {
		prefixChars = 3
	}
	suffixChars := 0
	if matchEnd < len(runes) {
		suffixChars = 3
	}

	windowChars := maxChars - prefixChars - suffixChars
	if windowChars <= 0 {
		windowChars = maxChars
	}
	if windowChars < matchLength {
		windowChars = matchLength
	}

	contextChars := max(0, windowChars-matchLength)
	start := matchStart - contextChars/2
	if start < 0 {
		start = 0
	}
	end := start + windowChars
	if end < matchEnd {
		end = matchEnd
		start = max(0, end-windowChars)
	}
	if end > len(runes) {
		end = len(runes)
		start = max(0, end-windowChars)
	}

	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(runes) {
		suffix = "..."
	}

	window := string(runes[start:end])
	snippet := prefix + window + suffix
	snippetRunes := []rune(snippet)
	if len(snippetRunes) > maxChars {
		snippet = string(snippetRunes[:maxChars])
	}
	return snippet
}
