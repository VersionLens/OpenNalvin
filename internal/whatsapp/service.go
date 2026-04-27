package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/encoding/protojson"
	gproto "google.golang.org/protobuf/proto"

	wa "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waevents "go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

const (
	liveSource          = "live"
	historySyncSource   = "history_sync"
	outboundSource      = "outbound"
	maxReplyChars       = 3500
	defaultListLimit    = 100
	groupRefreshTimeout = 15 * time.Second
)

// AgentRunFunc bridges incoming WhatsApp messages to the agent runtime.
// It mirrors the signature of internal/agent.Run.
type AgentRunFunc func(context.Context, *knowledge.Store, agentpkg.RunRequest, agentpkg.RunOptions) (string, error)

type Service struct {
	cfg      config.Config
	store    *knowledge.Store
	logger   *slog.Logger
	runAgent AgentRunFunc
	sendText func(context.Context, string, string, string) (*SendTextResult, error)

	container *sqlstore.Container
	client    *wa.Client

	lifecycleCtx      context.Context
	authMu            sync.Mutex
	mu                sync.Mutex
	started           bool
	inFlight          map[string]struct{}
	historySyncWaiter []chan struct{}
}

type Status struct {
	Enabled       bool   `json:"enabled"`
	Workspace     string `json:"workspace"`
	SessionDBPath string `json:"session_db_path"`
	Connected     bool   `json:"connected"`
	LoggedIn      bool   `json:"logged_in"`
	NeedsAuth     bool   `json:"needs_auth"`
	UserJID       string `json:"user_jid,omitempty"`
	PushName      string `json:"push_name,omitempty"`
}

type SendTextResult struct {
	ChatJID   string    `json:"chat_jid"`
	MessageID string    `json:"message_id"`
	Timestamp time.Time `json:"timestamp"`
	ReplyToID string    `json:"reply_to_id,omitempty"`
	Text      string    `json:"text"`
	StoredID  string    `json:"stored_id,omitempty"`
	QuotedJID string    `json:"quoted_jid,omitempty"`
}

func (s *Service) agentContext(ctx context.Context) context.Context {
	if ctx == nil {
		s.mu.Lock()
		ctx = s.lifecycleCtx
		s.mu.Unlock()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return config.WithContext(ctx, s.cfg)
}

func NewService(cfg config.Config, store *knowledge.Store, logger *slog.Logger, runAgent AgentRunFunc) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("knowledge store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if runAgent == nil {
		runAgent = agentpkg.Run
	}
	svc := &Service{
		cfg:      cfg,
		store:    store,
		logger:   logger,
		runAgent: runAgent,
		inFlight: map[string]struct{}{},
	}
	svc.sendText = svc.SendText
	return svc, nil
}

func (s *Service) sendTextMessage(ctx context.Context, chatJID, text, replyTo string) (*SendTextResult, error) {
	if s.sendText != nil {
		return s.sendText(ctx, chatJID, text, replyTo)
	}
	return s.SendText(ctx, chatJID, text, replyTo)
}

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if !s.cfg.WhatsApp.Enabled {
		s.started = true
		return nil
	}
	container, client, err := openClient(ctx, s.cfg, s.logger)
	if err != nil {
		return err
	}
	s.container = container
	s.client = client
	s.lifecycleCtx = ctx
	client.AddEventHandler(s.handleEvent)

	s.started = true
	if client.Store != nil && client.Store.ID != nil {
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect whatsapp client: %w", err)
		}
		if err := client.SendPresence(ctx, types.PresenceAvailable); err != nil {
			s.logger.Warn("set whatsapp presence", "error", err)
		}
		go s.refreshJoinedGroupsAsync(ctx)
	} else {
		s.logger.Info("whatsapp authentication required", "session_db_path", s.cfg.WhatsApp.SessionDBPath)
	}

	go func() {
		<-ctx.Done()
		s.Close()
	}()
	return nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	if s.client != nil {
		s.client.Disconnect()
		s.client = nil
	}
	if s.container != nil {
		if err := s.container.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.container = nil
	}
	s.lifecycleCtx = nil
	s.started = false
	return firstErr
}

func (s *Service) Status(_ context.Context) Status {
	status := Status{
		Enabled:       s.cfg.WhatsApp.Enabled,
		Workspace:     s.cfg.WhatsApp.Workspace,
		SessionDBPath: s.cfg.WhatsApp.SessionDBPath,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		status.Connected = s.client.IsConnected()
		status.LoggedIn = s.client.IsLoggedIn()
		if s.client.Store != nil {
			if s.client.Store.ID != nil {
				status.UserJID = s.client.Store.ID.String()
			}
			status.PushName = strings.TrimSpace(s.client.Store.PushName)
		}
	}
	status.NeedsAuth = s.cfg.WhatsApp.Enabled && status.UserJID == ""
	return status
}

