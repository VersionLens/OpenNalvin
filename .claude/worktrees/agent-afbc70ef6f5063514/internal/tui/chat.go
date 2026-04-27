package tui

import (
	"strings"
)

// Chat manages a scrollable list of MessageItems with follow-mode
// auto-scrolling, following Crush's Chat component pattern.
type Chat struct {
	items   []MessageItem
	idIndex map[string]int // message ID → index of first item for that message

	offset int // scroll offset in lines from top
	follow bool
	height int // visible area height
	width  int // render width
}

// NewChat creates a Chat with follow-mode enabled.
func NewChat() *Chat {
	return &Chat{
		idIndex: make(map[string]int),
		follow:  true,
	}
}

// SetSize updates the visible dimensions.
func (c *Chat) SetSize(width, height int) {
	c.width = width
	c.height = height
}

// AppendItems adds items and records the message-level index mapping.
func (c *Chat) AppendItems(msgID string, items ...MessageItem) {
	if len(items) == 0 {
		return
	}
	if _, exists := c.idIndex[msgID]; !exists {
		c.idIndex[msgID] = len(c.items)
	}
	c.items = append(c.items, items...)
}

// UpdateItems replaces all items associated with a message ID.
func (c *Chat) UpdateItems(msgID string, items []MessageItem) {
	startIdx, exists := c.idIndex[msgID]
	if !exists {
		// New message — append.
		c.AppendItems(msgID, items...)
		return
	}

	// Find the range of existing items for this message by scanning forward
	// and checking each item's base ID directly.
	endIdx := startIdx
	for endIdx < len(c.items) {
		if messageBaseID(c.items[endIdx].ItemID()) != msgID {
			break
		}
		endIdx++
	}

	// Replace the range [startIdx, endIdx) with the new items.
	oldLen := endIdx - startIdx
	newLen := len(items)

	if newLen == oldLen {
		copy(c.items[startIdx:], items)
	} else {
		tail := make([]MessageItem, len(c.items[endIdx:]))
		copy(tail, c.items[endIdx:])
		c.items = append(c.items[:startIdx], items...)
		c.items = append(c.items, tail...)

		// Rebuild the index since positions shifted.
		c.rebuildIndex()
	}
}

// Clear removes all items and resets scroll state.
func (c *Chat) Clear() {
	c.items = nil
	c.idIndex = make(map[string]int)
	c.offset = 0
	c.follow = true
}

// ScrollUp scrolls up by n lines and disables follow-mode.
func (c *Chat) ScrollUp(n int) {
	c.follow = false
	c.offset -= n
	if c.offset < 0 {
		c.offset = 0
	}
}

// ScrollDown scrolls down by n lines. Re-enables follow-mode if at bottom.
func (c *Chat) ScrollDown(n int) {
	c.offset += n
	totalLines := c.totalLines()
	maxOffset := totalLines - c.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if c.offset >= maxOffset {
		c.offset = maxOffset
		c.follow = true
	}
}

// View renders the visible portion of the chat.
func (c *Chat) View() string {
	if len(c.items) == 0 {
		return ""
	}

	// Render all items with separators.
	var all strings.Builder
	for idx, item := range c.items {
		if idx > 0 {
			all.WriteString("\n\n")
		}
		all.WriteString(item.Render(c.width))
	}

	lines := strings.Split(all.String(), "\n")
	totalLines := len(lines)

	// In follow-mode, pin to bottom.
	if c.follow {
		c.offset = totalLines - c.height
		if c.offset < 0 {
			c.offset = 0
		}
	}

	// Clamp offset.
	maxOffset := totalLines - c.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if c.offset > maxOffset {
		c.offset = maxOffset
	}

	// Extract the visible window.
	start := c.offset
	end := start + c.height
	if end > totalLines {
		end = totalLines
	}
	visible := lines[start:end]

	// Pad with empty lines if content doesn't fill the viewport.
	for len(visible) < c.height {
		visible = append(visible, "")
	}

	return strings.Join(visible, "\n")
}

// totalLines counts rendered lines (approximation for scroll math).
func (c *Chat) totalLines() int {
	if len(c.items) == 0 {
		return 0
	}
	var all strings.Builder
	for idx, item := range c.items {
		if idx > 0 {
			all.WriteString("\n\n")
		}
		all.WriteString(item.Render(c.width))
	}
	return strings.Count(all.String(), "\n") + 1
}

func (c *Chat) rebuildIndex() {
	c.idIndex = make(map[string]int)
	for i, item := range c.items {
		id := item.ItemID()
		// Extract the message-level ID (everything before the first ':' suffix).
		msgID := messageBaseID(id)
		if _, exists := c.idIndex[msgID]; !exists {
			c.idIndex[msgID] = i
		}
	}
}

// messageBaseID extracts the message ID from an item ID.
// Item IDs have the form "msgID" or "msgID:suffix".
func messageBaseID(itemID string) string {
	// The message ID is a UUID which doesn't contain ':'.
	if i := strings.Index(itemID, ":"); i >= 0 {
		return itemID[:i]
	}
	return itemID
}
