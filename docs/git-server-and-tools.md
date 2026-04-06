# Git Server And Tools

`nalvin` includes an embedded Git SSH server, a user-facing `nalvin git` CLI, and a hidden-by-default Git tool family for agent runs.

At a high level:

- `nalvin serve` can start both the HTTP API and a local Git SSH service
- managed bare repos live in a global repo root, not inside a workspace
- checked-out worktrees live inside the active workspace
- agents can discover and use `git`, `git_list_repos`, `git_create_repo`, `git_delete_repo`, `git_repo_tree`, and `git_repo_commits` through `search_tools`

## Embedded Git Server

The Git server is integrated into `nalvin serve`. When Git SSH is enabled, starting the server also starts an in-process SSH daemon that accepts Git clone/fetch/push traffic.

Typical startup log lines look like:

```text
time=... level=INFO msg="git ssh service listening" addr=127.0.0.1:4222 repo_root=/Users/you/.nalvin/git-repos
time=... level=INFO msg="server listening" addr=0.0.0.0:4210 workspace=default
```

By default:

- Git SSH listen address: `127.0.0.1:4222`
- Git repo root: `~/.nalvin/git-repos`
- SSH host key path: `~/.nalvin/git/ssh_host_ed25519`

Managed repos are bare repos under the global repo root:

- `~/.nalvin/git-repos/demo.git`
- `~/.nalvin/git-repos/team/api.git`

Worktrees are separate and stay under the selected workspace files root:

- `~/.nalvin/workspaces/default/demo`
- `~/.nalvin/workspaces/alpha/team/api`

That split is intentional:

- repo administration is global to the app
- checkouts are workspace-local

## Default Git Identity

The baked config defaults provision a local Git client identity for the embedded server:

- SSH user: `nalvin`
- private key: `~/.nalvin/git/id_nalvin`
- public key: `~/.nalvin/git/id_nalvin.pub`
- default author name: `nalvin`
- default author email: `nalvin@nalvin.local`

On first use, `nalvin` generates the client keypair if it does not exist and registers that public key for the default local Git user. This is what lets fresh installs clone and push against the embedded SSH server without manual SSH setup.

If you change Git user or key settings, restart `nalvin serve` so the SSH server reloads authorized keys.

## Docker Interop

Host-side checkouts still use the configured nalvin Git SSH URL, which by default is loopback-based:

- `ssh://nalvin@127.0.0.1:4222/demo.git`

When that repo is later mounted into a container created through `nalvin docker create` or `docker_create_container`, `nalvin` makes the same remote usable inside Docker by default:

- the container gets the nalvin Git private key and a mounted `known_hosts`
- the mounted `known_hosts` trusts both the configured host and `host.docker.internal`
- Docker injects a host alias so the container can reach the host machine
- Docker injects Git URL rewriting so loopback remotes continue to work unchanged from inside the container

That means a repo cloned on the host can usually be committed and pushed from inside a Docker container without changing `origin`.

## Configuration

Relevant config keys:

```yaml
git:
  repo_root: "~/.nalvin/git-repos"
  ssh:
    enabled: true
    addr: "127.0.0.1:4222"
    host_key_path: "~/.nalvin/git/ssh_host_ed25519"
  binaries:
    git: ""
    upload_pack: ""
    receive_pack: ""
  default_client:
    user: "nalvin"
    private_key_path: "~/.nalvin/git/id_nalvin"
    host: "127.0.0.1"
    author_name: ""
    author_email: ""
  repo_defaults:
    public_read: true
    default_writers:
      - nalvin
  users: {}
  repos: {}
```

Notes:

- Leaving `git.binaries.*` empty means `nalvin` resolves Git commands from `PATH`
- `repo_defaults.default_writers` controls who can push to newly created repos
- per-repo overrides live under `git.repos`
- explicit user SSH keys can be configured under `git.users`

## `nalvin git` CLI

The top-level `git` command is the human-facing CLI for repo management and workspace checkouts.

Repo administration:

```bash
nalvin git repo list
nalvin git repo create demo
nalvin git repo tree demo
nalvin git repo commits demo
```

Checkout and worktree operations:

```bash
nalvin --workspace alpha git checkout demo
nalvin --workspace alpha git status --repo-path demo
nalvin --workspace alpha git add --repo-path demo hello.txt
nalvin --workspace alpha git commit --repo-path demo -m "initial commit"
nalvin --workspace alpha git push --repo-path demo
```

Behavior summary:

- `git repo list` lists managed bare repos under the global repo root
- `git repo create <name>` creates `<repo_root>/<name>.git`
- `git repo tree <name> [--branch <name>]` shows the latest committed file tree for a managed repo branch and filters out paths matched by the branch's committed `.gitignore` files
- `git repo commits <name> [--branch <name>]` lists commit history for a managed repo branch
- `git checkout <repo> [dest]` clones the managed repo into the active workspace
- `git status`, `git add`, `git commit`, and `git push` operate on an existing checkout inside the active workspace

## Agent Git Tools

Agent runs expose a minimal hidden Git tool family:

- `git`
- `git_list_repos`
- `git_create_repo`
- `git_delete_repo`
- `git_repo_tree`
- `git_repo_commits`

These tools are enabled by default but hidden until the model reveals them through `search_tools`.

Common reveal queries:

- `git`
- `create git repo`
- `list repos`
- `delete git repo`
- `repo tree`
- `commit history`

The main `git` tool behaves like a scoped Git CLI:

- input: a single raw `args` string
- output: structured JSON with `ok`, `exit_code`, `stdout`, `stderr`, `argv`, and `cwd`
- scope: the active workspace plus the managed bare-repo root

The helper tools exist because some server-managed actions are not normal Git CLI operations:

- `git_list_repos` returns managed repos and `ssh_url`
- `git_create_repo` creates a managed bare repo and returns its `ssh_url`
- `git_delete_repo` deletes a managed bare repo by name
- `git_repo_tree` returns the latest committed file tree for a managed repo branch
- `git_repo_commits` returns commit history for a managed repo branch

## Direct Tool Runner Examples

Smoke-test the hidden Git tools directly without going through the model:

```bash
nalvin --workspace alpha agent tools run search_tools --query git
nalvin --workspace alpha agent tools run git_create_repo --name demo
nalvin --workspace alpha agent tools run git_list_repos
nalvin --workspace alpha agent tools run git_repo_tree --name demo
nalvin --workspace alpha agent tools run git_repo_commits --name demo
nalvin --workspace alpha agent tools run git --args "clone ssh://nalvin@127.0.0.1:4222/demo.git demo"
nalvin --workspace alpha agent tools run git --args "-C demo status --short"
nalvin --workspace alpha agent tools run git_delete_repo --name demo
```

## Safety Notes

The `git` agent tool is intentionally narrower than a full shell:

- it accepts shell-style quoting, but rejects shell operators like `&&`, `|`, and redirections
- it only allows a client-side Git subcommand set such as `status`, `diff`, `add`, `commit`, `clone`, `fetch`, `pull`, `push`, and `checkout`
- it rejects unsafe path escapes outside the active workspace and managed repo root

When the agent wants to chain multiple Git commands, it must call the `git` tool multiple times.

## Related Docs

- [Agent tools](./agent-tools.md)
