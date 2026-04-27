package agent

import (
	"sort"
	"strings"
)

// orderedUniqueStrings returns the concatenation of the input groups with
// duplicates removed and order preserved. Empty entries are dropped.
func orderedUniqueStrings(groups ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, group := range groups {
		for _, item := range group {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// trimStringValues removes blanks and duplicates and returns a sorted slice.
// nil is returned when no usable values remain.
func trimStringValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// normalizeProviderNames trims, deduplicates, and normalises provider names.
func normalizeProviderNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = normalizeProviderName(value)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// providerFallbackChain returns the fallback chain for a primary provider,
// optionally overridden. OpenNalvin's ProviderConfig does not expose a stored
// fallback list, so when fallbackOverride is empty this returns nil.
func providerFallbackChain(primary string, fallbackOverride []string) ([]string, error) {
	primary = normalizeProviderName(primary)
	if len(fallbackOverride) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(fallbackOverride))
	seen := map[string]struct{}{primary: {}}
	for _, name := range normalizeProviderNames(fallbackOverride) {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}