// ProbeStatus checks the local session DB without connecting to WhatsApp,
// so it won't conflict with a running server's connection.
func ProbeStatus(ctx context.Context, cfg config.Config, logger *slog.Logger) (Status, error) {
	status := Status{
		Enabled:       cfg.WhatsApp.Enabled,
		Workspace:     cfg.WhatsApp.Workspace,
		SessionDBPath: cfg.WhatsApp.SessionDBPath,
	}
	container, client, err := openClient(ctx, cfg, logger)
	if err != nil {
		return status, err
	}
	defer func() {
		client.Disconnect()
		_ = container.Close()
	}()
	if client.Store == nil || client.Store.ID == nil {
		status.NeedsAuth = cfg.WhatsApp.Enabled
		return status, nil
	}
	status.UserJID = client.Store.ID.String()
	status.PushName = strings.TrimSpace(client.Store.PushName)
	return status, nil
}

func (s *Service) ListChats(ctx context.Context, limit int) ([]knowledge.WhatsAppChat, error) {
	return s.store.ListWhatsAppChats(ctx, s.cfg.WhatsApp.Workspace, limit)
}

func (s *Service) ListMessages(ctx context.Context, chatJID string, limit int) ([]knowledge.WhatsAppMessage, error) {
	return s.store.ListWhatsAppMessages(ctx, knowledge.WhatsAppMessageFilter{ChatJID: chatJID, Limit: limit})
}

func (s *Service) ListMediaMessages(ctx context.Context, chatJID string, limit int) ([]knowledge.WhatsAppMessage, error) {
	return s.store.ListWhatsAppMessages(ctx, knowledge.WhatsAppMessageFilter{
		ChatJID:   chatJID,
		MediaOnly: true,
		Limit:     limit,
	})
}

func (s *Service) Logout(ctx context.Context) error {
	client, err := s.requireClient()
	if err != nil {
		return err
	}
	return client.Logout(ctx)
}

func (s *Service) SendText(ctx context.Context, chatJID, text, replyTo string) (*SendTextResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return nil, err
	}
	chat, err := types.ParseJID(strings.TrimSpace(chatJID))
	if err != nil {
		return nil, fmt.Errorf("parse chat jid: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	msg := &waE2E.Message{Conversation: gproto.String(text)}
	if strings.TrimSpace(replyTo) != "" {
		msg = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: gproto.String(text),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID:  gproto.String(strings.TrimSpace(replyTo)),
					RemoteJID: gproto.String(chat.String()),
				},
			},
		}
	}
	resp, err := client.SendMessage(ctx, chat, msg)
	if err != nil {
		return nil, fmt.Errorf("send whatsapp message: %w", err)
	}

	stored, saveErr := s.persistOutboundMessage(ctx, chat.String(), resp.ID, text, replyTo, resp.Timestamp)
	if saveErr != nil {
		s.logger.Warn("persist outbound whatsapp message", "error", saveErr, "chat_jid", chat.String(), "message_id", resp.ID)
	}
	return &SendTextResult{
		ChatJID:   chat.String(),
		MessageID: resp.ID,
		Timestamp: resp.Timestamp,
		ReplyToID: strings.TrimSpace(replyTo),
		Text:      text,
		StoredID:  stored,
	}, nil
}

func (s *Service) requireClient() (*wa.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, fmt.Errorf("whatsapp client is not initialized")
	}
	if s.client.Store == nil || s.client.Store.ID == nil {
		return nil, fmt.Errorf("whatsapp authentication required")
	}
	if !s.client.IsConnected() {
		if err := s.client.Connect(); err != nil {
			return nil, fmt.Errorf("connect whatsapp client: %w", err)
		}
	}
	return s.client, nil
}

