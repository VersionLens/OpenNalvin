package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	mcptransport "github.com/mark3labs/mcp-go/client/transport"
)

type fileTokenStore struct {
	path string
	mu   sync.RWMutex
}

func newFileTokenStore(dir, serverName string) *fileTokenStore {
	return &fileTokenStore{
		path: filepath.Join(dir, serverName+".json"),
	}
}

func oauthTokenDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nalvin", "oauth-tokens")
}

func (s *fileTokenStore) GetToken(ctx context.Context) (*mcptransport.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, mcptransport.ErrNoToken
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, mcptransport.ErrNoToken
	}

	var token mcptransport.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, mcptransport.ErrNoToken
	}
	if token.AccessToken == "" {
		return nil, mcptransport.ErrNoToken
	}
	return &token, nil
}

func (s *fileTokenStore) SaveToken(ctx context.Context, token *mcptransport.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
