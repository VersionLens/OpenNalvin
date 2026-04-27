---
name: python-in-containers
description: Use uv instead of pip when managing Python packages inside containers, and bind dev servers to 0.0.0.0 so published ports reach the host.
metadata:
  agent:
    activation: manual
    run_scopes: [root, child]
    tool_hints: [docker]
    autoreveal_tools:
      - docker_create_container
      - docker_start_container
      - docker_exec_foreground
      - docker_exec_background
      - docker_exec_tail
      - docker_exec_signal
      - docker_exec_list_processes
---

## Python In Containers

Activate this skill before containerized Python work. It auto-reveals the core Docker execution tools when those tools are already allowed in the run.

Inside `nalvin/dev`-style containers, use `uv` for Python package management and script execution. Do not call `pip` directly — it is not on PATH.

- Library installs: `uv venv .venv && uv pip install -r requirements.txt`
- CLI tools: `uvx <tool>` or `uv tool install <tool>` (uv-installed executables land on PATH)
- Scripts: `uv run script.py`

## Dev Servers In Containers

Dev servers running inside a container must bind to `0.0.0.0`, not `127.0.0.1` or `localhost`, or host-side clients will never reach them. Examples:

- `vite --host 0.0.0.0`
- `uvicorn main:app --host 0.0.0.0`
- `python -m http.server --bind 0.0.0.0 8000`

Plan all published ports up front when creating the container — ports cannot be added after creation with `docker_create_container`. Use `docker_exec_background` for the dev server itself so the parent run keeps control of it.

Use `search_tools` only if a needed Docker tool is still hidden after activation.
