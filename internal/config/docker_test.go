package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadDockerDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString(`
docker:
  host: ""
  binary: ""
  default_image: "nalvin/dev"
`))
	t.Cleanup(viper.Reset)

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Docker.Host != "" {
		t.Fatalf("expected empty default docker host, got %q", cfg.Docker.Host)
	}
	if cfg.Docker.Binary != "" {
		t.Fatalf("expected empty default docker binary, got %q", cfg.Docker.Binary)
	}
	if cfg.Docker.DefaultImage != "nalvin/dev" {
		t.Fatalf("expected default docker image nalvin/dev, got %q", cfg.Docker.DefaultImage)
	}
}

func TestLoadDockerEnvOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NALVIN_DOCKER_HOST", "tcp://docker.example:2375")
	t.Setenv("NALVIN_DOCKER_BINARY", "~/bin/docker")
	t.Setenv("NALVIN_DOCKER_DEFAULT_IMAGE", "busybox:latest")

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("docker:\n  default_image: nalvin/dev\n"))
	t.Cleanup(viper.Reset)

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Docker.Host != "tcp://docker.example:2375" {
		t.Fatalf("expected docker host override, got %q", cfg.Docker.Host)
	}
	if cfg.Docker.Binary != filepath.Join(home, "bin", "docker") {
		t.Fatalf("expected expanded docker binary path, got %q", cfg.Docker.Binary)
	}
	if cfg.Docker.DefaultImage != "busybox:latest" {
		t.Fatalf("expected docker default image override, got %q", cfg.Docker.DefaultImage)
	}
}

func TestLoadDockerConfigFileOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".nalvin")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
docker:
  host: tcp://127.0.0.1:2375
  binary: ~/custom/docker
  default_image: ghcr.io/example/custom:dev
`), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	viper.Reset()
	SetDefaultConfig(bytes.NewBufferString("docker:\n  default_image: nalvin/dev\n"))
	t.Cleanup(viper.Reset)

	if err := Init(""); err != nil {
		t.Fatalf("init config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Docker.Host != "tcp://127.0.0.1:2375" {
		t.Fatalf("expected docker host from config file, got %q", cfg.Docker.Host)
	}
	if cfg.Docker.Binary != filepath.Join(home, "custom", "docker") {
		t.Fatalf("expected expanded docker binary from config file, got %q", cfg.Docker.Binary)
	}
	if cfg.Docker.DefaultImage != "ghcr.io/example/custom:dev" {
		t.Fatalf("expected docker image from config file, got %q", cfg.Docker.DefaultImage)
	}
}
