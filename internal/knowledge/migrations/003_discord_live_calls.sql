CREATE TABLE IF NOT EXISTS discord_live_calls (
  id                           TEXT PRIMARY KEY,
  guild_id                     TEXT NOT NULL DEFAULT '',
  voice_channel_id             TEXT NOT NULL DEFAULT '',
  transcript_parent_channel_id TEXT NOT NULL DEFAULT '',
  transcript_thread_id         TEXT NOT NULL DEFAULT '',
  root_message_id              TEXT NOT NULL DEFAULT '',
  status_message_id            TEXT NOT NULL DEFAULT '',
  provider_name                TEXT NOT NULL DEFAULT '',
  session_handle               TEXT NOT NULL DEFAULT '',
  status                       TEXT NOT NULL DEFAULT '',
  state                        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(state)),
  created_at                   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at                   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_discord_live_calls_voice_channel
  ON discord_live_calls(guild_id, voice_channel_id, updated_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_discord_live_calls_transcript_thread
  ON discord_live_calls(transcript_thread_id)
  WHERE transcript_thread_id <> '';
