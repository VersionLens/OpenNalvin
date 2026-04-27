package discordbot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/disgoorg/disgo"
	botpkg "github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/libdave"
	"github.com/disgoorg/snowflake/v2"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/chatrun"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/geminilive"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

type discordRuntime interface {
	AddEventListeners(...botpkg.EventListener)
	OpenGateway(context.Context) error
	Close(context.Context)
	SetPresence(context.Context, ...gateway.PresenceOpt) error
	UpdateVoiceState(context.Context, snowflake.ID, *snowflake.ID, bool, bool) error
	ID() snowflake.ID
}

type discordAPI interface {
	GetChannel(context.Context, string) (*discordChannel, error)
	GetMessage(context.Context, string, string) (*discordMessage, error)
	ListMessages(context.Context, string, int) ([]discordMessage, error)
	CreateMessage(context.Context, string, createMessageRequest) (*discordMessage, error)
	UpdateMessage(context.Context, string, string, updateMessageRequest) error
	CreateThreadFromMessage(context.Context, string, string, string) (*discordChannel, error)
}

type voiceManager interface {
	CreateConn(guildID snowflake.ID) disgovoice.Conn
	RemoveConn(guildID snowflake.ID)
	Close(ctx context.Context)
}

type createMessageRequest struct {
	Content   string
	ReplyToID string
}

type updateMessageRequest struct {
	Content string
}

type discordChannel struct {
	ID   string
	Name string
}

type discordMessage struct {
	ID         string
	GuildID    string
	ChannelID  string
	AuthorID   string
	AuthorName string
	AuthorBot  bool
	Content    string
	Mentions   []string
	CreatedAt  time.Time
}

type chatRunnerFunc func(context.Context, chatrun.Request) (chatrun.Result, error)

type liveService interface {
	Start(context.Context, geminilive.StartRequest) (geminilive.Conversation, error)
}

type Service struct {
	cfg          config.Config
	store        *knowledge.Store
	logger       *slog.Logger
	runtime      discordRuntime
	api          discordAPI
	voiceManager voiceManager
	runChat      chatRunnerFunc

	openVoiceConnection func(context.Context, string, string) (voiceConnection, error)

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	mu        sync.Mutex
	started   bool
	closed    bool
	botUserID string
	inFlight  map[string]struct{}

	liveMu sync.Mutex
	live   liveRuntime
}

type liveRuntime struct {
	service      liveService
	occupants    map[string]map[string]string
	participants map[string]map[string]liveTraceParticipant
	sessions     map[string]*liveVoiceSession
	starting     map[string]struct{}
	maxSessions  int
}

type transcriptEntry struct {
	MessageID string
	Role      fantasy.MessageRole
	Content   string
}

type Status struct {
	Enabled      bool   `json:"enabled"`
	Workspace    string `json:"workspace"`
	Connected    bool   `json:"connected"`
	BotUserID    string `json:"bot_user_id,omitempty"`
	LiveSessions int    `json:"live_sessions"`
}

func NewService(cfg config.Config, store *knowledge.Store, logger *slog.Logger) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("knowledge store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	cfg.Workspace.Current = WorkspaceName
	if strings.TrimSpace(cfg.Discord.Token) == "" {
		return nil, fmt.Errorf("discord token not configured")
	}

	svc := &Service{
		cfg:    cfg,
		store:  store,
		logger: logger,
		runChat: func(ctx context.Context, req chatrun.Request) (chatrun.Result, error) {
			return chatrun.RunAttached(ctx, store, logger, req)
		},
		inFlight: map[string]struct{}{},
	}
	if cfg.Discord.Live.Enabled {
		svc.live = liveRuntime{
			service:      geminilive.New(cfg, logger),
			occupants:    map[string]map[string]string{},
			participants: map[string]map[string]liveTraceParticipant{},
			sessions:     map[string]*liveVoiceSession{},
			starting:     map[string]struct{}{},
			maxSessions:  cfg.Discord.Live.MaxSessions,
		}
	}
	svc.openVoiceConnection = svc.openDisgoVoiceConnection
	return svc, nil
}

