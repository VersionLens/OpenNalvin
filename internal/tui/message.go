package tui

import "sync"

// Role identifies the sender of a message.
type Role int

const (
	RoleUser Role = iota
	RoleAssistant
)

// ToolCall records a single tool invocation within an assistant message.
type ToolCall struct {
	ID        string
	ToolName  string
	Arguments string // raw JSON
}

// ToolResult records the outcome of a tool invocation.
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Output     string
	IsError    bool
}

// Message represents one chat message (user or assistant). Assistant messages
// accumulate content, reasoning, tool calls, and tool results as they stream.
type Message struct {
	ID          string
	Role        Role
	Content     string // text body (grows during streaming)
	Reasoning   string // reasoning tokens (grows during streaming)
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	Done        bool // true once the turn is complete
	IsError     bool
	ErrorText   string
}

// Service manages an ordered list of messages and publishes mutations through
// an embedded Broker[Message], following the Crush pattern where the agent
// layer mutates messages via the service and the UI subscribes to events.
type Service struct {
	*Broker[Message]
	mu       sync.RWMutex
	messages []Message
}

// NewService creates a ready-to-use message service.
func NewService() *Service {
	return &Service{
		Broker: NewBroker[Message](),
	}
}

// Create appends a new message and publishes a Created event.
func (s *Service) Create(msg Message) {
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	s.Publish(Event[Message]{Type: Created, Payload: msg})
}

// Update applies fn to the message with the given ID and publishes an Updated
// event with the new state. fn is called under the write lock.
func (s *Service) Update(id string, fn func(*Message)) {
	s.mu.Lock()
	var updated Message
	for i := range s.messages {
		if s.messages[i].ID == id {
			fn(&s.messages[i])
			updated = s.messages[i]
			break
		}
	}
	s.mu.Unlock()

	if updated.ID != "" {
		s.Publish(Event[Message]{Type: Updated, Payload: updated})
	}
}

// Messages returns a snapshot copy of all messages.
func (s *Service) Messages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Clear removes all messages and publishes a Deleted event for each.
func (s *Service) Clear() {
	s.mu.Lock()
	old := s.messages
	s.messages = nil
	s.mu.Unlock()

	for _, msg := range old {
		s.Publish(Event[Message]{Type: Deleted, Payload: msg})
	}
}
