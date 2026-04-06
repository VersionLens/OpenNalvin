package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestAugmentPathWithDefaultsAddsUsrLocalBinOnce(t *testing.T) {
	pathValue := strings.Join([]string{"/usr/bin", "/bin"}, string(os.PathListSeparator))

	got := augmentPathWithDefaults(pathValue, []string{"/usr/local/bin", "/usr/bin"})

	parts := strings.Split(got, string(os.PathListSeparator))
	if len(parts) != 3 {
		t.Fatalf("expected 3 path entries, got %v", parts)
	}
	if parts[0] != "/usr/bin" || parts[1] != "/bin" || parts[2] != "/usr/local/bin" {
		t.Fatalf("unexpected path order: %v", parts)
	}
}
