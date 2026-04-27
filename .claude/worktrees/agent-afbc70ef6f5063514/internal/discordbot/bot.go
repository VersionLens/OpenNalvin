package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/bwmarrin/discordgo"
	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const (
	WorkspaceName      = "discord"
	markerProgress     = "nalvin/progress"
	markerFinal        = "nalvin/final"
	markerToolResponse = "nalvin/tool"
	maxDiscordContent  = 1900
	maxEmbedText       = 3800
	defaultFetchLimit  = 100
)

type discordSession interface {
	AddHandler(handler interface{}) func()
	Open() error
	Close() error
	UpdateGameStatus(idle int, name string) error
	Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessage(channelID, messageID string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error)
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageEditComplex(m *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
	MessageThreadStartComplex(channelID, messageID string, data *discordgo.ThreadStart, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error
}

type agentRunnerFunc func(context.Context, *knowledge.Store, agentpkg.RunRequest, agentpkg.RunOptions) (string, error)

type Bot struct {
	cfg           config.Config
	store         *knowledge.Store
	logger        *slog.Logger
	session       discordSession
	runAgent      agentRunnerFunc
	flushInterval time.Duration

	ctx context.Context

	botUserID string

	mu       sync.Mutex
	inFlight map[string]struct{}
}

type viewState struct {
	Progress           []string        `json:"progress"`
	ToolCalls          []toolCallState `json:"tool_calls"`
	SelectedToolCallID string          `json:"selected_tool_call_id,omitempty"`
}

type toolCallState struct {
	ToolCallID    string                        `json:"tool_call_id"`
	ToolName      string                        `json:"tool_name"`
	Input         string                        `json:"input,omitempty"`
	Output        string                        `json:"output,omitempty"`
	IsError       bool                          `json:"is_error,omitempty"`
	OutputRef     *agentpkg.StoredToolOutputRef `json:"output_ref,omitempty"`
	Completed     bool                          `json:"completed"`
	ResultSummary string                        `json:"result_summary,omitempty"`
}

type transcriptEntry struct {
	MessageID string
	Role      fantasy.MessageRole
	Content   string
}

type streamState struct {
	bot       *Bot
	channelID string
	messageID string
	runID     string
	viewID    string
	conv      *knowledge.DiscordConversation

	mu         sync.Mutex
	status     string
	content    strings.Builder
	view       viewState
	dirty      bool
	done       bool
	doneReason string
}

func New(cfg config.Config, store *knowledge.Store, logger *slog.Logger) (*Bot, error) {
	if store == nil {
		return nil, fmt.Errorf("knowledge store is required")
	}
	cfg.Workspace.Current = WorkspaceName
	if strings.TrimSpace(cfg.Discord.Token) == "" {
		return nil, fmt.Errorf("discord token not configured")
	}
	if logger == nil {
		logger = slog.Default()
	}
	session, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentGuilds |
		discordgo.IntentGuildMessages |
		discordgo.IntentDirectMessages |
		discordgo.IntentMessageContent
	return newWithSession(cfg, store, logger, session), nil
}

func newWithSession(cfg config.Config, store *knowledge.Store, logger *slog.Logger, session discordSession) *Bot {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.Workspace.Current = WorkspaceName
	return &Bot{
		cfg:           cfg,
		store:         store,
		logger:        logger,
		session:       session,
		runAgent:      agentpkg.Run,
		flushInterval: 900 * time.Millisecond,
		inFlight:      map[string]struct{}{},
	}
}

func (b *Bot) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.ctx = ctx
	b.session.AddHandler(b.handleReady)
	b.session.AddHandler(b.handleMessageCreate)
	b.session.AddHandler(b.handleInteractionCreate)

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord session: %w", err)
	}
	defer func() {
		if err := b.session.Close(); err != nil {
			b.logger.Error("close discord session", "error", err)
		}
	}()

	if status := strings.TrimSpace(b.cfg.Discord.StatusMessage); status != "" {
		if err := b.session.UpdateGameStatus(0, status); err != nil {
			b.logger.Warn("set discord status", "error", err)
		}
	}

	<-ctx.Done()
	return nil
}

