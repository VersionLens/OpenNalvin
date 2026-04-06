package workspace

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/config"
)

type pathsContextKey string

const workspacePathsContextKey pathsContextKey = "workspace-paths"

type CopyStats struct {
	Kind      string `json:"kind"`
	FileCount int    `json:"file_count"`
	ByteCount int64  `json:"byte_count"`
}

func WithPaths(ctx context.Context, paths Paths) context.Context {
	return context.WithValue(ctx, workspacePathsContextKey, paths)
}

func PathsFromContext(ctx context.Context) (Paths, bool) {
	paths, ok := ctx.Value(workspacePathsContextKey).(Paths)
	return paths, ok
}

func ActivePaths(ctx context.Context, cfg config.Config) (Paths, error) {
	if paths, ok := PathsFromContext(ctx); ok {
		return paths, nil
	}
	return ResolvePaths(cfg, "")
}

func ResolveFilePath(ctx context.Context, cfg config.Config, rawPath string, allowRoot bool) (Paths, string, string, error) {
	paths, err := ActivePaths(ctx, cfg)
	if err != nil {
		return Paths{}, "", "", err
	}

	relativePath, absolutePath, err := ResolveFilePathForPaths(paths, rawPath, allowRoot)
	if err != nil {
		return Paths{}, "", "", err
	}

	return paths, relativePath, absolutePath, nil
}

func ResolveFilePathForPaths(paths Paths, rawPath string, allowRoot bool) (string, string, error) {
	relativePath, err := cleanRelativePath(rawPath, allowRoot)
	if err != nil {
		return "", "", err
	}

	absolutePath := filepath.Join(paths.FilesPath, filepath.FromSlash(relativePath))
	if err := ensureWithinRoot(paths.FilesPath, absolutePath); err != nil {
		return "", "", err
	}
	if err := ensureNoSymlinkTraversal(paths.FilesPath, absolutePath); err != nil {
		return "", "", err
	}

	return relativePath, absolutePath, nil
}

func CopyTree(src, dst string, overwrite bool) (CopyStats, error) {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return CopyStats{}, err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return CopyStats{}, fmt.Errorf("symlink sources are not supported: %s", src)
	}

	if _, err := os.Lstat(dst); err == nil {
		if !overwrite {
			return CopyStats{}, fmt.Errorf("destination already exists: %s", dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return CopyStats{}, fmt.Errorf("remove existing destination %q: %w", dst, err)
		}
	} else if !os.IsNotExist(err) {
		return CopyStats{}, err
	}

	if srcInfo.IsDir() {
		stats := CopyStats{Kind: "directory"}
		if err := copyDirectory(src, dst, &stats); err != nil {
			return CopyStats{}, err
		}
		return stats, nil
	}
	if !srcInfo.Mode().IsRegular() {
		return CopyStats{}, fmt.Errorf("unsupported source type at %s", src)
	}

	bytesWritten, err := copyRegularFile(src, dst, srcInfo.Mode())
	if err != nil {
		return CopyStats{}, err
	}
	return CopyStats{
		Kind:      "file",
		FileCount: 1,
		ByteCount: bytesWritten,
	}, nil
}

func cleanRelativePath(rawPath string, allowRoot bool) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rawPath) || strings.HasPrefix(filepath.ToSlash(rawPath), "/") {
		return "", fmt.Errorf("path must be workspace-relative")
	}

	cleaned := filepath.Clean(filepath.FromSlash(rawPath))
	if cleaned == "." {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("path must not resolve to the workspace root")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay within the workspace root")
	}
	return filepath.ToSlash(cleaned), nil
}

func ensureWithinRoot(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", target, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes the workspace root")
	}
	return nil
}

func ensureNoSymlinkTraversal(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", target, err)
	}
	if rel == "." {
		return nil
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("inspect path %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in workspace paths")
		}
	}

	return nil
}

func copyDirectory(src, dst string, stats *CopyStats) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create destination directory %q: %w", dst, err)
	}

	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == src {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink sources are not supported: %s", path)
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, relPath)

		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported source type at %s", path)
		}

		bytesWritten, err := copyRegularFile(path, targetPath, info.Mode())
		if err != nil {
			return err
		}
		stats.FileCount++
		stats.ByteCount += bytesWritten
		return nil
	})
}

func copyRegularFile(src, dst string, mode os.FileMode) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, fmt.Errorf("create destination directory for %q: %w", dst, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open source file %q: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return 0, fmt.Errorf("open destination file %q: %w", dst, err)
	}

	bytesWritten, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("copy %q to %q: %w", src, dst, copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close destination file %q: %w", dst, closeErr)
	}
	return bytesWritten, nil
}
