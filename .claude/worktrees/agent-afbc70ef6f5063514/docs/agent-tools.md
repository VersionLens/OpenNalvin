# Agent Tools

`nalvin` has a run-scoped tool system for agent runs. A run can expose internal Go tools, MCP-backed tools, custom Scriggo-compiled tools, or any combination, and each run can override which tools are enabled, pinned, hidden, or revealed.

Tool-result spillover and pagination are documented separately in [Agent Tool Output Spillover](./agent-tool-output-spillover.md).

Custom tools are documented separately in [Agent Custom Tools](./agent-custom-tools.md).

## Concepts

- `enabled`: the tool exists for the run and can be used.
- `pinned`: the tool is immediately visible to the model at the start of the run.
- `hidden`: the tool is enabled but not initially visible to the model.
- `revealed`: the tool was hidden at first and became visible after `search_tools`.

If a run has hidden enabled tools, `search_tools` is automatically pinned so the model can discover them. It uses an in-memory SQLite FTS5 index over tool ids, descriptions, keywords, server names, and schema/property terms, so both concise keywords and broader natural-language searches can work.

Good starter queries:

- `fetch`, `web fetch`, `http request`, `download url`, `article`, `read page`, `reader mode` -> `web_fetch_get`
- `git`, `git status`, `git diff`, `git add`, `git commit`, `git push`, `git checkout`, `git clone` -> `git`
- `list repos`, `git repos`, `available repos`, `clone targets` -> `git_list_repos`
- `create git repo`, `new repo`, `initialize managed repo` -> `git_create_repo`
- `delete git repo`, `remove repo`, `destroy repo` -> `git_delete_repo`
- `repo tree`, `git tree`, `repo contents` -> `git_repo_tree`
- `commit history`, `repo commits`, `git log for repo` -> `git_repo_commits`
- `docker`, `docker ps`, `docker images`, `docker build`, `docker create`, `docker exec` -> `docker`
- `list containers`, `docker containers`, `docker ps` -> `docker_list_containers`
- `list images`, `docker images`, `image list` -> `docker_list_images`
- `list networks`, `docker networks`, `network list` -> `docker_list_networks`
- `list volumes`, `docker volumes`, `volume list` -> `docker_list_volumes`
- `pull image`, `docker pull`, `download image` -> `docker_pull_image`
- `build image`, `docker build`, `dockerfile build` -> `docker_build_image`
- `create container`, `docker create`, `mount workspace` -> `docker_create_container`
- `start container`, `docker start` -> `docker_start_container`
- `stop container`, `docker stop` -> `docker_stop_container`
- `remove container`, `docker rm` -> `docker_remove_container`
- `docker exec`, `exec container`, `run command in container` -> `docker_exec_foreground`
- `background process`, `start server`, `dev server` -> `docker_exec_background`
- `tail output`, `process logs`, `read output` -> `docker_exec_tail`
- `kill process`, `stop process`, `signal process` -> `docker_exec_signal`
- `list processes`, `running processes`, `background processes` -> `docker_exec_list_processes`
- `read file`, `view file`, `open file` -> `view`
- `list files`, `directory tree`, `browse workspace` -> `ls`
- `glob files`, `find files`, `file pattern` -> `glob`
- `search text`, `find in files`, `grep files` -> `grep`
- `write file`, `create file`, `replace file` -> `write`
- `edit file`, `replace text`, `modify file` -> `edit`
- `node`, `knowledge base`, `create note` -> KB node tools
- `edge`, `relation`, `connect nodes` -> KB edge tools

If a broad query still returns no matches, retry with a concise 1-3 word query.

`search_tools` ranks results instead of doing a literal full-query substring match. It first tries a stricter AND-style FTS query, then falls back to a looser OR-style query if nothing matches, and filters out weak one-token overlaps before revealing tools. In practice this means descriptions and keywords now matter, not just the tool ID.

When you author or configure tools, good descriptions help discoverability. A description like "Fetch a URL over HTTP and return the response body" is easier to find from queries such as `http request` or `download url` than a vague description like "Gets data".

## Built-In Tools

Control and discovery:

