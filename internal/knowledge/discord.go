package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DiscordConversation struct {
	ID                      string    `json:"id"`
	Workspace               string    `json:"workspace"`
	GuildID                 string    `json:"guild_id,omitempty"`
	ChannelID               string    `json:"channel_id,omitempty"`
	ThreadID                string    `json:"thread_id,omitempty"`
	DMChannelID             string    `json:"dm_channel_id,omitempty"`
	RootMessageID           string    `json:"root_message_id,omitempty"`
	RunID                   string    `json:"run_id,omitempty"`
	BotUserID               string    `json:"bot_user_id,omitempty"`
	ActiveResponseMessageID string    `json:"active_response_message_id,omitempty"`
	Status                  string    `json:"status"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type SaveDiscordConversationInput struct {
	ID                      string
	Workspace               string
	GuildID                 string
	ChannelID               string
	ThreadID                string
	DMChannelID             string
	RootMessageID           string
	RunID                   string
	BotUserID               string
	ActiveResponseMessageID string
	Status                  string
}

type DiscordToolView struct {
	ID                 string          `json:"id"`
	ConversationID     string          `json:"conversation_id"`
	DiscordMessageID   string          `json:"discord_message_id,omitempty"`
	RunID              string          `json:"run_id,omitempty"`
	SelectedToolCallID string          `json:"selected_tool_call_id,omitempty"`
	State              json.RawMessage `json:"state"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type SaveDiscordToolViewInput struct {
	ID                 string
	ConversationID     string
	DiscordMessageID   string
	RunID              string
	SelectedToolCallID string
	State              json.RawMessage
}

func (s *Store) GetDiscordConversationByDMChannel(ctx context.Context, dmChannelID string) (*DiscordConversation, error) {
	return s.getDiscordConversationByColumn(ctx, "dm_channel_id", dmChannelID)
}

func (s *Store) GetDiscordConversationByThread(ctx context.Context, threadID string) (*DiscordConversation, error) {
	return s.getDiscordConversationByColumn(ctx, "thread_id", threadID)
}

func (s *Store) GetDiscordConversationByRootMessage(ctx context.Context, rootMessageID string) (*DiscordConversation, error) {
	return s.getDiscordConversationByColumn(ctx, "root_message_id", rootMessageID)
}

func (s *Store) SaveDiscordConversation(ctx context.Context, input SaveDiscordConversationInput) (*DiscordConversation, error) {
	if strings.TrimSpace(input.Workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(input.ThreadID) == "" && strings.TrimSpace(input.DMChannelID) == "" {
		return nil, fmt.Errorf("thread_id or dm_channel_id is required")
	}
	if strings.TrimSpace(input.ID) == "" {
		input.ID = uuid.NewString()
	}
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "idle"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO discord_conversations (
			id, workspace, guild_id, channel_id, thread_id, dm_channel_id, root_message_id,
			run_id, bot_user_id, active_response_message_id, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace = excluded.workspace,
			guild_id = excluded.guild_id,
			channel_id = excluded.channel_id,
			thread_id = excluded.thread_id,
			dm_channel_id = excluded.dm_channel_id,
			root_message_id = excluded.root_message_id,
			run_id = excluded.run_id,
			bot_user_id = excluded.bot_user_id,
			active_response_message_id = excluded.active_response_message_id,
			status = excluded.status,
			updated_at = excluded.updated_at`,
		strings.TrimSpace(input.ID),
		strings.TrimSpace(input.Workspace),
		strings.TrimSpace(input.GuildID),
		strings.TrimSpace(input.ChannelID),
		strings.TrimSpace(input.ThreadID),
		strings.TrimSpace(input.DMChannelID),
		strings.TrimSpace(input.RootMessageID),
		strings.TrimSpace(input.RunID),
		strings.TrimSpace(input.BotUserID),
		strings.TrimSpace(input.ActiveResponseMessageID),
		strings.TrimSpace(input.Status),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("save discord conversation: %w", err)
	}
	return s.GetDiscordConversation(ctx, input.ID)
}

func (s *Store) GetDiscordConversation(ctx context.Context, id string) (*DiscordConversation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace, guild_id, channel_id, thread_id, dm_channel_id, root_message_id,
		       run_id, bot_user_id, active_response_message_id, status, created_at, updated_at
		FROM discord_conversations
		WHERE id = ?`, strings.TrimSpace(id))
	item, err := scanDiscordConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get discord conversation: %w", err)
	}
	return item, nil
}

func (s *Store) SaveDiscordToolView(ctx context.Context, input SaveDiscordToolViewInput) (*DiscordToolView, error) {
	if strings.TrimSpace(input.ConversationID) == "" {
		return nil, fmt.Errorf("conversation_id is required")
	}
	if strings.TrimSpace(input.ID) == "" {
		input.ID = uuid.NewString()
	}
	state := normalizeTraceJSON(input.State)
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}
	if !json.Valid(state) {
		return nil, fmt.Errorf("discord tool view state must be valid JSON")
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO discord_tool_views (
			id, conversation_id, discord_message_id, run_id, selected_tool_call_id, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			conversation_id = excluded.conversation_id,
			discord_message_id = excluded.discord_message_id,
			run_id = excluded.run_id,
			selected_tool_call_id = excluded.selected_tool_call_id,
			state = excluded.state,
			updated_at = excluded.updated_at`,
		strings.TrimSpace(input.ID),
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.DiscordMessageID),
		strings.TrimSpace(input.RunID),
		strings.TrimSpace(input.SelectedToolCallID),
		string(state),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("save discord tool view: %w", err)
	}
	return s.GetDiscordToolView(ctx, input.ID)
}

func (s *Store) GetDiscordToolView(ctx context.Context, id string) (*DiscordToolView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, discord_message_id, run_id, selected_tool_call_id, state, created_at, updated_at
		FROM discord_tool_views
		WHERE id = ?`, strings.TrimSpace(id))
	item, err := scanDiscordToolView(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get discord tool view: %w", err)
	}
	return item, nil
}

func (s *Store) GetDiscordToolViewByMessage(ctx context.Context, messageID string) (*DiscordToolView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, discord_message_id, run_id, selected_tool_call_id, state, created_at, updated_at
		FROM discord_tool_views
		WHERE discord_message_id = ?`, strings.TrimSpace(messageID))
	item, err := scanDiscordToolView(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get discord tool view by message: %w", err)
	}
	return item, nil
}

func (s *Store) getDiscordConversationByColumn(ctx context.Context, column, value string) (*DiscordConversation, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ErrNotFound
	}
	query := fmt.Sprintf(`
		SELECT id, workspace, guild_id, channel_id, thread_id, dm_channel_id, root_message_id,
		       run_id, bot_user_id, active_response_message_id, status, created_at, updated_at
		FROM discord_conversations
		WHERE %s = ?`, column)
	row := s.db.QueryRowContext(ctx, query, value)
	item, err := scanDiscordConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get discord conversation by %s: %w", column, err)
	}
	return item, nil
}

func scanDiscordConversation(scanner interface {
	Scan(dest ...any) error
}) (*DiscordConversation, error) {
	var (
		item      DiscordConversation
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(
		&item.ID,
		&item.Workspace,
		&item.GuildID,
		&item.ChannelID,
		&item.ThreadID,
		&item.DMChannelID,
		&item.RootMessageID,
		&item.RunID,
		&item.BotUserID,
		&item.ActiveResponseMessageID,
		&item.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}

func scanDiscordToolView(scanner interface {
	Scan(dest ...any) error
}) (*DiscordToolView, error) {
	var (
		item      DiscordToolView
		state     string
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(
		&item.ID,
		&item.ConversationID,
		&item.DiscordMessageID,
		&item.RunID,
		&item.SelectedToolCallID,
		&state,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.State = append(json.RawMessage(nil), state...)
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}