func New(cfg config.Config, store *knowledge.Store, logger *slog.Logger) (*Service, error) {
	return NewService(cfg, store, logger)
}

func newWithRuntime(cfg config.Config, store *knowledge.Store, logger *slog.Logger, runtime discordRuntime, api discordAPI, voice voiceManager) *Service {
	svc, _ := NewService(cfg, store, logger)
	svc.runtime = runtime
	svc.api = api
	svc.voiceManager = voice
	return svc
}

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	if !s.cfg.Discord.Enabled {
		s.mu.Unlock()
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = config.WithContext(ctx, s.cfg)
	s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(ctx)
	runtime := s.runtime
	api := s.api
	voice := s.voiceManager
	liveEnabled := s.cfg.Discord.Live.Enabled
	s.mu.Unlock()

	if runtime == nil || api == nil || (liveEnabled && voice == nil) {
		builtRuntime, builtAPI, builtVoice, err := s.buildRuntime()
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.runtime = builtRuntime
		s.api = builtAPI
		s.voiceManager = builtVoice
		runtime = builtRuntime
		api = builtAPI
		voice = builtVoice
		s.mu.Unlock()
	}

	runtime.AddEventListeners(&events.ListenerAdapter{
		OnReady:                 s.handleReady,
		OnDMMessageCreate:       s.handleDMMessageCreate,
		OnGuildMessageCreate:    s.handleGuildMessageCreate,
		OnGuildVoiceStateUpdate: s.handleGuildVoiceStateUpdate,
		OnVoiceServerUpdate:     s.handleVoiceServerUpdate,
	})

	if err := runtime.OpenGateway(s.lifecycleCtx); err != nil {
		return fmt.Errorf("open discord gateway: %w", err)
	}

	if status := strings.TrimSpace(s.cfg.Discord.StatusMessage); status != "" {
		if err := runtime.SetPresence(s.lifecycleCtx, gateway.WithPlayingActivity(status)); err != nil {
			s.logger.Warn("set discord status", "error", err)
		}
	}

	go s.heartbeatLoop(s.lifecycleCtx)
	return nil
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return s.Close()
}

func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.lifecycleCancel
	runtime := s.runtime
	s.liveMu.Lock()
	sessions := make([]*liveVoiceSession, 0, len(s.live.sessions))
	for key, session := range s.live.sessions {
		delete(s.live.sessions, key)
		sessions = append(sessions, session)
	}
	s.liveMu.Unlock()
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, session := range sessions {
		session.shutdown("service stopped")
	}
	if runtime != nil {
		runtime.Close(context.Background())
	}
	return nil
}

func (s *Service) Status(_ context.Context) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, _, _ := s.liveStats()
	return Status{
		Enabled:      s.cfg.Discord.Enabled,
		Workspace:    WorkspaceName,
		Connected:    s.started && !s.closed,
		BotUserID:    s.botUserID,
		LiveSessions: active,
	}
}

func (s *Service) buildRuntime() (discordRuntime, discordAPI, voiceManager, error) {
	voiceLogger := s.logger.With("component", "discord_voice")
	libdave.SetDefaultLogger(voiceLogger.With("component", "libdave"))
	libdave.SetDefaultLogLoggerLevel(slog.LevelInfo)

	client, err := disgo.New(s.cfg.Discord.Token,
		botpkg.WithLogger(s.logger),
		botpkg.WithDefaultGateway(),
		botpkg.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuilds|
					gateway.IntentGuildMessages|
					gateway.IntentGuildVoiceStates|
					gateway.IntentDirectMessages|
					gateway.IntentMessageContent,
			),
		),
		botpkg.WithVoiceManagerConfigOpts(
			disgovoice.WithLogger(voiceLogger),
			disgovoice.WithDaveSessionCreateFunc(newDaveSession),
			disgovoice.WithDaveSessionLogger(voiceLogger),
		),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create disgo client: %w", err)
	}
	managedVoice := newManagedVoiceManager(client.VoiceManager, client.ApplicationID, voiceLogger)
	client.VoiceManager = managedVoice
	return client, newDisgoAPI(client.Rest), managedVoice, nil
}