- `search_tools`
- `spawn_agent`
- `send_input`
- `wait_agent`
- `close_agent`

General utility:

- `web_fetch_get`
- `git`
- `git_list_repos`
- `git_create_repo`
- `git_delete_repo`
- `git_repo_tree`
- `git_repo_commits`
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
- `news_rss_headlines`
- `news_reddit_top_posts`
- `news_reddit_post_details`
- `view`
- `ls`
- `glob`
- `grep`
- `view_tool_output`
- `grep_tool_output`
- `write`
- `edit`
- `multiedit`

Knowledge base tools:

- `kb_list_nodes`
- `kb_get_node`
- `kb_create_node`
- `kb_update_node`
- `kb_delete_node`
- `kb_list_edges`
- `kb_get_edge`
- `kb_create_edge`
- `kb_update_edge`
- `kb_delete_edge`

By default, the runtime enables:

- multi-agent control tools
- `search_tools`
- `web_fetch_get`
- hidden Git tools for client Git operations and managed repo administration
- hidden news tools for RSS feeds and Reddit
- workspace file tools
- KB read/list tools
- the bundled Exa MCP server, which currently exposes `mcp__exa__web_search_exa` when reachable
- the bundled DeepWiki MCP server, available for discovery through `search_tools`

The bundled default config also pins `mcp__exa__web_search_exa`, so the model can use Exa web search immediately without first revealing it through `search_tools`.
The bundled default config enables DeepWiki too, but leaves its tools unpinned by default.

KB write/delete tools are registered but disabled by default unless config or per-run overrides enable them.
Workspace file tools are enabled but not pinned by default, so agents discover them through `search_tools` and every file path stays rooted at the active workspace directory under `~/.nalvin/workspaces/<workspace>/`.

`view_tool_output` and `grep_tool_output` are special internal tools used for spilled tool results. They are enabled for runs but stay hidden until the runtime spills a large text result and reveals them.

## Git Tools

Six Git tools are built in and enabled by default, but they start hidden until the model reveals them through `search_tools`:

- `git` runs a scoped Git CLI against the active workspace or the managed bare-repo root using a single raw `args` string
- `git_list_repos` lists managed Git repos on the nalvin Git server and returns clone-ready SSH URLs
- `git_create_repo` creates a managed Git repo and returns its SSH URL
- `git_delete_repo` force-deletes a managed Git repo by name
- `git_repo_tree` returns the latest committed file tree for a managed repo branch and filters out paths matched by the branch's committed `.gitignore` files
- `git_repo_commits` returns commit history for a managed repo branch

Good discovery queries:

- `git`, `git status`, `git diff`, `git add`, `git commit`, `git push`, `git checkout`, `git clone`
- `git repos`, `list repos`, `available repos`, `clone targets`
- `create git repo`, `new repo`, `initialize managed repo`
- `delete git repo`, `remove repo`, `destroy repo`
- `repo tree`, `git tree`, `repo contents`
- `commit history`, `repo commits`, `git log for repo`

The main `git` tool is intentionally client-only. It accepts the arguments that would normally come after the `git` executable, tokenizes them with shell-style quoting, rejects shell operators and unsupported global options, and returns a structured result containing `ok`, `exit_code`, `stdout`, `stderr`, `argv`, and `cwd`.

Managed repo operations that are not part of normal Git CLI behavior live in the helper tools instead of `git` itself:

- use `git_list_repos` to discover available managed repos and their SSH URLs
- use `git_create_repo` to create a new managed bare repo
- use `git_delete_repo` to remove a managed bare repo
- use `git_repo_tree` to inspect the latest committed file tree for a managed repo branch
- use `git_repo_commits` to inspect commit history for a managed repo branch

## Docker Tools

Sixteen Docker tools are built in and enabled by default, but they start hidden until the model reveals them through `search_tools`:

