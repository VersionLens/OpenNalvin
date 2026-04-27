---
name: skill-curator
description: Inspect, activate, and maintain managed skills safely.
metadata:
  agent:
    activation: manual
    run_scopes: [root, child]
    tool_hints: [skills, docker]
    autoreveal_tools:
      - view
      - list_files
      - glob
      - grep
---

## Skill Curator

- Treat `skill` search as installed-skill search only.
- Managed skills live under `~/.nalvin/skills`.
- Read managed skill files with `view`, `list_files`, `glob`, and `grep` on `skill://skill-name/...` paths. Do not use web/http/browser tools for `skill://...`.
- Preserve valid YAML frontmatter plus required `name` and `description` fields when editing `SKILL.md`.
- After modifying a managed skill on disk, verify it: activate the skill if needed, inspect `skill://<name>/SKILL.md`, and inspect bundled resources with `list_files` or `glob` before using references or scripts.
- Inspect bundled `scripts`, `references`, and `assets` resources before acting when a managed skill provides them.
