# Agent Tool Output Spillover

`nalvin` keeps agent runs usable when a tool returns a very large text payload.

Instead of sending the full tool result back into model context, the runtime can:

1. keep a compact inline summary in the trace and prompt history
2. persist the full text in the workspace run database
3. reveal pagination and search tools so the agent can inspect the stored output on demand

This applies to agent runs. It does not change the behavior of `agent tools run`, which still prints the direct tool result for a human operator.

## Why It Exists

Large tool results can dominate prompt context and make traces noisy. A single `view` or `web_fetch_get` call can otherwise consume most of the run's token budget.

The spillover path keeps:

- model-visible tool results small
- full output available for later inspection
- persisted traces compact enough to resume efficiently

## How It Works

When a text tool result exceeds the active provider's inline limit:

1. the runtime estimates the result size with the model's token estimator
2. the full canonicalized text is written to `agent_run_tool_outputs` in the workspace SQLite database
3. the model receives a compact JSON summary instead of the full text
4. `view_tool_output` and `grep_tool_output` are revealed for the run
5. the trace stores only the inline summary plus a `tool_output_ref`

The summary includes:

- `output_id`
- original `tool_name`
- stored byte size
- estimated full-output token count
- stored line count
- truncation flags
- a short preview

For spilled `view` results, `nalvin` stores the inner file `content` instead of the outer JSON envelope. For spilled `web_fetch_get` results, it stores the inner `body` plus source metadata such as URL, mode, title, and selector. That makes pagination and grep operate on file text or fetched page content instead of on serialized wrapper JSON.

## Configuration

Configure spillover in `~/.nalvin/config.yaml`:

```yaml
providers:
  default:
    base_url: https://api.example.com
    api_key: REPLACE_ME
    model: some-model
    tool_output_token_limit: 4000

agent:
  tool_output:
    max_stored_bytes: 4194304
    default_page_lines: 200
    grep_default_limit: 100
```

Settings:

- `providers.<name>.tool_output_token_limit`
  - estimated maximum tool-result tokens to keep inline for that provider/model
  - default: `4000`
- `agent.tool_output.max_stored_bytes`
  - hard cap for stored spilled text per tool result
  - default: `4194304` bytes
- `agent.tool_output.default_page_lines`
  - default `limit` when `view_tool_output` is called without one
  - default: `200`
- `agent.tool_output.grep_default_limit`
  - default maximum match count when `grep_tool_output` is called without one
  - default: `100`

If stored content exceeds `max_stored_bytes`, the database row is still created, but `stored_truncated=true` is recorded in both the stored row and the inline summary.

## Agent-Facing Tools

These tools stay hidden until the first spill happens in a run.

### `view_tool_output`

Read a line window from a spilled result by `output_id`.

Example shape:

```json
{
  "output_id": "out_123",
  "offset": 200,
  "limit": 100
}
```

The response includes:

- `start_line`
- `end_line`
- `total_lines`
- `content`
- `truncated`

### `grep_tool_output`

Search inside a spilled result by `output_id`.

Example shape:

```json
{
  "output_id": "out_123",
  "pattern": "NEEDLE-4096",
  "literal_text": true,
  "limit": 20
}
```

The response includes matching line numbers and centered previews around the first match, plus a `truncated` flag if the result set hit the limit or exhausted the preview budget. Each preview is clipped to 240 characters and the overall grep preview payload is capped, so a single huge line cannot recreate the original context blow-up.

## Persistence Model

Spilled text is stored in the workspace database table:

- `agent_run_tool_outputs`

Each row is scoped to a single run and includes:

- `output_id`
- `run_id`
- `tool_call_id`
- `tool_name`
- `content`
- `size_bytes`
- `estimated_tokens`
- `total_lines`
- `stored_truncated`
- `inline_truncated`

The agent trace stores a compact `tool_output_ref` on the tool-result message so resumed runs do not reload the full spilled payload into model context.

## HTTP API

The web UI reads spilled outputs through two endpoints:

- `GET /api/agent-runs/{runID}/tool-outputs/{outputID}?offset=&limit=`
- `GET /api/agent-runs/{runID}/tool-outputs/{outputID}/grep?pattern=&literal_text=&limit=`

Both endpoints enforce run scoping, so an output id can only be read from the run that owns it.

## UI Behavior

In the run detail view, spilled tool results show:

- a spilled badge
- the inline preview
- stored byte size
- estimated full-output tokens
- line count
- pagination controls
- grep/search controls

Token accounting in the run trace reflects the inline summary that the model actually saw, not the full stored payload.

## Notes and Limits

- v1 spillover applies to successful text tool results.
- Non-text results are not spilled.
- Direct `agent tools run ...` output is intentionally unchanged.
- Progress is persisted during live runs, so externally terminated runs can be marked `aborted` with partial trace state preserved.
