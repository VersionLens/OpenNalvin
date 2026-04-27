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

const (
	WhatsAppChatTypeDirect = "direct"
	WhatsAppChatTypeGroup  = "group"
)

type WhatsAppChat struct {
	JID                  string     `json:"jid"`
	Workspace            string     `json:"workspace"`
	ChatType             string     `json:"chat_type"`
	DisplayName          string     `json:"display_name"`
	Subject              string     `json:"subject"`
	IsArchived           bool       `json:"is_archived"`
	IsMuted              bool       `json:"is_muted"`
	LastMessageID        string     `json:"last_message_id"`
	LastMessageTimestamp *time.Time `json:"last_message_timestamp,omitempty"`
	LastMessagePreview   string     `json:"last_message_preview"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type SaveWhatsAppChatInput struct {
	JID                  string
	Workspace            string
	ChatType             string
	DisplayName          string
	Subject              string
	IsArchived           bool
	IsMuted              bool
	LastMessageID        string
	LastMessageTimestamp *time.Time
	LastMessagePreview   string
}

type WhatsAppMessage struct {
	ID              string          `json:"id"`
	Workspace       string          `json:"workspace"`
	ChatJID         string          `json:"chat_jid"`
	MessageID       string          `json:"message_id"`
	SenderJID       string          `json:"sender_jid"`
	FromMe          bool            `json:"from_me"`
	Timestamp       time.Time       `json:"timestamp"`
	MessageType     string          `json:"message_type"`
	MediaKind       string          `json:"media_kind"`
	MIMEType        string          `json:"mime_type"`
	MediaPath       string          `json:"media_path"`
	MediaSizeBytes  int64           `json:"media_size_bytes"`
	Text            string          `json:"text"`
	Caption         string          `json:"caption"`
	QuotedMessageID string          `json:"quoted_message_id"`
	Source          string          `json:"source"`
	RawJSON         json.RawMessage `json:"raw_json"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type SaveWhatsAppMessageInput struct {
	ID              string
	Workspace       string
	ChatJID         string
	MessageID       string
	SenderJID       string
	FromMe          bool
	Timestamp       time.Time
	MessageType     string
	MediaKind       string
	MIMEType        string
	MediaPath       string
	MediaSizeBytes  int64
	Text            string
	Caption         string
	QuotedMessageID string
	Source          string
	RawJSON         json.RawMessage
}

type WhatsAppMessageFilter struct {
	ChatJID   string
	MediaOnly bool
	Limit     int
}

type WhatsAppConversation struct {
	ChatJID                       string    `json:"chat_jid"`
	Workspace                     string    `json:"workspace"`
	RunID                         string    `json:"run_id"`
	Status                        string    `json:"status"`
	ActiveMessageID               string    `json:"active_message_id"`
	LastProcessedInboundMessageID string    `json:"last_processed_inbound_message_id"`
	LastReplyMessageID            string    `json:"last_reply_message_id"`
	LastError                     string    `json:"last_error"`
	CreatedAt                     time.Time `json:"created_at"`
	UpdatedAt                     time.Time `json:"updated_at"`
}

type SaveWhatsAppConversationInput struct {
	ChatJID                       string
	Workspace                     string
	RunID                         string
	Status                        string
	ActiveMessageID               string
	LastProcessedInboundMessageID string
	LastReplyMessageID            string
	LastError                     string
}

func (s *Store) SaveWhatsAppChat(ctx context.Context, input SaveWhatsAppChatInput) (*WhatsAppChat, error) {
	if strings.TrimSpace(input.JID) == "" {
		return nil, fmt.Errorf("jid is required")
	}
	if strings.TrimSpace(input.Workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(input.ChatType) == "" {
		input.ChatType = WhatsAppChatTypeDirect
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_chats (
			jid, workspace, chat_type, display_name, subject, is_archived, is_muted,
			last_message_id, last_message_timestamp, last_message_preview, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			workspace = excluded.workspace,
			chat_type = excluded.chat_type,
			display_name = excluded.display_name,
			subject = excluded.subject,
			is_archived = excluded.is_archived,
			is_muted = excluded.is_muted,
			last_message_id = excluded.last_message_id,
			last_message_timestamp = excluded.last_message_timestamp,
			last_message_preview = excluded.last_message_preview,
			updated_at = excluded.updated_at`,
		strings.TrimSpace(input.JID),
		strings.TrimSpace(input.Workspace),
		strings.TrimSpace(input.ChatType),
		strings.TrimSpace(input.DisplayName),
		strings.TrimSpace(input.Subject),
		boolToInt(input.IsArchived),
		boolToInt(input.IsMuted),
		strings.TrimSpace(input.LastMessageID),
		nullableTimeString(input.LastMessageTimestamp),
		strings.TrimSpace(input.LastMessagePreview),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("save whatsapp chat: %w", err)
	}
	return s.GetWhatsAppChat(ctx, input.JID)
}

func (s *Store) GetWhatsAppChat(ctx context.Context, jid string) (*WhatsAppChat, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT jid, workspace, chat_type, display_name, subject, is_archived, is_muted,
		       last_message_id, last_message_timestamp, last_message_preview, created_at, updated_at
		FROM whatsapp_chats
		WHERE jid = ?`, strings.TrimSpace(jid))
	item, err := scanWhatsAppChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get whatsapp chat: %w", err)
	}
	return item, nil
}

func (s *Store) ListWhatsAppChats(ctx context.Context, workspace string, limit int) ([]WhatsAppChat, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT jid, workspace, chat_type, display_name, subject, is_archived, is_muted,
		       last_message_id, last_message_timestamp, last_message_preview, created_at, updated_at
		FROM whatsapp_chats
		WHERE workspace = ?
		ORDER BY COALESCE(last_message_timestamp, updated_at) DESC
		LIMIT ?`, strings.TrimSpace(workspace), limit)
	if err != nil {
		return nil, fmt.Errorf("list whatsapp chats: %w", err)
	}
	defer rows.Close()

	items := make([]WhatsAppChat, 0)
	for rows.Next() {
		item, err := scanWhatsAppChat(rows)
		if err != nil {
			return nil, fmt.Errorf("scan whatsapp chat: %w", err)
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) SaveWhatsAppMessage(ctx context.Context, input SaveWhatsAppMessageInput) (*WhatsAppMessage, error) {
	if strings.TrimSpace(input.ChatJID) == "" {
		return nil, fmt.Errorf("chat_jid is required")
	}
	if strings.TrimSpace(input.Workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(input.MessageID) == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	if input.Timestamp.IsZero() {
		input.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(input.ID) == "" {
		if existing, err := s.GetWhatsAppMessageByChatMessage(ctx, input.ChatJID, input.MessageID); err == nil {
			input.ID = existing.ID
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		} else {
			input.ID = uuid.NewString()
		}
	}

	rawJSON := normalizeTraceJSON(input.RawJSON)
	if len(rawJSON) == 0 {
		rawJSON = json.RawMessage(`{}`)
	}
	if !json.Valid(rawJSON) {
		return nil, fmt.Errorf("raw_json must be valid JSON")
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_messages (
			id, workspace, chat_jid, message_id, sender_jid, from_me, timestamp, message_type,
			media_kind, mime_type, media_path, media_size_bytes,
			text, caption, quoted_message_id, source, raw_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace = excluded.workspace,
			chat_jid = excluded.chat_jid,
			message_id = excluded.message_id,
			sender_jid = excluded.sender_jid,
			from_me = excluded.from_me,
			timestamp = excluded.timestamp,
			message_type = excluded.message_type,
			media_kind = excluded.media_kind,
			mime_type = excluded.mime_type,
			media_path = excluded.media_path,
			media_size_bytes = excluded.media_size_bytes,
			text = excluded.text,
			caption = excluded.caption,
			quoted_message_id = excluded.quoted_message_id,
			source = excluded.source,
			raw_json = excluded.raw_json,
			updated_at = excluded.updated_at`,
		input.ID,
		strings.TrimSpace(input.Workspace),
		strings.TrimSpace(input.ChatJID),
		strings.TrimSpace(input.MessageID),
		strings.TrimSpace(input.SenderJID),
		boolToInt(input.FromMe),
		input.Timestamp.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(input.MessageType),
		strings.TrimSpace(input.MediaKind),
		strings.TrimSpace(input.MIMEType),
		strings.TrimSpace(input.MediaPath),
		input.MediaSizeBytes,
		strings.TrimSpace(input.Text),
		strings.TrimSpace(input.Caption),
		strings.TrimSpace(input.QuotedMessageID),
		strings.TrimSpace(input.Source),
		string(rawJSON),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("save whatsapp message: %w", err)
	}
	return s.GetWhatsAppMessage(ctx, input.ID)
}

func (s *Store) GetWhatsAppMessage(ctx context.Context, id string) (*WhatsAppMessage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace, chat_jid, message_id, sender_jid, from_me, timestamp, message_type,
		       media_kind, mime_type, media_path, media_size_bytes,
		       text, caption, quoted_message_id, source, raw_json, created_at, updated_at
		FROM whatsapp_messages
		WHERE id = ?`, strings.TrimSpace(id))
	item, err := scanWhatsAppMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get whatsapp message: %w", err)
	}
	return item, nil
}

func (s *Store) GetWhatsAppMessageByChatMessage(ctx context.Context, chatJID, messageID string) (*WhatsAppMessage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace, chat_jid, message_id, sender_jid, from_me, timestamp, message_type,
		       media_kind, mime_type, media_path, media_size_bytes,
		       text, caption, quoted_message_id, source, raw_json, created_at, updated_at
		FROM whatsapp_messages
		WHERE chat_jid = ? AND message_id = ?`, strings.TrimSpace(chatJID), strings.TrimSpace(messageID))
	item, err := scanWhatsAppMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get whatsapp message by chat/message: %w", err)
	}
	return item, nil
}