func (s *Service) handleEvent(evt any) {
	switch event := evt.(type) {
	case *waevents.Message:
		go s.handleIncomingMessage(s.agentContext(nil), event, liveSource, true)
	case *waevents.HistorySync:
		if event != nil && event.Data != nil {
			s.logger.Debug("received whatsapp history sync event",
				"sync_type", event.Data.GetSyncType().String(),
				"chunk_order", event.Data.GetChunkOrder(),
				"progress", event.Data.GetProgress(),
				"conversation_count", len(event.Data.GetConversations()),
				"status_message_count", len(event.Data.GetStatusV3Messages()),
				"pushname_count", len(event.Data.GetPushnames()),
			)
		} else {
			s.logger.Debug("received empty whatsapp history sync event")
		}
		go s.handleHistorySync(s.agentContext(nil), event)
	case *waevents.Connected:
		s.logger.Info("whatsapp connected")
	case *waevents.Disconnected:
		s.logger.Warn("whatsapp disconnected")
	case *waevents.LoggedOut:
		s.logger.Warn("whatsapp logged out", "reason", event.Reason)
	case *waevents.JoinedGroup:
		go s.upsertGroupChat(s.agentContext(nil), event.JID, event.GroupInfo.Name)
	}
}

func (s *Service) handleHistorySync(ctx context.Context, evt *waevents.HistorySync) {
	if evt == nil || evt.Data == nil {
		s.logger.Debug("skip whatsapp history sync with empty payload")
		return
	}
	var (
		conversationCount int
		persistedChats    int
		persistedMessages int
		skippedMessages   int
		failedMessages    int
	)
	for _, conv := range evt.Data.GetConversations() {
		conversationCount++
		chatJID := strings.TrimSpace(conv.GetID())
		if chatJID == "" {
			continue
		}
		chatType := knowledge.WhatsAppChatTypeDirect
		if strings.Contains(chatJID, "g.us") {
			chatType = knowledge.WhatsAppChatTypeGroup
		}
		timestamp := timeFromUnix(uint64(conv.GetLastMsgTimestamp()))
		if _, err := s.store.SaveWhatsAppChat(ctx, knowledge.SaveWhatsAppChatInput{
			JID:                  chatJID,
			Workspace:            s.cfg.WhatsApp.Workspace,
			ChatType:             chatType,
			DisplayName:          strings.TrimSpace(conv.GetDisplayName()),
			Subject:              strings.TrimSpace(conv.GetName()),
			IsArchived:           conv.GetArchived(),
			IsMuted:              conv.GetMuteEndTime() > 0,
			LastMessageTimestamp: timestamp,
		}); err != nil {
			s.logger.Warn("save whatsapp chat from history sync", "error", err, "chat_jid", chatJID)
		} else {
			persistedChats++
		}
		messageCount := len(conv.GetMessages())
		s.logger.Debug("processing whatsapp history sync conversation",
			"chat_jid", chatJID,
			"chat_type", chatType,
			"display_name", strings.TrimSpace(conv.GetDisplayName()),
			"subject", strings.TrimSpace(conv.GetName()),
			"message_count", messageCount,
			"last_message_timestamp", timestamp,
			"archived", conv.GetArchived(),
			"muted", conv.GetMuteEndTime() > 0,
		)
		for _, msg := range conv.GetMessages() {
			webMsg := msg.GetMessage()
			if webMsg == nil || webMsg.GetMessage() == nil || webMsg.GetKey() == nil {
				skippedMessages++
				continue
			}
			if _, err := s.persistWebMessage(ctx, webMsg, historySyncSource); err != nil {
				failedMessages++
				s.logger.Warn("persist whatsapp history message", "error", err, "chat_jid", chatJID)
			} else {
				persistedMessages++
			}
		}
	}
	s.logger.Debug("processed whatsapp history sync event",
		"sync_type", evt.Data.GetSyncType().String(),
		"chunk_order", evt.Data.GetChunkOrder(),
		"progress", evt.Data.GetProgress(),
		"conversation_count", conversationCount,
		"persisted_chats", persistedChats,
		"persisted_messages", persistedMessages,
		"skipped_messages", skippedMessages,
		"failed_messages", failedMessages,
		"status_message_count", len(evt.Data.GetStatusV3Messages()),
		"pushname_count", len(evt.Data.GetPushnames()),
	)
	s.notifyHistorySyncWaiters()
}

func (s *Service) handleIncomingMessage(ctx context.Context, evt *waevents.Message, source string, maybeTrigger bool) {
	if evt == nil || evt.Message == nil {
		return
	}
	stored, err := s.persistLiveMessage(ctx, evt, source)
	if err != nil {
		s.logger.Error("persist whatsapp message", "error", err)
		return
	}
	if stored == nil || stored.FromMe || !maybeTrigger {
		return
	}
	if !agentChatEligible(stored.ChatJID, s.cfg.WhatsApp.AgentRequirePrefix) {
		return
	}
	if err := s.maybeRunAgent(ctx, *stored); err != nil {
		s.logger.Warn("run whatsapp agent turn", "error", err, "chat_jid", stored.ChatJID, "message_id", stored.MessageID)
	}
}