func (b *Bot) handleReady(_ *discordgo.Session, ready *discordgo.Ready) {
	if ready == nil || ready.User == nil {
		return
	}
	b.botUserID = ready.User.ID
	b.logger.Info("discord bot ready", "bot_user_id", ready.User.ID)
}

func (b *Bot) handleMessageCreate(_ *discordgo.Session, evt *discordgo.MessageCreate) {
	if evt == nil || evt.Message == nil || evt.Author == nil {
		return
	}
	if evt.Author.Bot {
		return
	}
	if b.botUserID != "" && evt.Author.ID == b.botUserID {
		return
	}
	if evt.GuildID != "" && !b.guildAllowed(evt.GuildID) {
		return
	}
	go b.processInboundMessage(evt.Message)
}

func (b *Bot) handleInteractionCreate(_ *discordgo.Session, evt *discordgo.InteractionCreate) {
	if evt == nil || evt.Interaction == nil || evt.Type != discordgo.InteractionMessageComponent {
		return
	}
	if err := b.processInteraction(evt.Interaction); err != nil {
		b.logger.Error("handle discord interaction", "error", err)
	}
}

func (b *Bot) processInboundMessage(msg *discordgo.Message) {
	ctx := b.context()

	conv, targetChannelID, prompt, err := b.resolveConversation(ctx, msg)
	if err != nil {
		b.logger.Error("resolve discord conversation", "error", err, "channel_id", msg.ChannelID)
		_ = b.sendErrorMessage(msg.ChannelID, err)
		return
	}
	if strings.TrimSpace(prompt) == "" {
		return
	}

	release, ok := b.tryStartConversation(conv.ID)
	if !ok {
		_ = b.sendInfoMessage(targetChannelID, "I’m still working on the previous message in this conversation. Please wait for that turn to finish.")
		return
	}
	defer release()

	history, err := b.loadConversationHistory(ctx, conv, msg.ID)
	if err != nil {
		b.logger.Error("load discord history", "error", err, "conversation_id", conv.ID)
		_ = b.sendErrorMessage(targetChannelID, err)
		return
	}

	activeMsg, err := b.session.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
		Content:         "Thinking…",
		AllowedMentions: noMentions(),
		Embeds:          []*discordgo.MessageEmbed{markerEmbed(markerProgress, "thinking")},
	})
	if err != nil {
		b.logger.Error("send discord placeholder", "error", err, "channel_id", targetChannelID)
		return
	}

	viewID := fmt.Sprintf("discord_view_%s", activeMsg.ID)
	conv, err = b.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
		ID:                      conv.ID,
		Workspace:               conv.Workspace,
		GuildID:                 conv.GuildID,
		ChannelID:               conv.ChannelID,
		ThreadID:                conv.ThreadID,
		DMChannelID:             conv.DMChannelID,
		RootMessageID:           conv.RootMessageID,
		RunID:                   conv.RunID,
		BotUserID:               b.botUserID,
		ActiveResponseMessageID: activeMsg.ID,
		Status:                  agentpkg.AgentStatusRunning,
	})
	if err != nil {
		b.logger.Error("save discord conversation active message", "error", err, "conversation_id", conv.ID)
	}
	if _, err := b.store.SaveDiscordToolView(ctx, knowledge.SaveDiscordToolViewInput{
		ID:               viewID,
		ConversationID:   conv.ID,
		DiscordMessageID: activeMsg.ID,
		RunID:            conv.RunID,
		State:            json.RawMessage(`{"progress":[],"tool_calls":[]}`),
	}); err != nil {
		b.logger.Error("save initial discord tool view", "error", err, "conversation_id", conv.ID)
	}

	stream := &streamState{
		bot:       b,
		channelID: targetChannelID,
		messageID: activeMsg.ID,
		viewID:    viewID,
		conv:      conv,
		status:    "thinking",
	}

	flushDone := make(chan struct{})
	go func() {
		defer close(flushDone)
		ticker := time.NewTicker(b.flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				stream.finalize("aborted")
				_ = stream.flush(ctx)
				return
			case <-ticker.C:
				if err := stream.flush(ctx); err != nil {
					b.logger.Error("flush discord stream state", "error", err, "conversation_id", conv.ID)
				}
				if stream.isDone() {
					return
				}
			}
		}
	}()

	runID, runErr := b.runAgent(ctx, b.store, agentpkg.RunRequest{
		Message: prompt,
		RunID:   b.reusableRunID(ctx, conv),
		History: history,
	}, agentpkg.RunOptions{
		OnChunk: func(chunk agentpkg.TraceChunk) error {
			stream.onChunk(chunk)
			return nil
		},
		OnToolResult: func(evt agentpkg.ToolResultEvent) error {
			stream.onToolResult(evt)
			return nil
		},
	})
	stream.setRunID(runID)

	if runErr != nil {
		b.logger.Error("discord agent run failed", "error", runErr, "conversation_id", conv.ID, "run_id", runID)
		stream.fail(runErr)
	} else {
		stream.complete()
	}

	if err := stream.flush(ctx); err != nil {
		b.logger.Error("final discord flush", "error", err, "conversation_id", conv.ID)
	}
	<-flushDone

	status := agentpkg.AgentStatusCompleted
	if runErr != nil {
		status = agentpkg.AgentStatusFailed
	}
	if _, err := b.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
		ID:                      conv.ID,
		Workspace:               conv.Workspace,
		GuildID:                 conv.GuildID,
		ChannelID:               conv.ChannelID,
		ThreadID:                conv.ThreadID,
		DMChannelID:             conv.DMChannelID,
		RootMessageID:           conv.RootMessageID,
		RunID:                   runID,
		BotUserID:               b.botUserID,
		ActiveResponseMessageID: activeMsg.ID,
		Status:                  status,
	}); err != nil {
		b.logger.Error("persist discord conversation completion", "error", err, "conversation_id", conv.ID)
	}
}

