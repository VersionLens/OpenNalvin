---
name: memory-management
description: Use workspace memory (memory_* tools) for ad-hoc cross-run recall; use knowledge skills for curated, topic-scoped stores.
metadata:
  agent:
    activation: system
    run_scopes: [root]
    tool_hints: [memory]
---

## Memory vs knowledge skills

Two distinct stores. Pick the right one:

**Memory** (`memory_*` tools, workspace-local SQLite) — ad-hoc, mutable, agent-writable. Scratch space for things you learn this run that might matter next run. Unreviewed, free-form, cheap.

**Knowledge skills** (e.g. activated skills that ship a CLI) — git-backed, curated, topic-scoped. Canonical source of truth for a subject area. Read-mostly from the agent's perspective; propose changes through the skill's own CLI, not through memory tools.

## When to reach for memory

- **At run start** for non-trivial tasks: call `memory_list_nodes` with a kind filter or keyword relevant to what you're about to do. Past-you may have left useful notes.
- **Before asking the user** something they may have told past-you: check memory first.
- **After producing a durable insight** — a user preference, a project constraint, a pitfall that cost time to discover, a carryover to-do — record it with `memory_create_node`.
- **Use edges sparingly** (`memory_create_edge`) — only when a relation adds real value. Most notes stand alone.

## When NOT to use memory

- Transient state inside a single run — use todos or regular conversation.
- Anything derivable from `git log`, `git blame`, or the current source tree — don't duplicate the code's authority.
- Canonical facts about a topic area that deserve review — propose them into the relevant knowledge skill instead.

## Suggested node kinds

- `preference` — "user likes X this way"
- `fact` — load-bearing truth about the project or workspace
- `incident` — "X broke because Y; fix was Z"
- `todo` — work to carry across runs

Use `kind` consistently so future `memory_list_nodes --kind …` calls are useful.
