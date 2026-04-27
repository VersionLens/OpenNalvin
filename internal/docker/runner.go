package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	shlex "github.com/anmitsu/go-shlex"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

const (
	DefaultWorkspaceMountTarget = "/workspace"
)

type RawRunOptions struct {
	CWD           string
	WorkspaceRoot string
	Args          string
	Host          string
}

type RawRunArgvOptions struct {
	CWD           string
	WorkspaceRoot string
	Args          []string
	Host          string
}

type CommandResult struct {
	OK       bool     `json:"ok"`
	ExitCode int      `json:"exit_code"`
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
	Argv     []string `json:"argv"`
	CWD      string   `json:"cwd"`
}

type Runner struct {
	cfg configpkg.Config
}

func NewRunner(cfg configpkg.Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context, opts RawRunOptions) (CommandResult, error) {
	argv, err := tokenizeDockerArgs(opts.Args)
	if err != nil {
		return CommandResult{}, err
	}

	workspaceRoot, err := normalizeWorkspaceRoot(opts.WorkspaceRoot)
	if err != nil {
		return CommandResult{}, err
	}

	cwd, validated, err := validateDockerArgs(opts.CWD, workspaceRoot, argv)
	if err != nil {
		return CommandResult{}, err
	}

	return r.runArgv(ctx, cwd, strings.TrimSpace(opts.Host), nil, validated)
}

// RunArgv runs the docker subcommand expressed as a pre-tokenized argv slice.
// The CWD/WorkspaceRoot scoping is identical to Run; only the input format differs.
func (r *Runner) RunArgv(ctx context.Context, opts RawRunArgvOptions) (CommandResult, error) {
	if len(opts.Args) == 0 {
		return CommandResult{}, fmt.Errorf("docker args are required")
	}

	workspaceRoot, err := normalizeWorkspaceRoot(opts.WorkspaceRoot)
	if err != nil {
		return CommandResult{}, err
	}

	cwd, validated, err := validateDockerArgs(opts.CWD, workspaceRoot, append([]string(nil), opts.Args...))
	if err != nil {
		return CommandResult{}, err
	}

	return r.runArgv(ctx, cwd, strings.TrimSpace(opts.Host), nil, validated)
}

func (r *Runner) commandBinary() string {
	if command := strings.TrimSpace(r.cfg.Docker.Binary); command != "" {
		return command
	}
	return "docker"
}

func (r *Runner) effectiveHost(host string) string {
	if host = strings.TrimSpace(host); host != "" {
		return host
	}
	return strings.TrimSpace(r.cfg.Docker.Host)
}

func (r *Runner) runArgv(ctx context.Context, cwd, host string, env []string, args []string) (CommandResult, error) {
	return r.runArgvWithIO(ctx, cwd, host, env, args, nil, nil, nil)
}

func (r *Runner) runArgvWithIO(ctx context.Context, cwd, host string, env []string, args []string, stdin io.Reader, stdoutWriter, stderrWriter io.Writer) (CommandResult, error) {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return CommandResult{}, fmt.Errorf("ensure docker cwd: %w", err)
	}

	command := r.commandBinary()
	invocation := make([]string, 0, len(args)+3)
	invocation = append(invocation, "docker")
	commandArgs := make([]string, 0, len(args)+2)
	if host = r.effectiveHost(host); host != "" {
		invocation = append(invocation, "--host", host)
		commandArgs = append(commandArgs, "--host", host)
	}
	invocation = append(invocation, args...)
	commandArgs = append(commandArgs, args...)

	cmd := exec.CommandContext(ctx, command, commandArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if stdoutWriter != nil {
		cmd.Stdout = io.MultiWriter(stdoutWriter, &stdout)
	} else {
		cmd.Stdout = &stdout
	}
	if stderrWriter != nil {
		cmd.Stderr = io.MultiWriter(stderrWriter, &stderr)
	} else {
		cmd.Stderr = &stderr
	}

	result := CommandResult{
		OK:       true,
		ExitCode: 0,
		Argv:     invocation,
		CWD:      cwd,
	}

	if err := cmd.Run(); err != nil {
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.OK = false
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return CommandResult{}, fmt.Errorf("run docker: %w", err)
	}

	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	return result, nil
}

func tokenizeDockerArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("docker args are required")
	}

	argv, err := shlex.Split(raw, true)
	if err != nil {
		return nil, fmt.Errorf("parse docker args: %w", err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("docker args are required")
	}

	for _, token := range argv {
		switch token {
		case "|", "||", "&", "&&", ";", ">", ">>", "<", "<<":
			return nil, fmt.Errorf("shell operators are not supported in docker args")
		}
		if strings.Contains(token, "$(") || strings.Contains(token, "`") {
			return nil, fmt.Errorf("shell substitution is not supported in docker args")
		}
	}

	return argv, nil
}

