package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/spf13/pflag"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const shellDefaultTimeoutSeconds = 60

// shellToolDescription is included verbatim in the tool's description shown to the model.
const shellToolDescription = `Execute a POSIX shell script that composes agent tools and Unix utilities.
All currently visible agent tools are available as commands with --flag syntax.
File operations use an atomic workspace snapshot; changes are committed to the workspace only if the script exits successfully.

IMPORTANT: This shell runs on the HOST machine inside the agent process — NOT inside any Docker container.
It cannot run programs installed in a container, and it cannot be used to execute commands inside a container.
To run commands inside a container, use the docker_exec_foreground tool directly (or docker_exec_background for long-running processes).
The shell is for composing tool calls (piping tool output through jq, looping over results, etc.) — not for executing arbitrary host-side programs.

AGENT TOOLS AS COMMANDS
  JSON input parameters become --flags. Tool JSON output is written to stdout.
  Use jq to extract fields and compose with other commands.

    view --path main.go
    view --path main.go --offset 100 --limit 50
    write --path out.txt --content "hello"
    edit --path main.go --old_string "foo" --new_string "bar"
    grep --pattern "TODO" --path src --include "*.go"
    glob --pattern "**/*.ts"
    ls --path src --depth 2
    web_fetch_get --url "https://api.example.com/data"          # body_json holds parsed JSON when response is JSON
    kb_list_nodes --query "auth"

  Use --help on any tool to see its flags and descriptions:
    view --help

UNIX UTILITIES
  Available exactly these commands — nothing else is on PATH:
  Text:   cat, head, tail, wc, sort, uniq, tr, cut, tee, sed, xargs
  Match:  fgrep (literal), egrep (regex)  [note: 'grep' is the agent tool]
  JSON:   jq  (critical for extracting fields from tool JSON output)
  Files:  mkdir, rm, cp, mv, touch, mktemp, realpath, basename, dirname, find
  Other:  seq, sleep, date, tree
  Shell:  echo, printf, test, [, cd, pwd, read, export, set, shift, ...

NOT AVAILABLE (not on PATH; do not attempt):
  docker, curl, wget, git, tar, zip, unzip, ln, ssh, scp,
  apt-get, apt, brew, pip, npm, python, node, go, make.
  To reach docker, use docker_exec_foreground / docker_exec_background / docker_* tools.
  To fetch URLs, use web_fetch_get.
  For git operations, use git_* tools.

COMPOSITION PATTERNS

  Extract paths from tool JSON and process:
    glob --pattern "*.go" | jq -r '.paths[]' | head -20

  Pipe tool output through text processing:
    view --path data.csv | jq -r '.content' | tail -n +2 | cut -d',' -f1,3 | sort -u

  Fetch JSON and access fields directly via body_json:
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

  Batch with xargs:
    glob --pattern "*.md" | jq -r '.paths[]' | xargs -I{} grep --pattern "TODO" --path {}

  Chain writes atomically — all-or-nothing on success:
    write --path a.txt --content "A" && write --path b.txt --content "B"

NOTES
  - All file paths used by agent tools and Unix utilities are workspace-relative
  - Tool output is JSON; use jq to extract text before piping to text tools
  - fgrep/egrep search stdin or files; 'grep' always invokes the agent grep tool
  - grep output schema: {"matches": [{"path", "line_number", "preview"}], ...}
    count matches:   grep ... | jq '.matches | length'
    extract lines:   grep ... | jq -r '.matches[].preview'
    count per file:  use egrep -c "pattern" file  (returns a plain integer)
  - Script changes are only written to the workspace on exit status 0
  - Default timeout is 60 seconds; override with the timeout parameter`

// bashToolDescription is the description shown to the model for the bash alias.
const bashToolDescription = `Alias for the restricted shell tool. This is not real Bash and it never runs arbitrary host commands. Use ` + "`command`" + ` for the program; ` + "`script`" + ` is accepted as an alias. Builtins and visible agent tools run against an atomic workspace snapshot. When the docker fallback is enabled, commands that are neither builtins nor agent tools are executed inside a temporary Docker container with the workspace mounted at /workspace and a sandboxed scratch dir at /tmp; otherwise such commands return "command not found". Default timeout is 60 seconds.`

type shellInput struct {
	Script  string `json:"script" description:"POSIX shell script to execute. All visible agent tools are available as commands with --flag syntax. Use pipes, redirections, variables, and control flow to compose tools."`
	Timeout int    `json:"timeout,omitempty" description:"Optional timeout in seconds. Defaults to 60."`
}

