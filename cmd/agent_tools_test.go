package cmd

import (
	"reflect"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/spf13/cobra"
)

func TestParseToolRunArgs(t *testing.T) {
	parsed, err := parseToolRunArgs([]string{
		"--run-id", "run_123",
		"web_fetch_get",
		"--url", "https://example.com",
		"--dry-run",
		"--tag", "alpha",
		"--tag=beta",
		"--json-args", `{"headers":{"X-Test":"1"}}`,
	})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}

	if parsed.runID != "run_123" {
		t.Fatalf("unexpected run id: %q", parsed.runID)
	}
	if parsed.toolID != "web_fetch_get" {
		t.Fatalf("unexpected tool id: %q", parsed.toolID)
	}
	if parsed.jsonArgs != `{"headers":{"X-Test":"1"}}` {
		t.Fatalf("unexpected json args: %q", parsed.jsonArgs)
	}
	if got, want := parsed.flagValues["url"], []string{"https://example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected url flags: got %v want %v", got, want)
	}
	if got, want := parsed.flagValues["dry_run"], []string{"true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected dry_run flags: got %v want %v", got, want)
	}
	if got, want := parsed.flagValues["tag"], []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tag flags: got %v want %v", got, want)
	}
}

func TestBuildToolInput(t *testing.T) {
	schema := agent.ToolSchema{
		Type: "object",
		Properties: map[string]any{
			"count":   map[string]any{"type": "integer"},
			"dry_run": map[string]any{"type": "boolean"},
			"tag": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}

	payload, err := buildToolInput(schema, `{"headers":{"X-Test":"1"}}`, map[string][]string{
		"count":   {"2"},
		"dry_run": {"true"},
		"tag":     {"alpha", "beta"},
	})
	if err != nil {
		t.Fatalf("build tool input: %v", err)
	}

	if payload["count"] != int64(2) {
		t.Fatalf("unexpected integer coercion: %#v", payload["count"])
	}
	if payload["dry_run"] != true {
		t.Fatalf("unexpected boolean coercion: %#v", payload["dry_run"])
	}
	tags, ok := payload["tag"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("unexpected array coercion: %#v", payload["tag"])
	}
	headers, ok := payload["headers"].(map[string]any)
	if !ok || headers["X-Test"] != "1" {
		t.Fatalf("unexpected merged json args: %#v", payload["headers"])
	}
}

func TestStripLeadingInheritedFlags(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().StringP("workspace", "w", "", "")
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().String("config", "", "")

	run := &cobra.Command{Use: "run"}
	root.AddCommand(run)

	got := stripLeadingInheritedFlags(run, []string{
		"--workspace", "default",
		"--json",
		"--config=config.yaml",
		"web_fetch_get",
		"--url", "https://example.com",
	})
	want := []string{
		"web_fetch_get",
		"--url", "https://example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected stripped args: got %v want %v", got, want)
	}
}

func TestStripLeadingInheritedFlagsSupportsShorthand(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().StringP("workspace", "w", "", "")

	run := &cobra.Command{Use: "run"}
	root.AddCommand(run)

	got := stripLeadingInheritedFlags(run, []string{
		"-w", "default",
		"web_fetch_get",
		"--url", "https://example.com",
	})
	want := []string{
		"web_fetch_get",
		"--url", "https://example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected stripped shorthand args: got %v want %v", got, want)
	}
}
