-- Knowledge graph
CREATE TABLE IF NOT EXISTS knowledge_nodes (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  attributes TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(attributes)),
  search_text TEXT NOT NULL DEFAULT '',
  embedding BLOB,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_knowledge_nodes_kind ON knowledge_nodes(kind);
CREATE INDEX IF NOT EXISTS idx_knowledge_nodes_search_text ON knowledge_nodes(search_text);

CREATE TABLE IF NOT EXISTS knowledge_edges (
  id TEXT PRIMARY KEY,
  source_node_id TEXT NOT NULL REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
  target_node_id TEXT NOT NULL REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
  relation TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(attributes)),
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_knowledge_edges_source ON knowledge_edges(source_node_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_edges_target ON knowledge_edges(target_node_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_edges_relation ON knowledge_edges(relation);

-- Full-text search for knowledge nodes
CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_nodes_fts USING fts5(
  id UNINDEXED,
  kind,
  name,
  content,
  search_text,
  tokenize = 'unicode61'
);

CREATE TRIGGER IF NOT EXISTS knowledge_nodes_ai
AFTER INSERT ON knowledge_nodes
BEGIN
  INSERT INTO knowledge_nodes_fts (id, kind, name, content, search_text)
  VALUES (new.id, new.kind, new.name, new.content, new.search_text);
END;

CREATE TRIGGER IF NOT EXISTS knowledge_nodes_au
AFTER UPDATE ON knowledge_nodes
BEGIN
  DELETE FROM knowledge_nodes_fts WHERE id = old.id;
  INSERT INTO knowledge_nodes_fts (id, kind, name, content, search_text)
  VALUES (new.id, new.kind, new.name, new.content, new.search_text);
END;

CREATE TRIGGER IF NOT EXISTS knowledge_nodes_ad
AFTER DELETE ON knowledge_nodes
BEGIN
  DELETE FROM knowledge_nodes_fts WHERE id = old.id;
END;

-- Scheduler state
CREATE TABLE IF NOT EXISTS scheduler_state (
  task TEXT NOT NULL,
  category TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'idle',
  run_key TEXT NOT NULL DEFAULT '',
  worker_id TEXT NOT NULL DEFAULT '',
  started_at TEXT,
  finished_at TEXT,
  next_scheduled_at TEXT,
  summary TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(summary)),
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  PRIMARY KEY (task, category)
);

-- Agent runs
CREATE TABLE IF NOT EXISTS agent_runs (
  id                  TEXT PRIMARY KEY,
  title               TEXT NOT NULL DEFAULT '',
  model               TEXT NOT NULL DEFAULT '',
  prompt              TEXT NOT NULL DEFAULT '',
  status              TEXT NOT NULL DEFAULT 'running',
  error               TEXT NOT NULL DEFAULT '',
  duration_ms         INTEGER NOT NULL DEFAULT 0,
  message_count       INTEGER NOT NULL DEFAULT 0,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  trace               TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(trace)),
  parent_run_id       TEXT NOT NULL DEFAULT '',
  root_run_id         TEXT NOT NULL DEFAULT '',
  task_name           TEXT NOT NULL DEFAULT '',
  run_kind            TEXT NOT NULL DEFAULT 'root',
  last_event_id       INTEGER NOT NULL DEFAULT 0,
  active_turn_id      TEXT NOT NULL DEFAULT '',
  queue_job_id        INTEGER NOT NULL DEFAULT 0,
  worker_id           TEXT NOT NULL DEFAULT '',
  cancel_requested_at TEXT,
  provider            TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_updated_at ON agent_runs(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_runs_status_updated_at ON agent_runs(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_runs_parent_run_id ON agent_runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_root_run_id ON agent_runs(root_run_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_task_name ON agent_runs(task_name);

-- Agent run tool outputs
CREATE TABLE IF NOT EXISTS agent_run_tool_outputs (
  output_id         TEXT PRIMARY KEY,
  run_id            TEXT NOT NULL,
  tool_call_id      TEXT NOT NULL DEFAULT '',
  tool_name         TEXT NOT NULL DEFAULT '',
  content           TEXT NOT NULL DEFAULT '',
  size_bytes        INTEGER NOT NULL DEFAULT 0,
  estimated_tokens  INTEGER NOT NULL DEFAULT 0,
  total_lines       INTEGER NOT NULL DEFAULT 0,
  stored_truncated  INTEGER NOT NULL DEFAULT 0,
  inline_truncated  INTEGER NOT NULL DEFAULT 0,
  created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  FOREIGN KEY (run_id) REFERENCES agent_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_run_tool_outputs_run_id_created_at
  ON agent_run_tool_outputs(run_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_run_tool_outputs_run_id_tool_call_id
  ON agent_run_tool_outputs(run_id, tool_call_id);

-- Discord conversations
CREATE TABLE IF NOT EXISTS discord_conversations (
  id                         TEXT PRIMARY KEY,
  workspace                  TEXT NOT NULL DEFAULT '',
  guild_id                   TEXT NOT NULL DEFAULT '',
  channel_id                 TEXT NOT NULL DEFAULT '',
  thread_id                  TEXT NOT NULL DEFAULT '',
  dm_channel_id              TEXT NOT NULL DEFAULT '',
  root_message_id            TEXT NOT NULL DEFAULT '',
  run_id                     TEXT NOT NULL DEFAULT '',
  bot_user_id                TEXT NOT NULL DEFAULT '',
  active_response_message_id TEXT NOT NULL DEFAULT '',
  status                     TEXT NOT NULL DEFAULT '',
  created_at                 TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at                 TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_discord_conversations_dm_channel_id
  ON discord_conversations(dm_channel_id)
  WHERE dm_channel_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_discord_conversations_thread_id
  ON discord_conversations(thread_id)
  WHERE thread_id <> '';

CREATE INDEX IF NOT EXISTS idx_discord_conversations_root_message_id
  ON discord_conversations(root_message_id);

CREATE INDEX IF NOT EXISTS idx_discord_conversations_workspace_updated_at
  ON discord_conversations(workspace, updated_at DESC);

CREATE TABLE IF NOT EXISTS discord_tool_views (
  id                    TEXT PRIMARY KEY,
  conversation_id       TEXT NOT NULL REFERENCES discord_conversations(id) ON DELETE CASCADE,
  discord_message_id    TEXT NOT NULL DEFAULT '',
  run_id                TEXT NOT NULL DEFAULT '',
  selected_tool_call_id TEXT NOT NULL DEFAULT '',
  state                 TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(state)),
  created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_discord_tool_views_message_id
  ON discord_tool_views(discord_message_id)
  WHERE discord_message_id <> '';

CREATE INDEX IF NOT EXISTS idx_discord_tool_views_conversation_id
  ON discord_tool_views(conversation_id, updated_at DESC);

-- Agent run queue (turns and events)
CREATE TABLE IF NOT EXISTS agent_run_turns (
  turn_id            TEXT PRIMARY KEY,
  run_id             TEXT NOT NULL,
  client_request_id  TEXT NOT NULL,
  message            TEXT NOT NULL DEFAULT '',
  title              TEXT NOT NULL DEFAULT '',
  system_prompt      TEXT NOT NULL DEFAULT '',
  provider_name      TEXT NOT NULL DEFAULT '',
  subagent_provider_name TEXT NOT NULL DEFAULT '',
  enabled_tool_ids   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(enabled_tool_ids)),
  pinned_tool_ids    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(pinned_tool_ids)),
  enable_tool_ids    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(enable_tool_ids)),
  disable_tool_ids   TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(disable_tool_ids)),
  pin_tool_ids       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(pin_tool_ids)),
  unpin_tool_ids     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(unpin_tool_ids)),
  status             TEXT NOT NULL DEFAULT 'queued',
  queue_job_id       INTEGER NOT NULL DEFAULT 0,
  error              TEXT NOT NULL DEFAULT '',
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  started_at         TEXT,
  finished_at        TEXT,
  FOREIGN KEY (run_id) REFERENCES agent_runs(id) ON DELETE CASCADE,
  UNIQUE (run_id, client_request_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_run_turns_run_id_created_at
  ON agent_run_turns(run_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_run_turns_run_id_status
  ON agent_run_turns(run_id, status);

CREATE TABLE IF NOT EXISTS agent_run_events (
  event_id       INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id         TEXT NOT NULL,
  turn_id        TEXT NOT NULL DEFAULT '',
  kind           TEXT NOT NULL,
  payload_json   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  FOREIGN KEY (run_id) REFERENCES agent_runs(id) ON DELETE CASCADE,
  FOREIGN KEY (turn_id) REFERENCES agent_run_turns(turn_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_run_events_run_id_event_id
  ON agent_run_events(run_id, event_id);
