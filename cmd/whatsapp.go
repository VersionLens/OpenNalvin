package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/output"
	whatsapppkg "github.com/versionlens/OpenNalvin/internal/whatsapp"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
)

var (
	whatsAppPhone    string
	whatsAppReplyTo  string
	whatsAppText     string
	whatsAppLimit    int
	whatsAppLogoutFn = whatsapppkg.Logout
)

var whatsAppCmd = &cobra.Command{
	Use:   "whatsapp",
	Short: "Authenticate, inspect, and operate the WhatsApp integration",
}

var whatsAppAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate the local WhatsApp session",
}

var whatsAppAuthQRCmd = &cobra.Command{
	Use:   "qr",
	Short: "Start QR-based WhatsApp authentication",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
		ok, err := relayWhatsAppAuthStream(cmd.Context(), cfg, "/api/whatsapp/auth/qr", nil, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		return whatsapppkg.AuthenticateQR(cmd.Context(), cfg, logger, cmd.OutOrStdout())
	},
}

var whatsAppAuthPairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Start pairing-code WhatsApp authentication",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		if strings.TrimSpace(whatsAppPhone) == "" {
			return fmt.Errorf("provide --phone in E.164 format")
		}
		logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
		ok, err := relayWhatsAppAuthStream(cmd.Context(), cfg, "/api/whatsapp/auth/pair", map[string]any{"phone": whatsAppPhone}, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		return whatsapppkg.AuthenticatePair(cmd.Context(), cfg, logger, whatsAppPhone, cmd.OutOrStdout())
	},
}

// --- Read-only commands: direct DB access, no WhatsApp connection ---

var whatsAppStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show WhatsApp session status",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		var status whatsapppkg.Status
		ok, err := relayToWhatsApp(cmd.Context(), cfg, http.MethodGet, "/api/whatsapp/status", nil, &status)
		if err != nil {
			return err
		}
		if !ok {
			logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
			status, err = whatsapppkg.ProbeStatus(cmd.Context(), cfg, logger)
			if err != nil {
				return err
			}
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(status)
		}
		w.Line("Enabled: %t", status.Enabled)
		w.Line("Workspace: %s", status.Workspace)
		w.Line("Session DB: %s", status.SessionDBPath)
		w.Line("Connected: %t", status.Connected)
		w.Line("Logged in: %t", status.LoggedIn)
		w.Line("Needs auth: %t", status.NeedsAuth)
		if status.UserJID != "" {
			w.Line("User JID: %s", status.UserJID)
		}
		if status.PushName != "" {
			w.Line("Push name: %s", status.PushName)
		}
		if !ok {
			w.Line("(whatsapp serve process not reachable at %s)", cfg.WhatsApp.ServeAddr)
		}
		return nil
	},
}

var whatsAppChatsCmd = &cobra.Command{
	Use:   "chats",
	Short: "Inspect mirrored WhatsApp chats and messages",
}

var whatsAppChatsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List mirrored WhatsApp chats",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, cfg, err := openWhatsAppWorkspaceDB(cmd.Context())
		if err != nil {
			return err
		}
		defer db.Close()
		store := knowledge.New(db)
		chats, err := store.ListWhatsAppChats(cmd.Context(), cfg.WhatsApp.Workspace, whatsAppLimit)
		if err != nil {
			return err
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(chats)
		}
		if len(chats) == 0 {
			w.Line("No chats found.")
			return nil
		}
		for _, item := range chats {
			name := strings.TrimSpace(item.DisplayName)
			if name == "" {
				name = strings.TrimSpace(item.Subject)
			}
			if name == "" {
				name = item.JID
			}
			w.Line("%s [%s]", name, item.ChatType)
			w.Line("  JID: %s", item.JID)
			if item.LastMessagePreview != "" {
				w.Line("  Last: %s", item.LastMessagePreview)
			}
		}
		return nil
	},
}

var whatsAppChatsMessagesCmd = &cobra.Command{
	Use:   "messages <jid>",
	Short: "List mirrored messages for a chat",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return renderWhatsAppMessages(cmd, args[0], false)
	},
}

