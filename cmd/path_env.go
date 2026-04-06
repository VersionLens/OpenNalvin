package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

var defaultExecutableSearchPaths = []string{
	"/usr/local/bin",
}

func ensureExecutableSearchPath() {
	pathValue := augmentPathWithDefaults(os.Getenv("PATH"), defaultExecutableSearchPaths)
	if pathValue == "" {
		return
	}
	_ = os.Setenv("PATH", pathValue)
}

func augmentPathWithDefaults(pathValue string, extras []string) string {
	parts := make([]string, 0)
	seen := make(map[string]struct{})

	add := func(entry string) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return
		}
		key := filepath.Clean(entry)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, entry)
	}

	for _, entry := range strings.Split(pathValue, string(os.PathListSeparator)) {
		add(entry)
	}
	for _, entry := range extras {
		add(entry)
	}

	return strings.Join(parts, string(os.PathListSeparator))
}
