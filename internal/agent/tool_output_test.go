package agent

import (
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "SGR color codes",
			input: "\x1b[31mred text\x1b[0m",
			want:  "red text",
		},
		{
			name:  "SGR reset",
			input: "before\x1b[0mafter",
			want:  "beforeafter",
		},
		{
			name:  "cursor movement",
			input: "\x1b[2Ahello\x1b[3B",
			want:  "hello",
		},
		{
			name:  "OSC title sequence BEL",
			input: "\x1b]0;window title\x07rest",
			want:  "rest",
		},
		{
			name:  "OSC title sequence ST",
			input: "\x1b]0;window title\x1b\\rest",
			want:  "rest",
		},
		{
			name:  "256 color",
			input: "\x1b[38;5;196mcolored\x1b[0m plain",
			want:  "colored plain",
		},
		{
			name:  "no ANSI",
			input: "plain text with unicode: café 日本語",
			want:  "plain text with unicode: café 日本語",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "erase in line",
			input: "hello\x1b[Kworld",
			want:  "helloworld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSI(tt.input)
			if got != tt.want {
				t.Fatalf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCanonicalizeToolOutputStripsANSI(t *testing.T) {
	t.Parallel()

	input := "\x1b[1;32mSuccess\x1b[0m: build complete\r\n\x1b[31mWarning\x1b[0m: deprecated"
	got := canonicalizeToolOutput(input)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("canonicalizeToolOutput still contains ANSI escapes: %q", got)
	}
	if !strings.Contains(got, "Success: build complete") {
		t.Fatalf("expected text content preserved, got %q", got)
	}
	if strings.Contains(got, "\r\n") {
		t.Fatalf("expected CRLF normalized, got %q", got)
	}
}

func TestShapeSpilledToolContentPreviewNoANSI(t *testing.T) {
	t.Parallel()

	content := canonicalizeToolOutput("\x1b[32mhello\x1b[0m world")
	shape := shapeSpilledToolContent("some_tool", content)
	if strings.Contains(shape.Content, "\x1b") {
		t.Fatalf("spilled content preview contains ANSI escapes: %q", shape.Content)
	}
}