func (b *Bot) processInteraction(interaction *discordgo.Interaction) error {
	data := interaction.MessageComponentData()
	parts := strings.Split(data.CustomID, ":")
	if len(parts) != 3 || parts[0] != "nalvin" {
		return nil
	}
	action := parts[1]
	viewID := parts[2]

	view, err := b.store.GetDiscordToolView(b.context(), viewID)
	if err != nil {
		return respondInteractionError(b.session, interaction, "I couldn’t load the tool view state for that message.")
	}

	var state viewState
	if err := json.Unmarshal(view.State, &state); err != nil {
		return respondInteractionError(b.session, interaction, "That tool view state is invalid.")
	}

	switch action {
	case "tool-select":
		if len(data.Values) == 0 {
			return respondInteractionError(b.session, interaction, "Please choose a tool call first.")
		}
		state.SelectedToolCallID = data.Values[0]
		if err := b.persistToolViewSelection(view, state); err != nil {
			return respondInteractionError(b.session, interaction, "I couldn’t save that tool selection.")
		}
		call := findToolCall(state, state.SelectedToolCallID)
		if call == nil {
			return respondInteractionError(b.session, interaction, "That tool call is no longer available.")
		}
		return b.respondWithEmbed(interaction, toolCallEmbed("Selected Tool", *call))
	case "tool-input":
		call := selectedToolCall(state)
		if call == nil {
			return respondInteractionError(b.session, interaction, "Choose a tool from the menu first.")
		}
		return b.respondWithEmbed(interaction, &discordgo.MessageEmbed{
			Title:       fmt.Sprintf("Tool Input: %s", call.ToolName),
			Description: truncateForEmbed(codeBlockOrFallback(call.Input, "No input recorded.")),
			Footer:      &discordgo.MessageEmbedFooter{Text: markerToolResponse},
		})
	case "tool-output":
		call := selectedToolCall(state)
		if call == nil {
			return respondInteractionError(b.session, interaction, "Choose a tool from the menu first.")
		}
		description := codeBlockOrFallback(call.Output, "No output recorded.")
		if call.OutputRef != nil && strings.TrimSpace(view.RunID) != "" {
			if page, err := b.store.GetAgentRunToolOutputPage(b.context(), view.RunID, call.OutputRef.OutputID, 0, 50); err == nil {
				description = fmt.Sprintf("Stored output `%s`, lines %d-%d of %d.\n\n```text\n%s\n```",
					call.OutputRef.OutputID,
					page.StartLine,
					page.EndLine,
					page.TotalLines,
					page.Content,
				)
			}
		}
		return b.respondWithEmbed(interaction, &discordgo.MessageEmbed{
			Title:       fmt.Sprintf("Tool Output: %s", call.ToolName),
			Description: truncateForEmbed(description),
			Footer:      &discordgo.MessageEmbedFooter{Text: markerToolResponse},
		})
	case "progress":
		description := "No progress entries recorded."
		if len(state.Progress) > 0 {
			description = "```text\n" + strings.Join(state.Progress, "\n") + "\n```"
		}
		return b.respondWithEmbed(interaction, &discordgo.MessageEmbed{
			Title:       "Run Progress",
			Description: truncateForEmbed(description),
			Footer:      &discordgo.MessageEmbedFooter{Text: markerToolResponse},
		})
	default:
		return nil
	}
}

