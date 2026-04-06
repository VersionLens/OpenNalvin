# Docker Client And Tools

`nalvin` includes a Docker client surface for the human CLI and a hidden-by-default Docker tool family for agent runs.

At a high level:

- `nalvin docker ...` wraps common Docker workflows
- Docker daemon resolution follows the local Docker client behavior unless config or `--host` overrides it
- new containers default to the `nalvin/dev` image, which keeps its own startup command so SSH and code-server come up automatically
- workspace-mounted repos can pull and push against nalvin-managed Git without manual remote rewriting
- agents can discover and use `docker`, `docker_list_containers`, `docker_list_images`, `docker_list_networks`, `docker_list_volumes`, `docker_pull_image`, `docker_build_image`, `docker_create_container`, `docker_start_container`, `docker_stop_container`, `docker_remove_container`, `docker_exec_foreground`, `docker_exec_background`, `docker_exec_tail`, `docker_exec_signal`, and `docker_exec_list_processes` through `search_tools`

## Configuration

Relevant config keys:

```yaml
docker:
  host: ""
  binary: ""
  default_image: "nalvin/dev"
```

Notes:

- leaving `docker.host` empty means `nalvin` does not pass `--host`, so the local Docker CLI resolves the active context normally
- leaving `docker.binary` empty means `nalvin` resolves `docker` from `PATH`
- `docker.default_image` is used by `nalvin docker create` and `docker_create_container` unless an image override is provided

## `nalvin docker` CLI

List resources:

```bash
nalvin docker containers list
nalvin docker images list
nalvin docker networks list
nalvin docker volumes list
```

Pull and build images:

```bash
nalvin docker pull nalvin/dev
nalvin docker build app --tag my-app:dev
```

Create and manage containers:

```bash
nalvin docker create --name ws-worker --mount-workspace --image my-app:dev
nalvin docker start ws-worker
nalvin docker exec ws-worker -- sh -lc "cd /workspace && git status"
nalvin docker exec ws-worker -- sh -lc "cd /workspace/repo && git pull && git push"
nalvin docker stop ws-worker
nalvin docker remove ws-worker
```

Important create flags:

- `--image`
- `--name`
- `--mount-workspace`
- `--repo-path`
- repeated `--bind`
- repeated `--volume`
- repeated `--ports` (e.g. `--ports 5173:5173 --ports 8000:8000`)
- `--network`
- repeated `--env`
- `--workdir`
- `--cmd`

Important build flags:

- positional `context-path`
- `--dockerfile`
- `--tag`
- repeated `--build-arg`
- `--target`

## Workspace Mounts

`--mount-workspace` is the convenience path for containerized workspace work:

- it bind-mounts the active workspace root at `/workspace`
- it defaults the container workdir to `/workspace` unless `--workdir` overrides it
- it is separate from `--repo-path`, which mounts one workspace-relative repo path at `/workspace`

Structured bind mounts also stay rooted in the active workspace:

- `--bind app:/src`
- `--bind app:/src:ro`

Named volumes use Docker volume names instead of host paths:

- `--volume cache:/cache`
- `--volume cache:/cache:ro`

For the default `nalvin/dev` image, `nalvin docker create` also:

- publishes loopback-only ephemeral host ports for SSH and code-server
- mounts persistent auth volumes at `/home/nalvin/.claude` and `/home/nalvin/.codex`
- mounts a generated `authorized_keys` file for SSH access based on configured nalvin Git keys
- preserves the image entrypoint/CMD so `sshd` and code-server can start normally

## Git Credentials Inside Containers

Containers created through `nalvin` automatically receive nalvin-managed Git credentials:

- the default client private key is mounted readonly
- a generated `known_hosts` file for the nalvin Git SSH server is mounted readonly
- `GIT_SSH_COMMAND` is set to use those mounted credentials
- Docker adds `host.docker.internal:host-gateway` when the configured nalvin Git host is loopback-based
- Docker injects a Git URL rewrite so remotes such as `ssh://nalvin@127.0.0.1:4222/demo.git` continue to work unchanged inside the container
- Git author/committer name and email default to the configured nalvin Git client identity

