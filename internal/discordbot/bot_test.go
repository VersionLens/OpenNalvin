package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/bwmarrin/discordgo"
	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

type mockDiscordSession struct {
	channels        map[string]*discordgo.Channel
	channelMessages map[string][]*discordgo.Message
	messageIndex    map[string]*discordgo.Message
	threadCounter   int
	sendCounter     int
	sentMessages    []*discordgo.MessageSend
	editedMessages  []*discordgo.MessageEdit
	responses       []*discordgo.InteractionResponse
}

func newMockDiscordSession() *mockDiscordSession {
	return &mockDiscordSession{
		channels:        map[string]*discordgo.Channel{},
		channelMessages: map[string][]*discordgo.Message{},
		messageIndex:    map[string]*discordgo.Message{},
	}
}

func (m *mockDiscordSession) AddHandler(handler interface{}) func() { return func() {} }
func (m *mockDiscordSession) Open() error                           { return nil }
func (m *mockDiscordSession) Close() error                          { return nil }
func (m *mockDiscordSession) UpdateGameStatus(idle int, name string) error {
	return nil
}
func (m *mockDiscordSession) Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	if ch, ok := m.channels[channelID]; ok {
		return ch, nil
	}
	return nil, fmt.Errorf("channel not found")
}
func (m *mockDiscordSession) ChannelMessage(channelID, messageID string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	if msg, ok := m.messageIndex[channelID+":"+messageID]; ok {
		return msg, nil
	}
	return nil, fmt.Errorf("message not found")
}
func (m *mockDiscordSession) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	messages := append([]*discordgo.Message(nil), m.channelMessages[channelID]...)
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
	}
	return messages, nil
}
func (m *mockDiscordSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	m.sendCounter++
	m.sentMessages = append(m.sentMessages, data)
	msg := &discordgo.Message{
		ID:        fmt.Sprintf("sent-%d", m.sendCounter),
		ChannelID: channelID,
		Content:   data.Content,
		Embeds:    data.Embeds,
		Author:    &discordgo.User{ID: "bot-1", Bot: true},
		Timestamp: time.Unix(int64(m.sendCounter), 0),
	}
	m.channelMessages[channelID] = append([]*discordgo.Message{msg}, m.channelMessages[channelID]...)
	m.messageIndex[channelID+":"+msg.ID] = msg
	return msg, nil
}
func (m *mockDiscordSession) ChannelMessageEditComplex(edit *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	m.editedMessages = append(m.editedMessages, edit)
	content := ""
	if edit.Content != nil {
		content = *edit.Content
	}
	msg := &discordgo.Message{
		ID:        edit.ID,
		ChannelID: edit.Channel,
		Content:   content,
		Timestamp: time.Now(),
		Author:    &discordgo.User{ID: "bot-1", Bot: true},
	}
	if edit.Embeds != nil {
		msg.Embeds = *edit.Embeds
	}
	return msg, nil
}
func (m *mockDiscordSession) MessageThreadStartComplex(channelID, messageID string, data *discordgo.ThreadStart, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	m.threadCounter++
	thread := &discordgo.Channel{
		ID:       fmt.Sprintf("thread-%d", m.threadCounter),
		ParentID: channelID,
		Type:     discordgo.ChannelTypeGuildPublicThread,
		Name:     data.Name,
	}
	m.channels[thread.ID] = thread
	return thread, nil
}
func (m *mockDiscordSession) InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error {
	m.responses = append(m.responses, resp)
	return nil
}

func newTestBot(t *testing.T, session *mockDiscordSession) (*Bot, *knowledge.Store) {
	t.Helper()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "discordbot.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := knowledge.New(db)
	bot := newWithSession(config.Config{
		Workspace: config.WorkspaceConfig{Current: "alpha"},
		Discord:   config.DiscordConfig{Enabled: true, GuildAllowlist: []string{"guild-1"}},
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)), session)
	bot.botUserID = "bot-1"
	bot.ctx = context.Background()
	return bot, store
}

func TestResolveConversationCreatesThreadForMention(t *testing.T) {
	t.Parallel()

	session := newMockDiscordSession()
	bot, _ := newTestBot(t, session)

	msg := &discordgo.Message{
		ID:        "msg-1",
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		Content:   "<@bot-1> help with this repo",
		Author:    &discordgo.User{ID: "user-1", Username: "pascal"},
		Mentions:  []*discordgo.User{{ID: "bot-1"}},
	}

	conv, target, prompt, err := bot.resolveConversation(context.Background(), msg)
	if err != nil {
		t.Fatalf("resolve conversation: %v", err)
	}
	if target == "" || conv.ThreadID == "" {
		t.Fatalf("expected thread target, got target=%q conversation=%+v", target, conv)
	}
	if prompt != "help with this repo" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if conv.RootMessageID != "msg-1" {
		t.Fatalf("unexpected root message id: %q", conv.RootMessageID)
	}
}

