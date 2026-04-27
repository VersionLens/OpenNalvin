# Tinker Trace Creation And Curation Plan

This plan describes the first harness-intuition dataset for Tinker SFT. The goal
is to teach Qwen-style open-reasoning models how agents should move
through the harness: load skills, reveal hidden tools, choose local/browser/MCP
tools correctly, recover from tool errors, and stop once they have enough
evidence.

## Baseline Signal

We ran a partial non-finetuned baseline probe against the manually served local
providers:

- Mac mini provider serving `Qwen/Qwen3.5-4B`
- Omen provider serving `Qwen/Qwen3.6-35B-A3B`

The model string in `~/.nalvin/config.yaml` does not need to match these names;
llama.cpp uses the model actually served by the endpoint.

Captured runs:

- `baseline-qwen35-4b`: 2 completed, 1 aborted
- `baseline-qwen36-35ba3b`: 4 completed, 1 aborted

Observed behavior:

- Skill-management prompts worked on both providers, but shared global skill
  names caused cross-run interference. Future baseline or generated traces
  should use provider-specific or run-specific skill names.
- Mac mini completed the first skill prompts but had many tool errors and
  recovery loops. This points to a need for more clean examples of interpreting
  tool errors and recovering without spiraling.
- Omen completed more cleanly, including skill selection and file counting, but
  tended to over-collect and over-summarize news. This points to a need for
  concise research traces that stop after enough evidence.
- Both models bogged down on news/tool-heavy prompts. The training set should
  emphasize the right sequence: reveal the correct tool, fetch minimal useful
  sources, answer in the requested shape.

The baseline traces are diagnostic probes, not primary training examples. Use
them to guide what high-quality demonstrations to curate or generate.

## Dataset Target

Build **50-75 approved traces**, ideally around **60 train / 12 eval**. If fewer
strong traces survive review, a minimum useful trial is **42 train / 8 eval**.

The adjusted target spread is:

| Category | Target Count | Why |
| --- | ---: | --- |
| Skill lifecycle / skill guardrail | 14 | The models need strong intuition for activating skills, reading `SKILL.md`, using `skill://` paths, and respecting managed-skill guardrails. |
| Hidden tool discovery | 12 | The harness relies on `search_tools` and reveal flows; both models need crisp examples of finding the right tool without wandering. |
| News / research discipline | 10 | Baselines over-collected and over-summarized. Use compact traces that fetch only enough sources and answer in the requested shape. |
| Tool-error recovery / loop avoidance | 9 | Mac mini showed tool errors and recovery loops. Include successful recoveries from expected errors without repeated failed calls. |
| Sub-agent orchestration | 8 | Teach when to spawn, how to assign bounded work, when to wait, and how to synthesize child results. |
| Shell / docker / workspace | 7 | Preserve local-tool intuition: shell for local facts, docker for container tasks, workspace tools for file inspection. |
| Browser / MCP | 6 | Teach when browser or connector tools are appropriate and when they are not. |
| Git / code / file-output | 6 | Cover git inspection, codebase search, large output summarization, and spilled-output handling. |

These categories can overlap. For example, a good sub-agent trace may also cover
browser or research discipline. Count a trace by its primary teaching purpose.

## Curation Criteria

Approve traces that show the desired harness behavior clearly:

- The agent chooses the right skill or tool path quickly.
- The trace includes useful reasoning for the choice.
- Tool calls are purposeful and not repetitive.
- Tool errors are interpreted correctly and followed by a better action.
- The final answer is grounded in tool results.
- The final answer follows the requested shape and stops without extra material.
- Tool output is summarized at the level the model actually saw, unless a full
  materialized output is necessary for understanding.

Reject or heavily edit traces with:

- Unresolved task failure.
- Repeated tool loops.
- Misleading or ungrounded final answers.
- Excessive browsing or web use for local workspace questions.
- Private/secrets data.
- Long news dumps where a concise synthesis was requested.

Allowed edits should stay light: redact sensitive values, remove duplicate
transcript noise, and fix obvious trace artifacts. Do not rewrite a poor trace
into a different imagined behavior; generate a better demonstration instead.

## Creation Priorities

Mine historical traces first, then generate targeted gap-fill traces for baseline
weaknesses.

Prioritize these demonstration patterns:

1. **Skill activation and scoped reading**
   - Search or select a relevant skill.
   - Activate it.
   - Read only the relevant `SKILL.md`, script, or reference.
   - Use `skill://` tools, not browser/web tools.

2. **Managed skill guardrail recovery**
   - Attempt a known-invalid `modify_skill`.
   - Observe the validation error.
   - Verify the skill remains valid.
   - Explain the guardrail succinctly.

3. **Hidden tool reveal**
   - Use `search_tools` with the right query.
   - Select the right revealed tool.
   - Avoid extra unrelated searches once the tool is found.

4. **Concise news/research**
   - Reveal or activate the news workflow.
   - Fetch one or two relevant sources.
   - Answer in the requested number of bullets or lines.
   - Avoid dumping all headlines.

5. **Tool-error recovery**
   - Encounter a missing file, invalid argument, failed edit, or unavailable
     tool.
   - Explain the error.
   - Choose the next best tool/path once.
   - Finish cleanly.

6. **Sub-agent orchestration**
   - Spawn only for work that benefits from delegation.
   - Give a bounded child task.
   - Wait when needed.
   - Synthesize child output without duplicating it verbatim.

7. **Local vs browser/tool choice**
   - Use shell/files for local repo questions.
   - Use browser only for open-tab or web UI inspection.
   - Use MCP/connectors only when the task actually needs them.

## CLI Workflow

Find candidates:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces candidates --status completed --min-messages 6 --min-tool-calls 1 --max-tool-errors 1 --limit 100 --json'
```

Review:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces validate <run-id>'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces stats <run-id> --json'
```

Curate and label:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces curate <run-id...> --quality high --reward 1 --split train --tag harness-intuition-v1 --tag <category> --approve'
```

Use `split=eval` for held-out examples. Keep eval prompt families distinct from
train when possible.

Export:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces export --out ./export/harness-intuition-v1/train --split train --tag harness-intuition-v1'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces export --out ./export/harness-intuition-v1/eval --split eval --tag harness-intuition-v1'
```

Verify:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces verify-export ./export/harness-intuition-v1/train --json'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces verify-export ./export/harness-intuition-v1/eval --json'
```

## Acceptance Criteria

The dataset is ready for the first Tinker trial when:

- At least 50 traces are approved.
- The adjusted category spread is roughly satisfied.
- Every trace has `quality=high`, `reward=1`, `split=train|eval`, and
  `harness-intuition-v1`.
- Reasoning is preserved by default.
- Tool calls and tool results verify as Tinker-compatible chat.
- No known secrets or irrelevant private paths remain.
- Eval traces are not near-duplicates of train traces.
- `verify-export` reports zero shape errors for both train and eval.
