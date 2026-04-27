package whatsapp

import (
	"testing"

	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestTriggerPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		msg           knowledge.WhatsAppMessage
		prefix        string
		requirePrefix bool
		wantPrompt    string
		wantOK        bool
	}{
		{
			name:          "prefix required text strips prefix",
			msg:           knowledge.WhatsAppMessage{Text: "!ai hello"},
			prefix:        "!ai",
			requirePrefix: true,
			wantPrompt:    "hello",
			wantOK:        true,
		},
		{
			name:          "prefix required text without prefix does not trigger",
			msg:           knowledge.WhatsAppMessage{Text: "hello"},
			prefix:        "!ai",
			requirePrefix: true,
			wantPrompt:    "",
			wantOK:        false,
		},
		{
			name:          "prefixless text uses full message",
			msg:           knowledge.WhatsAppMessage{Text: "hello there"},
			prefix:        "!ai",
			requirePrefix: false,
			wantPrompt:    "hello there",
			wantOK:        true,
		},
		{
			name:          "prefixless text ignores whitespace only",
			msg:           knowledge.WhatsAppMessage{Text: "   "},
			prefix:        "!ai",
			requirePrefix: false,
			wantPrompt:    "",
			wantOK:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotPrompt, gotOK := triggerPrompt(tt.msg, tt.prefix, tt.requirePrefix)
			if gotPrompt != tt.wantPrompt || gotOK != tt.wantOK {
				t.Fatalf("triggerPrompt() = (%q, %t), want (%q, %t)", gotPrompt, gotOK, tt.wantPrompt, tt.wantOK)
			}
		})
	}
}

func TestConversationalChatEligibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		chatJID       string
		requirePrefix bool
		want          bool
	}{
		{name: "prefix required preserves legacy behavior for status", chatJID: "status@broadcast", requirePrefix: true, want: true},
		{name: "prefixless direct s whatsapp net allowed", chatJID: "447857048513@s.whatsapp.net", requirePrefix: false, want: true},
		{name: "prefixless lid allowed", chatJID: "34428474638370@lid", requirePrefix: false, want: true},
		{name: "prefixless group allowed", chatJID: "120363298819846080@g.us", requirePrefix: false, want: true},
		{name: "prefixless status excluded", chatJID: "status@broadcast", requirePrefix: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := agentChatEligible(tt.chatJID, tt.requirePrefix); got != tt.want {
				t.Fatalf("agentChatEligible(%q, %t) = %t, want %t", tt.chatJID, tt.requirePrefix, got, tt.want)
			}
		})
	}
}
