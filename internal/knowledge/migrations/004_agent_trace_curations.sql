CREATE TABLE IF NOT EXISTS agent_trace_curations (
  id                 TEXT PRIMARY KEY,
  source_run_id      TEXT NOT NULL,
  title              TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL DEFAULT 'draft',
  tags               TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags)),
  notes              TEXT NOT NULL DEFAULT '',
  quality            TEXT NOT NULL DEFAULT '',
  reward             REAL,
  split              TEXT NOT NULL DEFAULT '',
  source_trace_hash  TEXT NOT NULL DEFAULT '',
  validation_summary TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(validation_summary)),
  trace              TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(trace)),
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  FOREIGN KEY (source_run_id) REFERENCES agent_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_trace_curations_source_run_id
  ON agent_trace_curations(source_run_id);

CREATE INDEX IF NOT EXISTS idx_agent_trace_curations_status_updated_at
  ON agent_trace_curations(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS agent_trace_curation_events (
  event_id     INTEGER PRIMARY KEY AUTOINCREMENT,
  curation_id  TEXT NOT NULL,
  kind         TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  FOREIGN KEY (curation_id) REFERENCES agent_trace_curations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_trace_curation_events_curation_id_event_id
  ON agent_trace_curation_events(curation_id, event_id);