func (b *Bot) respondWithEmbed(interaction *discordgo.Interaction, embed *discordgo.MessageEmbed) error {
	return b.session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func respondInteractionError(session discordSession, interaction *discordgo.Interaction, message string) error {
	return session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (b *Bot) persistToolViewSelection(view *knowledge.DiscordToolView, state viewState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = b.store.SaveDiscordToolView(b.context(), knowledge.SaveDiscordToolViewInput{
		ID:                 view.ID,
		ConversationID:     view.ConversationID,
		DiscordMessageID:   view.DiscordMessageID,
		RunID:              view.RunID,
		SelectedToolCallID: state.SelectedToolCallID,
		State:              payload,
	})
	return err
}

func (b *Bot) resolveConversation(ctx context.Context, msg *discordgo.Message) (*knowledge.DiscordConversation, string, string, error) {
	if msg.GuildID == "" {
		prompt := strings.TrimSpace(msg.Content)
		if prompt == "" {
			return nil, "", "", nil
		}
		conv, err := b.store.GetDiscordConversationByDMChannel(ctx, msg.ChannelID)
		if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
			return nil, "", "", err
		}
		if conv == nil {
			conv, err = b.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
				Workspace:   b.cfg.Workspace.Current,
				ChannelID:   msg.ChannelID,
				DMChannelID: msg.ChannelID,
				BotUserID:   b.botUserID,
				Status:      "idle",
			})
			if err != nil {
				return nil, "", "", err
			}
		}
		return conv, msg.ChannelID, prompt, nil
	}

	if conv, err := b.store.GetDiscordConversationByThread(ctx, msg.ChannelID); err == nil {
		prompt := strings.TrimSpace(stripBotMention(msg.Content, b.botUserID))
		if prompt == "" {
			prompt = strings.TrimSpace(msg.Content)
		}
		return conv, msg.ChannelID, prompt, nil
	} else if !errors.Is(err, knowledge.ErrNotFound) {
		return nil, "", "", err
	}

	channel, err := b.session.Channel(msg.ChannelID)
	if err == nil && channel != nil && channel.IsThread() {
		if !mentionsBot(msg, b.botUserID) {
			return nil, "", "", nil
		}
		prompt := strings.TrimSpace(stripBotMention(msg.Content, b.botUserID))
		conv, err := b.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
			Workspace: b.cfg.Workspace.Current,
			GuildID:   msg.GuildID,
			ChannelID: channel.ParentID,
			ThreadID:  channel.ID,
			BotUserID: b.botUserID,
			Status:    "idle",
		})
		return conv, msg.ChannelID, prompt, err
	}

	if !mentionsBot(msg, b.botUserID) {
		return nil, "", "", nil
	}

	prompt := strings.TrimSpace(stripBotMention(msg.Content, b.botUserID))
	if prompt == "" {
		return nil, "", "", nil
	}
	thread, err := b.session.MessageThreadStartComplex(msg.ChannelID, msg.ID, &discordgo.ThreadStart{
		Name:                deriveThreadName(prompt, msg.Author.Username),
		AutoArchiveDuration: 1440,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("start discord thread: %w", err)
	}

	conv, err := b.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
		Workspace:     b.cfg.Workspace.Current,
		GuildID:       msg.GuildID,
		ChannelID:     msg.ChannelID,
		ThreadID:      thread.ID,
		RootMessageID: msg.ID,
		BotUserID:     b.botUserID,
		Status:        "idle",
	})
	if err != nil {
		return nil, "", "", err
	}
	return conv, thread.ID, prompt, nil
}