- `docker` runs a scoped Docker CLI with raw CLI-style args for `ps`, `images`, `network ls`, `volume ls`, `pull`, `build`, `create`, `start`, `stop`, `rm`, and `exec`
- `docker_list_containers` lists Docker containers, including stopped containers, and reports detected published SSH/code-server endpoints
- `docker_list_images` lists Docker images
- `docker_list_networks` lists Docker networks
- `docker_list_volumes` lists Docker volumes
- `docker_pull_image` pulls a Docker image by reference
- `docker_build_image` builds a Docker image from a workspace-relative build context
- `docker_create_container` creates a Docker container with optional workspace mounts, named volumes, env vars, published ports, and default Git credential injection
- `docker_start_container` starts a Docker container
- `docker_stop_container` stops a Docker container
- `docker_remove_container` removes a Docker container
- `docker_exec_foreground` runs a command inside a running Docker container, waits for completion, and returns `ok`, `exit_code`, `stdout`, `stderr`, `argv`, and container metadata
- `docker_exec_background` starts a long-running command (e.g. dev server) in the background and returns a `process_id` for later monitoring
- `docker_exec_tail` reads recent output from a background process and reports whether it is still running
- `docker_exec_signal` sends a signal (default TERM) to a background process
- `docker_exec_list_processes` lists all tracked background processes in a container with PID, running status, and original command

Good discovery queries:

- `docker`, `docker ps`, `docker images`, `docker build`, `docker create`, `docker exec`
- `list containers`, `docker containers`, `container list`
- `list images`, `image list`, `available images`
- `list networks`, `network list`
- `list volumes`, `volume list`
- `pull image`, `docker pull`, `download image`
- `build image`, `docker build`, `dockerfile build`
- `create container`, `mount workspace`, `workspace mount`
- `start container`, `stop container`, `remove container`
- `background process`, `dev server`, `start server`
- `tail output`, `process logs`, `check server`
- `kill process`, `stop process`, `signal process`
- `list processes`, `running processes`

The main `docker` tool is intentionally scoped:

- it accepts shell-style quoting, but rejects shell operators and shell substitution
- it only allows `ps`, `images`, `network ls`, `volume ls`, `pull`, `build`, `create`, `start`, `stop`, `rm`, and `exec`
- local build contexts, Dockerfiles, and bind-mount source paths stay under the active workspace root

Use the helper tools when you want structured behavior:

- `docker_build_image` requires a workspace-relative `context_path`
- `docker_create_container` supports `mount_workspace` to bind the active workspace root at `/workspace`
- `docker_create_container` supports `ports` to publish container ports to the host (e.g. `["5173:5173", "8000:8000"]`); ports cannot be added after creation
- created containers default to image `nalvin/dev` unless overridden
- created containers automatically receive nalvin-managed Git SSH credentials so in-container Git can pull/push against nalvin-managed repos
- when the effective image is `nalvin/dev`, created containers also auto-publish loopback-only SSH and code-server ports plus persist `~/.claude` and `~/.codex`
- when nalvin Git uses a loopback host such as `127.0.0.1`, created containers also receive a Docker host alias plus Git URL rewriting so existing remotes keep working unchanged inside Docker

Background process tools (`docker_exec_background`, `docker_exec_tail`, `docker_exec_signal`, `docker_exec_list_processes`) enable agents to manage long-running processes like dev servers inside containers. Process state is tracked in `/tmp/nalvin-procs/` inside the container. Dev servers must bind to `0.0.0.0` for published ports to be reachable from the host. See [Docker client and tools](./docker-client-and-tools.md) for the full background process reference and workflow examples.

## News Tools

Three news tools are built in and enabled by default, but they start hidden until the model reveals them through `search_tools`:

- `news_rss_headlines` fetches headlines from configured RSS feeds by category or explicit feed names
- `news_reddit_top_posts` fetches top posts from configured subreddit groups or explicit subreddit names
- `news_reddit_post_details` fetches a Reddit post body, linked URL, metadata, and nested comments from a permalink or Reddit URL

Good discovery queries:

- `rss`, `rss headlines`, `feed`, `news feed`
- `reddit`, `subreddit`, `top posts`
- `reddit comments`, `permalink`, `post body`

## `web_fetch_get`

`web_fetch_get` is a single fetch tool with reader-mode defaults:

- HTML pages return readable article text in `body` by default, with `mode: "reader"` plus `title` and `excerpt` when extraction succeeds.
- Use `extract_article_content: false` to opt out of reader mode and return raw HTML instead.
- `selector` is only valid in raw HTML mode and returns the joined `outerHTML` of matching elements.
- JSON responses keep the old behavior, including `body_json` when the response body is valid JSON.
- Binary responses return a base64 preview in `body` and set `encoding: "base64"`.
- HTML, raw-text, and base64 preview bodies are capped at 5,000 tokens so the fetch tool stays usable with smaller-context models.

Reader-mode extraction is intentionally the default because it produces much smaller, more model-friendly outputs than raw HTML. If extraction fails or returns nothing useful, retry with raw mode or use `mcp__exa__crawling_exa`.

## Exa Web Search

The bundled default config includes the Exa MCP server:

- server URL: `https://mcp.exa.ai/mcp`
- auth: no API key required
- enabled by default: yes
- pinned by default in resolved runs: `mcp__exa__web_search_exa`

On March 29, 2026, the live Exa endpoint exposed these MCP tools:

- `mcp__exa__web_search_exa`
- `mcp__exa__crawling_exa`
- `mcp__exa__get_code_context_exa`

For normal agent runs, the important one is `mcp__exa__web_search_exa`. Its schema expects a natural-language `query` and supports optional fields such as `numResults`, `freshness`, `includeDomains`, and `type`.

Verified smoke prompt:

```text
Use the pinned web search tool to search the web for 'example domain RFC 2606'. Do not use web_fetch_get unless search fails. Finish with a compact JSON object containing search_tool_used, first_title, and first_url.
```

That smoke run used `mcp__exa__web_search_exa` directly and returned `https://www.iana.org/help/example-domains` as the first result.

## DeepWiki

The bundled default config also includes the DeepWiki MCP server:

- server URL: `https://mcp.deepwiki.com/mcp`
- auth: no API key required
- enabled by default: yes
- pinned by default in resolved runs: no

## GitHub

The bundled default config includes the GitHub MCP server:

- server URL: `https://api.githubcopilot.com/mcp/`
- auth: `Authorization: Bearer <PAT>` header (GitHub Personal Access Token)
- enabled by default: no (requires a PAT)
- pinned by default in resolved runs: no

GitHub's MCP server does not support dynamic client registration, so OAuth requires a pre-registered GitHub OAuth app. The simplest approach is to use a Personal Access Token:

```bash
nalvin mcp add github --url https://api.githubcopilot.com/mcp/ \
  --header "Authorization=Bearer ghp_xxx" --enabled-by-default --overwrite
```

Create a PAT at https://github.com/settings/tokens with the scopes you want to grant to the MCP server.

## Linear

The bundled default config includes the Linear MCP server:

- server URL: `https://mcp.linear.app/mcp`
- auth: OAuth 2.1 with dynamic client registration
- enabled by default: no (requires authentication)
- pinned by default in resolved runs: no

Authenticate with: `nalvin mcp auth linear`

Also supports `Authorization: Bearer <API key>` header for API key access.

## Sentry

The bundled default config includes the Sentry MCP server:

- server URL: `https://mcp.sentry.dev/mcp`
- auth: OAuth 2.1 with dynamic client registration
- enabled by default: no (requires authentication)
- pinned by default in resolved runs: no

Authenticate with: `nalvin mcp auth sentry`

## Slack RTS Search

`slack_search` is a built-in internal tool that uses the Slack Real-Time Search API (`assistant.search.context`):

- auth: user token (`xoxp-`) configured in `slack.user_token`
- enabled by default: yes (hidden, discoverable via `search_tools`)
- pinned by default: no

Required Slack App scopes: `search:read.public`, `search:read.private`, `search:read.im`, `search:read.mpim`, `search:read.files`, `search:read.users`

Configure in `~/.nalvin/config.yaml`:
```yaml
slack:
  user_token: "xoxp-..."
```

Good discovery queries: `slack`, `slack search`, `slack messages`, `workspace messages`

The tool supports semantic search (natural language questions) and keyword search. Results include author, channel, content, permalink, and timestamp.