func (s *Store) ListWhatsAppMessages(ctx context.Context, filter WhatsAppMessageFilter) ([]WhatsAppMessage, error) {
	if strings.TrimSpace(filter.ChatJID) == "" {
		return nil, fmt.Errorf("chat_jid is required")
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	query := `
		SELECT id, workspace, chat_jid, message_id, sender_jid, from_me, timestamp, message_type,
		       media_kind, mime_type, media_path, media_size_bytes,
		       text, caption, quoted_message_id, source, raw_json, created_at, updated_at
		FROM whatsapp_messages
		WHERE chat_jid = ?`
	args := []any{strings.TrimSpace(filter.ChatJID)}
	if filter.MediaOnly {
		query += ` AND media_kind <> ''`
	}
	query += ` ORDER BY timestamp DESC, updated_at DESC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list whatsapp messages: %w", err)
	}
	defer rows.Close()
	items := make([]WhatsAppMessage, 0)
	for rows.Next() {
		item, err := scanWhatsAppMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan whatsapp message: %w", err)
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *Store) SaveWhatsAppConversation(ctx context.Context, input SaveWhatsAppConversationInput) (*WhatsAppConversation, error) {
	if strings.TrimSpace(input.ChatJID) == "" {
		return nil, fmt.Errorf("chat_jid is required")
	}
	if strings.TrimSpace(input.Workspace) == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_conversations (
			chat_jid, workspace, run_id, status, active_message_id, last_processed_inbound_message_id,
			last_reply_message_id, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_jid) DO UPDATE SET
			workspace = excluded.workspace,
			run_id = excluded.run_id,
			status = excluded.status,
			active_message_id = excluded.active_message_id,
			last_processed_inbound_message_id = excluded.last_processed_inbound_message_id,
			last_reply_message_id = excluded.last_reply_message_id,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at`,
		strings.TrimSpace(input.ChatJID),
		strings.TrimSpace(input.Workspace),
		strings.TrimSpace(input.RunID),
		strings.TrimSpace(input.Status),
		strings.TrimSpace(input.ActiveMessageID),
		strings.TrimSpace(input.LastProcessedInboundMessageID),
		strings.TrimSpace(input.LastReplyMessageID),
		strings.TrimSpace(input.LastError),
		nowString(),
		nowString(),
	)
	if err != nil {
		return nil, fmt.Errorf("save whatsapp conversation: %w", err)
	}
	return s.GetWhatsAppConversation(ctx, input.ChatJID)
}

