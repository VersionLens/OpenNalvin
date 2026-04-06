package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// MessageItem is the display unit in the chat list. Each message may produce
// one or more items (e.g. an assistant message with tool calls produces a text
// item plus one item per tool call and one per tool result).
type MessageItem interface {
	ItemID() string
	Render(width int) string
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

type StatusMessageItem struct {
	id   string
	Text string
}

func (i *StatusMessageItem) ItemID() string          { return i.id }
func (i *StatusMessageItem) Render(width int) string { return statusText.Render(i.Text) }

// ---------------------------------------------------------------------------
// User
// ---------------------------------------------------------------------------

type UserMessageItem struct {
	id  string
	msg Message
}

func (i *UserMessageItem) ItemID() string { return i.id }
func (i *UserMessageItem) Render(width int) string {
	return userLabel.Render("You:") + "\n" + wrap(i.msg.Content, width)
}

// ---------------------------------------------------------------------------
// Assistant text
// ---------------------------------------------------------------------------

type AssistantMessageItem struct {
	id       string
	msg      Message
	rendered string // glamour-rendered markdown, set when Done
}

func (i *AssistantMessageItem) ItemID() string { return i.id }
func (i *AssistantMessageItem) Render(width int) string {
	var b strings.Builder

	if i.msg.Reasoning != "" {
		b.WriteString(reasoningStyle.Render("reasoning:") + "\n")
		b.WriteString(reasoningStyle.Render(wrap(i.msg.Reasoning, width)))
		if i.msg.Content != "" {
			b.WriteString("\n\n")
		}
	}

	b.WriteString(assistantLabel.Render("Assistant:") + "\n")

	if i.msg.Done && i.rendered != "" {
		b.WriteString(i.rendered)
	} else {
		b.WriteString(wrap(i.msg.Content, width))
	}

	return b.String()
}

// Finalize renders the assistant content through glamour for markdown display.
func (i *AssistantMessageItem) Finalize(width int) {
	if i.msg.Content == "" {
		return
	}
	renderWidth := width
	if renderWidth > 120 {
		renderWidth = 120
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithEnvironmentConfig(),
		glamour.WithWordWrap(renderWidth),
	)
	if err != nil {
		return
	}
	rendered, err := r.Render(i.msg.Content)
	if err == nil {
		i.rendered = strings.TrimRight(rendered, "\n")
	}
}

// ---------------------------------------------------------------------------
// Tool call
// ---------------------------------------------------------------------------

type ToolCallMessageItem struct {
	id   string
	call ToolCall
}

func (i *ToolCallMessageItem) ItemID() string { return i.id }
func (i *ToolCallMessageItem) Render(width int) string {
	header := toolCallLabel.Render(fmt.Sprintf("Tool: %s", i.call.ToolName))
	args := prettyJSON(i.call.Arguments)
	if args != "" {
		return header + "\n" + toolArgStyle.Render(wrap(args, width))
	}
	return header
}

// ---------------------------------------------------------------------------
// Tool result
// ---------------------------------------------------------------------------

type ToolResultMessageItem struct {
	id     string
	result ToolResult
}

func (i *ToolResultMessageItem) ItemID() string { return i.id }
func (i *ToolResultMessageItem) Render(width int) string {
	label := toolResultLabel.Render(fmt.Sprintf("Result: %s", i.result.ToolName))
	output := truncateLines(i.result.Output, 20)
	style := toolOutputStyle
	if i.result.IsError {
		style = errorStyle
	}
	if output != "" {
		return label + "\n" + style.Render(wrap(output, width))
	}
	return label
}

// ---------------------------------------------------------------------------
// Error
// ---------------------------------------------------------------------------

type ErrorMessageItem struct {
	id   string
	Text string
}

func (i *ErrorMessageItem) ItemID() string          { return i.id }
func (i *ErrorMessageItem) Render(width int) string { return errorStyle.Render(wrap(i.Text, width)) }

// ---------------------------------------------------------------------------
// ExtractItems converts a Message into display items.
// ---------------------------------------------------------------------------

func ExtractItems(msg Message) []MessageItem {
	var items []MessageItem

	switch msg.Role {
	case RoleUser:
		items = append(items, &UserMessageItem{id: msg.ID, msg: msg})

	case RoleAssistant:
		if msg.Content != "" || msg.Reasoning != "" || !msg.Done {
			items = append(items, &AssistantMessageItem{id: msg.ID + ":text", msg: msg})
		}
		for idx, tc := range msg.ToolCalls {
			items = append(items, &ToolCallMessageItem{
				id:   fmt.Sprintf("%s:tc:%d", msg.ID, idx),
				call: tc,
			})
		}
		for idx, tr := range msg.ToolResults {
			items = append(items, &ToolResultMessageItem{
				id:     fmt.Sprintf("%s:tr:%d", msg.ID, idx),
				result: tr,
			})
		}
		if msg.IsError {
			items = append(items, &ErrorMessageItem{
				id:   msg.ID + ":err",
				Text: msg.ErrorText,
			})
		}
	}

	return items
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func prettyJSON(raw string) string {
	if raw == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return buf.String()
}

// wrap soft-wraps text to the given width using lipgloss.
func wrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	truncated := lines[:maxLines]
	truncated = append(truncated, fmt.Sprintf("... (%d lines truncated)", len(lines)-maxLines))
	return strings.Join(truncated, "\n")
}