func (b *Bot) loadConversationHistory(ctx context.Context, conv *knowledge.DiscordConversation, currentMessageID string) ([]fantasy.Message, error) {
	entries := make([]transcriptEntry, 0, 32)

	if conv.ThreadID != "" && conv.RootMessageID != "" && conv.ChannelID != "" {
		rootMessage, err := b.session.ChannelMessage(conv.ChannelID, conv.RootMessageID)
		if err == nil && rootMessage != nil && rootMessage.Author != nil && !rootMessage.Author.Bot {
			content := strings.TrimSpace(stripBotMention(rootMessage.Content, b.botUserID))
			if content != "" {
				entries = append(entries, transcriptEntry{MessageID: rootMessage.ID, Role: fantasy.MessageRoleUser, Content: content})
			}
		}
	}

	channelID := conv.ThreadID
	if channelID == "" {
		channelID = conv.DMChannelID
	}
	messages, err := b.session.ChannelMessages(channelID, defaultFetchLimit, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("fetch discord messages: %w", err)
	}
	reverseMessages(messages)
	for _, msg := range messages {
		if msg == nil || msg.Author == nil {
			continue
		}
		if msg.Author.Bot {
			content, ok := canonicalAssistantContent(msg)
			if !ok {
				continue
			}
			if len(entries) > 0 && entries[len(entries)-1].Role == fantasy.MessageRoleAssistant {
				entries[len(entries)-1].Content += "\n" + content
				continue
			}
			entries = append(entries, transcriptEntry{MessageID: msg.ID, Role: fantasy.MessageRoleAssistant, Content: content})
			continue
		}
		content := strings.TrimSpace(stripBotMention(msg.Content, b.botUserID))
		if content == "" {
			continue
		}
		entries = append(entries, transcriptEntry{MessageID: msg.ID, Role: fantasy.MessageRoleUser, Content: content})
	}

	if len(entries) > 0 && entries[len(entries)-1].Role == fantasy.MessageRoleUser && entries[len(entries)-1].MessageID == currentMessageID {
		entries = entries[:len(entries)-1]
	}

	history := make([]fantasy.Message, 0, len(entries))
	for _, entry := range entries {
		switch entry.Role {
		case fantasy.MessageRoleUser:
			history = append(history, fantasy.NewUserMessage(entry.Content))
		case fantasy.MessageRoleAssistant:
			history = append(history, fantasy.Message{
				Role: fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{
					fantasy.TextPart{Text: entry.Content},
				},
			})
		}
	}
	return history, nil
}

func (b *Bot) reusableRunID(ctx context.Context, conv *knowledge.DiscordConversation) string {
	runID := strings.TrimSpace(conv.RunID)
	if runID == "" {
		return ""
	}
	if _, err := b.store.GetAgentRun(ctx, runID); err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return ""
		}
		b.logger.Warn("load existing discord run", "run_id", runID, "error", err)
		return ""
	}
	return runID
}

func (b *Bot) context() context.Context {
	if b.ctx != nil {
		return b.ctx
	}
	return context.Background()
}

func (b *Bot) guildAllowed(guildID string) bool {
	if len(b.cfg.Discord.GuildAllowlist) == 0 {
		return true
	}
	for _, allowed := range b.cfg.Discord.GuildAllowlist {
		if guildID == allowed {
			return true
		}
	}
	return false
}

func (b *Bot) tryStartConversation(conversationID string) (func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.inFlight[conversationID]; exists {
		return nil, false
	}
	b.inFlight[conversationID] = struct{}{}
	return func() {
		b.mu.Lock()
		delete(b.inFlight, conversationID)
		b.mu.Unlock()
	}, true
}

func (b *Bot) sendErrorMessage(channelID string, err error) error {
	return b.sendInfoMessage(channelID, "I hit an error while processing that message: "+err.Error())
}

func (b *Bot) sendInfoMessage(channelID, content string) error {
	_, err := b.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         truncateForDiscord(content, maxDiscordContent),
		AllowedMentions: noMentions(),
		Embeds:          []*discordgo.MessageEmbed{markerEmbed(markerProgress, "notice")},
	})
	return err
}

