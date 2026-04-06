package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestBundledDefaultConfigIncludesNewsSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	viper.Reset()
	t.Cleanup(viper.Reset)

	data, err := os.ReadFile(filepath.Join("..", "..", "cmd", "config.default.yaml"))
	if err != nil {
		t.Fatalf("read bundled default config: %v", err)
	}
	SetDefaultConfig(bytes.NewReader(data))

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := cfg.Reddit.BaseURL; got != "https://www.reddit.com" {
		t.Fatalf("unexpected reddit base url: %q", got)
	}
	if len(cfg.RSS.Feeds["ai"]) == 0 || cfg.RSS.Feeds["ai"][0].Name != "openai-blog" {
		t.Fatalf("expected bundled ai rss feeds, got %#v", cfg.RSS.Feeds["ai"])
	}
	if len(cfg.RSS.Feeds["tech"]) == 0 || cfg.RSS.Feeds["tech"][0].URL == "" {
		t.Fatalf("expected bundled tech rss feeds, got %#v", cfg.RSS.Feeds["tech"])
	}
	if got := cfg.Reddit.Subreddits["world"]; len(got) == 0 || got[0] != "worldnews" {
		t.Fatalf("expected bundled world subreddits, got %#v", got)
	}
	if got := cfg.Reddit.Subreddits["finance"]; len(got) == 0 || got[0] != "finance" {
		t.Fatalf("expected bundled finance subreddits, got %#v", got)
	}
}