## MCP Server Management

Use `nalvin mcp` to manage MCP servers from the CLI:

```bash
# List all configured MCP servers and their auth status
nalvin mcp list

# Add a new MCP server with OAuth
nalvin mcp add myserver --url https://mcp.example.com/mcp \
  --oauth-client-id my-client-id --oauth-callback-port 9876

# Add a server with API key header (no OAuth)
nalvin mcp add myserver --url https://mcp.example.com/mcp \
  --header "Authorization=Bearer sk-xxx"

# Add a stdio server
nalvin mcp add localserver --transport stdio \
  --command uvx --args my-mcp-server

# Run interactive OAuth browser flow
nalvin mcp auth github

# Test connectivity and list available tools
nalvin mcp test github

# Remove a server
nalvin mcp remove myserver
```

OAuth tokens are cached in `~/.nalvin/oauth-tokens/<server-name>.json`. Agent runs use cached tokens and never trigger interactive browser flows. If a token expires and cannot be refreshed, the agent run will warn that re-authentication is needed.

## Custom Tools

Place `.go` files in `~/.nalvin/custom-tools/` to add tools without modifying `nalvin`. Each file is compiled by the embedded Scriggo Go interpreter at runtime and registered alongside internal and MCP tools with source `custom`.

See [Agent Custom Tools](./agent-custom-tools.md) for the full authoring guide, available stdlib packages, shell integration, and configuration options.

## Tool Configuration

Add default tool policy, optional MCP servers, and custom tool settings in `~/.nalvin/config.yaml`:

```yaml
providers:
  default:
    model: some-model
    tool_output_token_limit: 4000

agent:
  subagents:
    provider_name: kimi

  tool_output:
    max_stored_bytes: 4194304
    default_page_lines: 200
    grep_default_limit: 100

  tools:
    default_enabled:
      - kb_create_node
    default_disabled:
      - web_fetch_get
    default_pinned:
      - kb_get_node
      - mcp__exa__web_search_exa
    metadata:
      web_fetch_get:
        keywords:
          - http request
          - download url
      mcp__exa__web_search_exa:
        keywords:
          - web search
          - search web
          - internet search

  custom_tools:
    dir: ~/.nalvin/custom-tools
    enabled: true
    timeout_seconds: 30

  mcp_servers:
    deepwiki:
      transport: streamable_http
      url: https://mcp.deepwiki.com/mcp
      enabled_by_default: true

    exa:
      transport: streamable_http
      url: https://mcp.exa.ai/mcp
      enabled_by_default: true

    local_docs:
      transport: stdio
      command: uvx
      args:
        - my-mcp-server
      enabled_by_default: true

    remote_api:
      transport: streamable_http
      url: https://mcp.example.com
      headers:
        Authorization: Bearer REPLACE_ME
      timeout_ms: 15000
      enabled_by_default: false

    github:
      transport: streamable_http
      url: https://api.githubcopilot.com/mcp/
      headers:
        Authorization: "Bearer ghp_YOUR_PAT_HERE"
      enabled_by_default: false

```

Notes:

- `providers.<name>.tool_output_token_limit` controls how large a text tool result can be before the runtime spills it to the run database and replaces it with a compact inline summary.
- `agent.tool_output.*` configures spill storage size and default pagination/search behavior for `view_tool_output` and `grep_tool_output`.
- MCP tool IDs are exposed as `mcp__<encoded_server_name>__<encoded_tool_name>`.
- Safe segments keep their original letters, digits, hyphens, and single underscores.
- Segments containing unsupported characters, or `__`, are encoded as `x` followed by lowercase hex bytes.
- The bundled default config includes DeepWiki at `https://mcp.deepwiki.com/mcp`; it does not require an auth key and its tools start unpinned.
- The bundled default config includes Exa at `https://mcp.exa.ai/mcp`; it does not require an auth key.
- On March 29, 2026, the live Exa endpoint exposed `web_search_exa`, so the bundled default pin uses `mcp__exa__web_search_exa`.
- `agent.tools.metadata.<tool_id>.keywords` appends search aliases for the exact tool id; it does not replace built-in keywords.
- Internal tools can ship with built-in keywords, and MCP tools can pick up keywords from config even if they do not define any by default.
- v1 supports `stdio` and `streamable_http`.
- `nalvin` creates fresh MCP client sessions per run. Live MCP sessions are not shared across concurrent runs.
- MCP servers with `oauth:` config use OAuth 2.1 with PKCE. Run `nalvin mcp auth <name>` to authenticate interactively. Tokens are cached in `~/.nalvin/oauth-tokens/`. Servers that support dynamic client registration (Linear, Sentry) do not require a `client_id`.
- The bundled default config includes GitHub, Linear, and Sentry MCP servers, all disabled by default. Enable them with `nalvin mcp auth <name>` then set `enabled_by_default: true` in config or use `--enable-tool` per run.
- Slack is available as a native internal tool (`slack_search`) using the RTS API, not as an MCP server. Configure `slack.user_token` in config.