func TestLoadConversationHistoryFiltersProgressAndCombinesFinalChunks(t *testing.T) {
	t.Parallel()

	session := newMockDiscordSession()
	bot, _ := newTestBot(t, session)

	root := &discordgo.Message{
		ID:        "root-1",
		ChannelID: "channel-1",
		Content:   "<@bot-1> start here",
		Author:    &discordgo.User{ID: "user-1"},
		Timestamp: time.Unix(1, 0),
	}
	session.messageIndex["channel-1:root-1"] = root
	session.channels["thread-1"] = &discordgo.Channel{ID: "thread-1", ParentID: "channel-1", Type: discordgo.ChannelTypeGuildPublicThread}
	session.channelMessages["thread-1"] = []*discordgo.Message{
		{
			ID:        "current",
			ChannelID: "thread-1",
			Content:   "what next?",
			Author:    &discordgo.User{ID: "user-1"},
			Timestamp: time.Unix(5, 0),
		},
		{
			ID:        "final-2",
			ChannelID: "thread-1",
			Content:   "Second part",
			Author:    &discordgo.User{ID: "bot-1", Bot: true},
			Embeds:    []*discordgo.MessageEmbed{{Footer: &discordgo.MessageEmbedFooter{Text: markerFinal}}},
			Timestamp: time.Unix(4, 0),
		},
		{
			ID:        "final-1",
			ChannelID: "thread-1",
			Content:   "First part",
			Author:    &discordgo.User{ID: "bot-1", Bot: true},
			Embeds:    []*discordgo.MessageEmbed{{Footer: &discordgo.MessageEmbedFooter{Text: markerFinal}}},
			Timestamp: time.Unix(3, 0),
		},
		{
			ID:        "progress-1",
			ChannelID: "thread-1",
			Content:   "Thinking...",
			Author:    &discordgo.User{ID: "bot-1", Bot: true},
			Embeds:    []*discordgo.MessageEmbed{{Footer: &discordgo.MessageEmbedFooter{Text: markerProgress}}},
			Timestamp: time.Unix(2, 0),
		},
	}

	conv := &knowledge.DiscordConversation{
		ID:            "conv-1",
		Workspace:     "alpha",
		ChannelID:     "channel-1",
		ThreadID:      "thread-1",
		RootMessageID: "root-1",
	}
	history, err := bot.loadConversationHistory(context.Background(), conv, "current")
	if err != nil {
		t.Fatalf("load conversation history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history messages, got %d", len(history))
	}
	if history[0].Role != fantasy.MessageRoleUser {
		t.Fatalf("unexpected first role: %v", history[0].Role)
	}
	if text, ok := fantasy.AsMessagePart[fantasy.TextPart](history[1].Content[0]); !ok || text.Text != "First part\nSecond part" {
		t.Fatalf("unexpected assistant history content: %#v", history[1].Content)
	}
}

func TestStreamStateFlushPersistsToolViewAndEditsMessage(t *testing.T) {
	t.Parallel()

	session := newMockDiscordSession()
	bot, store := newTestBot(t, session)
	conv, err := store.SaveDiscordConversation(context.Background(), knowledge.SaveDiscordConversationInput{
		Workspace: "alpha",
		ThreadID:  "thread-1",
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("save conversation: %v", err)
	}

	stream := &streamState{
		bot:       bot,
		channelID: "thread-1",
		messageID: "msg-1",
		viewID:    "view-1",
		conv:      conv,
		status:    "thinking",
	}
	stream.onChunk(agentpkg.TraceChunk{
		Choices: []agentpkg.TraceChoice{{Delta: &agentpkg.TraceDelta{Content: "hello from the bot"}}},
	})
	stream.onChunk(agentpkg.TraceChunk{
		Choices: []agentpkg.TraceChoice{{Delta: &agentpkg.TraceDelta{ToolCalls: []agentpkg.ChunkToolCall{{
			ID: "call-1",
			Function: &agentpkg.StoredToolFunction{
				Name:      "ls",
				Arguments: `{"path":"."}`,
			},
		}}}}},
	})
	stream.onToolResult(agentpkg.ToolResultEvent{
		ToolCallID: "call-1",
		ToolName:   "ls",
		Output:     "file.txt",
	})
	stream.setRunID("run-1")
	stream.complete()

	if err := stream.flush(context.Background()); err != nil {
		t.Fatalf("flush stream state: %v", err)
	}
	if len(session.editedMessages) != 1 {
		t.Fatalf("expected 1 edited message, got %d", len(session.editedMessages))
	}

	view, err := store.GetDiscordToolView(context.Background(), "view-1")
	if err != nil {
		t.Fatalf("get tool view: %v", err)
	}
	var state viewState
	if err := json.Unmarshal(view.State, &state); err != nil {
		t.Fatalf("unmarshal tool view state: %v", err)
	}
	if len(state.ToolCalls) != 1 || state.ToolCalls[0].ToolName != "ls" {
		t.Fatalf("unexpected tool view state: %+v", state)
	}
}
