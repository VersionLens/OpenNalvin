package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/google/uuid"
	"github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

// Config holds the settings for the TUI, provided by the cobra command.
type Config struct {
	Store         *knowledge.Store
	ProviderName  string
	SystemPrompt  string
	Tools         agent.ToolSelection
	Verbose       bool
	ContextWindow int
}

// focusArea tracks which component has input focus.
type focusArea int

const (
	focusEditor focusArea = iota
	focusChat
)

// agentDoneMsg is sent when agent.Run returns successfully.
type agentDoneMsg struct{ RunID string }

// agentErrorMsg is sent when agent.Run returns an error.
type agentErrorMsg struct{ Err error }

// ProgramRefMsg delivers the *tea.Program reference so the model can start
// the broker→bubbletea bridging goroutine.
type ProgramRefMsg struct{ Program *tea.Program }

// Model is the top-level bubbletea model for the agent REPL.
type Model struct {
	cfg     Config
	service *Service
	chat    *Chat
	keys    keyMap

	textarea textarea.Model
	spinner  spinner.Model

	runID   string // reused for multi-turn continuation
	running bool
	focus   focusArea
	width   int
	height  int

	program *tea.Program
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a Model ready to be passed to tea.NewProgram.
func New(cfg Config) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (ctrl+d to send, /clear to reset)"
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.CharLimit = 0
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	ctx, cancel := context.WithCancel(context.Background())

	return Model{
		cfg:     cfg,
		service: NewService(),
		chat:    NewChat(),
		keys:    defaultKeyMap(),

		textarea: ta,
		spinner:  sp,

		focus:  focusEditor,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

// startBridge launches a goroutine that reads from the broker subscription
// and forwards every event to bubbletea via program.Send(). This keeps the
// broker channel drained regardless of bubbletea's frame rate, preventing
// event drops on the fixed-size channel buffer.
func (m *Model) startBridge() {
	ch := m.service.Subscribe(m.ctx)
	p := m.program
	go func() {
		for evt := range ch {
			p.Send(evt)
		}
	}()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case ProgramRefMsg:
		m.program = msg.Program
		m.startBridge()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case Event[Message]:
		return m.handleMessageEvent(msg)

	case agentDoneMsg:
		m.running = false
		m.runID = msg.RunID
		// Finalize the assistant item with glamour rendering.
		m.finalizeAssistant()
		return m, nil

	case agentErrorMsg:
		m.running = false
		errText := msg.Err.Error()
		if m.ctx.Err() != nil {
			errText = "Aborted."
		}
		m.service.Create(Message{
			ID:        uuid.New().String(),
			Role:      RoleAssistant,
			Done:      true,
			IsError:   true,
			ErrorText: errText,
		})
		return m, nil

	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	// Forward unhandled messages to the textarea when it has focus.
	if m.focus == focusEditor {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "ctrl+c":
		if m.running {
			m.cancel()
			m.ctx, m.cancel = context.WithCancel(context.Background())
			m.startBridge()
			return m, nil
		}
		return m, tea.Quit

	case msg.String() == "ctrl+d":
		if m.focus == focusEditor {
			return m.submit()
		}

	case msg.String() == "ctrl+l":
		return m.clearConversation()

	case msg.String() == "tab":
		if m.focus == focusEditor {
			m.focus = focusChat
			m.textarea.Blur()
		} else {
			m.focus = focusEditor
			m.textarea.Focus()
		}
		return m, nil

	case msg.String() == "up" || msg.String() == "k":
		if m.focus == focusChat {
			m.chat.ScrollUp(1)
			return m, nil
		}

	case msg.String() == "down" || msg.String() == "j":
		if m.focus == focusChat {
			m.chat.ScrollDown(1)
			return m, nil
		}

	case msg.String() == "pgup" || msg.String() == "ctrl+u":
		if m.focus == focusChat {
			m.chat.ScrollUp(m.chat.height / 2)
			return m, nil
		}

	case msg.String() == "pgdown":
		if m.focus == focusChat {
			m.chat.ScrollDown(m.chat.height / 2)
			return m, nil
		}
	}

	// Forward to textarea.
	if m.focus == focusEditor {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleMessageEvent(evt Event[Message]) (tea.Model, tea.Cmd) {
	switch evt.Type {
	case Created:
		items := ExtractItems(evt.Payload)
		m.chat.AppendItems(evt.Payload.ID, items...)
	case Updated:
		items := ExtractItems(evt.Payload)
		m.chat.UpdateItems(evt.Payload.ID, items)
	case Deleted:
		// Handled via clearConversation which calls chat.Clear() directly.
	}

	return m, nil
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textarea.Value())
	if input == "" {
		return m, nil
	}
	m.textarea.Reset()

	if input == "/clear" {
		return m.clearConversation()
	}

	// Create the user message.
	m.service.Create(Message{
		ID:      uuid.New().String(),
		Role:    RoleUser,
		Content: input,
		Done:    true,
	})

	m.running = true
	return m, tea.Batch(m.runAgent(input), m.spinner.Tick)
}

func (m Model) clearConversation() (tea.Model, tea.Cmd) {
	m.cancel()
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.service.Clear()
	m.chat.Clear()
	m.runID = ""
	m.running = false
	m.startBridge()

	// Add a status item.
	m.chat.AppendItems("status:clear", &StatusMessageItem{
		id:   "status:clear:" + uuid.New().String(),
		Text: "— conversation cleared —",
	})

	return m, nil
}

// runAgent launches agent.Run in a goroutine (via tea.Cmd) and bridges
// streaming callbacks to the message service.
func (m Model) runAgent(message string) tea.Cmd {
	svc := m.service
	ctx := m.ctx
	cfg := m.cfg
	runID := m.runID

	return func() tea.Msg {
		assistantID := uuid.New().String()
		svc.Create(Message{ID: assistantID, Role: RoleAssistant})

		onChunk := func(chunk agent.TraceChunk) error {
			// Initial chunk: role=assistant, captures the run start.
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil && chunk.Choices[0].Delta.Role == "assistant" {
				return nil
			}

			// Error chunk.
			if chunk.Error != nil {
				svc.Update(assistantID, func(msg *Message) {
					msg.IsError = true
					msg.ErrorText = chunk.Error.Message
				})
				return nil
			}

			if len(chunk.Choices) == 0 {
				return nil
			}
			choice := chunk.Choices[0]

			// Finish chunk.
			if choice.FinishReason != nil {
				svc.Update(assistantID, func(msg *Message) {
					msg.Done = true
				})
				return nil
			}

			delta := choice.Delta
			if delta == nil {
				return nil
			}

			// Text content.
			if delta.Content != "" {
				svc.Update(assistantID, func(msg *Message) {
					msg.Content += delta.Content
				})
			}

			// Reasoning.
			if delta.Reasoning != "" {
				svc.Update(assistantID, func(msg *Message) {
					msg.Reasoning += delta.Reasoning
				})
			}

			// Tool calls.
			for _, call := range delta.ToolCalls {
				if call.ID == "" || call.Function == nil {
					continue
				}
				svc.Update(assistantID, func(msg *Message) {
					msg.ToolCalls = append(msg.ToolCalls, ToolCall{
						ID:        call.ID,
						ToolName:  call.Function.Name,
						Arguments: call.Function.Arguments,
					})
				})
			}

			return nil
		}

		onToolResult := func(evt agent.ToolResultEvent) error {
			svc.Update(assistantID, func(msg *Message) {
				msg.ToolResults = append(msg.ToolResults, ToolResult{
					ToolCallID: evt.ToolCallID,
					ToolName:   evt.ToolName,
					Output:     evt.Output,
					IsError:    evt.IsError,
				})
			})
			return nil
		}

		resultRunID, err := agent.Run(ctx, cfg.Store, agent.RunRequest{
			Message:      message,
			RunID:        runID,
			ProviderName: cfg.ProviderName,
			SystemPrompt: cfg.SystemPrompt,
			Tools:        cfg.Tools,
		}, agent.RunOptions{
			OnChunk:               onChunk,
			OnToolResult:          onToolResult,
			Verbose:               cfg.Verbose,
			ContextWindowOverride: cfg.ContextWindow,
		})

		if err != nil {
			return agentErrorMsg{Err: err}
		}
		return agentDoneMsg{RunID: resultRunID}
	}
}

// finalizeAssistant finds the last assistant text item and applies glamour
// markdown rendering now that streaming is complete.
func (m *Model) finalizeAssistant() {
	for i := len(m.chat.items) - 1; i >= 0; i-- {
		if item, ok := m.chat.items[i].(*AssistantMessageItem); ok {
			item.msg.Done = true
			item.Finalize(m.width)
			break
		}
	}
}

func (m *Model) layout() {
	taHeight := 3
	statusHeight := 1
	chatHeight := m.height - taHeight - statusHeight - 2 // 2 for borders/gaps
	if chatHeight < 1 {
		chatHeight = 1
	}
	m.chat.SetSize(m.width, chatHeight)
	m.textarea.SetWidth(m.width)
}

func (m Model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Loading...")
	}

	chatView := m.chat.View()
	status := m.statusBar()
	taView := m.textarea.View()

	v := tea.NewView(chatView + "\n" + status + "\n" + taView)
	v.AltScreen = true
	return v
}

func (m Model) statusBar() string {
	left := " nalvin repl"
	if m.runID != "" {
		left += fmt.Sprintf("  run:%s", m.runID[:8])
	}

	right := "ctrl+d send | tab focus | ctrl+l clear | ctrl+c quit"
	if m.running {
		right = m.spinner.View() + " running... | ctrl+c cancel"
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	bar := left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(m.width).Render(bar)
}
