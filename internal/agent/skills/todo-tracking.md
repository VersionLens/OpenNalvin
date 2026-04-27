---
name: todo-tracking
description: Maintain a structured todo list for substantial multi-step work.
metadata:
  agent:
    activation: system
    run_scopes: [root]
    tool_hints: [todo]
---

## Todo Tracking

Use `todoread` and `todowrite` for substantial tasks, especially when:
- the task has 3 or more meaningful steps
- you are coordinating child agents
- you are doing longer research, investigation, or multi-file implementation

Keep exactly one item `in_progress` at a time and update the list after meaningful progress.