var whatsAppChatsMediaCmd = &cobra.Command{
	Use:   "media <jid>",
	Short: "List media-rich messages for a chat",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return renderWhatsAppMessages(cmd, args[0], true)
	},
}

// --- Write commands: relay to whatsapp serve process ---

var whatsAppLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out the current WhatsApp session",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		ok, err := relayToWhatsApp(cmd.Context(), cfg, http.MethodPost, "/api/whatsapp/logout", map[string]any{}, nil)
		if err != nil {
			return err
		}
		if !ok {
			logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
			if err := whatsAppLogoutFn(cmd.Context(), cfg, logger); err != nil {
				return err
			}
		}
		if !output.FromContext(cmd.Context()).IsJSON() {
			output.FromContext(cmd.Context()).Line("Logged out.")
		}
		return nil
	},
}

var whatsAppSendCmd = &cobra.Command{
	Use:   "send <jid>",
	Short: "Send a WhatsApp text message",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		if strings.TrimSpace(whatsAppText) == "" {
			return fmt.Errorf("provide --text")
		}
		req := map[string]any{"chat_jid": args[0], "text": whatsAppText, "reply_to": whatsAppReplyTo}
		var result whatsapppkg.SendTextResult
		if err := requireWhatsAppRelay(cmd.Context(), cfg, http.MethodPost, "/api/whatsapp/send", req, &result); err != nil {
			return err
		}
		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}
		w.Line("Sent %s to %s", result.MessageID, result.ChatJID)
		return nil
	},
}

func init() {
	whatsAppAuthPairCmd.Flags().StringVar(&whatsAppPhone, "phone", "", "phone number in E.164 format")

	whatsAppChatsListCmd.Flags().IntVar(&whatsAppLimit, "limit", 100, "maximum number of chats to list")
	whatsAppChatsMessagesCmd.Flags().IntVar(&whatsAppLimit, "limit", 100, "maximum number of messages to list")
	whatsAppChatsMediaCmd.Flags().IntVar(&whatsAppLimit, "limit", 100, "maximum number of messages to list")
	whatsAppSendCmd.Flags().StringVar(&whatsAppText, "text", "", "text to send")
	whatsAppSendCmd.Flags().StringVar(&whatsAppReplyTo, "reply-to", "", "message id to quote in the reply")

	whatsAppAuthCmd.AddCommand(whatsAppAuthQRCmd)
	whatsAppAuthCmd.AddCommand(whatsAppAuthPairCmd)
	whatsAppChatsCmd.AddCommand(whatsAppChatsListCmd)
	whatsAppChatsCmd.AddCommand(whatsAppChatsMessagesCmd)
	whatsAppChatsCmd.AddCommand(whatsAppChatsMediaCmd)
	whatsAppCmd.AddCommand(whatsAppAuthCmd)
	whatsAppCmd.AddCommand(whatsAppStatusCmd)
	whatsAppCmd.AddCommand(whatsAppLogoutCmd)
	whatsAppCmd.AddCommand(whatsAppChatsCmd)
	whatsAppCmd.AddCommand(whatsAppSendCmd)
	rootCmd.AddCommand(whatsAppCmd)
}

func renderWhatsAppMessages(cmd *cobra.Command, chatJID string, mediaOnly bool) error {
	db, _, err := openWhatsAppWorkspaceDB(cmd.Context())
	if err != nil {
		return err
	}
	defer db.Close()
	store := knowledge.New(db)

	filter := knowledge.WhatsAppMessageFilter{
		ChatJID:   chatJID,
		Limit:     whatsAppLimit,
		MediaOnly: mediaOnly,
	}
	messages, err := store.ListWhatsAppMessages(cmd.Context(), filter)
	if err != nil {
		return err
	}

	w := output.FromContext(cmd.Context())
	if w.IsJSON() {
		return w.JSON(messages)
	}
	if len(messages) == 0 {
		w.Line("No messages found.")
		return nil
	}
	for _, item := range messages {
		label := item.MessageID
		if item.FromMe {
			label += " [me]"
		}
		w.Line("%s %s", item.Timestamp.Format(time.RFC3339), label)
		if item.Text != "" {
			w.Line("  Text: %s", item.Text)
		} else if item.Caption != "" {
			w.Line("  Caption: %s", item.Caption)
		} else if item.MediaKind != "" {
			w.Line("  Media: %s", item.MediaKind)
		}
		if item.MediaPath != "" {
			w.Line("  Path: %s", item.MediaPath)
		}
	}
	return nil
}