func validateDockerArgs(cwd, workspaceRoot string, argv []string) (string, []string, error) {
	cwd, err := resolveScopedPath(workspaceRoot, cwd)
	if err != nil {
		return "", nil, fmt.Errorf("resolve docker cwd: %w", err)
	}

	index := 0
	for index < len(argv) {
		arg := argv[index]
		switch arg {
		case "-H", "--host":
			index += 2
			if index > len(argv) {
				return "", nil, fmt.Errorf("docker %s requires a value", arg)
			}
		default:
			if strings.HasPrefix(arg, "--host=") {
				index++
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return "", nil, fmt.Errorf("docker global option %q is not supported", arg)
			}
			goto subcommand
		}
	}

subcommand:
	if index >= len(argv) {
		return "", nil, fmt.Errorf("docker subcommand is required")
	}

	switch argv[index] {
	case "ps":
		return cwd, argv[index:], validateSimpleDockerArgs("ps", argv[index+1:])
	case "images":
		return cwd, argv[index:], validateSimpleDockerArgs("images", argv[index+1:])
	case "pull":
		return cwd, argv[index:], validatePullArgs(argv[index+1:])
	case "build":
		validated, err := validateBuildArgs(cwd, workspaceRoot, argv[index+1:])
		if err != nil {
			return "", nil, err
		}
		return cwd, append([]string{"build"}, validated...), nil
	case "create":
		validated, err := validateCreateArgs(cwd, workspaceRoot, argv[index+1:])
		if err != nil {
			return "", nil, err
		}
		return cwd, append([]string{"create"}, validated...), nil
	case "start":
		return cwd, argv[index:], validateContainerRefArgs("start", argv[index+1:])
	case "stop":
		return cwd, argv[index:], validateContainerRefArgs("stop", argv[index+1:])
	case "rm":
		return cwd, argv[index:], validateContainerRefArgs("rm", argv[index+1:])
	case "exec":
		return cwd, argv[index:], validateExecArgs(argv[index+1:])
	case "info":
		return cwd, argv[index:], validateSimpleDockerArgs("info", argv[index+1:])
	case "version":
		return cwd, argv[index:], validateSimpleDockerArgs("version", argv[index+1:])
	case "inspect":
		return cwd, argv[index:], validateInspectArgs(argv[index+1:])
	case "network":
		if len(argv[index:]) >= 2 && argv[index+1] == "ls" {
			return cwd, argv[index:], validateSimpleDockerArgs("network ls", argv[index+2:])
		}
	case "volume":
		if len(argv[index:]) >= 2 && argv[index+1] == "ls" {
			return cwd, argv[index:], validateSimpleDockerArgs("volume ls", argv[index+2:])
		}
	}

	return "", nil, fmt.Errorf("docker subcommand %q is not supported", strings.Join(argv[index:], " "))
}

func validateSimpleDockerArgs(name string, args []string) error {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--env-file") || strings.HasPrefix(arg, "--cidfile") || strings.HasPrefix(arg, "--iidfile") || strings.HasPrefix(arg, "--label-file") || strings.HasPrefix(arg, "--metadata-file") {
			return fmt.Errorf("docker option %q is not supported for %s", arg, name)
		}
	}
	return nil
}

func validatePullArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("docker pull requires an image")
	}
	images := 0
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		images++
	}
	if images == 0 {
		return fmt.Errorf("docker pull requires an image")
	}
	return nil
}

func validateContainerRefArgs(name string, args []string) error {
	refs := 0
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		refs++
	}
	if refs == 0 {
		return fmt.Errorf("docker %s requires a container", name)
	}
	return nil
}

func validateExecArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("docker exec requires a container")
	}

	operandIndex := -1
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--":
			if index+2 > len(args) {
				return fmt.Errorf("docker exec requires a command")
			}
			return nil
		case strings.HasPrefix(arg, "--env-file"):
			return fmt.Errorf("docker option %q is not supported for exec", arg)
		case strings.HasPrefix(arg, "-"):
			if dockerOptionTakesValue(arg, execOptionsWithValues) && index+1 < len(args) && !strings.Contains(arg, "=") {
				index++
			}
		default:
			operandIndex = index
			index = len(args)
		}
	}

	if operandIndex < 0 || operandIndex >= len(args) {
		return fmt.Errorf("docker exec requires a container")
	}
	if operandIndex+1 >= len(args) {
		return fmt.Errorf("docker exec requires a command")
	}
	return nil
}

