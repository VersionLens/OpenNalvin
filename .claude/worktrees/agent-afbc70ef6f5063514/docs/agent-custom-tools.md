# Agent Custom Tools

Custom tools let you extend the agent with Go programs without modifying `nalvin` itself. Each `.go` file in `~/.nalvin/custom-tools/` is compiled at runtime by the embedded [Scriggo](https://scriggo.com) Go interpreter and registered as a first-class tool — visible to the agent, usable in the shell environment, and callable from other custom tools.

## Authoring a Custom Tool

Each file defines exactly one tool. The tool's identity and behavior are declared with `// tool:` comment directives and a `type Input struct`.

```go
package main

import (
	"tool"
	"strings"
)

// tool:name reverse_string
// tool:description Reverse a UTF-8 string and return the result.
// tool:keywords reverse,flip,mirror

type Input struct {
	Text string `json:"text" description:"The string to reverse."`
}

func main() {
	in := tool.GetInput()
	text, _ := in["text"].(string)

	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}

	_ = strings.Contains(text, "x") // stdlib is available

	tool.SetOutput(map[string]any{
		"reversed": string(runes),
	})
}
```

Save it as `~/.nalvin/custom-tools/reverse_string.go`. On the next agent run it will appear as `reverse_string` with source `custom`.

## Metadata Directives

Declare tool metadata with `// tool:key value` comment lines anywhere in the file.

| Directive | Default | Description |
|-----------|---------|-------------|
| `// tool:name <id>` | filename stem | Tool identifier (used in agent catalog and shell) |
| `// tool:description <text>` | *(empty)* | Description shown to the model |
| `// tool:keywords <kw1,kw2,...>` | *(none)* | Comma-separated search aliases for `search_tools` |
| `// tool:default_enabled <bool>` | `true` | Whether the tool starts enabled by default |
| `// tool:default_pinned <bool>` | `false` | Whether the tool starts pinned (immediately visible) by default |

The name should match the filename stem for clarity, but it is the directive that governs — you can rename the tool by changing `// tool:name` without renaming the file.

## The `tool` Package

All custom tools have access to a built-in `tool` package with four exported functions.

### `tool.GetInput() map[string]any`

Returns the tool's JSON input as a `map[string]any`. Access individual fields with type assertions:

```go
in := tool.GetInput()
text, _ := in["text"].(string)
limit, _ := in["limit"].(int)
verbose, _ := in["verbose"].(bool)
```

### `tool.SetOutput(v any)`

Sets the tool's return value. `v` must be JSON-serializable. If called multiple times, the last value wins. If never called, the tool returns `{"success": true}`.

```go
tool.SetOutput(map[string]any{
	"count":   42,
	"items":   []string{"a", "b", "c"},
})
```

### `tool.CallTool(name string, args map[string]any) (map[string]any, error)`

Invokes any other registered tool — internal, MCP, or custom — by ID. The tool runs with the same timeout context as the custom tool itself.

```go
result, err := tool.CallTool("glob", map[string]any{
	"pattern": "**/*.go",
})
if err != nil {
	tool.SetOutput(map[string]any{"error": err.Error()})
	return
}
paths, _ := result["paths"].([]any)
```

A tool cannot call itself (recursive self-calls return an error).

### `tool.Logf(format string, args ...any)`

Writes a debug line to stderr. Useful for local development; not shown in agent traces.

```go
tool.Logf("processing %d items", len(items))
```

## Input Schema from `type Input struct`

The `type Input struct` is parsed at load time (using `go/ast`) to generate the JSON schema that the agent and shell see. It is **not** used for runtime data transfer — use `tool.GetInput()` for that.

Supported field types and their JSON schema mappings:

| Go type | JSON schema type |
|---------|-----------------|
| `string` | `"string"` |
| `int`, `int8`…`int64`, `uint`…`uint64` | `"integer"` |
| `float32`, `float64` | `"number"` |
| `bool` | `"boolean"` |
| `[]T` | `"array"` |
| `map[string]T` | `"object"` |

Fields without `omitempty` in their `json` tag are required. The `description` struct tag sets the parameter description visible to the model.

```go
type Input struct {
	Query  string `json:"query" description:"Natural-language query."`
	Limit  int    `json:"limit,omitempty" description:"Max results. Default 10."`
	Format string `json:"format,omitempty" description:"Output format: json or text."`
}
```

If no `Input` struct is present, the tool takes no parameters.

## Available Standard Library Packages

The following packages from the Go standard library are available for import. Packages that could access the filesystem, network, or OS are intentionally excluded — use `tool.CallTool` for those operations, which keeps them visible in the agent's audit trail.

**Text and strings:**
- `fmt` — `Sprintf`, `Errorf`, `Sscanf`, `Sscan`, `Sscanln` (no `Print`/`Fprintf` to stdout)
- `strings`
- `strconv`
- `unicode`
- `unicode/utf8`

**Data:**
- `encoding/json` is **not** available — use `tool.GetInput()` / `tool.SetOutput()` for JSON I/O
- `math`
- `sort`

**Patterns and paths:**
- `regexp`
- `path`
- `path/filepath`

**Time:**
- `time`

**Errors:**
- `errors`

Blocked packages include `os`, `os/exec`, `syscall`, `unsafe`, `net`, `net/http`, `runtime`, `reflect`, `io`, `io/fs`, and `database/sql`.

## Calling Other Tools

Use `tool.CallTool` to compose custom tools from existing ones. This keeps filesystem and network access auditable in the agent's run trace.

```go
package main

import (
	"tool"
	"strings"
)

// tool:name go_file_summary
// tool:description List all Go files in the workspace and count total lines.

func main() {
	globResult, err := tool.CallTool("glob", map[string]any{
		"pattern": "**/*.go",
	})
	if err != nil {
		tool.SetOutput(map[string]any{"error": err.Error()})
		return
	}

	paths, _ := globResult["paths"].([]any)
	totalLines := 0

	for _, p := range paths {
		path, _ := p.(string)
		viewResult, err := tool.CallTool("view", map[string]any{"path": path})
		if err != nil {
			continue
		}
		content, _ := viewResult["content"].(string)
		totalLines += strings.Count(content, "\n")
	}

	tool.SetOutput(map[string]any{
		"file_count":  len(paths),
		"total_lines": totalLines,
	})
}
```

## Configuration

Configure custom tool loading in `~/.nalvin/config.yaml`:

```yaml
agent:
  custom_tools:
    dir: ~/.nalvin/custom-tools   # default
    enabled: true                  # default
    timeout_seconds: 30            # default
```

- `dir`: directory to scan for `*.go` custom tool files. `~` is expanded.
- `enabled`: set to `false` to disable all custom tool loading.
- `timeout_seconds`: per-invocation execution timeout. Tools that exceed this return an error response.

Custom tools participate in the standard tool enable/pin system. Use the same config keys or per-run flags as for built-in tools:

```yaml
agent:
  tools:
    default_disabled:
      - my_experimental_tool
    default_pinned:
      - reverse_string
    metadata:
      reverse_string:
        keywords:
          - flip string
          - invert text
```

## Shell Integration

Custom tools appear as shell subcommands in the `shell` tool automatically — no extra configuration needed. Parameters map to `--flags` exactly as they do for built-in tools.

```bash
# Use a custom tool in a shell script
reverse_string --text "hello world" | jq -r '.reversed'

# Compose with other tools
go_file_summary | jq '.total_lines'

# Get parameter help
reverse_string --help
```

## Tool Discovery

Custom tools are indexed by `search_tools` just like internal tools. The tool ID, description, and keywords from `// tool:` directives are all searchable.

To check that a custom tool loaded correctly:

```bash
# List all tools, filter to custom source
nalvin agent tools list --json | jq '.tools[] | select(.source == "custom")'

# Run the tool directly
nalvin agent tools run reverse_string --text "hello"
```

## Error Handling

At **load time**, errors are non-fatal. A file that fails to parse or compile is skipped with a warning, and the remaining tools continue loading. Warnings appear in `agent tools list --json` under the `warnings` field.

Common load-time errors:

- **Syntax error** in the Go source: fix the Go code and restart.
- **Import of blocked package** (e.g. `import "os"`): remove the import and use `tool.CallTool` instead.
- **Name collision** with an internal tool: rename the tool with `// tool:name`.

At **run time**, errors from the Scriggo VM are returned as error tool responses (not Go errors). The agent sees an `is_error: true` result with the error message. Unrecovered panics and timeouts both produce error responses.

## Limitations

- **No hot reload**: custom tools are compiled once per agent runtime creation. After changing a file, the next agent run picks up the change automatically.
- **No direct I/O**: `os`, `net`, and `io` packages are blocked. Use `tool.CallTool` for file reads, web fetches, and writes.
- **No goroutines**: the `go` statement is disabled in Scriggo programs.
- **No `encoding/json`**: use `tool.GetInput()` and `tool.SetOutput()` for JSON data. Use the `strings`, `strconv`, or `regexp` packages for text processing.
- **Single file**: each tool must be a single `.go` file. Multi-file packages are not supported.
- **Program-mode only**: templates and packages other than `main` are not supported. Each file must declare `package main` and define `func main()`.
