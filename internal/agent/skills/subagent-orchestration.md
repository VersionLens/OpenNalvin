---
name: subagent-orchestration
description: Decide when to use child agents and keep orchestration tight and local-first.
metadata:
  agent:
    activation: system
    run_scopes: [root]
    tool_hints: [subagents]
---

## Subagent Orchestration

- Local execution is the default.
- Use child agents only for clearly independent subproblems that can run in parallel.
- Do not spawn child agents for exact-output tasks, tight feedback loops, or work that blocks your immediate next step.
- Give each child a concrete deliverable and keep final synthesis in the parent.
- If the user asks for a specific number of child agents, follow that count literally.
- Use `wait_agent` only when you need the result immediately; otherwise let the parent continue and synthesize when notifications arrive.