func (s *Service) handleReady(event *events.Ready) {
	if event == nil {
		return
	}
	s.mu.Lock()
	s.botUserID = event.User.ID.String()
	s.mu.Unlock()
	s.logger.Info("discord service ready", "bot_user_id", s.botUserID)
}

func (s *Service) handleDMMessageCreate(event *events.DMMessageCreate) {
	if event == nil {
		return
	}
	msg := translateMessage(event.Message)
	if msg.AuthorBot || s.isBotUser(msg.AuthorID) {
		return
	}
	go s.processTextMessage(msg)
}

func (s *Service) handleGuildMessageCreate(event *events.GuildMessageCreate) {
	if event == nil {
		return
	}
	msg := translateMessage(event.Message)
	if msg.AuthorBot || s.isBotUser(msg.AuthorID) || !s.guildAllowed(msg.GuildID) {
		return
	}
	go s.processTextMessage(msg)
}

func (s *Service) processTextMessage(msg discordMessage) {
	ctx := s.context()

	conv, prompt, err := s.resolveConversation(ctx, msg)
	if err != nil {
		s.logger.Error("resolve discord conversation", "error", err, "channel_id", msg.ChannelID)
		_ = s.sendInfoMessage(msg.ChannelID, msg.ID, "I hit an error while loading this conversation: "+err.Error())
		return
	}
	if strings.TrimSpace(prompt) == "" {
		return
	}

	release, ok := s.tryStartConversation(conv.ID)
	if !ok {
		_ = s.sendInfoMessage(msg.ChannelID, msg.ID, "I’m still working on the previous message in this conversation. Please wait for that turn to finish.")
		return
	}
	defer release()

	history, err := s.loadConversationHistory(ctx, conv, msg.ID)
	if err != nil {
		s.logger.Error("load discord history", "error", err, "conversation_id", conv.ID)
		_ = s.sendInfoMessage(msg.ChannelID, msg.ID, "I hit an error while loading message history: "+err.Error())
		return
	}

	var out bytes.Buffer
	result, runErr := s.runChat(ctx, chatrun.Request{
		Message:  prompt,
		RunID:    s.reusableRunID(ctx, conv),
		History:  history,
		Out:      &out,
		WorkerID: "discord-" + conv.ID,
	})
	runID := strings.TrimSpace(result.RunID)
	response := strings.TrimSpace(out.String())
	if response == "" && runErr != nil {
		response = "I hit an error while processing that message: " + runErr.Error()
	}
	if response == "" {
		response = "I don’t have a response for that yet."
	}

	lastMessageID, sendErr := s.sendReplyChunks(msg.ChannelID, msg.ID, response)
	if sendErr != nil {
		s.logger.Error("send discord reply", "error", sendErr, "channel_id", msg.ChannelID)
		return
	}

	status := agentpkg.AgentStatusCompleted
	if runErr != nil {
		status = agentpkg.AgentStatusFailed
		s.logger.Error("discord text turn failed", "error", runErr, "conversation_id", conv.ID, "run_id", runID)
	}

	if _, err := s.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
		ID:                      conv.ID,
		Workspace:               conv.Workspace,
		GuildID:                 conv.GuildID,
		ChannelID:               conv.ChannelID,
		ThreadID:                conv.ThreadID,
		DMChannelID:             conv.DMChannelID,
		RootMessageID:           conv.RootMessageID,
		RunID:                   runID,
		BotUserID:               s.botUserID,
		ActiveResponseMessageID: lastMessageID,
		Status:                  status,
	}); err != nil {
		s.logger.Error("persist discord conversation", "error", err, "conversation_id", conv.ID)
	}
}

