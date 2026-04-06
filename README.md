# nalvin

A fresh Go + web foundation for the next iteration of `nalvin`.

## Stack

- Go 1.26
- Cobra + Viper
- Chi HTTP server
- Bun + Vite + React 19 + TypeScript
- shadcn/ui + Tailwind CSS 4
- Zustand

## Layout

```text
.
├── docs/                Project documentation
├── cmd/                 Cobra commands
├── internal/            App internals (config, server, build info)
├── web/                 Bun/Vite frontend plus Go embed package
├── .air.toml            Go hot-reload config
├── main.go              CLI entrypoint
└── package.json         Root Bun scripts
```

## Getting Started

Install backend and frontend dependencies:

```bash
go mod tidy
bun install
```

For the Go hot-reload loop, install Air once:

```bash
go install github.com/air-verse/air@latest
```

## Development

Start the API server with live reload:

```bash
bun run dev:api
```

Start the API server once without Air:

```bash
bun run run:api
```

Start the web app:

```bash
bun run dev:web
```

Or run both together:

```bash
bun run dev
```

Run CLI commands:

```bash
bun run cli -- kb nodes list --json
bun run cli -- jobs enqueue hello --message "hi"
```

The Go server binds to `0.0.0.0:4210` by default. The Vite dev server binds to `0.0.0.0:4211` and proxies `/api` requests to Go, so you can connect via your machine hostname such as `http://<hostname>:4210`.

## SQLite Features

The repo ships a local `go-sqlite3` replacement with FTS5 enabled by default, so plain `go test ./...`, `go run . serve`, and `go build` all use the same SQLite feature set without extra build tags.

## Configuration

The CLI loads configuration in this order:

1. baked defaults
2. optional `.env`
3. optional `~/.nalvin/config.yaml`
4. `NALVIN_` environment variables
5. CLI flags

Primary config keys:

- `providers.default.base_url`
- `providers.default.api_key`
- `providers.default.model`
- `docker.host`
- `docker.binary`
- `docker.default_image`
- `git.repo_root`
- `git.ssh.enabled`
- `git.ssh.addr`
- `git.ssh.host_key_path`
- `git.default_client.user`
- `git.default_client.private_key_path`
- `agent.subagents.provider_name`
- `app.env`
- `server.addr`
- `server.allowed_origins`
- `workspace.db_root`
- `workspace.files_root`
- `workspace.current`
- `work.agent_job_timeout`

Workspace state lives under `~/.nalvin` by default:

- SQLite DBs: `~/.nalvin/workspace-db/<name>.sqlite`
- Workspace files: `~/.nalvin/workspaces/<name>/`
- Managed bare Git repos: `~/.nalvin/git-repos/<name>.git`
- Git SSH host and client keys: `~/.nalvin/git/`

Useful workspace commands:

```bash
bun run cli -- workspace list
bun run cli -- workspace create alpha
bun run cli -- workspace switch alpha
bun run cli -- --workspace alpha workspace put ./notes.md docs/notes.md
bun run cli -- --workspace alpha workspace get docs/notes.md ./notes-copy.md
bun run cli -- --workspace alpha kb nodes list --json
```

## Embedded Git

`nalvin` can run an embedded Git SSH server alongside the normal HTTP server. By default:

- HTTP listens on `0.0.0.0:4210`
- Git SSH listens on `127.0.0.1:4222`
- managed bare repos live under `~/.nalvin/git-repos`
- a local default Git client identity uses the `nalvin` SSH user and `~/.nalvin/git/id_nalvin`

Start the server:

```bash
bun run cli -- serve
```

You should see logs similar to:

```text
time=... level=INFO msg="git ssh service listening" addr=127.0.0.1:4222 repo_root=/Users/you/.nalvin/git-repos
time=... level=INFO msg="server listening" addr=0.0.0.0:4210 workspace=default
```

Manage global bare repos and workspace checkouts with the CLI:

```bash
bun run cli -- git repo list
bun run cli -- git repo create demo
bun run cli -- git repo tree demo
bun run cli -- git repo commits demo
bun run cli -- --workspace alpha git checkout demo
bun run cli -- --workspace alpha git status --repo-path demo
bun run cli -- --workspace alpha git add --repo-path demo hello.txt
bun run cli -- --workspace alpha git commit --repo-path demo -m "initial commit"
bun run cli -- --workspace alpha git push --repo-path demo
```

Agent runs can also discover and use the hidden Git tool family via `search_tools`:

- `git`
- `git_list_repos`
- `git_create_repo`
- `git_delete_repo`
- `git_repo_tree`
- `git_repo_commits`

