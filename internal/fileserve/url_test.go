package fileserve

import (
	"testing"

	"github.com/versionlens/OpenNalvin/internal/config"
)

func TestBuildWorkspaceFileURLUsesStableWorkspaceRoute(t *testing.T) {
	t.Parallel()

	url, base, err := BuildWorkspaceFileURL(config.Config{
		Server: config.ServerConfig{Addr: ":4210"},
	}, "alpha", "downloads/videos/Ghosts S05E14.mp4", "")
	if err != nil {
		t.Fatalf("build file url: %v", err)
	}

	if base != "http://127.0.0.1:4210" {
		t.Fatalf("base url = %q, want http://127.0.0.1:4210", base)
	}
	if url != "http://127.0.0.1:4210/files/alpha/downloads/videos/Ghosts%20S05E14.mp4" {
		t.Fatalf("url = %q", url)
	}
}

func TestBuildWorkspaceFileURLSupportsBaseOverride(t *testing.T) {
	t.Parallel()

	url, base, err := BuildWorkspaceFileURL(config.Config{
		Server: config.ServerConfig{Addr: "0.0.0.0:4210"},
	}, "alpha", "downloads/video.mp4", "https://media.example.test/root")
	if err != nil {
		t.Fatalf("build file url: %v", err)
	}

	if base != "https://media.example.test/root" {
		t.Fatalf("base url = %q", base)
	}
	if url != "https://media.example.test/root/files/alpha/downloads/video.mp4" {
		t.Fatalf("url = %q", url)
	}
}

func TestBuildWorkspaceFileURLRejectsTraversal(t *testing.T) {
	t.Parallel()

	if _, _, err := BuildWorkspaceFileURL(config.Config{
		Server: config.ServerConfig{Addr: ":4210"},
	}, "alpha", "../secret.mp4", ""); err == nil {
		t.Fatal("expected traversal to fail")
	}
}
