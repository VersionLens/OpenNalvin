package knowledge_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/versionlens/OpenNalvin/internal/database"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func TestDiscordConversationAndToolViewPersistence(t *testing.T) {
	t.Parallel()

	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "discord-state.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	store := knowledge.New(db)
	ctx := context.Background()

	conv, err := store.SaveDiscordConversation(ctx, knowledge.SaveDiscordConversationInput{
		Workspace:     "alpha",
		GuildID:       "guild-1",
		ChannelID:     "channel-1",
		ThreadID:      "thread-1",
		RootMessageID: "root-1",
		RunID:         "run-1",
		BotUserID:     "bot-1",
		Status:        "running",
	})
	if err != nil {
		t.Fatalf("save discord conversation: %v", err)
	}

	loadedConv, err := store.GetDiscordConversationByThread(ctx, "thread-1")
	if err != nil {
		t.Fatalf("get discord conversation by thread: %v", err)
	}
	if loadedConv.ID != conv.ID || loadedConv.RunID != "run-1" {
		t.Fatalf("unexpected loaded conversation: %+v", loadedConv)
	}

	view, err := store.SaveDiscordToolView(ctx, knowledge.SaveDiscordToolViewInput{
		ConversationID:     conv.ID,
		DiscordMessageID:   "msg-1",
		RunID:              "run-1",
		SelectedToolCallID: "call-1",
		State: json.RawMessage(`{
			"progress":["calling ls"],
			"tool_calls":[{"tool_call_id":"call-1","tool_name":"ls","completed":true}]
		}`),
	})
	if err != nil {
		t.Fatalf("save discord tool view: %v", err)
	}

	loadedView, err := store.GetDiscordToolViewByMessage(ctx, "msg-1")
	if err != nil {
		t.Fatalf("get discord tool view by message: %v", err)
	}
	if loadedView.ID != view.ID || loadedView.SelectedToolCallID != "call-1" {
		t.Fatalf("unexpected loaded view: %+v", loadedView)
	}
}