See the dedicated Git doc for server behavior, config, and tool examples.

## Docker Client

`nalvin` also exposes a Docker client surface for local container workflows. By default:

- it uses the local Docker client's current-context behavior unless `docker.host` or `--host` overrides it
- new containers default to image `nalvin/dev`
- created containers automatically receive nalvin-managed Git credentials
- created containers automatically get Docker-aware access to loopback-hosted nalvin Git remotes, so mounted repos can push without manual remote rewriting
- `--mount-workspace` mounts the active workspace root at `/workspace`
- `--ports` publishes container ports to the host (ports cannot be added after creation)

Useful CLI examples:

```bash
bun run cli -- docker images list
bun run cli -- docker build app --tag my-app:dev
bun run cli -- docker create --name ws-worker --mount-workspace --image my-app:dev --ports 5173:5173 --ports 8000:8000
bun run cli -- docker start ws-worker
bun run cli -- docker exec ws-worker -- sh -lc "cd /workspace && git status"
bun run cli -- docker exec ws-worker -- sh -lc "cd /workspace/repo && git pull && git push"
```

Agent runs can discover the hidden Docker tool family through `search_tools`:

- `docker` — scoped Docker CLI
- `docker_list_containers`, `docker_list_images`, `docker_list_networks`, `docker_list_volumes` — resource listing
- `docker_pull_image`, `docker_build_image` — image management
- `docker_create_container` — create with workspace mounts, ports, volumes, env, and Git credentials
- `docker_start_container`, `docker_stop_container`, `docker_remove_container` — container lifecycle
- `docker_exec_foreground` — run a command and wait for output
- `docker_exec_background` — start a long-running process (dev server, watcher) and return immediately
- `docker_exec_tail` — read recent output from a background process
- `docker_exec_signal` — send TERM/KILL to a background process
- `docker_exec_list_processes` — list managed background processes

Background process state is tracked inside the container under `/tmp/nalvin-procs/` using PID files and log files. Dev servers must bind to `0.0.0.0` (not localhost) for published ports to be reachable from the host.

See [Docker client and tools](docs/docker-client-and-tools.md) for the full reference.

Basic remote agent run:

```yaml
# ~/.nalvin/config.yaml
providers:
  default:
    type: openai
    api_key: ${OPENAI_API_KEY}
    model: gpt-5.4-mini

agent:
  subagents:
    provider_name: kimi
```

Provider management helpers:

```bash
bun run cli -- provider list
bun run cli -- provider add lmstudio --base-url http://localhost:1234/v1 --model local-model
bun run cli -- provider add openai --type openai --model gpt-5.4
```

```bash
bun run cli -- agent run -p "Write a haiku about SQLite"
bun run cli -- agent run --provider lmstudio -p "Say hello in one sentence."
bun run cli -- agent run -p "Summarize this repo" --system "Be concise and technical."
bun run cli -- agent run --queue --timing -p "Reply with exactly TTFT_OK and nothing else."
bun run cli -- agent run --timing -p "Reply with exactly TTFT_OK and nothing else."
bash ./scripts/benchmark-agent-run-ttft.sh --iterations 4 --warmups 1
```

## Plan Mode

`nalvin` supports a dedicated planning workflow for agent runs. Start a plan-only run with:

```bash
bun run cli -- --workspace alpha agent run --mode plan -p "Plan the release automation cleanup."
```

A plan run creates a canonical `.plans/<timestamp>-<slug>.md` file in the active workspace, pins the direct file-editing tools plus `plan_exit`, and instructs the agent to investigate and refine the plan without implementing the requested repo changes yet.

When the plan is ready, the agent calls `plan_exit`. Attached and queued CLI flows can then prompt to start a fresh implementation run from the approved plan. The implementation handoff also carries the canonical plan path and plan body forward, and the current branch adds target-directory inference to avoid accidentally applying nested-repo plans at the workspace root.

Use `--resume <run-id>` to continue an existing plan run. If the stored run is already in plan mode, resuming it without `--mode` keeps the planning workflow active.

## Docs

- [Agent plan mode](docs/agent-plan-mode.md)
- [Agent tools](docs/agent-tools.md)
- [Git server and tools](docs/git-server-and-tools.md)
- [Docker client and tools](docs/docker-client-and-tools.md)

## Build

Build the frontend bundle:

```bash
bun run build:web
```

Then rebuild the Go binary so it embeds the generated `web/dist` assets:

```bash
bun run build:api
```

## Verification

```bash
bun run test:go
bun run typecheck:web
bun run build:web
```