func (s *Service) maybeRunAgent(ctx context.Context, msg knowledge.WhatsAppMessage) error {
	ctx = s.agentContext(ctx)
	prompt, ok := triggerPrompt(msg, s.cfg.WhatsApp.AgentPrefix, s.cfg.WhatsApp.AgentRequirePrefix)
	if !ok {
		return nil
	}
	release, ok := s.tryStartConversation(msg.ChatJID)
	if !ok {
		_, _ = s.sendTextMessage(ctx, msg.ChatJID, "I'm still working on the previous message in this chat.", msg.MessageID)
		return nil
	}
	defer release()

	conv, err := s.store.GetWhatsAppConversation(ctx, msg.ChatJID)
	if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
		return err
	}
	runID := ""
	if conv != nil {
		runID = strings.TrimSpace(conv.RunID)
	}
	if _, err := s.store.SaveWhatsAppConversation(ctx, knowledge.SaveWhatsAppConversationInput{
		ChatJID:                       msg.ChatJID,
		Workspace:                     s.cfg.WhatsApp.Workspace,
		RunID:                         runID,
		Status:                        agentpkg.AgentStatusRunning,
		ActiveMessageID:               msg.MessageID,
		LastProcessedInboundMessageID: msg.MessageID,
	}); err != nil {
		return err
	}

	history, err := s.buildHistory(ctx, msg.ChatJID, msg.MessageID)
	if err != nil {
		return err
	}
	presenceCtx, cancelPresence := context.WithCancel(ctx)
	defer cancelPresence()
	go s.presenceLoop(presenceCtx, msg.ChatJID)

	forwarder := s.newAssistantForwarder(ctx, runID, history, msg.ChatJID, msg.MessageID)
	newRunID, runErr := s.runAgent(ctx, s.store, agentpkg.RunRequest{
		Message: prompt,
		RunID:   runID,
		History: history,
	}, agentpkg.RunOptions{
		OnToolResult: func(_ agentpkg.ToolResultEvent) error {
			if forwarder.runID == "" && runID != "" {
				forwarder.runID = runID
			}
			return forwarder.flushCompletedMessages(ctx)
		},
	})
	if newRunID != "" {
		runID = newRunID
		forwarder.runID = newRunID
	}

	if runErr != nil {
		_, _ = s.sendTextMessage(ctx, msg.ChatJID, "I hit an error while processing that message: "+runErr.Error(), msg.MessageID)
		_, _ = s.store.SaveWhatsAppConversation(ctx, knowledge.SaveWhatsAppConversationInput{
			ChatJID:                       msg.ChatJID,
			Workspace:                     s.cfg.WhatsApp.Workspace,
			RunID:                         runID,
			Status:                        agentpkg.AgentStatusFailed,
			LastProcessedInboundMessageID: msg.MessageID,
			LastError:                     runErr.Error(),
		})
		return runErr
	}

	// Final flush after agent run completes.
	if err := forwarder.flushCompletedMessages(ctx); err != nil {
		s.logger.Warn("final flush whatsapp assistant messages", "error", err, "chat_jid", msg.ChatJID)
	}

	var lastReplyID string
	if forwarder.lastReplyID() != "" {
		lastReplyID = forwarder.lastReplyID()
	} else {
		result, sendErr := s.sendTextMessage(ctx, msg.ChatJID, "Done.", msg.MessageID)
		if sendErr != nil {
			return sendErr
		}
		lastReplyID = result.MessageID
	}
	_, _ = s.store.SaveWhatsAppConversation(ctx, knowledge.SaveWhatsAppConversationInput{
		ChatJID:                       msg.ChatJID,
		Workspace:                     s.cfg.WhatsApp.Workspace,
		RunID:                         runID,
		Status:                        agentpkg.AgentStatusCompleted,
		LastProcessedInboundMessageID: msg.MessageID,
		LastReplyMessageID:            lastReplyID,
	})
	return nil
}

type assistantForwarder struct {
	service                 *Service
	runID                   string
	chatJID                 string
	replyToID               string
	seenAssistantMessages   int
	lastReplyMessageIDValue string
}

func (s *Service) newAssistantForwarder(ctx context.Context, runID string, history []fantasy.Message, chatJID, replyToID string) *assistantForwarder {
	return &assistantForwarder{
		service:               s,
		runID:                 runID,
		chatJID:               chatJID,
		replyToID:             replyToID,
		seenAssistantMessages: s.initialAssistantMessageCount(ctx, runID, history),
	}
}