func (s *streamState) onChunk(chunk agentpkg.TraceChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if chunk.Error != nil {
		s.status = "failed"
		if strings.TrimSpace(chunk.Error.Message) != "" {
			s.content.WriteString("\n")
			s.content.WriteString(chunk.Error.Message)
		}
		s.dirty = true
		return
	}
	if len(chunk.Choices) == 0 {
		return
	}
	choice := chunk.Choices[0]
	if choice.FinishReason != nil {
		s.done = true
		s.doneReason = strings.TrimSpace(*choice.FinishReason)
		switch s.doneReason {
		case "", "stop":
			s.status = "done"
		case agentpkg.AgentStatusFailed, agentpkg.AgentStatusAborted:
			s.status = "failed"
		default:
			s.status = s.doneReason
		}
		s.dirty = true
		return
	}
	if choice.Delta == nil {
		return
	}
	if choice.Delta.Content != "" {
		s.status = "responding"
		s.content.WriteString(choice.Delta.Content)
		s.dirty = true
	}
	for _, call := range choice.Delta.ToolCalls {
		if call.Function == nil {
			continue
		}
		s.status = "calling tools"
		s.view.Progress = appendBounded(s.view.Progress, fmt.Sprintf("Calling %s", call.Function.Name), 20)
		s.view.ToolCalls = appendOrUpdateToolCall(s.view.ToolCalls, toolCallState{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Input:      call.Function.Arguments,
		})
		if s.view.SelectedToolCallID == "" {
			s.view.SelectedToolCallID = call.ID
		}
		s.dirty = true
	}
}

func (s *streamState) onToolResult(evt agentpkg.ToolResultEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = "waiting"
	s.view.Progress = appendBounded(s.view.Progress, fmt.Sprintf("Finished %s", evt.ToolName), 20)
	s.view.ToolCalls = appendOrUpdateToolCall(s.view.ToolCalls, toolCallState{
		ToolCallID:    evt.ToolCallID,
		ToolName:      evt.ToolName,
		Output:        evt.Output,
		IsError:       evt.IsError,
		OutputRef:     evt.OutputRef,
		Completed:     true,
		ResultSummary: summarizeToolResult(evt.Output, evt.IsError, evt.OutputRef),
	})
	if s.view.SelectedToolCallID == "" {
		s.view.SelectedToolCallID = evt.ToolCallID
	}
	s.dirty = true
}

func (s *streamState) setRunID(runID string) {
	s.mu.Lock()
	s.runID = strings.TrimSpace(runID)
	s.dirty = true
	s.mu.Unlock()
}

func (s *streamState) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = "failed"
	if err != nil {
		if s.content.Len() > 0 {
			s.content.WriteString("\n\n")
		}
		s.content.WriteString(err.Error())
	}
	s.done = true
	s.doneReason = agentpkg.AgentStatusFailed
	s.dirty = true
}

func (s *streamState) complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = "done"
	s.done = true
	if s.doneReason == "" {
		s.doneReason = "stop"
	}
	s.dirty = true
}

func (s *streamState) finalize(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	s.doneReason = reason
	if s.status == "" {
		s.status = reason
	}
	s.dirty = true
}

