package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	shlex "github.com/anmitsu/go-shlex"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

type ClientRunOptions struct {
	CWD          string
	Args         string
	AllowedRoots []string
}

type ClientCommandResult struct {
	OK       bool     `json:"ok"`
	ExitCode int      `json:"exit_code"`
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
	Argv     []string `json:"argv"`
	CWD      string   `json:"cwd"`
}

type ClientRunner struct {
	cfg configpkg.Config
}

func NewClientRunner(cfg configpkg.Config) *ClientRunner {
	return &ClientRunner{cfg: cfg}
}

func (r *ClientRunner) Run(ctx context.Context, opts ClientRunOptions) (ClientCommandResult, error) {
	argv, err := tokenizeGitArgs(opts.Args)
	if err != nil {
		return ClientCommandResult{}, err
	}

	effectiveCWD, validatedArgv, err := validateClientArgs(argv, opts.CWD, opts.AllowedRoots)
	if err != nil {
		return ClientCommandResult{}, err
	}
	if err := os.MkdirAll(effectiveCWD, 0o755); err != nil {
		return ClientCommandResult{}, fmt.Errorf("ensure git cwd: %w", err)
	}

	env, cleanup, err := r.commandEnv()
	if err != nil {
		return ClientCommandResult{}, err
	}
	defer cleanup()

	command := strings.TrimSpace(r.cfg.Git.Binaries.Git)
	if command == "" {
		command = "git"
	}

	cmd := exec.CommandContext(ctx, command, validatedArgv...)
	cmd.Dir = effectiveCWD
	cmd.Env = append(os.Environ(), env...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := ClientCommandResult{
		OK:       true,
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Argv:     append([]string{"git"}, validatedArgv...),
		CWD:      effectiveCWD,
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
		return ClientCommandResult{}, fmt.Errorf("run git: %w", err)
	}

	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	return result, nil
}

func (r *ClientRunner) commandEnv() ([]string, func(), error) {
	env := DefaultClientEnv(r.cfg)

	privateKeyPath := strings.TrimSpace(r.cfg.Git.DefaultClient.PrivateKeyPath)
	hostKeyPath := strings.TrimSpace(r.cfg.Git.SSH.HostKeyPath)
	if privateKeyPath == "" || hostKeyPath == "" {
		return env, func() {}, nil
	}

	knownHostsPath, err := EnsureKnownHostsFile(r.cfg)
	if err != nil {
		return nil, nil, err
	}

	sshCommand := DefaultClientSSHCommand(privateKeyPath, knownHostsPath)
	env = append(env, "GIT_SSH_COMMAND="+sshCommand)

	return env, func() {}, nil
}

func tokenizeGitArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("git args are required")
	}

	argv, err := shlex.Split(raw, true)
	if err != nil {
		return nil, fmt.Errorf("parse git args: %w", err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("git args are required")
	}

	for _, token := range argv {
		switch token {
		case "|", "||", "&", "&&", ";", ">", ">>", "<", "<<":
			return nil, fmt.Errorf("shell operators are not supported in git args")
		}
		if strings.Contains(token, "$(") || strings.Contains(token, "`") {
			return nil, fmt.Errorf("shell substitution is not supported in git args")
		}
	}

	return argv, nil
}

func validateClientArgs(argv []string, cwd string, allowedRoots []string) (string, []string, error) {
	roots, err := normalizeAllowedRoots(allowedRoots)
	if err != nil {
		return "", nil, err
	}

	effectiveCWD, err := resolveScopedPath(cwd, ".", roots)
	if err != nil {
		return "", nil, fmt.Errorf("resolve git cwd: %w", err)
	}

	prefixArgs := make([]string, 0, len(argv))
	index := 0
	for index < len(argv) {
		arg := argv[index]
		switch {
		case arg == "-C":
			if index+1 >= len(argv) {
				return "", nil, fmt.Errorf("git -C requires a path")
			}
			effectiveCWD, err = resolveScopedPath(effectiveCWD, argv[index+1], roots)
			if err != nil {
				return "", nil, fmt.Errorf("git -C path %q is not allowed: %w", argv[index+1], err)
			}
			index += 2
		case strings.HasPrefix(arg, "-C") && len(arg) > 2:
			effectiveCWD, err = resolveScopedPath(effectiveCWD, arg[2:], roots)
			if err != nil {
				return "", nil, fmt.Errorf("git -C path %q is not allowed: %w", arg[2:], err)
			}
			index++
		case arg == "-c":
			if index+1 >= len(argv) {
				return "", nil, fmt.Errorf("git -c requires a key=value pair")
			}
			prefixArgs = append(prefixArgs, argv[index], argv[index+1])
			index += 2
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			prefixArgs = append(prefixArgs, arg)
			index++
		case arg == "--no-pager" || arg == "--paginate" || arg == "--literal-pathspecs" || arg == "--no-optional-locks":
			prefixArgs = append(prefixArgs, arg)
			index++
		case strings.HasPrefix(arg, "--git-dir"), strings.HasPrefix(arg, "--work-tree"), strings.HasPrefix(arg, "--namespace"), strings.HasPrefix(arg, "--exec-path"), strings.HasPrefix(arg, "--config-env"):
			return "", nil, fmt.Errorf("git global option %q is not supported", arg)
		case strings.HasPrefix(arg, "-"):
			return "", nil, fmt.Errorf("git global option %q is not supported", arg)
		case strings.Contains(arg, "="):
			return "", nil, fmt.Errorf("environment-style prefixes are not supported in git args")
		default:
			goto subcommand
		}
	}

subcommand:
	if index >= len(argv) {
		return "", nil, fmt.Errorf("git subcommand is required")
	}

	subcommand := argv[index]
	if !allowedGitSubcommands[subcommand] {
		return "", nil, fmt.Errorf("git subcommand %q is not supported", subcommand)
	}

	if err := validateSubcommandArgs(subcommand, effectiveCWD, roots, argv[index+1:]); err != nil {
		return "", nil, err
	}

	return effectiveCWD, append(prefixArgs, argv[index:]...), nil
}