type bashInput struct {
	Command     string `json:"command,omitempty" description:"Restricted shell program to execute. This is an alias for script; it is not real host Bash."`
	Script      string `json:"script,omitempty" description:"Alias for command. If both command and script are provided they must be identical."`
	Description string `json:"description,omitempty" description:"Optional human-readable description. Accepted and ignored by execution."`
	Timeout     int    `json:"timeout,omitempty" description:"Optional timeout in seconds. Defaults to 60."`
}

func (rt *agentRuntime) runShell(ctx context.Context, input shellInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool shell script_len=%d timeout=%d", len(input.Script), input.Timeout)
	return rt.runRestrictedShell(ctx, "shell", input.Script, input.Timeout)
}

func (rt *agentRuntime) runBash(ctx context.Context, input bashInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	command := strings.TrimSpace(input.Command)
	script := strings.TrimSpace(input.Script)
	if command != "" && script != "" && command != script {
		return fantasy.NewTextErrorResponse("command and script were both provided but differ"), nil
	}
	if command == "" {
		command = script
	}
	rt.session.debugf("tool bash command_len=%d timeout=%d description_len=%d", len(command), input.Timeout, len(input.Description))
	return rt.runRestrictedShell(ctx, "bash", command, input.Timeout)
}

func (rt *agentRuntime) runRestrictedShell(ctx context.Context, toolName, rawScript string, timeoutSeconds int) (fantasy.ToolResponse, error) {
	script := strings.TrimSpace(rawScript)
	if script == "" {
		if toolName == "bash" {
			return fantasy.NewTextErrorResponse("command is required"), nil
		}
		return fantasy.NewTextErrorResponse("script is required"), nil
	}

	// Parse the shell script.
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "shell")
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("parse error: %v", err)), nil
	}

	// Snapshot workspace into a temp directory.
	tmpDir, cleanupTmp, snapshotErr := snapshotWorkspace(ctx, rt.cfg)
	defer cleanupTmp()
	if snapshotErr != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("workspace snapshot failed: %v", snapshotErr)), nil
	}

	// Per-invocation scratch dir mounted at /tmp inside the docker fallback
	// container. Created up front so the cleanup runs even if init fails.
	scratchDir, cleanupScratch, scratchErr := snapshotShellScratch()
	defer cleanupScratch()
	if scratchErr != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("shell scratch tmp failed: %v", scratchErr)), nil
	}

	// Build a context with the temp dir as the active workspace.
	shellCtx := ctx
	if originalPaths, ok := workspacepkg.PathsFromContext(ctx); ok {
		shellCtx = workspacepkg.WithPaths(ctx, workspacepkg.Paths{
			Name:      originalPaths.Name,
			FilesPath: tmpDir,
			DBPath:    originalPaths.DBPath,
		})
	} else if paths, err := workspacepkg.ActivePaths(ctx, rt.cfg); err == nil {
		shellCtx = workspacepkg.WithPaths(ctx, workspacepkg.Paths{
			Name:      paths.Name,
			FilesPath: tmpDir,
			DBPath:    paths.DBPath,
		})
	}

	// Apply timeout.
	timeout := timeoutSeconds
	if timeout <= 0 {
		timeout = shellDefaultTimeoutSeconds
	}
	shellCtx, cancel := context.WithTimeout(shellCtx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Capture output.
	var stdout, stderr bytes.Buffer

	// Lazy docker fallback container: created on first non-builtin/non-tool
	// command and torn down at the end of this shell invocation.
	fallback := newShellDockerFallback(rt, shellCtx, tmpDir, scratchDir)
	defer fallback.cleanup(context.Background())

	// Build the combined dispatch function for both builtins and tools.
	// Builtins map is created first (without xargs); xargs is added after dispatch is defined.
	builtins := rt.coreBuiltinsWithoutXargs(tmpDir)
	dispatch := rt.makeShellDispatch(shellCtx, tmpDir, builtins, shellDispatchOptions{
		scratchDir: scratchDir,
		fallback:   fallback,
	})
	builtins["xargs"] = makeXargsBuiltin(dispatch)

	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Dir(tmpDir),
		interp.ExecHandlers(func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return dispatch
		}),
		interp.OpenHandler(shellOpenHandler(tmpDir)),
	)
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("shell init failed: %v", err)), nil
	}

	runErr := runner.Run(shellCtx, prog)

	// Always sync workspace changes back to the real workspace, regardless of
	// exit status. This prevents silent data loss when a script writes files
	// and then fails: the agent (and the user) expect the writes to persist.
	if realPaths, err := workspacepkg.ActivePaths(ctx, rt.cfg); err == nil {
		if syncErr := syncWorkspaceChanges(tmpDir, realPaths.FilesPath); syncErr != nil {
			rt.session.debugf("shell workspace sync error: %v", syncErr)
		}
	}

	combined := stdout.String()
	if stderrStr := stderr.String(); stderrStr != "" {
		if combined != "" {
			combined += "\n--- stderr ---\n" + stderrStr
		} else {
			combined = stderrStr
		}
	}

	if runErr != nil && !isExitStatus(runErr, 0) {
		if combined == "" {
			combined = runErr.Error()
		}
		combined += "\n\nNote: shell exited non-zero; any workspace file writes were still applied."
		return fantasy.NewTextErrorResponse(combined), nil
	}

	if combined == "" {
		combined = "(no output)"
	}
	return fantasy.NewTextResponse(combined), nil
}