func (s *streamState) isDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *streamState) flush(ctx context.Context) error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	snapshot := s.snapshotLocked()
	s.dirty = false
	s.mu.Unlock()

	payload, err := json.Marshal(snapshot.view)
	if err != nil {
		return err
	}
	if _, err := s.bot.store.SaveDiscordToolView(ctx, knowledge.SaveDiscordToolViewInput{
		ID:                 s.viewID,
		ConversationID:     s.conv.ID,
		DiscordMessageID:   s.messageID,
		RunID:              snapshot.runID,
		SelectedToolCallID: snapshot.view.SelectedToolCallID,
		State:              payload,
	}); err != nil {
		return err
	}

	chunks := splitDiscordChunks(snapshot.text)
	first := "_No text response yet._"
	if len(chunks) > 0 && strings.TrimSpace(chunks[0]) != "" {
		first = chunks[0]
	}
	if !snapshot.done && strings.TrimSpace(snapshot.text) == "" {
		first = "Thinking…"
	}

	content := fmt.Sprintf("**Status:** %s\n\n%s", snapshot.status, first)
	components := buildComponents(s.viewID, snapshot.view)
	embeds := []*discordgo.MessageEmbed{markerEmbed(markerProgress, snapshot.status)}
	if snapshot.done {
		embeds = []*discordgo.MessageEmbed{markerEmbed(markerFinal, snapshot.status)}
		if len(chunks) > 0 {
			content = chunks[0]
		}
		components = buildComponents(s.viewID, snapshot.view)
	}

	edit := discordgo.NewMessageEdit(s.channelID, s.messageID)
	edit.Content = ptr(truncateForDiscord(content, maxDiscordContent))
	edit.AllowedMentions = noMentions()
	edit.Embeds = &embeds
	edit.Components = &components
	if _, err := s.bot.session.ChannelMessageEditComplex(edit); err != nil {
		return err
	}

	if snapshot.done && len(chunks) > 1 {
		for _, chunk := range chunks[1:] {
			if _, err := s.bot.session.ChannelMessageSendComplex(s.channelID, &discordgo.MessageSend{
				Content:         chunk,
				AllowedMentions: noMentions(),
				Embeds:          []*discordgo.MessageEmbed{markerEmbed(markerFinal, snapshot.status)},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

type streamSnapshot struct {
	status string
	text   string
	view   viewState
	done   bool
	runID  string
}

func (s *streamState) snapshotLocked() streamSnapshot {
	viewCopy := viewState{
		Progress:           append([]string(nil), s.view.Progress...),
		ToolCalls:          append([]toolCallState(nil), s.view.ToolCalls...),
		SelectedToolCallID: s.view.SelectedToolCallID,
	}
	return streamSnapshot{
		status: s.status,
		text:   s.content.String(),
		view:   viewCopy,
		done:   s.done,
		runID:  s.runID,
	}
}

func buildComponents(viewID string, state viewState) []discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(state.ToolCalls))
	for _, call := range state.ToolCalls {
		options = append(options, discordgo.SelectMenuOption{
			Label:       truncateForDiscord(call.ToolName, 80),
			Value:       call.ToolCallID,
			Description: truncateForDiscord(call.ResultSummary, 100),
			Default:     call.ToolCallID == state.SelectedToolCallID,
		})
	}

	inputDisabled := selectedToolCall(state) == nil
	outputDisabled := selectedToolCall(state) == nil
	progressDisabled := len(state.Progress) == 0

	components := []discordgo.MessageComponent{}
	if len(options) > 0 {
		minValues := 1
		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    "nalvin:tool-select:" + viewID,
					Placeholder: "Inspect a tool call",
					MinValues:   &minValues,
					MaxValues:   1,
					Options:     options,
				},
			},
		})
	}
	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{CustomID: "nalvin:tool-input:" + viewID, Label: "Input", Style: discordgo.SecondaryButton, Disabled: inputDisabled},
			discordgo.Button{CustomID: "nalvin:tool-output:" + viewID, Label: "Output", Style: discordgo.SecondaryButton, Disabled: outputDisabled},
			discordgo.Button{CustomID: "nalvin:progress:" + viewID, Label: "Progress", Style: discordgo.PrimaryButton, Disabled: progressDisabled},
		},
	})
	return components
}

func markerEmbed(marker, status string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Footer: &discordgo.MessageEmbedFooter{Text: marker},
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: status, Inline: true},
		},
	}
}

func toolCallEmbed(title string, call toolCallState) *discordgo.MessageEmbed {
	description := fmt.Sprintf("`%s`\n\n%s", call.ToolName, truncateForEmbed(codeBlockOrFallback(call.Input, "No input recorded.")))
	if call.Completed {
		description += "\n\n" + truncateForEmbed(codeBlockOrFallback(call.Output, "No output recorded."))
	}
	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Footer:      &discordgo.MessageEmbedFooter{Text: markerToolResponse},
	}
}

