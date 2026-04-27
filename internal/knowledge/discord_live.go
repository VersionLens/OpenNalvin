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

type DiscordLiveCall struct {
	ID                        string          `json:"id"`
	GuildID                   string          `json:"guild_id"`
	VoiceChannelID            string          `json:"voice_channel_id"`
	TranscriptParentChannelID string          `json:"transcript_parent_channel_id"`
	TranscriptThreadID        string          `json:"transcript_thread_id"`
	RootMessageID             string          `json:"root_message_id"`
	StatusMessageID           string          `json:"status_message_id"`
	ProviderName              string          `json:"provider_name"`
	SessionHandle             string          `json:"session_handle"`
	Status                    string          `json:"status"`
	State                     json.RawMessage `json:"state"`
	CreatedAt                 time.Time       `json:"created_at"`
	UpdatedAt                 time.Time       `json:"updated_at"`
}

type SaveDiscordLiveCallInput struct {
	ID                        string
	GuildID                   string
	VoiceChannelID            string
	TranscriptParentChannelID string
	TranscriptThreadID        string
	RootMessageID             string
	StatusMessageID           string
	ProviderName              string
	SessionHandle             string
	Status                    string
	State                     json.RawMessage
}

func (s *Store) SaveDiscordLiveCall(ctx context.Context, input SaveDiscordLiveCallInput) (*DiscordLiveCall, error) {
	if strings.TrimSpace(input.GuildID) == "" {
		return nil, fmt.Errorf("guild_id is required")
	}
	if strings.TrimSpace(input.VoiceChannelID) == "" {
		return nil, fmt.Errorf("voice_channel_id is required")
	}
	if strings.TrimSpace(input.ID) == "" {
		input.ID = uuid.NewString()
	}
	if strings.TrimSpace(input.Status) == "" {
		input.Status = "idle"
	}
	state := normalizeTraceJSON(input.State)
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}
	if !json.Valid(state) {
		return nil, fmt.Errorf("discord live call state must be valid JSON")
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO discord_live_calls (
			id, guild_id, voice_channel_id, transcript_parent_channel_id, transcript_thread_id,
			root_message_id, status_message_id, provider_name, session_handle, status, state,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			guild_id = excluded.guild_id,
			voice_channel_id = excluded.voice_channel_id,
			transcript_parent_channel_id = excluded.transcript_parent_channel_id,
			transcript_thread_id = excluded.transcript_thread_id,
			root_message_id = excluded.root_message_id,
			status_message_id = excluded.status_message_id,
			provider_name = excluded.provider_name,
			session_handle = excluded.session_handle,
			status = excluded.status,
			state = excluded.state,
			updated_at = excluded.updated_at`,
		strings.TrimSpace(input.ID),
		strings.TrimSpace(input.GuildID),
		strings.TrimSpace(input.VoiceChannelID),
		strings.TrimSpace(input.TranscriptParentChannelID),
		strings.TrimSpace(input.TranscriptThreadID),
		strings.TrimSpace(input.RootMessageID),
		strings.TrimSpace(input.StatusMessageID),
		strings.TrimSpace(input.ProviderName),
		strings.TrimSpace(input.SessionHandle),
		strings.TrimSpace(input.Status),
		string(state),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("save discord live call: %w", err)
	}
	return s.GetDiscordLiveCall(ctx, input.ID)
}

func (s *Store) GetDiscordLiveCall(ctx context.Context, id string) (*DiscordLiveCall, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, guild_id, voice_channel_id, transcript_parent_channel_id, transcript_thread_id,
		       root_message_id, status_message_id, provider_name, session_handle, status, state,
		       created_at, updated_at
		FROM discord_live_calls
		WHERE id = ?`, strings.TrimSpace(id))
	item, err := scanDiscordLiveCall(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get discord live call: %w", err)
	}
	return item, nil
}

func (s *Store) GetLatestDiscordLiveCallByVoiceChannel(ctx context.Context, guildID, voiceChannelID string) (*DiscordLiveCall, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, guild_id, voice_channel_id, transcript_parent_channel_id, transcript_thread_id,
		       root_message_id, status_message_id, provider_name, session_handle, status, state,
		       created_at, updated_at
		FROM discord_live_calls
		WHERE guild_id = ? AND voice_channel_id = ?
		ORDER BY updated_at DESC
		LIMIT 1`,
		strings.TrimSpace(guildID),
		strings.TrimSpace(voiceChannelID),
	)
	item, err := scanDiscordLiveCall(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest discord live call: %w", err)
	}
	return item, nil
}

func scanDiscordLiveCall(scanner interface {
	Scan(dest ...any) error
}) (*DiscordLiveCall, error) {
	var (
		item      DiscordLiveCall
		createdAt string
		updatedAt string
		state     string
	)
	if err := scanner.Scan(
		&item.ID,
		&item.GuildID,
		&item.VoiceChannelID,
		&item.TranscriptParentChannelID,
		&item.TranscriptThreadID,
		&item.RootMessageID,
		&item.StatusMessageID,
		&item.ProviderName,
		&item.SessionHandle,
		&item.Status,
		&state,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.State = json.RawMessage(state)
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}
