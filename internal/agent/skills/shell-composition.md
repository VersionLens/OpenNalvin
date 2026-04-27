---
name: shell-composition
description: Use the shell tool for loops, pipelines, batching, workspace-wide file analysis, and atomic multi-step workflows.
metadata:
  agent:
    activation: manual
    run_scopes: [root, child]
    tool_hints: [shell]
    autoreveal_tools: [shell, bash]
---

## Shell Composition

Reach for the `shell` tool when a task needs loops, pipelines, batching, or a multi-step workflow that is cleaner as one atomic script than many separate tool calls. `bash` is an alias for the same restricted runner. These tools are not a real host shell and cannot execute arbitrary host commands.

Activating this skill auto-reveals `shell` and `bash` when those tools are already allowed in the run.

When you call the `shell` tool, put the program in the `script` parameter. Do not use a `command` parameter.

When you call the `bash` tool, put the program in the `command` parameter. It also accepts `script` as an alias, but it still uses the same restricted runner rather than host Bash.

Example tool call shape:

    {"script":"glob --pattern \"**/*.go\" | jq -r '.paths[]'"}

Alias shape:

    {"command":"glob --pattern \"**/*.go\" | jq -r '.paths[]'"}

### When to use it

- Looping over many files, paths, or results
- Piping one tool's JSON output into another tool or a text utility
- Multi-step edits that should only land if the whole sequence succeeds
- Combining `web_fetch_get`, file tools, grep, and write in one step

Prefer individual tool calls when you only need one tool invocation or when the work is simple enough to reason about in-line.

### Patterns

Extract paths from tool JSON and process them:

    glob --pattern "*.go" | jq -r '.paths[]' | head -20

Pipe tool output through text processing:

    view --path data.csv | jq -r '.content' | tail -n +2 | cut -d',' -f1,3 | sort -u

Fetch JSON and access fields directly via `body_json`:

    web_fetch_get --url "https://api.example.com/user/1" | jq '.body_json.name'
    uuid=$(web_fetch_get --url "https://httpbin.org/uuid" | jq -r '.body_json.uuid')

Redirect tool output to a file, then process:

    web_fetch_get --url "https://api.example.com/users" | jq '.body_json' > users.json
    write --path "data/users.json" --content "$(cat users.json)"

Variables and conditionals:

    count=$(grep --pattern "error" --path logs | jq '.matches | length')
    if [ "$count" -gt 0 ]; then echo "Found $count errors"; fi

Loops over tool results:

    for f in $(glob --pattern "src/**/*.ts" | jq -r '.paths[]'); do
      grep --pattern "import" --path "$f" | jq -r '.matches[].preview'
    done

Batch with `xargs`:

    glob --pattern "*.md" | jq -r '.paths[]' | xargs -I{} grep --pattern "TODO" --path {}

Chain writes atomically — all-or-nothing on success:

    write --path a.txt --content "A" && write --path b.txt --content "B"

### Notes

- All agent-tool paths are workspace-relative.
- The `shell` tool input field is `script`. Bare snippets in this skill are examples of what goes inside that field.
- The `bash` tool input field is `command`; it is an alias for this restricted shell, not real Bash.
- Tool output is JSON; use `jq` to extract text before piping to text utilities.
- If a script uses an agent tool such as `glob`, `view`, `grep`, `write`, or `web_fetch_get`, reveal that supporting tool before the shell call. Inside shell, only currently visible agent tools are callable by name.
- `grep` calls the visible agent grep tool; use `fgrep` or `egrep` for plain text matching on stdin/files.
- Count agent-grep matches with `grep ... | jq '.matches | length'`; per-file counts are easier with `egrep -c "pattern" file`.
- Use `search_tools` outside the shell if you need another hidden supporting tool after activating this skill. Recursive `shell`, `bash`, and `search_tools` calls are blocked inside shell scripts.
- Builtins and visible agent tools run against an atomic workspace snapshot.
- Other non-tool commands run inside a temporary `nalvin/dev` Docker container with the snapshot mounted at `/workspace`.
- `/tmp` is per-call scratch. Files written there are available to the script and Docker fallback commands during that shell call, but they are not synced back to the workspace.
- Use `docker_exec_background` for long-running container processes such as dev servers.