func openWhatsAppWorkspaceDB(ctx context.Context) (*sql.DB, config.Config, error) {
	cfg, _, err := activeWorkspaceConfig(ctx)
	if err != nil {
		return nil, config.Config{}, err
	}
	cfg.Workspace.Current = cfg.WhatsApp.Workspace
	db, _, err := workspacepkg.OpenDB(ctx, cfg, cfg.WhatsApp.Workspace)
	if err != nil {
		return nil, config.Config{}, err
	}
	return db, cfg, nil
}

func requireWhatsAppRelay(ctx context.Context, cfg config.Config, method, path string, body any, into any) error {
	ok, err := relayToWhatsApp(ctx, cfg, method, path, body, into)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("whatsapp serve process is not reachable at %s; run 'whatsapp serve' first", cfg.WhatsApp.ServeAddr)
	}
	return nil
}

func relayToWhatsApp(ctx context.Context, cfg config.Config, method, path string, requestBody any, into any) (bool, error) {
	return relayWhatsAppJSONWithTimeout(ctx, cfg.WhatsApp.ServeAddr, method, path, requestBody, into, 5*time.Second)
}

func relayWhatsAppJSONWithTimeout(ctx context.Context, addr string, method, path string, requestBody any, into any, timeout time.Duration) (bool, error) {
	baseURL := relayWhatsAppBaseURL(addr)
	if baseURL == "" {
		return false, nil
	}
	var body io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return false, err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return false, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		if isWhatsAppNetworkError(err) {
			return false, nil
		}
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		if msg, ok := payload["message"].(string); ok && msg != "" {
			return true, fmt.Errorf("%s", msg)
		}
		return true, fmt.Errorf("relay request failed with status %s", resp.Status)
	}
	if into == nil {
		return true, nil
	}
	return true, json.NewDecoder(resp.Body).Decode(into)
}

func relayWhatsAppAuthStream(ctx context.Context, cfg config.Config, path string, requestBody any, out io.Writer) (bool, error) {
	baseURL := relayWhatsAppBaseURL(cfg.WhatsApp.ServeAddr)
	if baseURL == "" {
		return false, nil
	}
	var body io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return false, err
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, body)
	if err != nil {
		return false, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		if isWhatsAppNetworkError(err) {
			return false, nil
		}
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		if msg, ok := payload["message"].(string); ok && msg != "" {
			return true, fmt.Errorf("%s", msg)
		}
		return true, fmt.Errorf("relay request failed with status %s", resp.Status)
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var evt whatsapppkg.AuthStreamEvent
		if err := dec.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				return true, fmt.Errorf("authentication stream closed before completion")
			}
			return true, err
		}
		switch evt.Event {
		case "code":
			_, _ = fmt.Fprintln(out)
			qrterminal.GenerateWithConfig(strings.TrimSpace(evt.Code), qrterminal.Config{
				Level:      qrterminal.L,
				Writer:     out,
				QuietZone:  1,
				HalfBlocks: true,
			})
			_, _ = fmt.Fprintln(out, "Scan this QR code in WhatsApp > Linked Devices")
		case "pair_code":
			_, _ = fmt.Fprintf(out, "Pairing code: %s\n", strings.TrimSpace(evt.PairCode))
		case "success":
			if strings.TrimSpace(evt.Message) != "" {
				_, _ = fmt.Fprintln(out, strings.TrimSpace(evt.Message))
			}
			return true, nil
		case "error":
			if strings.TrimSpace(evt.Message) == "" {
				return true, fmt.Errorf("authentication failed")
			}
			return true, fmt.Errorf("%s", strings.TrimSpace(evt.Message))
		default:
			if strings.TrimSpace(evt.Message) != "" {
				_, _ = fmt.Fprintln(out, strings.TrimSpace(evt.Message))
			}
		}
	}
}

func relayWhatsAppBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func isWhatsAppNetworkError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}
