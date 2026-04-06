package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
)

func (s *Server) handleGetAgentRunToolOutput(w http.ResponseWriter, r *http.Request) {
	store, err := s.runtime.currentStore(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := store.GetAgentRunToolOutputPage(r.Context(), chi.URLParam(r, "runID"), chi.URLParam(r, "outputID"), offset, limit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, knowledge.ErrNotFound) {
			status = http.StatusNotFound
		}
		s.writeError(w, status, err.Error())
		return
	}

	s.writeJSON(w, r, http.StatusOK, page)
}

func (s *Server) handleSearchAgentRunToolOutput(w http.ResponseWriter, r *http.Request) {
	store, err := s.runtime.currentStore(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	literalText, _ := strconv.ParseBool(query.Get("literal_text"))
	result, err := store.SearchAgentRunToolOutput(
		r.Context(),
		chi.URLParam(r, "runID"),
		chi.URLParam(r, "outputID"),
		query.Get("pattern"),
		literalText,
		limit,
	)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, knowledge.ErrNotFound) {
			status = http.StatusNotFound
		} else if err.Error() == "pattern is required" || len(query.Get("pattern")) == 0 {
			status = http.StatusBadRequest
		}
		s.writeError(w, status, err.Error())
		return
	}

	s.writeJSON(w, r, http.StatusOK, result)
}