func (s *Service) resolveConversation(ctx context.Context, msg discordMessage) (*knowledge.DiscordConversation, string, error) {
	prompt := strings.TrimSpace(msg.Content)
	if msg.GuildID == "" {
		_, prompt = stripCompactCommand(prompt)
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return nil, "", nil
		}
		conv, err := s.store.GetDiscordConversationByDMChannel(ctx, msg.ChannelID)
		if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
			return nil, "", err
		}
		if conv == nil {
			conv, err = s.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
				Workspace:   s.cfg.Workspace.Current,
				ChannelID:   msg.ChannelID,
				DMChannelID: msg.ChannelID,
				BotUserID:   s.botUserID,
				Status:      "idle",
			})
			if err != nil {
				return nil, "", err
			}
		}
		return conv, prompt, nil
	}

	if !mentionsBot(msg, s.botUserID) {
		return nil, "", nil
	}
	prompt = stripBotMention(prompt, s.botUserID)
	_, prompt = stripCompactCommand(prompt)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, "", nil
	}

	conversationID := guildConversationID(msg.GuildID, msg.ChannelID)
	conv, err := s.store.GetDiscordConversation(ctx, conversationID)
	if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
		return nil, "", err
	}
	if conv == nil {
		conv, err = s.store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
			ID:        conversationID,
			Workspace: s.cfg.Workspace.Current,
			GuildID:   msg.GuildID,
			ChannelID: msg.ChannelID,
			BotUserID: s.botUserID,
			Status:    "idle",
		})
		if err != nil {
			return nil, "", err
		}
	}
	return conv, prompt, nil
}

func (s *Service) loadConversationHistory(ctx context.Context, conv *knowledge.DiscordConversation, currentMessageID string) ([]fantasy.Message, error) {
	channelID := strings.TrimSpace(conv.DMChannelID)
	if channelID == "" {
		channelID = strings.TrimSpace(conv.ChannelID)
	}
	messages, err := s.api.ListMessages(ctx, channelID, defaultFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch discord messages: %w", err)
	}
	sortMessagesOldestFirst(messages)

	entries := make([]transcriptEntry, 0, len(messages))
	for _, msg := range messages {
		if msg.AuthorID == "" {
			continue
		}
		if s.isBotUser(msg.AuthorID) {
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				continue
			}
			if len(entries) > 0 && entries[len(entries)-1].Role == fantasy.MessageRoleAssistant {
				entries[len(entries)-1].Content += "\n" + content
			} else {
				entries = append(entries, transcriptEntry{
					MessageID: msg.ID,
					Role:      fantasy.MessageRoleAssistant,
					Content:   content,
				})
			}
			continue
		}

		content := strings.TrimSpace(msg.Content)
		if conv.GuildID != "" {
			if !mentionsBot(msg, s.botUserID) {
				continue
			}
			content = stripBotMention(content, s.botUserID)
		}
		_, content = stripCompactCommand(content)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		entries = append(entries, transcriptEntry{
			MessageID: msg.ID,
			Role:      fantasy.MessageRoleUser,
			Content:   content,
		})
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

func (s *Service) sendReplyChunks(channelID, replyToID, content string) (string, error) {
	chunks := splitDiscordChunks(content)
	if len(chunks) == 0 {
		chunks = []string{"I don’t have a response for that yet."}
	}
	lastMessageID := ""
	for i, chunk := range chunks {
		req := createMessageRequest{Content: truncateForDiscord(chunk, maxDiscordContent)}
		if i == 0 {
			req.ReplyToID = replyToID
		}
		msg, err := s.api.CreateMessage(s.context(), channelID, req)
		if err != nil {
			return lastMessageID, err
		}
		lastMessageID = msg.ID
	}
	return lastMessageID, nil
}

func (s *Service) reusableRunID(ctx context.Context, conv *knowledge.DiscordConversation) string {
	runID := strings.TrimSpace(conv.RunID)
	if runID == "" {
		return ""
	}
	if _, err := s.store.GetAgentRun(ctx, runID); err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return ""
		}
		s.logger.Warn("load existing discord run", "run_id", runID, "error", err)
		return ""
	}
	return runID
}

