package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type workspaceSwitchRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.runtime.list()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []workspacepkg.Info{}
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"workspaces": items})
}

func (s *Server) handleCurrentWorkspace(w http.ResponseWriter, r *http.Request) {
	info, err := s.runtime.currentInfo()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"name":        info.Name,
		"persisted":   s.runtime.currentName(),
		"db_path":     info.DBPath,
		"files_path":  info.FilesPath,
		"db_exists":   info.DBExists,
		"files_exists": info.FilesExist,
	})
}

func (s *Server) handleSwitchWorkspace(w http.ResponseWriter, r *http.Request) {
	var req workspaceSwitchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if err := workspacepkg.ValidateName(req.Name); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	exists, err := workspacepkg.Exists(s.runtime.cfg, req.Name)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("workspace %q does not exist", req.Name))
		return
	}
	if err := config.SaveWorkspaceCurrent("", req.Name); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.runtime.switchCurrent(req.Name)
	s.handleCurrentWorkspace(w, r)
}