This makes it straightforward to create containers that operate on workspace-mounted repos and then pull/push against nalvin-managed Git without rewriting `origin` or overriding SSH settings by hand.

When the configured nalvin Git server lives on a loopback address such as `127.0.0.1:4222`, `nalvin docker create` makes that work inside Docker by:

- trusting both the normal Git host and `host.docker.internal` in the mounted `known_hosts`
- adding a Docker host alias so the container can reach the host machine
- rewriting loopback-style nalvin Git URLs to the Docker-reachable host alias through injected Git config

That means a repo cloned on the host can usually be mounted into a container and pushed from inside Docker as-is:

```bash
nalvin --workspace alpha git checkout demo repo
nalvin --workspace alpha docker create --name repo-worker --mount-workspace
nalvin --workspace alpha docker start repo-worker
nalvin --workspace alpha docker exec repo-worker -- sh -lc "cd /workspace/repo && git pull && git push"
```

## Agent Docker Tools

The Docker tools are enabled by default but hidden until the model reveals them through `search_tools`.

Tool list:

- `docker`
- `docker_list_containers`
- `docker_list_images`
- `docker_list_networks`
- `docker_list_volumes`
- `docker_pull_image`
- `docker_build_image`
- `docker_create_container`
- `docker_start_container`
- `docker_stop_container`
- `docker_remove_container`
- `docker_exec_foreground`
- `docker_exec_background`
- `docker_exec_tail`
- `docker_exec_signal`
- `docker_exec_list_processes`

Common reveal queries:

- `docker`
- `list containers`
- `list images`
- `list networks`
- `list volumes`
- `pull image`
- `build image`
- `docker build`
- `create container`
- `mount workspace`
- `docker exec`
- `background process`, `dev server`, `start server`
- `tail output`, `process logs`
- `kill process`, `stop process`

The main `docker` tool is intentionally scoped:

- it supports `ps`, `images`, `network ls`, `volume ls`, `pull`, `build`, `create`, `start`, `stop`, `rm`, and `exec`
- it accepts shell-style quoting, but rejects shell operators and shell substitution
- workspace-local file paths used for build contexts, Dockerfiles, and bind mounts stay within the active workspace root

The helper tools are preferred for structured workflows:

- `docker_build_image` requires `context_path`
- `docker_create_container` supports `mount_workspace`, `repo_path`, bind mounts, named volumes, env vars, and optional command override
- `docker_create_container` also injects Docker-aware nalvin Git access for mounted repos, including loopback-host URL rewriting when needed
- `docker_list_containers` and `nalvin docker containers list` report detected SSH and code-server endpoints when those ports are published
- `docker_exec_foreground` returns structured `ok`, `exit_code`, `stdout`, `stderr`, `argv`, and `container`
- `docker_exec_background` starts a long-running process and returns immediately with a `process_id`
- `docker_exec_tail` reads recent output from a background process and reports whether it is still running
- `docker_exec_signal` sends a signal (default TERM) to a background process
- `docker_exec_list_processes` lists all tracked background processes in a container
- `docker_create_container` accepts `ports` for publishing container ports to the host (e.g. `5173:5173`); ports cannot be added after creation

## Port Publishing

Docker does not support adding ports to a running container. All port mappings must be declared at container creation time using the `ports` parameter on `docker_create_container` or the `--ports` flag on `nalvin docker create`.

Port specs use the same format as `docker run -p`:

- `5173:5173` — map host port 5173 to container port 5173 on all interfaces
- `127.0.0.1:8000:8000` — map host port 8000 to container port 8000 on loopback only
- `0:3000` — map an ephemeral host port to container port 3000

For dev images (`nalvin/dev`), SSH (22/tcp) and code-server (8080/tcp) are always auto-published on loopback with ephemeral host ports. User-specified ports are added alongside these.

If the agent needs a new port mid-session, the container must be stopped, removed, and recreated with the additional port. Since workspace files are volume-mounted, no filesystem state is lost.

## Background Process Management

The `docker_exec_background`, `docker_exec_tail`, `docker_exec_signal`, and `docker_exec_list_processes` tools enable agents to manage long-running processes inside containers — dev servers, watchers, build processes, and similar workloads.