func (s *Store) GetWhatsAppConversation(ctx context.Context, chatJID string) (*WhatsAppConversation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT chat_jid, workspace, run_id, status, active_message_id, last_processed_inbound_message_id,
		       last_reply_message_id, last_error, created_at, updated_at
		FROM whatsapp_conversations
		WHERE chat_jid = ?`, strings.TrimSpace(chatJID))
	item, err := scanWhatsAppConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get whatsapp conversation: %w", err)
	}
	return item, nil
}

func scanWhatsAppChat(scanner interface{ Scan(dest ...any) error }) (*WhatsAppChat, error) {
	var (
		item                    WhatsAppChat
		isArchived              int
		isMuted                 int
		lastMessageTimestampRaw sql.NullString
		createdAt               string
		updatedAt               string
	)
	if err := scanner.Scan(
		&item.JID,
		&item.Workspace,
		&item.ChatType,
		&item.DisplayName,
		&item.Subject,
		&isArchived,
		&isMuted,
		&item.LastMessageID,
		&lastMessageTimestampRaw,
		&item.LastMessagePreview,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.IsArchived = isArchived != 0
	item.IsMuted = isMuted != 0
	item.LastMessageTimestamp = parseNullTime(lastMessageTimestampRaw)
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}

func scanWhatsAppMessage(scanner interface{ Scan(dest ...any) error }) (*WhatsAppMessage, error) {
	var (
		item         WhatsAppMessage
		fromMe       int
		createdAt    string
		updatedAt    string
		timestampRaw string
		rawJSONStr   string
	)
	if err := scanner.Scan(
		&item.ID,
		&item.Workspace,
		&item.ChatJID,
		&item.MessageID,
		&item.SenderJID,
		&fromMe,
		&timestampRaw,
		&item.MessageType,
		&item.MediaKind,
		&item.MIMEType,
		&item.MediaPath,
		&item.MediaSizeBytes,
		&item.Text,
		&item.Caption,
		&item.QuotedMessageID,
		&item.Source,
		&rawJSONStr,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.RawJSON = json.RawMessage(rawJSONStr)
	item.FromMe = fromMe != 0
	item.Timestamp = mustParseTime(timestampRaw)
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}

func scanWhatsAppConversation(scanner interface{ Scan(dest ...any) error }) (*WhatsAppConversation, error) {
	var (
		item      WhatsAppConversation
		createdAt string
		updatedAt string
	)
	if err := scanner.Scan(
		&item.ChatJID,
		&item.Workspace,
		&item.RunID,
		&item.Status,
		&item.ActiveMessageID,
		&item.LastProcessedInboundMessageID,
		&item.LastReplyMessageID,
		&item.LastError,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return &item, nil
}
