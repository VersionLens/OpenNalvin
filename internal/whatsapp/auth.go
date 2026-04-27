package whatsapp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	wa "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/versionlens/OpenNalvin/internal/config"
)

type AuthStreamEvent struct {
	Event    string `json:"event"`
	Code     string `json:"code,omitempty"`
	PairCode string `json:"pair_code,omitempty"`
	Message  string `json:"message,omitempty"`
	UserJID  string `json:"user_jid,omitempty"`
}

func AuthenticateQR(ctx context.Context, cfg config.Config, logger *slog.Logger, out io.Writer) error {
	container, client, err := openClient(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		client.Disconnect()
		_ = container.Close()
	}()
	if client.Store != nil && client.Store.ID != nil {
		return fmt.Errorf("whatsapp is already authenticated as %s", client.Store.ID.String())
	}

	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := client.Connect(); err != nil {
		return err
	}
	for item := range qrChan {
		switch item.Event {
		case wa.QRChannelEventCode:
			_, _ = fmt.Fprintln(out)
			qrterminal.GenerateWithConfig(strings.TrimSpace(item.Code), qrterminal.Config{
				Level:      qrterminal.L,
				Writer:     out,
				QuietZone:  1,
				HalfBlocks: true,
			})
			_, _ = fmt.Fprintln(out, "Scan this QR code in WhatsApp > Linked Devices")
		case "success":
			time.Sleep(500 * time.Millisecond)
			if client.Store != nil && client.Store.ID != nil {
				_, _ = fmt.Fprintf(out, "Authenticated as %s\n", client.Store.ID.String())
			} else {
				_, _ = fmt.Fprintln(out, "Authentication succeeded.")
			}
			return nil
		case "timeout":
			return fmt.Errorf("qr authentication timed out")
		case wa.QRChannelEventError:
			if item.Error != nil {
				return item.Error
			}
			return fmt.Errorf("qr authentication failed")
		default:
			if item.Error != nil {
				return item.Error
			}
		}
	}
	return fmt.Errorf("qr channel closed before authentication completed")
}

func AuthenticatePair(ctx context.Context, cfg config.Config, logger *slog.Logger, phone string, out io.Writer) error {
	container, client, err := openClient(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		client.Disconnect()
		_ = container.Close()
	}()
	if client.Store != nil && client.Store.ID != nil {
		return fmt.Errorf("whatsapp is already authenticated as %s", client.Store.ID.String())
	}
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := client.Connect(); err != nil {
		return err
	}

	paired := false
	for item := range qrChan {
		if item.Event == wa.QRChannelEventCode && !paired {
			code, err := client.PairPhone(ctx, strings.TrimSpace(phone), false, wa.PairClientChrome, "Chrome (Linux)")
			if err != nil {
				return err
			}
			paired = true
			_, _ = fmt.Fprintf(out, "Pairing code: %s\n", strings.TrimSpace(code))
			continue
		}
		switch item.Event {
		case "success":
			time.Sleep(500 * time.Millisecond)
			if client.Store != nil && client.Store.ID != nil {
				_, _ = fmt.Fprintf(out, "Authenticated as %s\n", client.Store.ID.String())
			} else {
				_, _ = fmt.Fprintln(out, "Authentication succeeded.")
			}
			return nil
		case "timeout":
			return fmt.Errorf("pairing timed out")
		case wa.QRChannelEventError:
			if item.Error != nil {
				return item.Error
			}
			return fmt.Errorf("pairing failed")
		default:
			if item.Error != nil {
				return item.Error
			}
		}
	}
	time.Sleep(250 * time.Millisecond)
	if client.Store != nil && client.Store.ID != nil {
		return nil
	}
	return fmt.Errorf("pairing did not complete")
}

func Logout(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	container, client, err := openClient(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		client.Disconnect()
		_ = container.Close()
	}()
	if client.Store == nil || client.Store.ID == nil {
		return fmt.Errorf("whatsapp authentication required")
	}
	if !client.IsConnected() {
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect whatsapp client: %w", err)
		}
	}
	return client.Logout(ctx)
}

func (s *Service) AuthenticateQRStream(ctx context.Context, emit func(AuthStreamEvent) error) error {
	return s.runAuthStream(ctx, "", emit)
}

