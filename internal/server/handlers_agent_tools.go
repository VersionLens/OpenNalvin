package server

import (
	"net/http"

	agentpkg "github.com/versionlens/OpenNalvin/internal/agent"
)

func (s *Server) handleListAgentTools(w http.ResponseWriter, r *http.Request) {
	store, err := s.runtime.currentStore(r.Context())
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	result, err := agentpkg.ListTools(r.Context(), store, r.URL.Query().Get("run_id"), agentpkg.ToolSelection{})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, r, http.StatusOK, result)
}
