package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

type ServiceRunner interface {
	RunUploadPack(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error
	RunReceivePack(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error
}

type LocalServiceRunner struct {
	cfg configpkg.GitConfig
}

func NewLocalServiceRunner(cfg configpkg.GitConfig) *LocalServiceRunner {
	return &LocalServiceRunner{cfg: cfg}
}

func (r *LocalServiceRunner) RunUploadPack(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return r.runService(ctx, repoPath, env, stdin, stdout, stderr, "upload-pack")
}

func (r *LocalServiceRunner) RunReceivePack(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return r.runService(ctx, repoPath, env, stdin, stdout, stderr, "receive-pack")
}

func (r *LocalServiceRunner) runService(ctx context.Context, repoPath string, env []string, stdin io.Reader, stdout, stderr io.Writer, service string) error {
	command, args, err := r.resolveServiceCommand(service, repoPath)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), env...)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &execError{
				error: fmt.Errorf("%s failed: %w", commandDisplay(command, args), err),
				code:  exitErr.ExitCode(),
			}
		}
		return fmt.Errorf("%s failed: %w", commandDisplay(command, args), err)
	}
	return nil
}

func (r *LocalServiceRunner) resolveServiceCommand(service, repoPath string) (string, []string, error) {
	switch service {
	case "upload-pack":
		if path := strings.TrimSpace(r.cfg.Binaries.UploadPack); path != "" {
			return path, []string{repoPath}, nil
		}
	case "receive-pack":
		if path := strings.TrimSpace(r.cfg.Binaries.ReceivePack); path != "" {
			return path, []string{repoPath}, nil
		}
	default:
		return "", nil, fmt.Errorf("unsupported git service %q", service)
	}

	if path := strings.TrimSpace(r.cfg.Binaries.Git); path != "" {
		return path, []string{service, repoPath}, nil
	}

	return "git", []string{service, repoPath}, nil
}

func commandDisplay(command string, args []string) string {
	if len(args) == 0 {
		return command
	}
	return command + " " + strings.Join(args, " ")
}
