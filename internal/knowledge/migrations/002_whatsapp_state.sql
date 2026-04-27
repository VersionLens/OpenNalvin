CREATE TABLE IF NOT EXISTS whatsapp_chats (
  jid                      TEXT PRIMARY KEY,
  workspace                TEXT NOT NULL DEFAULT '',
  chat_type                TEXT NOT NULL DEFAULT '',
  display_name             TEXT NOT NULL DEFAULT '',
  subject                  TEXT NOT NULL DEFAULT '',
  is_archived              INTEGER NOT NULL DEFAULT 0,
  is_muted                 INTEGER NOT NULL DEFAULT 0,
  last_message_id          TEXT NOT NULL DEFAULT '',
  last_message_timestamp   TEXT,
  last_message_preview     TEXT NOT NULL DEFAULT '',
  created_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_whatsapp_chats_workspace_updated_at
  ON whatsapp_chats(workspace, updated_at DESC);

CREATE TABLE IF NOT EXISTS whatsapp_messages (
  id                       TEXT PRIMARY KEY,
  workspace                TEXT NOT NULL DEFAULT '',
  chat_jid                 TEXT NOT NULL REFERENCES whatsapp_chats(jid) ON DELETE CASCADE,
  message_id               TEXT NOT NULL DEFAULT '',
  sender_jid               TEXT NOT NULL DEFAULT '',
  from_me                  INTEGER NOT NULL DEFAULT 0,
  timestamp                TEXT NOT NULL,
  message_type             TEXT NOT NULL DEFAULT '',
  media_kind               TEXT NOT NULL DEFAULT '',
  mime_type                TEXT NOT NULL DEFAULT '',
  media_path               TEXT NOT NULL DEFAULT '',
  media_size_bytes         INTEGER NOT NULL DEFAULT 0,
  text                     TEXT NOT NULL DEFAULT '',
  caption                  TEXT NOT NULL DEFAULT '',
  quoted_message_id        TEXT NOT NULL DEFAULT '',
  source                   TEXT NOT NULL DEFAULT '',
  raw_json                 TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(raw_json)),
  created_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_messages_chat_message
  ON whatsapp_messages(chat_jid, message_id);

CREATE INDEX IF NOT EXISTS idx_whatsapp_messages_chat_timestamp
  ON whatsapp_messages(chat_jid, timestamp DESC, updated_at DESC);

CREATE TABLE IF NOT EXISTS whatsapp_conversations (
  chat_jid                         TEXT PRIMARY KEY REFERENCES whatsapp_chats(jid) ON DELETE CASCADE,
  workspace                        TEXT NOT NULL DEFAULT '',
  run_id                           TEXT NOT NULL DEFAULT '',
  status                           TEXT NOT NULL DEFAULT '',
  active_message_id                TEXT NOT NULL DEFAULT '',
  last_processed_inbound_message_id TEXT NOT NULL DEFAULT '',
  last_reply_message_id            TEXT NOT NULL DEFAULT '',
  last_error                       TEXT NOT NULL DEFAULT '',
  created_at                       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at                       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_whatsapp_conversations_workspace_updated_at
  ON whatsapp_conversations(workspace, updated_at DESC);