// shellDispatchOptions configures optional behaviors for the shell exec
// dispatcher: a scratch dir mounted at /tmp inside the fallback container, and
// a docker fallback used when a command is neither a builtin nor a visible
// agent tool.
type shellDispatchOptions struct {
	scratchDir string
	fallback   *shellDockerFallback
}

// makeShellDispatch creates the combined exec dispatch function for the shell.
// Order: builtins -> blocked names (shell/bash/search_tools) -> literal `docker`
// (scoped client) -> visible agent tools -> docker fallback (when enabled) ->
// reject.
func (rt *agentRuntime) makeShellDispatch(shellCtx context.Context, tmpDir string, builtins map[string]shellBuiltinFn, optionList ...shellDispatchOptions) interp.ExecHandlerFunc {
	options := shellDispatchOptions{}
	if len(optionList) > 0 {
		options = optionList[0]
	}
	return func(ctx context.Context, args []string) error {
		if len(args) == 0 {
			return nil
		}
		cmdName := args[0]
		rest := args[1:]

		// 1. Check coreutils builtins.
		if fn, ok := builtins[cmdName]; ok {
			return fn(ctx, rest)
		}

		// 2. Block recursive shell/bash/tool-search calls.
		if cmdName == "shell" || cmdName == "bash" || cmdName == "search_tools" {
			hc := interp.HandlerCtx(ctx)
			fmt.Fprintf(hc.Stderr, "shell: %s: not available inside shell\n", cmdName)
			return interp.ExitStatus(1)
		}

		// 3. Route literal `docker` commands through the scoped Docker client.
		if cmdName == "docker" {
			return rt.runShellDockerCLI(shellCtx, ctx, tmpDir, rest)
		}

		// 4. Visible agent tools are also shell commands.
		tool, ok := rt.tools[cmdName]
		if !ok {
			// 5. Docker fallback: run the command inside a temp container.
			if options.fallback != nil && options.fallback.enabled() {
				return options.fallback.exec(ctx, args)
			}
			// 6. Reject.
			hc := interp.HandlerCtx(ctx)
			fmt.Fprintf(hc.Stderr, "shell: %s: command not found\n", cmdName)
			return interp.ExitStatus(127)
		}

		// Parse --flags into JSON input.
		info := tool.tool.Info()
		jsonInput, helpRequested, parseErr := parseToolArgs(info, rest)
		if helpRequested {
			hc := interp.HandlerCtx(ctx)
			fmt.Fprint(hc.Stdout, formatToolHelp(cmdName, info))
			return nil
		}
		if parseErr != nil {
			hc := interp.HandlerCtx(ctx)
			fmt.Fprintf(hc.Stderr, "%s: %v\nRun '%s --help' for usage.\n", cmdName, parseErr, cmdName)
			return interp.ExitStatus(1)
		}

		// Execute the tool using the shell context (which has tmpDir as workspace).
		resp, err := tool.tool.Run(shellCtx, fantasy.ToolCall{
			ID:    "shell_" + cmdName,
			Name:  cmdName,
			Input: jsonInput,
		})
		if err != nil {
			hc := interp.HandlerCtx(ctx)
			fmt.Fprintf(hc.Stderr, "%s: %v\n", cmdName, err)
			return interp.ExitStatus(1)
		}

		hc := interp.HandlerCtx(ctx)
		if resp.IsError {
			fmt.Fprint(hc.Stderr, resp.Content)
			if !strings.HasSuffix(resp.Content, "\n") {
				fmt.Fprint(hc.Stderr, "\n")
			}
			return interp.ExitStatus(1)
		}
		fmt.Fprint(hc.Stdout, resp.Content)
		if !strings.HasSuffix(resp.Content, "\n") {
			fmt.Fprint(hc.Stdout, "\n")
		}
		return nil
	}
}

