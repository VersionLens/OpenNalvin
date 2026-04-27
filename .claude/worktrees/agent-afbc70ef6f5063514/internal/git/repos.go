package git

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

type RepoInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type RepoAuth struct {
	PublicRead bool
	Readers    map[string]struct{}
	Writers    map[string]struct{}
}

var ErrRepoNotFound = errors.New("git repo not found")

func NormalizeRepoName(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("repo name is required")
	}

	value = strings.ReplaceAll(value, `\`, "/")
	value = strings.TrimPrefix(value, "/")
	if strings.HasSuffix(value, ".git") {
		value = strings.TrimSuffix(value, ".git")
	}
	value = path.Clean(value)

	switch {
	case value == "", value == ".", value == "..":
		return "", fmt.Errorf("repo name is invalid")
	case strings.HasPrefix(value, "../"), strings.Contains(value, "/../"):
		return "", fmt.Errorf("repo name must stay within the repo root")
	}

	return value, nil
}

func RepoRoot(cfg configpkg.Config) string {
	return strings.TrimSpace(cfg.Git.RepoRoot)
}

func RepoPath(cfg configpkg.Config, repoName string) (string, string, error) {
	name, err := NormalizeRepoName(repoName)
	if err != nil {
		return "", "", err
	}

	root := RepoRoot(cfg)
	if root == "" {
		return "", "", fmt.Errorf("git repo root is not configured")
	}

	repoPath := filepath.Join(root, filepath.FromSlash(name+".git"))
	if err := ensureWithin(root, repoPath); err != nil {
		return "", "", err
	}

	return name, repoPath, nil
}

func EnsureRepoRoot(cfg configpkg.Config) error {
	root := RepoRoot(cfg)
	if root == "" {
		return fmt.Errorf("git repo root is not configured")
	}
	return os.MkdirAll(root, 0o755)
}

func CreateBareRepo(cfg configpkg.Config, repoName string) (RepoInfo, error) {
	name, repoPath, err := RepoPath(cfg, repoName)
	if err != nil {
		return RepoInfo{}, err
	}

	if err := EnsureRepoRoot(cfg); err != nil {
		return RepoInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		return RepoInfo{}, fmt.Errorf("create repo parent directory: %w", err)
	}

	if _, err := gogit.PlainInit(repoPath, true); err != nil {
		return RepoInfo{}, err
	}

	return RepoInfo{Name: name, Path: repoPath}, nil
}

func DeleteBareRepo(cfg configpkg.Config, repoName string) (RepoInfo, error) {
	name, repoPath, err := RepoPath(cfg, repoName)
	if err != nil {
		return RepoInfo{}, err
	}

	info, err := os.Stat(repoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return RepoInfo{}, ErrRepoNotFound
		}
		return RepoInfo{}, err
	}
	if !info.IsDir() || !isBareRepoDir(repoPath) {
		return RepoInfo{}, ErrRepoNotFound
	}

	if err := os.RemoveAll(repoPath); err != nil {
		return RepoInfo{}, fmt.Errorf("delete repo %q: %w", name, err)
	}
	return RepoInfo{Name: name, Path: repoPath}, nil
}

func ListRepos(cfg configpkg.Config) ([]RepoInfo, error) {
	root := RepoRoot(cfg)
	if root == "" {
		return nil, fmt.Errorf("git repo root is not configured")
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	items := make([]RepoInfo, 0)
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() || current == root || !strings.HasSuffix(entry.Name(), ".git") {
			return nil
		}
		if !isBareRepoDir(current) {
			return nil
		}

		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(strings.TrimSuffix(rel, ".git"))
		items = append(items, RepoInfo{Name: name, Path: current})
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func LookupRepoAuth(cfg configpkg.Config, repoName string) (string, string, RepoAuth, error) {
	name, repoPath, err := RepoPath(cfg, repoName)
	if err != nil {
		return "", "", RepoAuth{}, err
	}

	auth := RepoAuth{
		PublicRead: cfg.Git.RepoDefaults.PublicRead,
		Readers:    map[string]struct{}{},
		Writers:    map[string]struct{}{},
	}
	for _, user := range cfg.Git.RepoDefaults.DefaultWriters {
		user = strings.TrimSpace(user)
		if user == "" {
			continue
		}
		auth.Writers[user] = struct{}{}
	}

	if override, ok := cfg.Git.Repos[name]; ok {
		if override.PublicRead != nil {
			auth.PublicRead = *override.PublicRead
		}
		for _, user := range override.Readers {
			user = strings.TrimSpace(user)
			if user == "" {
				continue
			}
			auth.Readers[user] = struct{}{}
		}
		for _, user := range override.Writers {
			user = strings.TrimSpace(user)
			if user == "" {
				continue
			}
			auth.Writers[user] = struct{}{}
		}
	}

	return name, repoPath, auth, nil
}

func (a RepoAuth) CanRead(user string) bool {
	user = strings.TrimSpace(user)
	if user == "" {
		return false
	}
	if a.PublicRead {
		return true
	}
	if _, ok := a.Readers[user]; ok {
		return true
	}
	_, ok := a.Writers[user]
	return ok
}

func (a RepoAuth) CanWrite(user string) bool {
	user = strings.TrimSpace(user)
	if user == "" {
		return false
	}
	_, ok := a.Writers[user]
	return ok
}

func RepoSSHURL(cfg configpkg.Config, repoName string) (string, error) {
	name, _, err := RepoPath(cfg, repoName)
	if err != nil {
		return "", err
	}

	user := strings.TrimSpace(cfg.Git.DefaultClient.User)
	if user == "" {
		return "", fmt.Errorf("git default client user is required")
	}

	return fmt.Sprintf("ssh://%s@%s:%s/%s.git", user, DefaultClientHost(cfg), DefaultClientSSHPort(cfg), name), nil
}

func ensureWithin(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve repo path %q: %w", target, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("repo path escapes the repo root")
	}
	return nil
}

func isBareRepoDir(path string) bool {
	required := []string{"HEAD", "config", "objects", "refs"}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			return false
		}
	}
	return true
}