func (s *Service) initialAssistantMessageCount(ctx context.Context, runID string, history []fantasy.Message) int {
	runID = strings.TrimSpace(runID)
	if runID != "" {
		run, err := s.store.GetAgentRun(ctx, runID)
		if err == nil {
			trace, parseErr := agentpkg.ParseStoredTrace(run.Trace)
			if parseErr == nil {
				return countAssistantMessages(trace.Messages)
			}
		}
	}
	count := 0
	for _, msg := range history {
		if msg.Role == fantasy.MessageRoleAssistant {
			count++
		}
	}
	return count
}

func (f *assistantForwarder) flushCompletedMessages(ctx context.Context) error {
	runID := strings.TrimSpace(f.runID)
	if runID == "" {
		return nil
	}
	run, err := f.service.store.GetAgentRun(ctx, runID)
	if err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return nil
		}
		return err
	}
	trace, err := agentpkg.ParseStoredTrace(run.Trace)
	if err != nil {
		return err
	}
	assistantIndex := 0
	for _, msg := range trace.Messages {
		if msg.Role != "assistant" {
			continue
		}
		assistantIndex++
		if assistantIndex <= f.seenAssistantMessages {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content != "" {
			for _, chunk := range splitChunks(content, maxReplyChars) {
				result, sendErr := f.service.sendTextMessage(ctx, f.chatJID, chunk, f.replyToID)
				if sendErr != nil {
					return sendErr
				}
				f.lastReplyMessageIDValue = result.MessageID
			}
		}
		f.seenAssistantMessages = assistantIndex
	}
	return nil
}

func (f *assistantForwarder) lastReplyID() string {
	return strings.TrimSpace(f.lastReplyMessageIDValue)
}

func countAssistantMessages(messages []agentpkg.StoredMessage) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == "assistant" {
			count++
		}
	}
	return count
}

func (s *Service) buildHistory(ctx context.Context, chatJID, currentMessageID string) ([]fantasy.Message, error) {
	items, err := s.store.ListWhatsAppMessages(ctx, knowledge.WhatsAppMessageFilter{ChatJID: chatJID, Limit: defaultListLimit})
	if err != nil {
		return nil, err
	}
	// Store returns descending timestamps; reverse to ascending.
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	history := make([]fantasy.Message, 0, len(items))
	for _, item := range items {
		if item.MessageID == currentMessageID {
			continue
		}
		text := historyText(item)
		if text == "" {
			continue
		}
		if item.FromMe {
			history = append(history, fantasy.Message{
				Role: fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{
					fantasy.TextPart{Text: text},
				},
			})
		} else {
			history = append(history, fantasy.NewUserMessage(text))
		}
	}
	return history, nil
}

func (s *Service) persistLiveMessage(ctx context.Context, evt *waevents.Message, source string) (*knowledge.WhatsAppMessage, error) {
	info := evt.Info
	chatJID := info.Chat.String()
	chatType := knowledge.WhatsAppChatTypeDirect
	if info.IsGroup {
		chatType = knowledge.WhatsAppChatTypeGroup
	}
	normalized, err := normalizeMessage(info.Chat, info.Sender, evt.Message)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.SaveWhatsAppChat(ctx, knowledge.SaveWhatsAppChatInput{
		JID:                  chatJID,
		Workspace:            s.cfg.WhatsApp.Workspace,
		ChatType:             chatType,
		DisplayName:          coalesce(strings.TrimSpace(info.PushName), normalized.DisplayName),
		Subject:              normalized.Subject,
		LastMessageID:        info.ID,
		LastMessageTimestamp: ptrTime(info.Timestamp),
		LastMessagePreview:   previewText(normalized.Text, normalized.Caption, normalized.MediaKind),
	}); err != nil {
		return nil, err
	}
	if normalized.MediaPath == "" && normalized.Downloadable != nil {
		path, err := s.downloadMedia(ctx, info.Chat, info.ID, normalized.MediaKind, normalized.MIMEType, normalized.Downloadable)
		if err != nil {
			s.logger.Warn("download whatsapp media", "error", err, "chat_jid", chatJID, "message_id", info.ID)
		} else {
			normalized.MediaPath = path
		}
	}
	rawJSON, _ := protojson.Marshal(evt.Message)
	return s.store.SaveWhatsAppMessage(ctx, knowledge.SaveWhatsAppMessageInput{
		Workspace:       s.cfg.WhatsApp.Workspace,
		ChatJID:         chatJID,
		MessageID:       info.ID,
		SenderJID:       info.Sender.String(),
		FromMe:          info.IsFromMe,
		Timestamp:       info.Timestamp,
		MessageType:     normalized.MessageType,
		MediaKind:       normalized.MediaKind,
		MIMEType:        normalized.MIMEType,
		MediaPath:       normalized.MediaPath,
		MediaSizeBytes:  normalized.MediaSizeBytes,
		Text:            normalized.Text,
		Caption:         normalized.Caption,
		QuotedMessageID: normalized.QuotedMessageID,
		Source:          source,
		RawJSON:         rawJSON,
	})
}

