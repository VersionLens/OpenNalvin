package discordbot

import (
	"context"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/gateway"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

type managedVoiceManager struct {
	inner     disgovoice.Manager
	botUserID snowflake.ID
	logger    *slog.Logger

	mu     sync.Mutex
	guilds map[snowflake.ID]*managedVoiceGuildState
}

type managedVoiceGuildState struct {
	mu sync.Mutex

	conn      disgovoice.Conn
	botState  managedBotVoiceState
	server    managedVoiceServerState
	lastClose time.Time
}

type managedBotVoiceState struct {
	channelID string
	sessionID string
}

type managedVoiceServerState struct {
	token    string
	endpoint string
}

func newManagedVoiceManager(inner disgovoice.Manager, botUserID snowflake.ID, logger *slog.Logger) *managedVoiceManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &managedVoiceManager{
		inner:     inner,
		botUserID: botUserID,
		logger:    logger,
		guilds:    map[snowflake.ID]*managedVoiceGuildState{},
	}
}

func (m *managedVoiceManager) HandleVoiceStateUpdate(update gateway.EventVoiceStateUpdate) {
	if m.inner == nil || update.UserID != m.botUserID {
		return
	}
	state := m.guildState(update.GuildID)
	state.mu.Lock()
	if state.conn == nil {
		state.conn = m.inner.GetConn(update.GuildID)
	}
	next := managedBotVoiceState{
		sessionID: strings.TrimSpace(update.SessionID),
	}
	if update.ChannelID != nil {
		next.channelID = update.ChannelID.String()
	}
	if state.botState == next {
		state.mu.Unlock()
		return
	}
	state.botState = next
	if next.channelID == "" {
		state.server = managedVoiceServerState{}
	}
	state.mu.Unlock()
	m.inner.HandleVoiceStateUpdate(update)
}

func (m *managedVoiceManager) HandleVoiceServerUpdate(update gateway.EventVoiceServerUpdate) {
	if m.inner == nil {
		return
	}
	state := m.guildState(update.GuildID)
	state.mu.Lock()
	if state.conn == nil {
		state.conn = m.inner.GetConn(update.GuildID)
	}
	conn := state.conn
	if conn == nil {
		state.mu.Unlock()
		return
	}
	next := managedVoiceServerState{
		token: strings.TrimSpace(update.Token),
	}
	if update.Endpoint != nil {
		next.endpoint = strings.TrimSpace(*update.Endpoint)
	}
	sameServer := state.server == next
	status := conn.Gateway().Status()
	if sameServer && status != disgovoice.StatusDisconnected && status != disgovoice.StatusUnconnected {
		state.mu.Unlock()
		return
	}

	shouldClose := !sameServer && !state.server.isZero() && status != disgovoice.StatusDisconnected && status != disgovoice.StatusUnconnected
	state.server = next
	state.mu.Unlock()

	if shouldClose {
		m.logger.Info("discord voice transport resetting gateway for new server tuple",
			"guild_id", update.GuildID.String(),
			"endpoint", next.endpoint,
		)
		conn.Gateway().Close()
		waitForVoiceGatewayClosed(conn.Gateway(), 750*time.Millisecond)
	}

	m.inner.HandleVoiceServerUpdate(update)
}

func (m *managedVoiceManager) CreateConn(guildID snowflake.ID) disgovoice.Conn {
	conn := m.inner.CreateConn(guildID)
	state := m.guildState(guildID)
	state.mu.Lock()
	state.conn = conn
	state.mu.Unlock()
	return conn
}

func (m *managedVoiceManager) GetConn(guildID snowflake.ID) disgovoice.Conn {
	return m.inner.GetConn(guildID)
}

func (m *managedVoiceManager) Conns() iter.Seq[disgovoice.Conn] {
	return m.inner.Conns()
}

func (m *managedVoiceManager) RemoveConn(guildID snowflake.ID) {
	m.mu.Lock()
	delete(m.guilds, guildID)
	m.mu.Unlock()
	m.inner.RemoveConn(guildID)
}

func (m *managedVoiceManager) Close(ctx context.Context) {
	m.inner.Close(ctx)
}

func (m *managedVoiceManager) guildState(guildID snowflake.ID) *managedVoiceGuildState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.guilds[guildID]
	if state == nil {
		state = &managedVoiceGuildState{}
		m.guilds[guildID] = state
	}
	return state
}

func (s managedVoiceServerState) isZero() bool {
	return s.token == "" && s.endpoint == ""
}

func waitForVoiceGatewayClosed(gateway disgovoice.Gateway, timeout time.Duration) {
	if gateway == nil {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := gateway.Status()
		if status == disgovoice.StatusDisconnected || status == disgovoice.StatusUnconnected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