func validateInspectArgs(args []string) error {
	refs := 0
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case strings.HasPrefix(arg, "--env-file"), strings.HasPrefix(arg, "--cidfile"):
			return fmt.Errorf("docker option %q is not supported for inspect", arg)
		case arg == "-f" || arg == "--format":
			if index+1 >= len(args) {
				return fmt.Errorf("docker inspect %s requires a value", arg)
			}
			index++ // skip value
		case strings.HasPrefix(arg, "--format="):
			// ok
		case arg == "--type":
			if index+1 >= len(args) {
				return fmt.Errorf("docker inspect --type requires a value")
			}
			index++ // skip value
		case strings.HasPrefix(arg, "--type="):
			// ok
		case arg == "-s" || arg == "--size":
			// ok, flag only
		case strings.HasPrefix(arg, "-"):
			// allow other flags passthrough
		default:
			refs++
		}
	}
	if refs == 0 {
		return fmt.Errorf("docker inspect requires at least one container or image reference")
	}
	return nil
}

func validateBuildArgs(cwd, workspaceRoot string, args []string) ([]string, error) {
	validated := make([]string, 0, len(args))
	contextIndex := -1

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-f" || arg == "--file":
			if index+1 >= len(args) {
				return nil, fmt.Errorf("docker build %s requires a value", arg)
			}
			resolved, err := resolveScopedPath(cwd, args[index+1])
			if err != nil {
				return nil, fmt.Errorf("docker build file %q is not allowed: %w", args[index+1], err)
			}
			validated = append(validated, arg, resolved)
			index++
		case strings.HasPrefix(arg, "--file="):
			value := strings.TrimPrefix(arg, "--file=")
			resolved, err := resolveScopedPath(cwd, value)
			if err != nil {
				return nil, fmt.Errorf("docker build file %q is not allowed: %w", value, err)
			}
			validated = append(validated, "--file="+resolved)
		case arg == "--":
			if index+1 >= len(args) {
				return nil, fmt.Errorf("docker build requires a context path")
			}
			contextIndex = len(validated)
			resolved, err := resolveScopedPath(cwd, args[index+1])
			if err != nil {
				return nil, fmt.Errorf("docker build context %q is not allowed: %w", args[index+1], err)
			}
			validated = append(validated, resolved)
			index = len(args)
		case strings.HasPrefix(arg, "--iidfile"), strings.HasPrefix(arg, "--metadata-file"):
			return nil, fmt.Errorf("docker option %q is not supported for build", arg)
		case strings.HasPrefix(arg, "-"):
			validated = append(validated, arg)
			if dockerOptionTakesValue(arg, buildOptionsWithValues) && index+1 < len(args) && !strings.Contains(arg, "=") {
				validated = append(validated, args[index+1])
				index++
			}
		default:
			contextIndex = len(validated)
			resolved, err := resolveScopedPath(cwd, arg)
			if err != nil {
				return nil, fmt.Errorf("docker build context %q is not allowed: %w", arg, err)
			}
			validated = append(validated, resolved)
		}
	}

	if contextIndex < 0 {
		return nil, fmt.Errorf("docker build requires a context path")
	}
	if len(validated) == 0 {
		return nil, fmt.Errorf("docker build requires a context path")
	}
	_ = workspaceRoot
	return validated, nil
}

func validateCreateArgs(cwd, workspaceRoot string, args []string) ([]string, error) {
	validated := make([]string, 0, len(args))
	operands := 0

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-v" || arg == "--volume":
			if index+1 >= len(args) {
				return nil, fmt.Errorf("docker create %s requires a value", arg)
			}
			value, err := normalizeVolumeArg(cwd, args[index+1])
			if err != nil {
				return nil, err
			}
			validated = append(validated, arg, value)
			index++
		case strings.HasPrefix(arg, "--volume="):
			value, err := normalizeVolumeArg(cwd, strings.TrimPrefix(arg, "--volume="))
			if err != nil {
				return nil, err
			}
			validated = append(validated, "--volume="+value)
		case arg == "--mount":
			if index+1 >= len(args) {
				return nil, fmt.Errorf("docker create --mount requires a value")
			}
			value, err := normalizeMountArg(cwd, args[index+1])
			if err != nil {
				return nil, err
			}
			validated = append(validated, arg, value)
			index++
		case strings.HasPrefix(arg, "--mount="):
			value, err := normalizeMountArg(cwd, strings.TrimPrefix(arg, "--mount="))
			if err != nil {
				return nil, err
			}
			validated = append(validated, "--mount="+value)
		case strings.HasPrefix(arg, "--env-file"), strings.HasPrefix(arg, "--cidfile"), strings.HasPrefix(arg, "--label-file"):
			return nil, fmt.Errorf("docker option %q is not supported for create", arg)
		case arg == "--":
			validated = append(validated, args[index:]...)
			index = len(args)
		case strings.HasPrefix(arg, "-"):
			validated = append(validated, arg)
			if dockerOptionTakesValue(arg, createOptionsWithValues) && index+1 < len(args) && !strings.Contains(arg, "=") {
				validated = append(validated, args[index+1])
				index++
			}
		default:
			operands++
			validated = append(validated, arg)
		}
	}

	if operands == 0 {
		return nil, fmt.Errorf("docker create requires an image")
	}
	_ = workspaceRoot
	return validated, nil
}

