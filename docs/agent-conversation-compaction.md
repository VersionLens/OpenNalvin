# Agent Conversation Compaction

`nalvin` keeps long agent runs within context window limits by compacting old conversation history into a structured checkpoint summary.

Instead of letting the prompt grow until the provider rejects it, the runtime can:

1. estimate the current prompt size against the provider's context window
2. summarize old messages into a compact checkpoint using the same model
3. inject the checkpoint into the system prompt and trim the compacted prefix
4. preserve the full transcript in the stored trace for audit and debugging

This is a separate layer from [tool output spillover](./agent-tool-output-spillover.md), which handles individual large tool results. Compaction handles the aggregate growth of the conversation itself.

## Why It Exists

Multi-step agent runs accumulate messages quickly. Each tool call produces an assistant message, a tool result, and often reasoning text. A run that reads several files and makes edits can exceed the model's context window in a handful of steps.

Compaction keeps:

- the model-visible prompt within context limits
- recent conversation turns intact so the model retains immediate context
- older context preserved as a structured summary rather than lost entirely
- spilled tool output references usable through `view_tool_output` and `grep_tool_output`

## How It Works

### Proactive compaction

Before each agent step, the runtime estimates the total prompt size (system prompt + all messages). When the estimate exceeds `trigger_pct` of the configured `context_window_tokens`:

1. the compaction manager selects a split point that keeps the most recent messages intact
2. the split snaps to a clean conversation boundary (user or assistant message, never a bare tool result)
3. the compactable prefix is summarized via a no-tools model call using the same provider and model
4. the model returns a structured JSON summary capturing goal, constraints, decisions, completed work, open tasks, files touched, and referenced tool outputs
5. the summary is rendered as a `Conversation Checkpoint` block and appended to the system prompt
6. the compacted messages are trimmed from the model-visible prompt

### Incremental compaction

If a previous checkpoint exists, the new compaction folds the delta into it. The summarization prompt includes the previous checkpoint so the model can merge rather than re-summarize from scratch. Tool output references from earlier checkpoints are carried forward.

### Reactive overflow recovery

If the provider returns `IsContextTooLarge()` despite proactive compaction (or when `context_window_tokens` is not configured), the runtime:

1. flushes the current builder state
2. runs aggressive compaction with fewer kept messages (`aggressive_keep_recent_messages`)
3. rebuilds the message list and retries the stream once
4. if the retry still overflows, the run fails with a clear error

### Resumption

Compaction state is persisted in the trace's `compactions[]` array. When a run is resumed, the checkpoint is restored from the stored trace. No re-summarization call is needed.

## Configuration

Configure compaction in `~/.nalvin/config.yaml`:

```yaml
providers:
  default:
    base_url: https://api.example.com
    api_key: ${PROVIDER_API_KEY}
    model: some-model
    context_window_tokens: 128000

agent:
  compaction:
    enabled: true
    trigger_pct: 85
    reserved_tokens: 12000
    keep_recent_messages: 8
    aggressive_keep_recent_messages: 4
    max_summary_tokens: 1200
    max_compactions_per_run: 8
```

Settings:

- `providers.<name>.context_window_tokens`
  - the model's context window size in tokens
  - if unset or `0`, proactive compaction is skipped; only reactive overflow recovery is used
- `agent.compaction.enabled`
  - master switch for the compaction system
  - default: `true`
- `agent.compaction.trigger_pct`
  - percentage of context window that triggers proactive compaction
  - default: `85`
- `agent.compaction.reserved_tokens`
  - reserved token budget to keep clear of the context limit
  - default: `12000`
- `agent.compaction.keep_recent_messages`
  - number of recent messages to keep uncompacted during proactive compaction
  - default: `8`
- `agent.compaction.aggressive_keep_recent_messages`
  - number of recent messages to keep during reactive (overflow) compaction
  - default: `4`
- `agent.compaction.max_summary_tokens`
  - maximum output tokens for the summarization model call
  - default: `1200`
- `agent.compaction.max_compactions_per_run`
  - safety limit on total compaction calls per run
  - default: `8`

## Checkpoint Shape

Each compaction produces a structured summary:

```json
{
  "goal": "Implement the login feature",
  "constraints": ["Must use OAuth 2.0", "No new dependencies"],
  "decisions": ["Chose PKCE flow for public clients"],
  "completed": ["Added OAuth config", "Created auth middleware"],
  "open": ["Write integration tests"],
  "files": ["internal/auth/oauth.go", "internal/auth/middleware.go"],
  "tool_outputs": [
    {
      "output_id": "out_abc123",
      "tool_name": "view",
      "why_it_matters": "OAuth config file content"
    }
  ]
}
```

The `tool_outputs` array preserves references to spilled tool outputs from before compaction, so the agent can still inspect them via `view_tool_output` and `grep_tool_output`.

## Model-Visible Prompt After Compaction

After compaction, the model sees:

1. **System prompt**: the original system prompt plus a rendered `Conversation Checkpoint` block
2. **Messages**: a synthetic user continuation message followed by the uncompacted tail

The checkpoint block is formatted as a readable summary with labeled sections (Goal, Constraints, Decisions Made, Completed, Open Tasks, Files Touched, Stored Tool Outputs).

## Trace Schema

Compaction records are stored in the trace under `compactions[]`:

```json
{
  "compactions": [
    {
      "id": "46d111b1-22b3-4fd7-b18b-a957a4346292",
      "created_at": "2026-03-29T15:30:00Z",
      "trigger": "proactive",
      "covered_message_count": 9,
      "pre_tokens": 564,
      "post_tokens": 367,
      "summary": { "..." }
    }
  ]
}
```

Each entry records:

- `id` - unique compaction identifier
- `created_at` - when the compaction occurred
- `trigger` - `proactive` or `reactive`
- `covered_message_count` - how many messages from the start are summarized
- `pre_tokens` - estimated tokens in the compacted prefix before summarization
- `post_tokens` - estimated tokens in the rendered checkpoint after summarization
- `summary` - the structured checkpoint payload

The full transcript is always preserved in `messages[]`. Compaction only changes what the model sees, not what is stored.

## Interaction with Spillover

Compaction and spillover work together:

- when a message with a `tool_output_ref` is compacted, the reference is carried forward into the checkpoint's `tool_outputs` array
- the rendered checkpoint tells the model that stored outputs remain inspectable
- `view_tool_output` and `grep_tool_output` continue to work after compaction

## CLI Testing Flag

The `agent run` command accepts `--context-window <tokens>` to override the provider's context window for testing:

```bash
bun run cli -- agent run \
  --context-window 500 \
  --verbose \
  -p "Read every file and summarize them"
```

With `--verbose`, compaction events appear in debug output:

```text
[debug] compaction needed: estimated=1011 threshold=425 context_window=500
[debug] compacting proactive: compactable_messages=6 keep_recent=8 pre_tokens=564
[debug] compaction complete: id=b69eabf7-... covered=6 pre=564 post=367
```

## Notes and Limits

- v1 compaction uses the same configured provider and model for summarization. No separate "small compaction model" is used.
- v1 is automatic only. There is no manual `/compact` command or API endpoint.
- Compaction never drops the most recent tail or strips tool visibility state.
- Disabled compaction (`enabled: false`) leaves all existing behavior unchanged.
- If `context_window_tokens` is not set, proactive compaction is skipped but reactive overflow recovery still works.
- The `max_compactions_per_run` limit prevents runaway summarization in pathological cases.
- The split point always lands on a user or assistant message boundary, never in the middle of a tool-call/result pair.