## Agent Run Flags

Use per-run flags to change tool state without editing config:

```bash
bun run cli -- --workspace alpha agent run \
  --prompt "Summarize the workspace and fetch https://example.com" \
  --queue \
  --enable-tool web_fetch_get \
  --pin-tool web_fetch_get \
  --disable-tool kb_delete_node \
  --unpin-tool spawn_agent \
  --timing \
  --verbose
```

Run flags:

- `--enable-tool <id>`
- `--disable-tool <id>`
- `--pin-tool <id>`
- `--unpin-tool <id>`
- `--queue`
- `--timing`
- `--verbose`

`--queue` submits the run through the River agent-worker queue and waits for stored run events, which is useful for measuring worker dispatch and queue latency. If no recently active external agent worker is detected, the CLI starts a temporary in-process agent worker and reuses it for the enqueue so queued runs do not sit behind the 1 second poll interval by default.

`--timing` writes a TTFT-oriented timing summary to stderr, including:

- local request to turn creation
- local queue enqueue and mark-queued steps when `--queue` is used
- server queue wait
- server started to first meaningful streamed event
- server and local first-content TTFT

`--verbose` writes debug logs to stderr for:

- resolved tool state
- visible tools per step
- tool calls and results
- sub-agent lifecycle events

## Listing Tools

List the current catalog for a workspace:

```bash
bun run cli -- --workspace alpha agent tools list
```

Inspect the resolved state for an existing run:

```bash
bun run cli -- --workspace alpha agent tools list --run-id <run-id>
```

JSON output is supported:

```bash
bun run cli -- --workspace alpha agent tools list --json
```

Each entry includes:

- tool ID
- description
- keywords
- source (`internal`, `mcp`, or `custom`)
- enabled/pinned/visible flags
- default-enabled/default-pinned flags
- JSON schema for direct execution

Those `keywords` are the merged search aliases the runtime will use for `search_tools`: built-in keywords first, then any config-appended aliases for that exact tool ID.

The `enabled` and `pinned` fields are the resolved state for the current run or workspace. `default_enabled` and `default_pinned` describe the tool's built-in registration defaults before config and per-run overrides are applied.

## Running Tools Directly

Use the direct tool runner to smoke test a tool without going through the model:

```bash
bun run cli -- --workspace alpha agent tools run web_fetch_get --url https://example.com/article
bun run cli -- --workspace alpha agent tools run web_fetch_get \
  --url https://www.rfc-editor.org/rfc/rfc2616.txt \
  --save-to-filepath downloads/rfc2616.txt
bun run cli -- --workspace alpha agent tools run web_fetch_get \
  --json-args '{"url":"https://example.com/page","extract_article_content":false,"selector":"article p"}'
bun run cli -- --workspace alpha agent tools run news_rss_headlines --category ai --limit 5
bun run cli -- --workspace alpha agent tools run news_rss_headlines \
  --feed-names openai-blog --feed-names huggingface-blog
bun run cli -- --workspace alpha agent tools run news_reddit_top_posts --category world --time-filter day --limit 5
bun run cli -- --workspace alpha agent tools run news_reddit_post_details \
  --url-or-permalink https://www.reddit.com/r/worldnews/comments/example/story/
```

