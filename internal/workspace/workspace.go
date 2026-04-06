package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/database"
)

var (
	namePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	ErrAlreadyExists = errors.New("workspace already exists")
	ErrNotFound      = errors.New("workspace not found")
)

type Paths struct {
	Name      string
	DBPath    string
	FilesPath string
}

type Info struct {
	Name       string `json:"name"`
	Current    bool   `json:"current"`
	DBPath     string `json:"db_path"`
	FilesPath  string `json:"files_path"`
	DBExists   bool   `json:"db_exists"`
	FilesExist bool   `json:"files_exists"`
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return fmt.Errorf("workspace name is required")
	case name == "." || name == "..":
		return fmt.Errorf("workspace name %q is not allowed", name)
	case strings.Contains(name, "/") || strings.Contains(name, `\`):
		return fmt.Errorf("workspace name %q must be a single path segment", name)
	case strings.Contains(name, string(filepath.Separator)):
		return fmt.Errorf("workspace name %q must be a single path segment", name)
	case !namePattern.MatchString(name):
		return fmt.Errorf("workspace name %q may only contain letters, numbers, dot, underscore, and hyphen", name)
	default:
		return nil
	}
}

func ResolvePaths(cfg config.Config, override string) (Paths, error) {
	name := strings.TrimSpace(override)
	if name == "" {
		name = strings.TrimSpace(cfg.Workspace.Current)
	}
	if name == "" {
		name = "default"
	}
	return PathsForName(cfg, name)
}

func PathsForName(cfg config.Config, name string) (Paths, error) {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return Paths{}, err
	}
	return Paths{
		Name:      name,
		DBPath:    filepath.Join(cfg.Workspace.DBRoot, name+".sqlite"),
		FilesPath: filepath.Join(cfg.Workspace.FilesRoot, name),
	}, nil
}

func OpenDB(ctx context.Context, cfg config.Config, override string) (*sql.DB, Paths, error) {
	paths, err := ResolvePaths(cfg, override)
	if err != nil {
		return nil, Paths{}, err
	}
	if err := ensureRoots(cfg); err != nil {
		return nil, Paths{}, err
	}
	if err := os.MkdirAll(paths.FilesPath, 0o755); err != nil {
		return nil, Paths{}, fmt.Errorf("create workspace files directory: %w", err)
	}

	db, err := database.Open(ctx, paths.DBPath)
	if err != nil {
		return nil, Paths{}, err
	}
	return db, paths, nil
}

func Create(ctx context.Context, cfg config.Config, name string) (Info, error) {
	paths, err := PathsForName(cfg, name)
	if err != nil {
		return Info{}, err
	}
	if err := ensureRoots(cfg); err != nil {
		return Info{}, err
	}

	info, err := inspect(paths, cfg.Workspace.Current == paths.Name)
	if err != nil {
		return Info{}, err
	}
	if info.DBExists || info.FilesExist {
		return Info{}, ErrAlreadyExists
	}

	if err := os.MkdirAll(paths.FilesPath, 0o755); err != nil {
		return Info{}, fmt.Errorf("create workspace files directory: %w", err)
	}

	db, err := database.Open(ctx, paths.DBPath)
	if err != nil {
		return Info{}, err
	}
	if err := db.Close(); err != nil {
		return Info{}, fmt.Errorf("close workspace database: %w", err)
	}

	return inspect(paths, cfg.Workspace.Current == paths.Name)
}

func CurrentInfo(cfg config.Config, override string) (Info, error) {
	paths, err := ResolvePaths(cfg, override)
	if err != nil {
		return Info{}, err
	}
	return inspect(paths, cfg.Workspace.Current == paths.Name)
}

func Exists(cfg config.Config, name string) (bool, error) {
	paths, err := PathsForName(cfg, name)
	if err != nil {
		return false, err
	}
	info, err := inspect(paths, cfg.Workspace.Current == paths.Name)
	if err != nil {
		return false, err
	}
	return info.DBExists, nil
}

func Delete(cfg config.Config, name string) error {
	paths, err := PathsForName(cfg, name)
	if err != nil {
		return err
	}
	info, err := inspect(paths, cfg.Workspace.Current == paths.Name)
	if err != nil {
		return err
	}
	if !info.DBExists && !info.FilesExist {
		return ErrNotFound
	}

	if info.DBExists {
		if err := os.Remove(paths.DBPath); err != nil {
			return fmt.Errorf("remove workspace database: %w", err)
		}
	}
	if info.FilesExist {
		if err := os.RemoveAll(paths.FilesPath); err != nil {
			return fmt.Errorf("remove workspace files directory: %w", err)
		}
	}
	return nil
}

func List(cfg config.Config) ([]Info, error) {
	if err := ensureRoots(cfg); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(cfg.Workspace.DBRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace database root: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sqlite") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".sqlite"))
	}
	sort.Strings(names)

	items := make([]Info, 0, len(names))
	for _, name := range names {
		paths, err := PathsForName(cfg, name)
		if err != nil {
			continue
		}
		info, err := inspect(paths, cfg.Workspace.Current == name)
		if err != nil {
			return nil, err
		}
		items = append(items, info)
	}
	return items, nil
}

func inspect(paths Paths, current bool) (Info, error) {
	dbExists, err := pathExists(paths.DBPath)
	if err != nil {
		return Info{}, fmt.Errorf("inspect workspace database: %w", err)
	}
	filesExist, err := pathExists(paths.FilesPath)
	if err != nil {
		return Info{}, fmt.Errorf("inspect workspace files directory: %w", err)
	}

	return Info{
		Name:       paths.Name,
		Current:    current,
		DBPath:     paths.DBPath,
		FilesPath:  paths.FilesPath,
		DBExists:   dbExists,
		FilesExist: filesExist,
	}, nil
}

func ensureRoots(cfg config.Config) error {
	for _, root := range []string{cfg.Workspace.DBRoot, cfg.Workspace.FilesRoot} {
		if root == "" {
			return fmt.Errorf("workspace roots must not be empty")
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("create workspace root %q: %w", root, err)
		}
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