var allowedGitSubcommands = map[string]bool{
	"status":     true,
	"diff":       true,
	"add":        true,
	"restore":    true,
	"reset":      true,
	"commit":     true,
	"log":        true,
	"show":       true,
	"branch":     true,
	"switch":     true,
	"checkout":   true,
	"fetch":      true,
	"pull":       true,
	"push":       true,
	"remote":     true,
	"rev-parse":  true,
	"merge-base": true,
	"ls-remote":  true,
	"clone":      true,
}

func validateSubcommandArgs(subcommand, cwd string, roots []string, args []string) error {
	switch subcommand {
	case "clone":
		return validateCloneArgs(cwd, roots, args)
	default:
		for _, arg := range args {
			if strings.HasPrefix(arg, "--git-dir") || strings.HasPrefix(arg, "--work-tree") {
				return fmt.Errorf("git option %q is not supported", arg)
			}
			if !looksLikeScopedPathArgument(arg) {
				continue
			}
			if _, err := resolveScopedPath(cwd, arg, roots); err != nil {
				return fmt.Errorf("git path %q is not allowed: %w", arg, err)
			}
		}
		return nil
	}
}

func validateCloneArgs(cwd string, roots []string, args []string) error {
	operands := make([]string, 0, 2)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "--git-dir") || strings.HasPrefix(arg, "--work-tree") {
			return fmt.Errorf("git option %q is not supported", arg)
		}
		if arg == "--" {
			operands = append(operands, args[index+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") {
			if strings.Contains(arg, "=") {
				continue
			}
			if cloneOptionsWithValues[arg] && index+1 < len(args) {
				index++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if cloneOptionsWithValues[arg] && index+1 < len(args) {
				index++
			}
			continue
		}
		operands = append(operands, arg)
	}

	if len(operands) == 0 {
		return fmt.Errorf("git clone requires a repository argument")
	}

	source := operands[0]
	if looksLikeLocalCloneSource(source) {
		if _, err := resolveScopedPath(cwd, source, roots); err != nil {
			return fmt.Errorf("git clone source %q is not allowed: %w", source, err)
		}
	}

	if len(operands) > 1 {
		if _, err := resolveScopedPath(cwd, operands[1], roots); err != nil {
			return fmt.Errorf("git clone destination %q is not allowed: %w", operands[1], err)
		}
	}

	return nil
}

var cloneOptionsWithValues = map[string]bool{
	"-b":                  true,
	"--branch":            true,
	"-c":                  true,
	"--config":            true,
	"--depth":             true,
	"--filter":            true,
	"-j":                  true,
	"--jobs":              true,
	"-o":                  true,
	"--origin":            true,
	"--reference":         true,
	"--reference-if-able": true,
	"--separate-git-dir":  true,
	"--server-option":     true,
	"--shallow-exclude":   true,
	"--shallow-since":     true,
	"--template":          true,
	"--upload-pack":       true,
}

func looksLikeLocalCloneSource(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "://") || strings.HasPrefix(lower, "git@") {
		return false
	}
	return looksLikeScopedPathArgument(value) || !strings.ContainsAny(value, ":@")
}

func looksLikeScopedPathArgument(value string) bool {
	value = strings.TrimSpace(value)
	switch {
	case value == "", strings.HasPrefix(value, "-"):
		return false
	case filepath.IsAbs(value):
		return true
	case value == ".", value == "..":
		return true
	case strings.HasPrefix(value, "."+string(filepath.Separator)):
		return true
	case strings.HasPrefix(filepath.ToSlash(value), "./"), strings.HasPrefix(filepath.ToSlash(value), "../"):
		return true
	case strings.Contains(filepath.ToSlash(value), "/../"):
		return true
	default:
		return false
	}
}

func normalizeAllowedRoots(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		absolute = filepath.Clean(absolute)
		if slices.Contains(out, absolute) {
			continue
		}
		out = append(out, absolute)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one allowed git root is required")
	}
	return out, nil
}

func resolveScopedPath(base, target string, roots []string) (string, error) {
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

	for _, root := range roots {
		rel, err := filepath.Rel(root, absolute)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return absolute, nil
		}
	}

	return "", fmt.Errorf("path escapes allowed roots")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func SplitSSHAddr(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", "", fmt.Errorf("invalid ssh addr %q: %w", addr, err)
	}
	return host, port, nil
}
