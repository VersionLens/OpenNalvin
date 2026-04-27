package git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	gliderssh "github.com/gliderlabs/ssh"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	xssh "golang.org/x/crypto/ssh"
)

type SSHServer struct {
	cfg      configpkg.Config
	logger   *slog.Logger
	runner   ServiceRunner
	server   *gliderssh.Server
	userKeys map[string][][]byte

	mu      sync.Mutex
	started bool
}

func NewSSHServer(cfg configpkg.Config, logger *slog.Logger, runner ServiceRunner) (*SSHServer, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if runner == nil {
		runner = NewLocalServiceRunner(cfg.Git)
	}

	userKeys, err := parseAuthorizedKeys(cfg.Git.Users)
	if err != nil {
		return nil, err
	}
	signer, err := ensureHostSigner(cfg.Git.SSH.HostKeyPath)
	if err != nil {
		return nil, err
	}

	s := &SSHServer{
		cfg:      cfg,
		logger:   logger,
		runner:   runner,
		userKeys: userKeys,
	}
	s.server = &gliderssh.Server{
		Addr:    cfg.Git.SSH.Addr,
		Handler: s.handleSession,
		PublicKeyHandler: func(ctx gliderssh.Context, key gliderssh.PublicKey) bool {
			return s.authorizeKey(ctx.User(), key)
		},
		PtyCallback: func(ctx gliderssh.Context, _ gliderssh.Pty) bool {
			return false
		},
		SessionRequestCallback: func(sess gliderssh.Session, requestType string) bool {
			return requestType == "exec" || requestType == "env"
		},
	}
	s.server.AddHostKey(signer)

	return s, nil
}

func (s *SSHServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}

	listener, err := net.Listen("tcp", s.cfg.Git.SSH.Addr)
	if err != nil {
		return fmt.Errorf("listen ssh git service: %w", err)
	}

	s.started = true
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, gliderssh.ErrServerClosed) {
			s.logger.Error("git ssh server failed", "error", err)
		}
	}()

	s.logger.Info("git ssh service listening", "addr", s.cfg.Git.SSH.Addr, "repo_root", s.cfg.Git.RepoRoot)
	return nil
}

func (s *SSHServer) Close(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.started = false
	s.mu.Unlock()
	if !started {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *SSHServer) authorizeKey(user string, key gliderssh.PublicKey) bool {
	allowedKeys, ok := s.userKeys[strings.TrimSpace(user)]
	if !ok {
		return false
	}
	marshaled := key.Marshal()
	for _, candidate := range allowedKeys {
		if string(candidate) == string(marshaled) {
			return true
		}
	}
	return false
}

func (s *SSHServer) handleSession(session gliderssh.Session) {
	command := session.Command()
	service, repoArg, err := parseSSHGitCommand(command)
	if err != nil {
		_, _ = io.WriteString(session.Stderr(), err.Error()+"\n")
		_ = session.Exit(1)
		return
	}

	user := strings.TrimSpace(session.User())
	repoName, repoPath, auth, err := LookupRepoAuth(s.cfg, repoArg)
	if err != nil {
		_, _ = io.WriteString(session.Stderr(), err.Error()+"\n")
		_ = session.Exit(1)
		return
	}
	if _, err := os.Stat(repoPath); err != nil {
		_, _ = io.WriteString(session.Stderr(), "repository not found\n")
		_ = session.Exit(1)
		return
	}

	switch service {
	case "upload-pack":
		if !auth.CanRead(user) {
			_, _ = io.WriteString(session.Stderr(), "permission denied\n")
			_ = session.Exit(1)
			return
		}
	case "receive-pack":
		if !auth.CanWrite(user) {
			_, _ = io.WriteString(session.Stderr(), "permission denied\n")
			_ = session.Exit(1)
			return
		}
	default:
		_, _ = io.WriteString(session.Stderr(), "unsupported service\n")
		_ = session.Exit(1)
		return
	}

	env := forwardedGitEnv(session.Environ())
	s.logger.Info("git ssh request", "service", service, "repo", repoName, "user", user, "remote", session.RemoteAddr().String())

	var runErr error
	switch service {
	case "upload-pack":
		runErr = s.runner.RunUploadPack(session.Context(), repoPath, env, session, session, session.Stderr())
	case "receive-pack":
		runErr = s.runner.RunReceivePack(session.Context(), repoPath, env, session, session, session.Stderr())
	}

	if runErr != nil {
		var exitErr *execError
		if errors.As(runErr, &exitErr) {
			_ = session.Exit(exitErr.Code())
			return
		}
		_, _ = io.WriteString(session.Stderr(), runErr.Error()+"\n")
		_ = session.Exit(1)
		return
	}

	_ = session.Exit(0)
}

func parseSSHGitCommand(args []string) (string, string, error) {
	switch {
	case len(args) == 2 && (args[0] == "git-upload-pack" || args[0] == "git-receive-pack"):
		return strings.TrimPrefix(args[0], "git-"), args[1], nil
	case len(args) == 3 && args[0] == "git" && (args[1] == "upload-pack" || args[1] == "receive-pack"):
		return args[1], args[2], nil
	default:
		return "", "", fmt.Errorf("only git-upload-pack and git-receive-pack are supported")
	}
}

func forwardedGitEnv(env []string) []string {
	out := make([]string, 0, 1)
	for _, item := range env {
		if strings.HasPrefix(item, "GIT_PROTOCOL=") {
			out = append(out, item)
			break
		}
	}
	return out
}

func parseAuthorizedKeys(users map[string]configpkg.GitUserConfig) (map[string][][]byte, error) {
	out := make(map[string][][]byte, len(users))
	for user, cfg := range users {
		user = strings.TrimSpace(user)
		if user == "" {
			continue
		}
		keys := make([][]byte, 0, len(cfg.PublicKeys))
		for _, raw := range cfg.PublicKeys {
			key, _, _, _, err := xssh.ParseAuthorizedKey([]byte(strings.TrimSpace(raw)))
			if err != nil {
				return nil, fmt.Errorf("parse public key for git user %q: %w", user, err)
			}
			keys = append(keys, key.Marshal())
		}
		out[user] = keys
	}
	return out, nil
}

func ensureHostSigner(path string) (xssh.Signer, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("git ssh host key path is required")
	}

	if privateKey, err := os.ReadFile(path); err == nil {
		return xssh.ParsePrivateKey(privateKey)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read ssh host key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create ssh host key directory: %w", err)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ssh host key: %w", err)
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal ssh host key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	if err := os.WriteFile(path, privateKeyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write ssh host key: %w", err)
	}

	return xssh.ParsePrivateKey(privateKeyPEM)
}

// execError allows the SSH session layer to preserve child process exit codes.
type execError struct {
	error
	code int
}

func (e *execError) Code() int { return e.code }
