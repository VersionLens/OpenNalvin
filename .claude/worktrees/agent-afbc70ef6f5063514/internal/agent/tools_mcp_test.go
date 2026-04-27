package agent

import (
	"regexp"
	"testing"
)

func TestMCPToolIDEncodesSafeCanonicalIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		server string
		tool   string
		want   string
	}{
		{server: "exa", tool: "web_search_exa", want: "mcp__exa__web_search_exa"},
		{server: "exa", tool: "crawling_exa", want: "mcp__exa__crawling_exa"},
		{server: "exa", tool: "get_code_context_exa", want: "mcp__exa__get_code_context_exa"},
		{server: "local-docs", tool: "tool.name", want: "mcp__local-docs__x746f6f6c2e6e616d65"},
		{server: "api/v2", tool: "lookup:primary", want: "mcp__x6170692f7632__x6c6f6f6b75703a7072696d617279"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.server+"_"+tc.tool, func(t *testing.T) {
			t.Parallel()
			if got := mcpToolID(tc.server, tc.tool); got != tc.want {
				t.Fatalf("unexpected mcp tool id: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMCPToolIDMatchesProviderSafePattern(t *testing.T) {
	t.Parallel()

	safePattern := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	ids := []string{
		mcpToolID("exa", "web_search_exa"),
		mcpToolID("local-docs", "tool.name"),
		mcpToolID("api/v2", "lookup:primary"),
		mcpToolID("strange server", "tool@beta"),
	}

	for _, id := range ids {
		if !safePattern.MatchString(id) {
			t.Fatalf("expected %q to match provider-safe pattern", id)
		}
	}
}

func TestMCPToolIDIsUniqueAcrossRepresentativePairs(t *testing.T) {
	t.Parallel()

	pairs := [][2]string{
		{"exa", "web_search_exa"},
		{"exa", "web.search_exa"},
		{"exa", "web-search-exa"},
		{"exa", "web__search__exa"},
		{"local-docs", "lookup:primary"},
		{"local/docs", "lookup_primary"},
	}

	seen := make(map[string][2]string, len(pairs))
	for _, pair := range pairs {
		id := mcpToolID(pair[0], pair[1])
		if existing, ok := seen[id]; ok {
			t.Fatalf("id collision for %v and %v => %q", existing, pair, id)
		}
		seen[id] = pair
	}
}

func TestIsSafeMCPToolIDPart(t *testing.T) {
	t.Parallel()

	if !isSafeMCPToolIDPart("web_search_exa") {
		t.Fatal("expected single underscores to remain safe")
	}
	if isSafeMCPToolIDPart("web__search__exa") {
		t.Fatal("expected double underscores to be encoded")
	}
	if isSafeMCPToolIDPart("tool.name") {
		t.Fatal("expected dots to be encoded")
	}
}
