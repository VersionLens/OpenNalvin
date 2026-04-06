# AGENTS

## Project Dev Notes

- This repo is a Go backend plus a Bun-powered web workspace under `web/`.
- **All Go commands require `-tags sqlite_fts5`.** Use the Makefile (preferred) or bun scripts — both set this automatically. If running `go` directly, always pass `-tags sqlite_fts5` (e.g. `go test -tags sqlite_fts5 ./...`).
- Use `make dev` for the normal full-stack dev loop, or `make dev-api` / `make dev-web` when working on one side only. The API binds to `0.0.0.0:4210`; the Vite dev server binds to `0.0.0.0:4211` and proxies `/api` to Go.
- Use `make cli ARGS="--workspace <ws> agent run -p '...'"` for CLI/manual workflow checks.
- Common verification commands:
  - `make test-go` (or `make test` for Go + web)
  - `make test-web`
  - `make typecheck-web`
  - `make build-web`
  - `make build` (web + Go binary)
- The Go binary expects the web assets to be built for embedded frontend serving, so if UI changes need to be reflected in the packaged app, run `make build-web` before rebuilding Go binaries.
- SQLite behavior matters in this repo. The project uses upstream `mattn/go-sqlite3` with the `sqlite_fts5` build tag for FTS5 support. The Makefile handles this via `GOFLAGS=-tags=sqlite_fts5`.
- Workspace state lives under `~/.nalvin/`, especially:
  - `~/.nalvin/config.yaml` (providers, default pinned/enabled tools — see `cmd/config.default.yaml`)
  - `~/.nalvin/workspace-db/<name>.sqlite` (agent runs, KB nodes/edges, tool outputs per workspace)
- Default Docker workflows in this repo assume the `nalvin/dev` image and the built-in Docker tooling/docs; preserve those conventions unless there is a clear reason to change them.

## Running the Agent

The primary smoke-test loop is `make cli ARGS="--workspace <ws> agent run -p '...'"`. Key flags (see `cmd/agent_run.go`):

- `-p, --prompt` (required) — the user prompt.
- `--resume <run-id>` — continue an existing run with a new user turn.
- `--provider <name>` / `--subagent-provider <name>` — pick from `providers:` in `config.yaml`.
- `--system <text>` — override system prompt for this run.
- `--enable-tool` / `--disable-tool` / `--pin-tool` / `--unpin-tool` — per-run tool overrides (repeatable). Defaults come from `config.yaml` pinned/enabled lists.
- `--verbose` — debug logs for tool resolution, tool calls, and subagent lifecycle.
- `--timing` — print queue/first-token/first-content timing breakdown.
- `--context-window <n>` — force a smaller window to exercise compaction.
- `--mode default|plan` — plan mode gates write tools (see `docs/agent-plan-mode.md`).
- `--queue` — enqueue through the River worker instead of running inline.

Tool metadata/inspection should go through the runtime path, not ad hoc source reading:

- `make cli ARGS="--workspace <ws> agent tools list --json"`
- `make cli ARGS="--workspace <ws> agent tools run <tool> --input '{...}'"`
- `make cli ARGS="--workspace <ws> agent repl"` for interactive sessions.

## Inspecting Agent Runs

Every `agent run` writes to `~/.nalvin/workspace-db/<workspace>.sqlite`, table `agent_runs` (id, title, model, prompt, status, error, duration_ms, message_count, input/output tokens, trace JSON, timestamps). Related tables cover messages, tool calls, and tool outputs.

To look up runs for analysis:

- **Web UI**: start `bun run dev` and browse the agent runs list — richest view (timing, compaction, revealed tools, streamed tool outputs).
- **HTTP API** (while `serve` is running on `:4210`):
  - `GET /api/agent-runs?status=&limit=&offset=` — list
  - `GET /api/agent-runs/{runID}` — full run incl. trace
  - `GET /api/agent-runs/{runID}/events` — SSE event stream
  - `GET /api/agent-runs/{runID}/tool-outputs/{outputID}?offset=&limit=` — paginated tool output
  - `GET /api/agent-runs/{runID}/tool-outputs/{outputID}/grep?pattern=&literal_text=` — search within a tool output (useful for spillover runs)
- **Direct SQLite** (fastest for quick postmortems): `sqlite3 ~/.nalvin/workspace-db/<ws>.sqlite "SELECT id, status, model, duration_ms, message_count, substr(prompt,1,80) FROM agent_runs ORDER BY updated_at DESC LIMIT 20;"`. The `trace` column holds the full message/tool-call sequence as JSON.

When diagnosing a failing or suspicious run, prefer the real data path (DB row + trace JSON, or the `/events` SSE) over re-reasoning about the code — the trace shows exactly what tools the model saw, what it called, and what came back.

## Tool Schema Notes

- For internal typed agent tools, use `newParallelAgentTool(...)` instead of calling `fantasy.NewParallelAgentTool(...)` directly.
- Reason: the raw `fantasy` helper preserves `description:"..."` tags but drops our internal `jsonschema_description:"..."` field docs.
- Internal tool input structs may keep using `jsonschema_description`, but the runtime wrapper must be the path that exposes those descriptions to the model and to `search_tools`.
- When adding or changing internal tool inputs, verify the live catalog shows `schema.properties.<field>.description` for the new fields (via `agent tools list --json`).