Top-level scalar arguments map to flags:

```bash
bun run cli -- --workspace alpha agent tools run kb_create_node \
  --kind Note \
  --name "Docs Smoke Node" \
  --content "created from the direct tool runner"
```

Arrays are repeatable flags:

```bash
bun run cli -- --workspace alpha agent tools run some_tool \
  --tag alpha \
  --tag beta
```

Nested objects or complex arrays should go through `--json-args`:

```bash
bun run cli -- --workspace alpha agent tools run some_tool \
  --json-args '{"filter":{"kind":"Note"},"limit":5}'
```

Workspace file tools use workspace-relative paths:

```bash
bun run cli -- --workspace alpha agent tools run view --path docs/readme.md
bun run cli -- --workspace alpha agent tools run grep --pattern "TODO" --include "*.md"
bun run cli -- --workspace alpha agent tools run write --path scratch/note.txt --content "hello"
```

Git tool family examples:

```bash
bun run cli -- --workspace alpha agent tools run search_tools --query git
bun run cli -- --workspace alpha agent tools run git_create_repo --name demo
bun run cli -- --workspace alpha agent tools run git_list_repos
bun run cli -- --workspace alpha agent tools run git_repo_tree --name demo
bun run cli -- --workspace alpha agent tools run git_repo_commits --name demo
bun run cli -- --workspace alpha agent tools run git \
  --args "clone ssh://nalvin@127.0.0.1:4222/demo.git demo"
bun run cli -- --workspace alpha agent tools run git --args "-C demo status --short"
bun run cli -- --workspace alpha agent tools run git_delete_repo --name demo
```

You can also target the tool state of an existing run:

```bash
bun run cli -- --workspace alpha agent tools run --run-id <run-id> search_tools --query fetch
bun run cli -- --workspace alpha agent tools run --run-id <run-id> search_tools --query "http request"
bun run cli -- --workspace alpha agent tools run --run-id <run-id> search_tools --query "rss headlines"
bun run cli -- --workspace alpha agent tools run --run-id <run-id> search_tools --query "reddit comments"
bun run cli -- --workspace alpha agent tools run --run-id <run-id> search_tools --query "create note"
bun run cli -- --workspace alpha agent tools run --run-id <run-id> search_tools --query "knowledge base fetch node"
```

The direct runner uses the same internal and MCP implementations as normal runs. It is intended for smoke tests and debugging, so it can also execute tools that are disabled by default as long as the tool exists and its backing configuration is valid.

## Workspace File Transfers

Move files across the workspace boundary with FTP-style verbs:

```bash
bun run cli -- --workspace alpha workspace put ./notes/today.md docs/today.md
bun run cli -- --workspace alpha workspace get docs/today.md ./today-copy.md
```

Both commands accept files or directories, default the destination to the source basename when omitted, and require `--overwrite` if the destination already exists.

## Sub-Agents

Agents can spawn fresh-context child agents with their own tool selection. A child run:

- starts with a new conversation history
- keeps its own enabled and pinned tool state
- inherits the parent provider by default, or uses `agent.subagents.provider_name` when configured
- runs in a background goroutine
- is persisted as its own linked run record
- cannot spawn or manage descendant child agents

The parent communicates with children through:

- `spawn_agent`
- `send_input`
- `wait_agent`
- `get_agent_result`
- `close_agent`

Example prompt:

```text
Use spawn_agent to start a child named smoke_child with enabled_tool_ids ["web_fetch_get"] and pinned_tool_ids ["web_fetch_get"]. Ask it to fetch https://example.com and return JSON. Then call wait_agent on smoke_child, call get_agent_result on smoke_child, and summarize the returned latest assistant output.
```

## Web And API

The web agent chat UI uses the same runtime and can toggle tool state per run.

Relevant endpoints:

- `GET /api/agent-tools`
- `GET /api/agent-tools?run_id=<run-id>`

- `enabled_tool_ids`
- `pinned_tool_ids`

The API response for tool listing includes the same metadata the CLI uses to render tool toggles and debugging views.