func (s *Service) AuthenticatePairStream(ctx context.Context, phone string, emit func(AuthStreamEvent) error) error {
	if strings.TrimSpace(phone) == "" {
		return fmt.Errorf("provide --phone in E.164 format")
	}
	return s.runAuthStream(ctx, phone, emit)
}

func (s *Service) runAuthStream(ctx context.Context, phone string, emit func(AuthStreamEvent) error) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	client, err := s.prepareAuthClient()
	if err != nil {
		return err
	}
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := client.Connect(); err != nil {
		return err
	}

	success := false
	defer func() {
		if success {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.client != nil {
			s.client.Disconnect()
		}
	}()

	paired := false
	for item := range qrChan {
		if strings.TrimSpace(phone) != "" && item.Event == wa.QRChannelEventCode && !paired {
			code, err := client.PairPhone(ctx, strings.TrimSpace(phone), false, wa.PairClientChrome, "Chrome (Linux)")
			if err != nil {
				return err
			}
			paired = true
			if err := emit(AuthStreamEvent{Event: "pair_code", PairCode: strings.TrimSpace(code)}); err != nil {
				return err
			}
			continue
		}
		switch item.Event {
		case wa.QRChannelEventCode:
			if err := emit(AuthStreamEvent{Event: "code", Code: strings.TrimSpace(item.Code)}); err != nil {
				return err
			}
		case "success":
			time.Sleep(500 * time.Millisecond)
			success = true
			s.afterAuthSuccess()
			evt := AuthStreamEvent{Event: "success", Message: "Authentication succeeded."}
			if client.Store != nil && client.Store.ID != nil {
				evt.UserJID = client.Store.ID.String()
				evt.Message = fmt.Sprintf("Authenticated as %s", evt.UserJID)
			}
			if err := emit(evt); err != nil {
				return err
			}
			return nil
		case "timeout":
			if strings.TrimSpace(phone) != "" {
				return fmt.Errorf("pairing timed out")
			}
			return fmt.Errorf("qr authentication timed out")
		case wa.QRChannelEventError:
			if item.Error != nil {
				return item.Error
			}
			if strings.TrimSpace(phone) != "" {
				return fmt.Errorf("pairing failed")
			}
			return fmt.Errorf("qr authentication failed")
		default:
			if item.Error != nil {
				return item.Error
			}
		}
	}

	if strings.TrimSpace(phone) != "" {
		time.Sleep(250 * time.Millisecond)
		if client.Store != nil && client.Store.ID != nil {
			success = true
			s.afterAuthSuccess()
			evt := AuthStreamEvent{Event: "success", Message: "Authentication succeeded.", UserJID: client.Store.ID.String()}
			if err := emit(evt); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("pairing did not complete")
	}
	return fmt.Errorf("qr channel closed before authentication completed")
}

func (s *Service) prepareAuthClient() (*wa.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.lifecycleCtx == nil {
		return nil, fmt.Errorf("whatsapp service is not started")
	}
	if s.client != nil && s.client.Store != nil && s.client.Store.ID != nil {
		return nil, fmt.Errorf("whatsapp is already authenticated as %s", s.client.Store.ID.String())
	}
	if s.client != nil {
		s.client.Disconnect()
		s.client = nil
	}
	if s.container != nil {
		if err := s.container.Close(); err != nil {
			s.logger.Warn("close whatsapp auth container", "error", err)
		}
		s.container = nil
	}

	container, client, err := openClient(s.lifecycleCtx, s.cfg, s.logger)
	if err != nil {
		return nil, err
	}
	s.container = container
	s.client = client
	client.AddEventHandler(s.handleEvent)
	return client, nil
}

func (s *Service) afterAuthSuccess() {
	s.mu.Lock()
	client := s.client
	ctx := s.lifecycleCtx
	s.mu.Unlock()

	if client == nil || ctx == nil {
		return
	}
	if err := client.SendPresence(ctx, types.PresenceAvailable); err != nil {
		s.logger.Warn("set whatsapp presence", "error", err)
	}
	go s.refreshJoinedGroupsAsync(ctx)
}