func splitDiscordChunks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	chunks := make([]string, 0, (len(text)/maxDiscordContent)+1)
	for len(text) > 0 {
		if len(text) <= maxDiscordContent {
			chunks = append(chunks, text)
			break
		}
		cut := maxDiscordContent
		for cut > 0 && text[cut] != '\n' && text[cut] != ' ' {
			cut--
		}
		if cut <= 0 {
			cut = maxDiscordContent
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	return chunks
}

func truncateForDiscord(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	return text[:limit-1] + "…"
}

func truncateForEmbed(text string) string {
	return truncateForDiscord(text, maxEmbedText)
}

func appendBounded(values []string, value string, limit int) []string {
	values = append(values, value)
	if limit > 0 && len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func appendOrUpdateToolCall(items []toolCallState, incoming toolCallState) []toolCallState {
	for i := range items {
		if items[i].ToolCallID != incoming.ToolCallID {
			continue
		}
		if incoming.ToolName != "" {
			items[i].ToolName = incoming.ToolName
		}
		if incoming.Input != "" {
			items[i].Input = incoming.Input
		}
		if incoming.Output != "" {
			items[i].Output = incoming.Output
		}
		if incoming.OutputRef != nil {
			items[i].OutputRef = incoming.OutputRef
		}
		if incoming.ResultSummary != "" {
			items[i].ResultSummary = incoming.ResultSummary
		}
		items[i].IsError = incoming.IsError
		items[i].Completed = items[i].Completed || incoming.Completed
		return items
	}
	return append(items, incoming)
}

func summarizeToolResult(output string, isError bool, ref *agentpkg.StoredToolOutputRef) string {
	if ref != nil {
		return fmt.Sprintf("stored output %s (%d lines)", ref.OutputID, ref.TotalLines)
	}
	prefix := "ok"
	if isError {
		prefix = "error"
	}
	return prefix + ": " + truncateForDiscord(strings.ReplaceAll(strings.TrimSpace(output), "\n", " "), 90)
}

func codeBlockOrFallback(text, fallback string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback
	}
	return "```text\n" + truncateForEmbed(text) + "\n```"
}

func selectedToolCall(state viewState) *toolCallState {
	return findToolCall(state, state.SelectedToolCallID)
}

func findToolCall(state viewState, toolCallID string) *toolCallState {
	for i := range state.ToolCalls {
		if state.ToolCalls[i].ToolCallID == toolCallID {
			return &state.ToolCalls[i]
		}
	}
	if len(state.ToolCalls) == 0 {
		return nil
	}
	return &state.ToolCalls[len(state.ToolCalls)-1]
}

func mentionsBot(msg *discordgo.Message, botUserID string) bool {
	if strings.TrimSpace(botUserID) == "" {
		return false
	}
	for _, user := range msg.Mentions {
		if user != nil && user.ID == botUserID {
			return true
		}
	}
	return strings.Contains(msg.Content, "<@"+botUserID+">") || strings.Contains(msg.Content, "<@!"+botUserID+">")
}

func stripBotMention(content, botUserID string) string {
	if botUserID == "" {
		return strings.TrimSpace(content)
	}
	content = strings.ReplaceAll(content, "<@"+botUserID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botUserID+">", "")
	return strings.TrimSpace(content)
}

func canonicalAssistantContent(msg *discordgo.Message) (string, bool) {
	for _, embed := range msg.Embeds {
		if embed != nil && embed.Footer != nil && embed.Footer.Text == markerFinal {
			content := strings.TrimSpace(msg.Content)
			if content == "" && embed.Description != "" {
				content = strings.TrimSpace(embed.Description)
			}
			if content != "" {
				return content, true
			}
		}
	}
	return "", false
}

func noMentions() *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}
}

func deriveThreadName(prompt, username string) string {
	base := strings.TrimSpace(prompt)
	if base == "" {
		base = "Chat with " + strings.TrimSpace(username)
	}
	base = strings.ReplaceAll(base, "\n", " ")
	base = strings.TrimSpace(base)
	if len(base) <= 90 {
		return base
	}
	return strings.TrimSpace(base[:90]) + "…"
}

func reverseMessages(messages []*discordgo.Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i] == nil || messages[j] == nil {
			return i < j
		}
		return messages[i].Timestamp.Before(messages[j].Timestamp)
	})
}

func ptr[T any](value T) *T {
	return &value
}
