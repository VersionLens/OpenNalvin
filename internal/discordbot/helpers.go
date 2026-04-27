package discordbot

import (
	"sort"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
)

const (
	WorkspaceName     = "discord"
	maxDiscordContent = 1900
	defaultFetchLimit = 100
	heartbeatInterval = 20 * time.Second
)

func noMentions() *discord.AllowedMentions {
	return &discord.AllowedMentions{
		Parse:       []discord.AllowedMentionType{},
		RepliedUser: false,
	}
}

func truncateForDiscord(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:limit]
	}
	return text[:limit-1] + "…"
}

func splitDiscordChunks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	chunks := make([]string, 0, (len(text)/maxDiscordContent)+1)
	for len(text) > 0 {
		if len(text) <= maxDiscordContent {
			chunks = append(chunks, text)
			break
		}
		cut := maxDiscordContent
		for cut > 0 && text[cut] != '\n' && text[cut] != ' ' {
			cut--
		}
		if cut <= 0 {
			cut = maxDiscordContent
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	return chunks
}

func deriveThreadName(prompt, fallback string) string {
	base := strings.TrimSpace(prompt)
	if base == "" {
		base = strings.TrimSpace(fallback)
	}
	base = strings.ReplaceAll(base, "\n", " ")
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Gemini Live"
	}
	if len(base) <= 90 {
		return base
	}
	return strings.TrimSpace(base[:90]) + "…"
}

func sortMessagesOldestFirst(messages []discordMessage) {
	sort.SliceStable(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
}

func stripBotMention(content, botUserID string) string {
	if botUserID == "" {
		return strings.TrimSpace(content)
	}
	content = strings.ReplaceAll(content, "<@"+botUserID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botUserID+">", "")
	return strings.TrimSpace(content)
}

// stripCompactCommand is a no-op stub: OpenNalvin doesn't implement an
// inline /compact directive on the chat surface, so we just return the
// prompt unchanged. The first return value mirrors the upstream signature
// (a flag indicating whether the directive was present) — always false.
func stripCompactCommand(prompt string) (bool, string) {
	return false, prompt
}

func mentionsBot(msg discordMessage, botUserID string) bool {
	if botUserID == "" {
		return false
	}
	for _, mentionedUserID := range msg.Mentions {
		if mentionedUserID == botUserID {
			return true
		}
	}
	return strings.Contains(msg.Content, "<@"+botUserID+">") || strings.Contains(msg.Content, "<@!"+botUserID+">")
}