// shellOpenHandler returns an OpenHandlerFunc that restricts file I/O to tmpDir.
// Relative paths are resolved against the handler's current Dir (also tmpDir).
// Special device paths (/dev/null, /dev/stdin, etc.) are passed through.
func shellOpenHandler(tmpDir string) interp.OpenHandlerFunc {
	return func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		hc := interp.HandlerCtx(ctx)

		// Resolve relative paths using the handler's current directory.
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(hc.Dir, path)
		}
		path = filepath.Clean(path)

		// Allow special device paths.
		if isAllowedDevPath(path) {
			return os.OpenFile(path, flag, perm)
		}

		// Enforce tmpDir boundary.
		if !strings.HasPrefix(path, filepath.Clean(tmpDir)+string(filepath.Separator)) &&
			path != filepath.Clean(tmpDir) {
			return nil, fmt.Errorf("path %q is outside the shell workspace snapshot; use a workspace-relative path under the active workspace, or use write/edit directly for canonical plan-file updates", path)
		}

		// Create parent directories for write operations.
		if flag&(os.O_CREATE|os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_TRUNC) != 0 {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
		}

		return os.OpenFile(path, flag, perm)
	}
}

func isAllowedDevPath(path string) bool {
	if runtime.GOOS == "windows" {
		return path == "NUL"
	}
	switch path {
	case "/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr",
		"/dev/fd/0", "/dev/fd/1", "/dev/fd/2":
		return true
	}
	return false
}

// snapshotWorkspace copies the active workspace files into a new temp directory.
// Returns the tmpDir path and a cleanup function. The cleanup removes the tmpDir.
func snapshotWorkspace(ctx context.Context, cfg configpkg.Config) (tmpDir string, cleanup func(), err error) {
	cleanup = func() {}

	tmpDir, err = os.MkdirTemp("", "nalvin-shell-*")
	if err != nil {
		return "", cleanup, fmt.Errorf("create temp workspace: %w", err)
	}
	cleanup = func() { os.RemoveAll(tmpDir) }

	paths, pathErr := workspacepkg.ActivePaths(ctx, cfg)
	if pathErr != nil {
		// No workspace: return an empty temp dir.
		return tmpDir, cleanup, nil
	}

	// Check if workspace files directory exists.
	if _, statErr := os.Stat(paths.FilesPath); os.IsNotExist(statErr) {
		return tmpDir, cleanup, nil
	}

	// Copy workspace files into the temp directory.
	if _, copyErr := workspacepkg.CopyTree(paths.FilesPath, tmpDir, true); copyErr != nil {
		return "", cleanup, fmt.Errorf("snapshot workspace: %w", copyErr)
	}

	return tmpDir, cleanup, nil
}

// syncWorkspaceChanges syncs changes from tmpDir back to realDir.
// Files added or modified in tmpDir are written to realDir.
// Files deleted from tmpDir are removed from realDir.
func syncWorkspaceChanges(tmpDir, realDir string) error {
	// Collect all relative paths present in tmpDir.
	tmpRelPaths := make(map[string]struct{})
	if err := filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tmpDir, path)
		if err != nil {
			return err
		}
		tmpRelPaths[rel] = struct{}{}
		return nil
	}); err != nil {
		return fmt.Errorf("walk tmp workspace: %w", err)
	}

	// Step 1: copy new/modified files from tmpDir to realDir.
	if err := filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(tmpDir, path)
		if rel == "." {
			return nil
		}
		realPath := filepath.Join(realDir, rel)

		if d.IsDir() {
			return os.MkdirAll(realPath, 0o755)
		}

		tmpContent, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		realContent, realErr := os.ReadFile(realPath)
		if realErr != nil || !bytes.Equal(tmpContent, realContent) {
			if err := os.MkdirAll(filepath.Dir(realPath), 0o755); err != nil {
				return err
			}
			return os.WriteFile(realPath, tmpContent, 0o644)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("sync new/modified files: %w", err)
	}

	// Step 2: collect paths in realDir that are absent from tmpDir.
	var toRemove []string
	if _, err := os.Stat(realDir); err == nil {
		if err := filepath.WalkDir(realDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // ignore errors walking realDir
			}
			rel, _ := filepath.Rel(realDir, path)
			if rel == "." {
				return nil
			}
			if _, exists := tmpRelPaths[rel]; !exists {
				toRemove = append(toRemove, path)
				if d.IsDir() {
					return filepath.SkipDir
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("walk real workspace: %w", err)
		}
	}

	// Remove deepest paths first.
	sort.Slice(toRemove, func(i, j int) bool {
		return len(toRemove[i]) > len(toRemove[j])
	})
	for _, p := range toRemove {
		_ = os.RemoveAll(p)
	}

	return nil
}