func (s *Service) persistWebMessage(ctx context.Context, webMsg *waWeb.WebMessageInfo, source string) (*knowledge.WhatsAppMessage, error) {
	key := webMsg.GetKey()
	if key == nil || webMsg.GetMessage() == nil {
		return nil, fmt.Errorf("web message key and message are required")
	}
	chat, err := types.ParseJID(key.GetRemoteJID())
	if err != nil {
		return nil, err
	}
	senderJID := chat
	if participant := strings.TrimSpace(key.GetParticipant()); participant != "" {
		if parsed, err := types.ParseJID(participant); err == nil {
			senderJID = parsed
		}
	}
	info := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chat,
			Sender:   senderJID,
			IsFromMe: key.GetFromMe(),
			IsGroup:  strings.Contains(chat.Server, "g.us"),
		},
		ID:        key.GetID(),
		PushName:  webMsg.GetPushName(),
		Timestamp: timeFromUnix(webMsg.GetMessageTimestamp()).UTC(),
	}
	evt := &waevents.Message{
		Info:         info,
		Message:      webMsg.GetMessage(),
		SourceWebMsg: webMsg,
	}
	return s.persistLiveMessage(ctx, evt, source)
}

func (s *Service) refreshJoinedGroups(ctx context.Context) error {
	client, err := s.requireClient()
	if err != nil {
		return err
	}
	groups, err := client.GetJoinedGroups(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if group == nil {
			continue
		}
		if _, err := s.store.SaveWhatsAppChat(ctx, knowledge.SaveWhatsAppChatInput{
			JID:       group.JID.String(),
			Workspace: s.cfg.WhatsApp.Workspace,
			ChatType:  knowledge.WhatsAppChatTypeGroup,
			Subject:   strings.TrimSpace(group.Name),
		}); err != nil {
			s.logger.Warn("persist joined whatsapp group", "error", err, "jid", group.JID.String())
		}
	}
	return nil
}

func (s *Service) refreshJoinedGroupsAsync(ctx context.Context) {
	refreshCtx, cancel := context.WithTimeout(ctx, groupRefreshTimeout)
	defer cancel()
	if err := s.refreshJoinedGroups(refreshCtx); err != nil {
		s.logger.Warn("refresh whatsapp groups", "error", err)
	}
}

func (s *Service) upsertGroupChat(ctx context.Context, jid types.JID, name string) error {
	_, err := s.store.SaveWhatsAppChat(ctx, knowledge.SaveWhatsAppChatInput{
		JID:       jid.String(),
		Workspace: s.cfg.WhatsApp.Workspace,
		ChatType:  knowledge.WhatsAppChatTypeGroup,
		Subject:   strings.TrimSpace(name),
	})
	return err
}

func (s *Service) downloadMedia(ctx context.Context, chat types.JID, messageID, mediaKind, mimeType string, part wa.DownloadableMessage) (string, error) {
	client, err := s.requireClient()
	if err != nil {
		return "", err
	}
	data, err := client.Download(ctx, part)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.cfg.WhatsApp.MediaDir, 0o755); err != nil {
		return "", err
	}
	ext := ".bin"
	if exts, _ := mime.ExtensionsByType(strings.TrimSpace(mimeType)); len(exts) > 0 {
		ext = exts[0]
	}
	filePath := filepath.Join(s.cfg.WhatsApp.MediaDir, sanitizeFilename(chat.String())+"-"+sanitizeFilename(messageID)+"-"+sanitizeFilename(mediaKind)+ext)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return "", err
	}
	return filePath, nil
}