func (s *Service) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lifecycleCtx != nil {
		return s.lifecycleCtx
	}
	return config.WithContext(context.Background(), s.cfg)
}

func (s *Service) isBotUser(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.botUserID != "" && userID == s.botUserID
}

func (s *Service) guildAllowed(guildID string) bool {
	if len(s.cfg.Discord.GuildAllowlist) == 0 {
		return true
	}
	for _, allowed := range s.cfg.Discord.GuildAllowlist {
		if guildID == allowed {
			return true
		}
	}
	return false
}

func (s *Service) tryStartConversation(conversationID string) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inFlight[conversationID]; exists {
		return nil, false
	}
	s.inFlight[conversationID] = struct{}{}
	return func() {
		s.mu.Lock()
		delete(s.inFlight, conversationID)
		s.mu.Unlock()
	}, true
}

func (s *Service) sendInfoMessage(channelID, replyToID, content string) error {
	_, err := s.api.CreateMessage(s.context(), channelID, createMessageRequest{
		Content:   truncateForDiscord(content, maxDiscordContent),
		ReplyToID: replyToID,
	})
	return err
}

func (s *Service) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("discord service heartbeat stopped")
			return
		case <-ticker.C:
			active, starting, tracked := s.liveStats()
			s.logger.Info("discord service heartbeat",
				"workspace", WorkspaceName,
				"live_enabled", s.cfg.Discord.Live.Enabled,
				"active_live_sessions", active,
				"starting_live_sessions", starting,
				"tracked_voice_guilds", tracked,
			)
		}
	}
}

func (s *Service) liveStats() (active int, starting int, trackedGuilds int) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return len(s.live.sessions), len(s.live.starting), len(s.live.occupants)
}

func guildConversationID(guildID, channelID string) string {
	return fmt.Sprintf("discord:%s:%s", guildID, channelID)
}

func translateMessage(msg discord.Message) discordMessage {
	guildID := ""
	if msg.GuildID != nil {
		guildID = msg.GuildID.String()
	}
	mentions := make([]string, 0, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		mentions = append(mentions, mention.ID.String())
	}
	return discordMessage{
		ID:         msg.ID.String(),
		GuildID:    guildID,
		ChannelID:  msg.ChannelID.String(),
		AuthorID:   msg.Author.ID.String(),
		AuthorName: msg.Author.EffectiveName(),
		AuthorBot:  msg.Author.Bot,
		Content:    msg.Content,
		Mentions:   mentions,
		CreatedAt:  msg.CreatedAt,
	}
}

type disgoAPI struct {
	rest rest.Rest
}

func newDisgoAPI(restClient rest.Rest) *disgoAPI {
	return &disgoAPI{rest: restClient}
}

func (a *disgoAPI) GetChannel(ctx context.Context, channelID string) (*discordChannel, error) {
	_ = ctx
	id, err := parseSnowflake(channelID)
	if err != nil {
		return nil, err
	}
	channel, err := a.channelRest().GetChannel(id)
	if err != nil {
		return nil, err
	}
	return &discordChannel{ID: channel.ID().String(), Name: channel.Name()}, nil
}

func (a *disgoAPI) GetMessage(ctx context.Context, channelID, messageID string) (*discordMessage, error) {
	_ = ctx
	cid, err := parseSnowflake(channelID)
	if err != nil {
		return nil, err
	}
	mid, err := parseSnowflake(messageID)
	if err != nil {
		return nil, err
	}
	msg, err := a.messageRest().GetMessage(cid, mid)
	if err != nil {
		return nil, err
	}
	translated := translateMessage(*msg)
	return &translated, nil
}