// isExitStatus returns true if err is an exit status error with the given code.
func isExitStatus(err error, code uint8) bool {
	if err == nil {
		return code == 0
	}
	var es interp.ExitStatus
	if errors.As(err, &es) {
		return uint8(es) == code
	}
	return false
}

// --- Flag adapter ---

// parseToolArgs converts CLI-style args ([]string) into a JSON input string for the tool.
// Returns helpRequested=true if --help/-h was requested.
func parseToolArgs(info fantasy.ToolInfo, args []string) (jsonInput string, helpRequested bool, err error) {
	// Early --help check.
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return "", true, nil
		}
	}

	fs := pflag.NewFlagSet(info.Name, pflag.ContinueOnError)
	fs.SetOutput(io.Discard) // suppress pflag's own usage output

	type entry struct {
		typ  string
		sptr *string
		bptr *bool
		iptr *int
		fptr *float64
	}
	entries := make(map[string]*entry)

	for name, rawProp := range info.Parameters {
		prop, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}
		propType := ""
		if t, ok := prop["type"].(string); ok {
			propType = t
		}
		desc := ""
		if d, ok := prop["description"].(string); ok {
			desc = d
		}
		e := &entry{typ: propType}
		switch propType {
		case "boolean":
			e.bptr = fs.Bool(name, false, desc)
		case "integer":
			e.iptr = fs.Int(name, 0, desc)
		case "number":
			e.fptr = fs.Float64(name, 0, desc)
		default:
			e.sptr = fs.String(name, "", desc)
		}
		entries[name] = e
	}

	if err := fs.Parse(args); err != nil {
		return "", false, err
	}

	// Build JSON object from parsed flags.
	result := make(map[string]any)
	for name, e := range entries {
		switch e.typ {
		case "boolean":
			if e.bptr != nil && *e.bptr {
				result[name] = true
			} else if fs.Changed(name) {
				result[name] = false
			}
		case "integer":
			if fs.Changed(name) && e.iptr != nil {
				result[name] = *e.iptr
			}
		case "number":
			if fs.Changed(name) && e.fptr != nil {
				result[name] = *e.fptr
			}
		case "array":
			if e.sptr != nil {
				val := strings.TrimSpace(*e.sptr)
				if val != "" {
					var arr []any
					if jsonErr := json.Unmarshal([]byte(val), &arr); jsonErr != nil {
						return "", false, fmt.Errorf("flag --%s: expected JSON array (e.g. '[\"a\",\"b\"]'): %v", name, jsonErr)
					}
					result[name] = arr
				}
			}
		case "object":
			if e.sptr != nil {
				val := strings.TrimSpace(*e.sptr)
				if val != "" {
					var obj any
					if jsonErr := json.Unmarshal([]byte(val), &obj); jsonErr != nil {
						return "", false, fmt.Errorf("flag --%s: expected JSON object (e.g. '{\"key\":\"value\"}'): %v", name, jsonErr)
					}
					result[name] = obj
				}
			}
		default: // string
			if e.sptr != nil && *e.sptr != "" {
				result[name] = *e.sptr
			}
		}
	}

	// Validate required fields.
	for _, reqName := range info.Required {
		if _, ok := result[reqName]; !ok {
			return "", false, fmt.Errorf("required flag --%s not provided", reqName)
		}
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return "", false, err
	}
	return string(payload), false, nil
}

// formatToolHelp generates a --help text from a tool's ToolInfo.
func formatToolHelp(toolID string, info fantasy.ToolInfo) string {
	var b strings.Builder
	b.WriteString(toolID)
	if info.Description != "" {
		b.WriteString(" — ")
		b.WriteString(info.Description)
	}
	b.WriteString("\n\nUsage: ")
	b.WriteString(toolID)
	b.WriteString(" [flags]\n\nFlags:\n")

	required := make(map[string]bool, len(info.Required))
	for _, r := range info.Required {
		required[r] = true
	}

	// Collect and sort parameter names.
	names := make([]string, 0, len(info.Parameters))
	for name := range info.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rawProp := info.Parameters[name]
		prop, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}
		propType := "string"
		if t, ok := prop["type"].(string); ok {
			propType = t
		}
		desc := ""
		if d, ok := prop["description"].(string); ok {
			desc = d
		}
		req := ""
		if required[name] {
			req = " (required)"
		}
		fmt.Fprintf(&b, "  --%-20s %s\t%s%s\n", name, propType, desc, req)
	}
	return b.String()
}
