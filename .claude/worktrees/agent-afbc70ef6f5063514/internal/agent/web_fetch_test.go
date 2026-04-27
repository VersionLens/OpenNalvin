package agent

import (
	"strings"
	"testing"
)

func TestValidateWebFetchURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url       string
		wantErr   bool
		errSubstr string
	}{
		{url: "https://example.com", wantErr: false},
		{url: "http://example.com/path", wantErr: false},
		{url: "", wantErr: true, errSubstr: "url must include an http:// or https:// scheme"},
		{url: "example.com", wantErr: true, errSubstr: "url must include an http:// or https:// scheme"},
		{url: "example.com/path", wantErr: true, errSubstr: "url must include an http:// or https:// scheme"},
		{url: "file:///etc/passwd", wantErr: true, errSubstr: `url scheme "file" is not supported`},
		{url: "ftp://example.com", wantErr: true, errSubstr: `url scheme "ftp" is not supported`},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateWebFetchURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.url)
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tt.errSubstr, err.Error())
				}
			} else if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.url, err)
			}
		})
	}
}
