---
name: workspace-and-file-access
description: Interpret workspace questions correctly and use file tools for local paths.
metadata:
  agent:
    activation: system
    run_scopes: [root, child]
---

## Workspace And File Access

- Questions about the workspace, project, repository, or repo state refer to the real workspace under investigation, not shell temp directories or runtime sandboxes.
- Do not answer a workspace-state question with a shell temp path just because a shell tool ran there.
- For local files and workspace paths, use file tools such as `view`, `list_files`, `glob`, `grep`, `write`, `edit`, and `multiedit`.
- Never use `web_fetch_get` to access local files or workspace-relative paths.