### How it works

Background processes use the container as the process manager. No Go-side state is held:

1. `docker_exec_background` runs `docker exec` with a wrapper script that:
   - creates `/tmp/nalvin-procs/` inside the container
   - records the original command in `<process_id>.cmd`
   - backgrounds the command with stdout/stderr redirected to `<process_id>.log`
   - writes the PID to `<process_id>.pid`
   - returns immediately

2. `docker_exec_tail` runs `docker exec` to read the last N lines of the log file and check whether the PID is still alive, in a single call.

3. `docker_exec_signal` sends a signal to the tracked PID via `kill -<signal>`.

4. `docker_exec_list_processes` iterates `/tmp/nalvin-procs/*.pid`, checks liveness, and returns JSON.

All state lives inside the container under `/tmp/nalvin-procs/`. This means:
- background processes survive agent restarts and run resumptions
- process state is lost when the container is removed
- multiple agents or runs can manage processes in the same container

### Dev servers must bind to 0.0.0.0

Processes inside a container that listen on `127.0.0.1` or `localhost` are only reachable from inside the container. For a port published with `-p HOST:CONTAINER` to work, the server must bind to `0.0.0.0`.

Common examples:

```bash
# Vite
vite --host 0.0.0.0 --port 5173

# Python stdlib
python -m http.server 8000 --bind 0.0.0.0

# FastAPI / Uvicorn
uvicorn main:app --host 0.0.0.0 --port 8000

# Django
python manage.py runserver 0.0.0.0:8000

# Express / Node
# Set host in code: app.listen(3000, '0.0.0.0')
```

The `docker_exec_background` tool description includes this guidance so the model applies it automatically.

### Typical agent workflow

A full dev-server workflow driven by the agent looks like:

1. Create a container with the needed ports:
   ```
   docker_create_container(name="dev", mount_workspace=true, ports=["5173:5173", "8000:8000"])
   ```

2. Start the container:
   ```
   docker_start_container(container="dev")
   ```

3. Install dependencies:
   ```
   docker_exec_foreground(container="dev", command=["sh", "-c", "cd /workspace && pip install -r requirements.txt"])
   ```

4. Start the backend:
   ```
   docker_exec_background(container="dev", process_id="backend", command=["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"])
   ```

5. Start the frontend:
   ```
   docker_exec_background(container="dev", process_id="frontend", command=["sh", "-c", "cd /workspace/frontend && npx vite --host 0.0.0.0 --port 5173"])
   ```

6. Check startup:
   ```
   docker_exec_tail(container="dev", process_id="backend", lines=20)
   docker_exec_tail(container="dev", process_id="frontend", lines=20)
   ```

7. Edit source files (via workspace volume mount) and check for errors:
   ```
   docker_exec_tail(container="dev", process_id="backend", lines=10)
   ```

8. Restart a process:
   ```
   docker_exec_signal(container="dev", process_id="backend", signal="TERM")
   docker_exec_background(container="dev", process_id="backend", command=["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"])
   ```

9. List all managed processes:
   ```
   docker_exec_list_processes(container="dev")
   ```

## Direct Tool Runner Examples

```bash
nalvin --workspace alpha agent tools run search_tools --query docker
nalvin --workspace alpha agent tools run docker_build_image --context-path app --tag my-app:dev
nalvin --workspace alpha agent tools run docker_create_container --name ws-worker --mount-workspace --ports 5173:5173 --ports 8000:8000
nalvin --workspace alpha agent tools run docker_start_container --container ws-worker
nalvin --workspace alpha agent tools run docker_exec_foreground --container ws-worker --command sh --command -lc --command "cd /workspace && git status"
nalvin --workspace alpha agent tools run docker_exec_background --container ws-worker --process-id backend --command python --command -m --command uvicorn --command main:app --command --host --command 0.0.0.0 --command --port --command 8000
nalvin --workspace alpha agent tools run docker_exec_tail --container ws-worker --process-id backend --lines 20
nalvin --workspace alpha agent tools run docker_exec_signal --container ws-worker --process-id backend
```
