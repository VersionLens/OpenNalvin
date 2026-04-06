package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	mcptransport "github.com/mark3labs/mcp-go/client/transport"
)

func TestFileTokenStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := newFileTokenStore(dir, "test-server")

	ctx := context.Background()

	// Initially no token.
	_, err := store.GetToken(ctx)
	if !errors.Is(err, mcptransport.ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}

	// Save a token.
	token := &mcptransport.Token{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		RefreshToken: "refresh-456",
		ExpiresIn:    3600,
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := store.SaveToken(ctx, token); err != nil {
		t.Fatalf("save token: %v", err)
	}

	// Read it back.
	got, err := store.GetToken(ctx)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got.AccessToken != "access-123" {
		t.Fatalf("expected access token %q, got %q", "access-123", got.AccessToken)
	}
	if got.RefreshToken != "refresh-456" {
		t.Fatalf("expected refresh token %q, got %q", "refresh-456", got.RefreshToken)
	}

	// New store instance reads the same file.
	store2 := newFileTokenStore(dir, "test-server")
	got2, err := store2.GetToken(ctx)
	if err != nil {
		t.Fatalf("get token from second store: %v", err)
	}
	if got2.AccessToken != "access-123" {
		t.Fatalf("expected access token from second store %q, got %q", "access-123", got2.AccessToken)
	}
}

func TestFileTokenStoreEmptyFile(t *testing.T) {
	dir := t.TempDir()
	store := newFileTokenStore(dir, "empty")
	ctx := context.Background()

	// Save empty token (no access token).
	if err := store.SaveToken(ctx, &mcptransport.Token{}); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err := store.GetToken(ctx)
	if !errors.Is(err, mcptransport.ErrNoToken) {
		t.Fatalf("expected ErrNoToken for empty access token, got %v", err)
	}
}

func TestFileTokenStoreCanceledContext(t *testing.T) {
	dir := t.TempDir()
	store := newFileTokenStore(dir, "cancel-test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.GetToken(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	err = store.SaveToken(ctx, &mcptransport.Token{AccessToken: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
