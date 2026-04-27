package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	workspaceName := strings.TrimSpace(chi.URLParam(r, "workspace"))
	if err := workspacepkg.ValidateName(workspaceName); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	paths, err := workspacepkg.PathsForName(s.cfg, workspaceName)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	relativePath, absolutePath, err := workspacepkg.ResolveFilePathForPaths(paths, chi.URLParam(r, "*"), false)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "failed to stat workspace file")
		return
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(absolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "failed to open workspace file")
		return
	}
	defer file.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(relativePath), info.ModTime(), file)
}
