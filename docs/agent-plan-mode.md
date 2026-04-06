# Agent Plan Mode

`nalvin` supports a dedicated `plan` run mode for tasks where you want the agent to investigate, write down an implementation plan, and stop before changing the repository.

Plan mode is separate from normal execution:

- `default` mode is for doing the work.
- `plan` mode is for producing and refining an implementation plan that can later be handed off to a fresh execution run.

## Why It Exists

Plan mode gives the agent a place to think in-repo without mixing planning and implementation in the same run. It is useful when you want:

- a durable plan file inside the workspace
- a review point before code changes begin
- a clean implementation run with the approved plan injected as the task
- a safer workflow for larger or riskier changes

## Starting A Plan Run

From the CLI:

```bash
bun run cli -- --workspace alpha agent run --mode plan -p "Plan the migration to the new auth flow."
```

For queued execution:

```bash
bun run cli -- --workspace alpha agent run --queue --mode plan -p "Plan the migration to the new auth flow."
```

The first turn of a new plan run creates a canonical plan artifact under `.plans/` inside the active workspace:

```text
.plans/20260405-153045-plan-the-migration-to-the-new-auth-flow.md
```

The filename is timestamped and slugged from the run title or prompt. The initial file is scaffolded with:

- a title
- `Status: planning`
- `Updated: ...`
- `## Summary`
- `## Requested Work`
- `## Implementation Notes`
- `## Verification`

That file is the source of truth for the run's plan.

## Agent Behavior In Plan Mode

When a run is in `plan` mode, the system prompt adds explicit planning-only guidance:

- use tools to inspect code, search files, run tests, and validate assumptions
- do not use tools to carry out the requested repository changes yet
- update the canonical plan file directly with file tools instead of staging plan text in temp files
- call `plan_exit` once the plan file is updated and ready for approval

Plan mode also changes the default tool surface:

- `view`
- `write`
- `edit`
- `multiedit`
- `plan_exit`

Those tools are automatically enabled and pinned for plan runs. `plan_exit` is only available on root plan runs, not child runs.

Child runs can still help gather information for a plan, but they are instructed not to finalize the plan or call `plan_exit` themselves.

## Resuming A Plan Run

If you resume an existing plan run and do not pass a new mode override, it stays in `plan` mode. In practice that means:

```bash
bun run cli -- --workspace alpha agent run --resume <run-id> -p "Refine the rollout and test plan."
```

continues the same plan workflow and keeps using the same canonical `.plans/...` file recorded in the run trace.

For persisted runs, both the run mode and the `plan_ref` are treated as immutable metadata, so changing either on an existing run is rejected.

## Completing The Plan

When the plan is ready, the agent calls `plan_exit`.

That does three things:

1. marks the plan reference as `ready`
2. emits a `plan_ready` event for queue/API consumers
3. terminates the planning run once the ready result is observed

The plan metadata is stored in the run trace under `metadata.plan_ref`:

```json
{
  "mode": "plan",
  "plan_ref": {
    "path": ".plans/20260405-153045-example.md",
    "source_run_id": "run_123",
    "status": "ready"
  }
}
```

Plan status values currently used by the workflow are:

- `active` while the plan is being written
- `ready` after `plan_exit`
- `approved` on the follow-up implementation run that consumes the approved plan

## Implementation Handoff

Plan mode is intentionally a two-run workflow:

1. a plan run produces the canonical handoff plan file
2. a fresh default-mode run implements that plan

The implementation run is prepared from the plan run by reading the canonical plan file and building a new root-run prompt that includes:

- the canonical plan path
- the full approved plan body
- instructions to follow workspace-relative paths exactly as written

The follow-up run is reset to normal execution mode:

- `mode` becomes `default`
- `run_kind` becomes `root`
- the new run carries a `plan_ref` pointing back to the source plan run with status `approved`

If provider, model, title, or system prompt are not overridden for the implementation run, `nalvin` reuses them from the source plan run.

## Target Root Inference

The current working copy adds an extra safety layer during implementation handoff.

When `nalvin` prepares the implementation run, it tries to infer a target workspace-relative root from:

- repeated code-span paths in the approved plan
- the source run title and prompt
- top-level directories in the active workspace

If it can confidently identify a directory, the generated implementation prompt adds guidance like:

```text
Target workspace-relative root: demo-default-1775260677
Create and update files under that directory unless the plan explicitly says otherwise.
Do not place those files at the workspace root by default.
```

This is meant to protect plan handoff for nested repos or project folders whose plan uses repo-root wording like `README.md` or `scripts/release.sh`.

## CLI Handoff Behavior

Both attached and queued CLI flows watch for the `plan_ready` event.

After the planning run finishes:

- in an interactive terminal, the CLI prompts whether to start a fresh implementation run
- in a non-interactive context, the CLI prints the plan path and leaves implementation as a manual next step

If you confirm the handoff interactively, the CLI immediately creates and executes the follow-up implementation run and prints its new run ID.

## API Surface

The server exposes two main plan-related surfaces:

- `POST /api/agent-runs` with `"mode": "plan"` to start a plan run
- `POST /api/agent-runs/{runID}/implement-plan` to create a new queued implementation run from an existing plan run by reading its canonical plan file

The trace returned by run APIs also exposes:

- `metadata.mode`
- `metadata.plan_ref`

Queue/event consumers can watch for the `plan_ready` event, whose payload includes:

- `plan_path`
- `source_run_id`
- `status`

## Notes And Limits

- Plan mode is a workflow constraint, not a sandbox. The safety comes from the run prompt, tool defaults, and handoff flow.
- The canonical plan file always lives in the workspace, not in transient temp storage.
- Plan runs still have access to normal investigation tools such as search, file inspection, and tests when those help improve the plan.
- `plan_exit` is not available in child runs.
- Implementation handoff creates a new run instead of mutating the plan run into an execution run.

## Related Docs

- [Agent tools](./agent-tools.md)
- [Agent conversation compaction](./agent-conversation-compaction.md)