func (s *Service) persistOutboundMessage(ctx context.Context, chatJID, messageID, text, replyTo string, ts time.Time) (string, error) {
	chatType := knowledge.WhatsAppChatTypeDirect
	if strings.Contains(chatJID, "g.us") {
		chatType = knowledge.WhatsAppChatTypeGroup
	}
	if _, err := s.store.SaveWhatsAppChat(ctx, knowledge.SaveWhatsAppChatInput{
		JID:                  chatJID,
		Workspace:            s.cfg.WhatsApp.Workspace,
		ChatType:             chatType,
		LastMessageID:        messageID,
		LastMessageTimestamp: ptrTime(ts),
		LastMessagePreview:   previewText(text, "", ""),
	}); err != nil {
		return "", err
	}
	me := ""
	if status := s.Status(ctx); status.UserJID != "" {
		me = status.UserJID
	}
	msg, err := s.store.SaveWhatsAppMessage(ctx, knowledge.SaveWhatsAppMessageInput{
		Workspace:       s.cfg.WhatsApp.Workspace,
		ChatJID:         chatJID,
		MessageID:       messageID,
		SenderJID:       me,
		FromMe:          true,
		Timestamp:       ts,
		MessageType:     "text",
		Text:            text,
		QuotedMessageID: replyTo,
		Source:          outboundSource,
		RawJSON:         json.RawMessage(`{}`),
	})
	if err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (s *Service) presenceLoop(ctx context.Context, chatJID string) {
	client, err := s.requireClient()
	if err != nil {
		return
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return
	}
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
	for {
		_ = client.SendChatPresence(ctx, chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		select {
		case <-ctx.Done():
			_ = client.SendChatPresence(context.Background(), chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) tryStartConversation(chatJID string) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inFlight[chatJID]; exists {
		return nil, false
	}
	s.inFlight[chatJID] = struct{}{}
	return func() {
		s.mu.Lock()
		delete(s.inFlight, chatJID)
		s.mu.Unlock()
	}, true
}

func (s *Service) notifyHistorySyncWaiters() {
	s.mu.Lock()
	waiters := append([]chan struct{}(nil), s.historySyncWaiter...)
	s.historySyncWaiter = nil
	s.mu.Unlock()
	s.logger.Debug("notify whatsapp history sync waiters", "waiter_count", len(waiters))
	for _, waiter := range waiters {
		select {
		case waiter <- struct{}{}:
		default:
		}
	}
}

type normalizedMessage struct {
	MessageType     string
	Text            string
	Caption         string
	MediaKind       string
	MIMEType        string
	MediaPath       string
	MediaSizeBytes  int64
	QuotedMessageID string
	DisplayName     string
	Subject         string
	Downloadable    wa.DownloadableMessage
}

func normalizeMessage(chat, sender types.JID, msg *waE2E.Message) (normalizedMessage, error) {
	if msg == nil {
		return normalizedMessage{}, fmt.Errorf("message is required")
	}
	out := normalizedMessage{}
	switch {
	case strings.TrimSpace(msg.GetConversation()) != "":
		out.MessageType = "text"
		out.Text = strings.TrimSpace(msg.GetConversation())
	case msg.GetExtendedTextMessage() != nil:
		out.MessageType = "extended_text"
		out.Text = strings.TrimSpace(msg.GetExtendedTextMessage().GetText())
		out.QuotedMessageID = quotedMessageID(msg.GetExtendedTextMessage().GetContextInfo())
	case msg.GetAudioMessage() != nil:
		audio := msg.GetAudioMessage()
		out.MessageType = "audio"
		out.MediaKind = "audio"
		if audio.GetPTT() {
			out.MediaKind = "voice_note"
		}
		out.MIMEType = strings.TrimSpace(audio.GetMimetype())
		out.MediaSizeBytes = int64(audio.GetFileLength())
		out.QuotedMessageID = quotedMessageID(audio.GetContextInfo())
		out.Downloadable = audio
	case msg.GetImageMessage() != nil:
		image := msg.GetImageMessage()
		out.MessageType = "image"
		out.MediaKind = "image"
		out.MIMEType = strings.TrimSpace(image.GetMimetype())
		out.MediaSizeBytes = int64(image.GetFileLength())
		out.Caption = strings.TrimSpace(image.GetCaption())
		out.QuotedMessageID = quotedMessageID(image.GetContextInfo())
		out.Downloadable = image
	case msg.GetVideoMessage() != nil:
		video := msg.GetVideoMessage()
		out.MessageType = "video"
		out.MediaKind = "video"
		out.MIMEType = strings.TrimSpace(video.GetMimetype())
		out.MediaSizeBytes = int64(video.GetFileLength())
		out.Caption = strings.TrimSpace(video.GetCaption())
		out.QuotedMessageID = quotedMessageID(video.GetContextInfo())
		out.Downloadable = video
	case msg.GetDocumentMessage() != nil:
		doc := msg.GetDocumentMessage()
		out.MessageType = "document"
		out.MediaKind = "document"
		out.MIMEType = strings.TrimSpace(doc.GetMimetype())
		out.MediaSizeBytes = int64(doc.GetFileLength())
		out.Caption = strings.TrimSpace(doc.GetCaption())
		out.QuotedMessageID = quotedMessageID(doc.GetContextInfo())
		out.Downloadable = doc
	default:
		out.MessageType = "unknown"
	}
	if out.DisplayName == "" {
		out.DisplayName = sender.User
	}
	if strings.Contains(chat.Server, "g.us") {
		out.Subject = chat.User
	}
	return out, nil
}

// triggerPrompt determines whether an incoming message should kick off an
// agent run. Voice memos are no longer transcribed in this build.
func triggerPrompt(msg knowledge.WhatsAppMessage, prefix string, requirePrefix bool) (string, bool) {
	if requirePrefix {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			prefix = "!ai"
		}
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return "", false
	}
	if !requirePrefix {
		return text, true
	}
	if !strings.HasPrefix(text, prefix) {
		return "", false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
	return text, text != ""
}

func agentChatEligible(chatJID string, requirePrefix bool) bool {
	if requirePrefix {
		return true
	}
	return isConversationalChatJID(chatJID)
}

func isConversationalChatJID(chatJID string) bool {
	chatJID = strings.TrimSpace(chatJID)
	switch {
	case strings.HasSuffix(chatJID, "@s.whatsapp.net"):
		return true
	case strings.HasSuffix(chatJID, "@lid"):
		return true
	case strings.HasSuffix(chatJID, "@g.us"):
		return true
	default:
		return false
	}
}

func historyText(msg knowledge.WhatsAppMessage) string {
	switch {
	case strings.TrimSpace(msg.Text) != "":
		return strings.TrimSpace(msg.Text)
	case strings.TrimSpace(msg.Caption) != "":
		return strings.TrimSpace(msg.Caption)
	case strings.TrimSpace(msg.MediaKind) != "":
		return "[" + strings.TrimSpace(msg.MediaKind) + "]"
	default:
		return ""
	}
}

func previewText(text, caption, mediaKind string) string {
	for _, candidate := range []string{strings.TrimSpace(text), strings.TrimSpace(caption)} {
		if candidate != "" {
			if len(candidate) > 120 {
				return candidate[:119] + "…"
			}
			return candidate
		}
	}
	if strings.TrimSpace(mediaKind) != "" {
		return "[" + strings.TrimSpace(mediaKind) + "]"
	}
	return ""
}

func quotedMessageID(ctx *waE2E.ContextInfo) string {
	if ctx == nil {
		return ""
	}
	return strings.TrimSpace(ctx.GetStanzaID())
}

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.NewString()
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "@", "_", " ", "_")
	return replacer.Replace(value)
}

func timeFromUnix(sec uint64) *time.Time {
	if sec == 0 {
		return nil
	}
	t := time.Unix(int64(sec), 0).UTC()
	return &t
}

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	value := t.UTC()
	return &value
}

func splitChunks(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 || len(text) <= limit {
		return []string{text}
	}
	chunks := make([]string, 0, (len(text)/limit)+1)
	for len(text) > 0 {
		if len(text) <= limit {
			chunks = append(chunks, text)
			break
		}
		cut := limit
		for cut > 0 && text[cut] != '\n' && text[cut] != ' ' {
			cut--
		}
		if cut <= 0 {
			cut = limit
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	return chunks
}

func coalesce(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func openClient(ctx context.Context, cfg config.Config, logger *slog.Logger) (*sqlstore.Container, *wa.Client, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.WhatsApp.SessionDBPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create whatsapp session dir: %w", err)
	}
	address := "file:" + filepath.Clean(cfg.WhatsApp.SessionDBPath) + "?_foreign_keys=on&_journal_mode=WAL"
	waLogger := waLog.Zerolog(zerolog.New(os.Stderr).With().Timestamp().Logger())
	container, err := sqlstore.New(ctx, "sqlite3", address, waLogger)
	if err != nil {
		return nil, nil, fmt.Errorf("open whatsapp sqlstore: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, nil, fmt.Errorf("get whatsapp device: %w", err)
	}
	client := wa.NewClient(device, waLogger)
	client.EnableAutoReconnect = true
	client.InitialAutoReconnect = true
	client.BackgroundEventCtx = ctx
	_ = logger
	return container, client, nil
}