func dockerOptionTakesValue(arg string, options map[string]bool) bool {
	if options[arg] {
		return true
	}
	if !strings.HasPrefix(arg, "--") || !strings.Contains(arg, "=") {
		return false
	}
	name := strings.SplitN(arg, "=", 2)[0]
	return options[name]
}

var buildOptionsWithValues = map[string]bool{
	"-f":           true,
	"--file":       true,
	"-t":           true,
	"--tag":        true,
	"--build-arg":  true,
	"--target":     true,
	"--label":      true,
	"--network":    true,
	"--platform":   true,
	"--progress":   true,
	"--secret":     true,
	"--ssh":        true,
	"--output":     true,
	"--cache-from": true,
}

var createOptionsWithValues = map[string]bool{
	"--name":          true,
	"--network":       true,
	"--env":           true,
	"-e":              true,
	"--entrypoint":    true,
	"--workdir":       true,
	"-w":              true,
	"--hostname":      true,
	"--user":          true,
	"-u":              true,
	"--label":         true,
	"--mount":         true,
	"--volume":        true,
	"-v":              true,
	"--publish":       true,
	"-p":              true,
	"--publish-all":   false,
	"--restart":       true,
	"--platform":      true,
	"--pull":          true,
	"--stop-timeout":  true,
	"--stop-signal":   true,
	"--add-host":      true,
	"--tmpfs":         true,
	"--userns":        true,
	"--shm-size":      true,
	"--annotation":    true,
	"--network-alias": true,
}

var execOptionsWithValues = map[string]bool{
	"--env":         true,
	"-e":            true,
	"--user":        true,
	"-u":            true,
	"--workdir":     true,
	"-w":            true,
	"--detach-keys": true,
}

func normalizeVolumeArg(cwd, value string) (string, error) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return value, nil
	}

	source := strings.TrimSpace(parts[0])
	if source == "" || !looksLikeHostPath(source) {
		return value, nil
	}

	resolved, err := resolveScopedPath(cwd, source)
	if err != nil {
		return "", fmt.Errorf("docker create volume source %q is not allowed: %w", source, err)
	}
	parts[0] = resolved
	return strings.Join(parts, ":"), nil
}

func normalizeMountArg(cwd, value string) (string, error) {
	parts := strings.Split(value, ",")
	typeIndex := -1
	sourceIndex := -1
	for index, part := range parts {
		part = strings.TrimSpace(part)
		key, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "type":
			typeIndex = index
		case "src", "source":
			sourceIndex = index
			parts[index] = key + "=" + strings.TrimSpace(rawValue)
		}
	}

	mountType := "volume"
	if typeIndex >= 0 {
		_, rawValue, _ := strings.Cut(parts[typeIndex], "=")
		mountType = strings.ToLower(strings.TrimSpace(rawValue))
	}
	if mountType != "bind" || sourceIndex < 0 {
		return value, nil
	}

	key, source, _ := strings.Cut(parts[sourceIndex], "=")
	source = strings.TrimSpace(source)
	if !looksLikeHostPath(source) {
		return value, nil
	}
	resolved, err := resolveScopedPath(cwd, source)
	if err != nil {
		return "", fmt.Errorf("docker create mount source %q is not allowed: %w", source, err)
	}
	parts[sourceIndex] = key + "=" + resolved
	return strings.Join(parts, ","), nil
}

func looksLikeHostPath(value string) bool {
	value = strings.TrimSpace(value)
	switch {
	case value == "", value == "." || value == "..":
		return true
	case filepath.IsAbs(value):
		return true
	case strings.HasPrefix(filepath.ToSlash(value), "./"), strings.HasPrefix(filepath.ToSlash(value), "../"):
		return true
	case strings.Contains(filepath.ToSlash(value), "/"):
		return true
	default:
		return false
	}
}

func normalizeWorkspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func resolveScopedPath(base, target string) (string, error) {
	base, err := filepath.Abs(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("path is required")
	}

	var candidate string
	if filepath.IsAbs(target) {
		candidate = target
	} else {
		candidate = filepath.Join(base, filepath.FromSlash(target))
	}

	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)

	rel, err := filepath.Rel(base, absolute)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace root")
	}
	return absolute, nil
}

// ensureExecEnvDefaults returns -e flags for TERM=dumb and NO_COLOR=1 to
// suppress color output in non-interactive exec. The returned slice is
// inserted into the exec argv between "exec" and the container name.
func ensureExecEnvDefaults() []string {
	return []string{"-e", "TERM=dumb", "-e", "NO_COLOR=1"}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
