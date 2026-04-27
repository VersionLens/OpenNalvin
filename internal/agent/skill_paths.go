package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

type resolvedReadablePath struct {
	RootDir      string
	RelativePath string
	AbsolutePath string
	DisplayPath  string
}

func (rt *agentRuntime) resolveReadablePath(ctx context.Context, rawPath string, allowRoot bool) (resolvedReadablePath, error) {
	rawPath = strings.TrimSpace(rawPath)
	if strings.HasPrefix(rawPath, activeSkillPathScheme) {
		return rt.resolveActiveSkillPath(rawPath, allowRoot)
	}
	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, rawPath, allowRoot)
	if err != nil {
		return resolvedReadablePath{}, err
	}
	return resolvedReadablePath{
		RootDir:      filepath.Dir(absolutePath),
		RelativePath: relativePath,
		AbsolutePath: absolutePath,
		DisplayPath:  relativePath,
	}, nil
}

func (rt *agentRuntime) resolveReadableRoot(ctx context.Context, rawPath string) (resolvedReadablePath, error) {
	rawPath = defaultRootPath(rawPath)
	if strings.HasPrefix(strings.TrimSpace(rawPath), activeSkillPathScheme) {
		return rt.resolveActiveSkillPath(rawPath, true)
	}
	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, rawPath, true)
	if err != nil {
		return resolvedReadablePath{}, err
	}
	return resolvedReadablePath{
		RootDir:      absolutePath,
		RelativePath: relativePath,
		AbsolutePath: absolutePath,
		DisplayPath:  relativePath,
	}, nil
}

func (rt *agentRuntime) resolveActiveSkillPath(rawPath string, allowRoot bool) (resolvedReadablePath, error) {
	if rt.skills == nil {
		return resolvedReadablePath{}, fmt.Errorf("skills are not available in this run")
	}
	value := strings.TrimSpace(strings.TrimPrefix(rawPath, activeSkillPathScheme))
	if value == "" {
		return resolvedReadablePath{}, fmt.Errorf("skill path must include the active skill name")
	}
	skillName := value
	relativeRaw := "."
	if slash := strings.Index(value, "/"); slash >= 0 {
		skillName = value[:slash]
		relativeRaw = value[slash+1:]
	}
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return resolvedReadablePath{}, fmt.Errorf("skill path must include the active skill name")
	}
	active, ok := rt.skills.activeSkillByName(skillName)
	if !ok {
		return resolvedReadablePath{}, fmt.Errorf("skill %q is not active; activate it before reading skill resources", skillName)
	}
	rootDir := strings.TrimSpace(active.SkillDir)
	if rootDir == "" {
		rootDir = filepath.Dir(strings.TrimSpace(active.Path))
	}
	if rootDir == "" {
		return resolvedReadablePath{}, fmt.Errorf("skill %q has no readable skill directory", skillName)
	}
	relativePath, err := cleanResolvedRelativePath(relativeRaw, allowRoot)
	if err != nil {
		return resolvedReadablePath{}, err
	}
	absolutePath := filepath.Join(rootDir, filepath.FromSlash(relativePath))
	if err := ensureWithinResolvedRoot(rootDir, absolutePath); err != nil {
		return resolvedReadablePath{}, err
	}
	if err := ensureNoSymlinkTraversalResolved(rootDir, absolutePath); err != nil {
		return resolvedReadablePath{}, err
	}
	displayPath := activeSkillPathScheme + skillName
	if relativePath != "." {
		displayPath += "/" + relativePath
	}
	return resolvedReadablePath{
		RootDir:      rootDir,
		RelativePath: relativePath,
		AbsolutePath: absolutePath,
		DisplayPath:  displayPath,
	}, nil
}

func cleanResolvedRelativePath(rawPath string, allowRoot bool) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rawPath) || strings.HasPrefix(filepath.ToSlash(rawPath), "/") {
		return "", fmt.Errorf("path must stay within the resolved root")
	}
	cleaned := filepath.Clean(filepath.FromSlash(rawPath))
	if cleaned == "." {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("path must not resolve to the root")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay within the resolved root")
	}
	return filepath.ToSlash(cleaned), nil
}

func ensureWithinResolvedRoot(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", target, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes the resolved root")
	}
	return nil
}

func ensureNoSymlinkTraversalResolved(root, target string) error {
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
			return fmt.Errorf("symlinks are not allowed in skill paths")
		}
	}
	return nil
}

func displayJoinedResolvedPath(rootDisplay, relative string) string {
	if relative == "." || relative == "" {
		return rootDisplay
	}
	if strings.HasPrefix(rootDisplay, activeSkillPathScheme) {
		return strings.TrimRight(rootDisplay, "/") + "/" + filepath.ToSlash(relative)
	}
	if rootDisplay == "." {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(filepath.Join(rootDisplay, relative))
}