func (a *disgoAPI) ListMessages(ctx context.Context, channelID string, limit int) ([]discordMessage, error) {
	_ = ctx
	cid, err := parseSnowflake(channelID)
	if err != nil {
		return nil, err
	}
	msgs, err := a.messageRest().GetMessages(cid, 0, 0, 0, limit)
	if err != nil {
		return nil, err
	}
	out := make([]discordMessage, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, translateMessage(msg))
	}
	return out, nil
}

func (a *disgoAPI) CreateMessage(ctx context.Context, channelID string, req createMessageRequest) (*discordMessage, error) {
	_ = ctx
	cid, err := parseSnowflake(channelID)
	if err != nil {
		return nil, err
	}
	create := discord.NewMessageCreate().
		WithContent(truncateForDiscord(req.Content, maxDiscordContent)).
		WithAllowedMentions(noMentions())
	if strings.TrimSpace(req.ReplyToID) != "" {
		replyID, err := parseSnowflake(req.ReplyToID)
		if err != nil {
			return nil, err
		}
		create = create.WithMessageReferenceByID(replyID)
	}
	msg, err := a.messageRest().CreateMessage(cid, create)
	if err != nil {
		return nil, err
	}
	translated := translateMessage(*msg)
	return &translated, nil
}

func (a *disgoAPI) UpdateMessage(ctx context.Context, channelID, messageID string, req updateMessageRequest) error {
	_ = ctx
	cid, err := parseSnowflake(channelID)
	if err != nil {
		return err
	}
	mid, err := parseSnowflake(messageID)
	if err != nil {
		return err
	}
	update := discord.NewMessageUpdate().
		WithContent(truncateForDiscord(req.Content, maxDiscordContent)).
		WithAllowedMentions(noMentions())
	_, err = a.messageRest().UpdateMessage(cid, mid, update)
	return err
}

func (a *disgoAPI) CreateThreadFromMessage(ctx context.Context, channelID, messageID, name string) (*discordChannel, error) {
	_ = ctx
	cid, err := parseSnowflake(channelID)
	if err != nil {
		return nil, err
	}
	mid, err := parseSnowflake(messageID)
	if err != nil {
		return nil, err
	}
	thread, err := a.threadRest().CreateThreadFromMessage(cid, mid, discord.ThreadCreateFromMessage{
		Name:                deriveThreadName(name, "Gemini Live"),
		AutoArchiveDuration: discord.AutoArchiveDuration24h,
	})
	if err != nil {
		return nil, err
	}
	return &discordChannel{ID: thread.ID().String(), Name: thread.Name()}, nil
}

func (a *disgoAPI) channelRest() interface {
	GetChannel(channelID snowflake.ID, opts ...rest.RequestOpt) (discord.Channel, error)
} {
	return a.rest
}

func (a *disgoAPI) messageRest() interface {
	GetMessage(channelID snowflake.ID, messageID snowflake.ID, opts ...rest.RequestOpt) (*discord.Message, error)
	GetMessages(channelID snowflake.ID, around snowflake.ID, before snowflake.ID, after snowflake.ID, limit int, opts ...rest.RequestOpt) ([]discord.Message, error)
	CreateMessage(channelID snowflake.ID, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
	UpdateMessage(channelID snowflake.ID, messageID snowflake.ID, messageUpdate discord.MessageUpdate, opts ...rest.RequestOpt) (*discord.Message, error)
} {
	return a.rest
}

func (a *disgoAPI) threadRest() interface {
	CreateThreadFromMessage(channelID snowflake.ID, messageID snowflake.ID, threadCreate discord.ThreadCreateFromMessage, opts ...rest.RequestOpt) (*discord.GuildThread, error)
} {
	return a.rest
}

func parseSnowflake(value string) (snowflake.ID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("snowflake id is required")
	}
	return snowflake.Parse(value)
}
