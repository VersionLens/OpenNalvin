package whatsapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

type sendRequest struct {
	ChatJID string `json:"chat_jid"`
	Text    string `json:"text"`
	ReplyTo string `json:"reply_to"`
}

type authRequest struct {
	Phone string `json:"phone"`
}

// Handler returns an http.Handler that exposes the WhatsApp service API.
func (s *Service) Handler() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/whatsapp/status", s.httpStatus)
	r.Post("/api/whatsapp/auth/qr", s.httpAuthQR)
	r.Post("/api/whatsapp/auth/pair", s.httpAuthPair)
	r.Get("/api/whatsapp/chats", s.httpChats)
	r.Get("/api/whatsapp/chats/{jid}/messages", s.httpMessages)
	r.Post("/api/whatsapp/send", s.httpSend)
	r.Post("/api/whatsapp/logout", s.httpLogout)
	return r
}

func (s *Service) httpStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Status(r.Context()))
}

func (s *Service) httpAuthQR(w http.ResponseWriter, r *http.Request) {
	streamAuthEvents(w, r, func(emit func(AuthStreamEvent) error) error {
		return s.AuthenticateQRStream(r.Context(), emit)
	})
}

func (s *Service) httpAuthPair(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	streamAuthEvents(w, r, func(emit func(AuthStreamEvent) error) error {
		return s.AuthenticatePairStream(r.Context(), req.Phone, emit)
	})
}

func (s *Service) httpChats(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.ListChats(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": items})
}

func (s *Service) httpMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jid := chi.URLParam(r, "jid")
	mediaOnly := strings.EqualFold(r.URL.Query().Get("media_only"), "true")
	var (
		items any
		err   error
	)
	if mediaOnly {
		items, err = s.ListMediaMessages(r.Context(), jid, limit)
	} else {
		items, err = s.ListMessages(r.Context(), jid, limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items})
}

func (s *Service) httpSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	result, err := s.SendText(r.Context(), req.ChatJID, req.Text, req.ReplyTo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) httpLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Logout(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error":   http.StatusText(status),
		"message": message,
	})
}

func streamAuthEvents(w http.ResponseWriter, _ *http.Request, run func(func(AuthStreamEvent) error) error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")

	enc := json.NewEncoder(w)
	emit := func(evt AuthStreamEvent) error {
		if err := enc.Encode(evt); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := run(emit); err != nil {
		_ = emit(AuthStreamEvent{Event: "error", Message: err.Error()})
	}
}
