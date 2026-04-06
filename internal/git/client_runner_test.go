package git

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeGitArgsHandlesQuotes(t *testing.T) {
	argv, err := tokenizeGitArgs(`commit -m 'hello world'`)
	if err != nil {
		t.Fatalf("tokenize git args: %v", err)
	}

	want := []string{"commit", "-m", "hello world"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("unexpected argv: got %#v want %#v", argv, want)
	}
}

func TestTokenizeGitArgsRejectsShellOperators(t *testing.T) {
	if _, err := tokenizeGitArgs(`status && echo nope`); err == nil || !strings.Contains(err.Error(), "shell operators") {
		t.Fatalf("expected shell operator error, got %v", err)
	}
}

func TestValidateClientArgsRejectsUnsupportedSubcommand(t *testing.T) {
	root := t.TempDir()

	_, _, err := validateClientArgs([]string{"init"}, root, []string{root})
	if err == nil || !strings.Contains(err.Error(), `subcommand "init" is not supported`) {
		t.Fatalf("expected unsupported subcommand error, got %v", err)
	}
}

func TestValidateClientArgsAllowsScopedDashC(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "demo")

	cwd, argv, err := validateClientArgs([]string{"-C", "demo", "status"}, root, []string{root})
	if err != nil {
		t.Fatalf("validate client args: %v", err)
	}
	if cwd != repoDir {
		t.Fatalf("unexpected cwd: got %q want %q", cwd, repoDir)
	}
	if !reflect.DeepEqual(argv, []string{"status"}) {
		t.Fatalf("unexpected argv: %#v", argv)
	}
}

func TestValidateClientArgsRejectsDashCEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	_, _, err := validateClientArgs([]string{"-C", filepath.Join(outside, "repo"), "status"}, root, []string{root})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected -C scope error, got %v", err)
	}
}

func TestValidateClientArgsRejectsCloneSourceOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	_, _, err := validateClientArgs([]string{"clone", filepath.Join(outside, "other.git"), "demo"}, root, []string{root})
	if err == nil || !strings.Contains(err.Error(), "clone source") {
		t.Fatalf("expected clone source scope error, got %v", err)
	}
}
