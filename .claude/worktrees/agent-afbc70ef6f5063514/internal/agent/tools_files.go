package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/versionlens/OpenNalvin/internal/config"
	"github.com/versionlens/OpenNalvin/internal/epubextract"
	fileservepkg "github.com/versionlens/OpenNalvin/internal/fileserve"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

const (
	viewDefaultLimit = 2000
	lsDefaultDepth   = 3
	lsResultLimit    = 1000
	globDefaultLimit = 100
	grepDefaultLimit = 100
)

type viewInput struct {
	Path   string `json:"path" jsonschema_description:"Workspace-relative path to the file to read."`
	Offset int    `json:"offset,omitempty" jsonschema_description:"Optional 0-based line offset to start from."`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"Optional max number of lines to return. Defaults to 2000."`
}

type lsInput struct {
	Path  string `json:"path,omitempty" jsonschema_description:"Optional workspace-relative directory path. Defaults to the workspace root."`
	Depth int    `json:"depth,omitempty" jsonschema_description:"Optional max traversal depth. Defaults to 3."`
}

type globInput struct {
	Pattern string `json:"pattern" jsonschema_description:"Glob pattern to match against workspace-relative file paths. Supports *, **, ?, character classes, and simple {a,b} groups."`
	Path    string `json:"path,omitempty" jsonschema_description:"Optional workspace-relative directory to search from. Defaults to the workspace root."`
	Limit   int    `json:"limit,omitempty" jsonschema_description:"Optional max number of matched paths to return. Defaults to 100."`
}

type grepInput struct {
	Pattern     string `json:"pattern" jsonschema_description:"Text or regex pattern to search for within workspace files."`
	Path        string `json:"path,omitempty" jsonschema_description:"Optional workspace-relative directory to search from. Defaults to the workspace root."`
	Include     string `json:"include,omitempty" jsonschema_description:"Optional glob filter for file paths, for example *.go or src/**/*.ts."`
	LiteralText bool   `json:"literal_text,omitempty" jsonschema_description:"If true, treat pattern as literal text instead of a regex."`
	Limit       int    `json:"limit,omitempty" jsonschema_description:"Optional max number of matches to return. Defaults to 100."`
}

type writeInput struct {
	Path    string `json:"path" jsonschema_description:"Workspace-relative path to create or replace."`
	Content string `json:"content" jsonschema_description:"Full file contents to write."`
}

type fileURLInput struct {
	Path    string `json:"path" jsonschema_description:"Workspace-relative file path to expose under nalvin serve."`
	BaseURL string `json:"base_url,omitempty" jsonschema_description:"Optional absolute base URL override, for example http://127.0.0.1:4210. Omit to derive it from the configured server address."`
}

type fileURLResult struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	BaseURL   string `json:"base_url"`
	URL       string `json:"url"`
}

type extractEPUBInput struct {
	Path    string `json:"path" jsonschema_description:"Workspace-relative EPUB file path to extract."`
	OutPath string `json:"out_path,omitempty" jsonschema_description:"Optional workspace-relative output directory. Defaults to <epub-basename>_extracted."`
	Force   bool   `json:"force,omitempty" jsonschema_description:"If true, replace an existing output directory before extracting."`
}

type editInput struct {
	Path       string `json:"path" jsonschema_description:"Workspace-relative path to update."`
	OldString  string `json:"old_string,omitempty" jsonschema_description:"Exact text to replace. Leave empty only to create a new file or replace an empty file."`
	NewString  string `json:"new_string,omitempty" jsonschema_description:"Replacement text."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema_description:"Replace every exact match instead of requiring a unique match."`
}

type multiEditInput struct {
	Path  string             `json:"path" jsonschema_description:"Workspace-relative path to update."`
	Edits []multiEditElement `json:"edits" jsonschema_description:"Ordered exact-match replacements to apply atomically."`
}

type multiEditElement struct {
	OldString  string `json:"old_string,omitempty" jsonschema_description:"Exact text to replace."`
	NewString  string `json:"new_string,omitempty" jsonschema_description:"Replacement text."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema_description:"Replace every exact match for this edit."`
}

type lsEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type grepMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	Preview    string `json:"preview"`
}

type rgJSONMessage struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

func (rt *agentRuntime) getWorkspaceFileURL(ctx context.Context, input fileURLInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, input.Path, false)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if info.IsDir() {
		return fantasy.NewTextErrorResponse("path refers to a directory; use a file path instead"), nil
	}
	if !info.Mode().IsRegular() {
		return fantasy.NewTextErrorResponse("path must refer to a regular file"), nil
	}

	fullURL, baseURL, err := fileservepkg.BuildWorkspaceFileURL(rt.cfg, paths.Name, relativePath, input.BaseURL)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(fileURLResult{
		Workspace: paths.Name,
		Path:      relativePath,
		BaseURL:   baseURL,
		URL:       fullURL,
	})
}

func (rt *agentRuntime) extractEPUB(ctx context.Context, input extractEPUBInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	paths, err := workspacepkg.ActivePaths(ctx, rt.cfg)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	result, err := epubextract.Extract(paths, epubextract.Options{
		InputPath:  input.Path,
		OutputPath: input.OutPath,
		Force:      input.Force,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(result)
}

func (rt *agentRuntime) viewFile(ctx context.Context, input viewInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool view path=%q offset=%d limit=%d", input.Path, input.Offset, input.Limit)

	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, input.Path, false)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if info.IsDir() {
		return fantasy.NewTextErrorResponse("path refers to a directory; use ls instead"), nil
	}

	contentBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if isBinaryContent(contentBytes) {
		return fantasy.NewTextErrorResponse("binary files are not supported by view"), nil
	}

	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	limit := input.Limit
	if limit <= 0 {
		limit = viewDefaultLimit
	}

	text := strings.ReplaceAll(string(contentBytes), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if offset > len(lines) {
		offset = len(lines)
	}
	end := offset + limit
	truncated := false
	if end < len(lines) {
		truncated = true
	} else {
		end = len(lines)
	}

	window := ""
	if offset < end {
		window = strings.Join(lines[offset:end], "\n")
	}

	startLine := 0
	if offset < end {
		startLine = offset + 1
	}

	return jsonToolResponse(map[string]any{
		"path":        relativePath,
		"offset":      offset,
		"limit":       limit,
		"start_line":  startLine,
		"end_line":    end,
		"total_lines": len(lines),
		"content":     window,
		"truncated":   truncated,
	})
}

func (rt *agentRuntime) listFiles(ctx context.Context, input lsInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool ls path=%q depth=%d", input.Path, input.Depth)

	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, defaultRootPath(input.Path), true)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if !info.IsDir() {
		return fantasy.NewTextErrorResponse("path refers to a file; use view instead"), nil
	}

	maxDepth := input.Depth
	if maxDepth <= 0 {
		maxDepth = lsDefaultDepth
	}

	entries := make([]lsEntry, 0)
	truncated := false
	rootDepth := strings.Count(filepath.ToSlash(absolutePath), "/")
	err = filepath.WalkDir(absolutePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == absolutePath {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in workspace paths")
		}

		relToRoot, err := filepath.Rel(absolutePath, path)
		if err != nil {
			return err
		}
		depth := strings.Count(filepath.ToSlash(path), "/") - rootDepth
		if depth > maxDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= lsResultLimit {
			truncated = true
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		}
		joinedPath := filepath.ToSlash(filepath.Join(relativePath, relToRoot))
		if relativePath == "." {
			joinedPath = filepath.ToSlash(relToRoot)
		}
		entries = append(entries, lsEntry{
			Path: joinedPath,
			Kind: kind,
		})
		return nil
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"path":      relativePath,
		"depth":     maxDepth,
		"entries":   entries,
		"truncated": truncated,
	})
}

func (rt *agentRuntime) globFiles(ctx context.Context, input globInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool glob pattern=%q path=%q limit=%d", input.Pattern, input.Path, input.Limit)

	if strings.TrimSpace(input.Pattern) == "" {
		return fantasy.NewTextErrorResponse("pattern is required"), nil
	}

	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, defaultRootPath(input.Path), true)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if !info.IsDir() {
		return fantasy.NewTextErrorResponse("path refers to a file; glob requires a directory"), nil
	}

	pattern, err := compileWorkspaceGlob(input.Pattern)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = globDefaultLimit
	}

	results := make([]string, 0)
	truncated := false
	err = filepath.WalkDir(absolutePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in workspace paths")
		}

		relPath, err := filepath.Rel(absolutePath, path)
		if err != nil {
			return err
		}
		candidate := filepath.ToSlash(relPath)
		if pattern.MatchString(candidate) {
			if len(results) >= limit {
				truncated = true
				return nil
			}
			if relativePath == "." {
				results = append(results, candidate)
			} else {
				results = append(results, filepath.ToSlash(filepath.Join(relativePath, candidate)))
			}
		}
		return nil
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	sort.Strings(results)
	return jsonToolResponse(map[string]any{
		"pattern":   input.Pattern,
		"path":      relativePath,
		"paths":     results,
		"truncated": truncated,
	})
}

func (rt *agentRuntime) grepFiles(ctx context.Context, input grepInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool grep pattern=%q path=%q include=%q literal=%t limit=%d", input.Pattern, input.Path, input.Include, input.LiteralText, input.Limit)

	if strings.TrimSpace(input.Pattern) == "" {
		return fantasy.NewTextErrorResponse("pattern is required"), nil
	}

	paths, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, defaultRootPath(input.Path), true)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	info, err := os.Stat(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = grepDefaultLimit
	}

	includePattern := (*regexp.Regexp)(nil)
	if strings.TrimSpace(input.Include) != "" {
		includePattern, err = compileWorkspaceGlob(input.Include)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
	}

	matches, truncated, err := grepWithRipgrep(paths.FilesPath, relativePath, input, includePattern, limit)
	if err != nil {
		matches, truncated, err = grepWithGo(relativePath, absolutePath, info.IsDir(), input, includePattern, limit)
	}
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"pattern":      input.Pattern,
		"path":         relativePath,
		"include":      strings.TrimSpace(input.Include),
		"literal_text": input.LiteralText,
		"matches":      matches,
		"truncated":    truncated,
	})
}

func (rt *agentRuntime) writeFile(ctx context.Context, input writeInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool write path=%q", input.Path)

	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, input.Path, false)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	action := "created"
	if _, err := os.Stat(absolutePath); err == nil {
		action = "updated"
	} else if !os.IsNotExist(err) {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	if err := os.WriteFile(absolutePath, []byte(input.Content), 0o644); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"path":   relativePath,
		"action": action,
		"bytes":  len(input.Content),
	})
}

func (rt *agentRuntime) editFile(ctx context.Context, input editInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool edit path=%q replace_all=%t", input.Path, input.ReplaceAll)

	result, err := applyExactEdit(ctx, rt.cfg, input.Path, input.OldString, input.NewString, input.ReplaceAll)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return jsonToolResponse(result)
}

func (rt *agentRuntime) multiEditFile(ctx context.Context, input multiEditInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	rt.session.debugf("tool multiedit path=%q edits=%d", input.Path, len(input.Edits))

	if len(input.Edits) == 0 {
		return fantasy.NewTextErrorResponse("at least one edit is required"), nil
	}

	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, rt.cfg, input.Path, false)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	content, action, err := readEditableFile(absolutePath)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	updated := content
	for index, edit := range input.Edits {
		next, _, editErr := applyExactEditString(updated, edit.OldString, edit.NewString, edit.ReplaceAll)
		if editErr != nil {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("edit %d failed: %v", index+1, editErr)), nil
		}
		updated = next
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if err := os.WriteFile(absolutePath, []byte(updated), 0o644); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	return jsonToolResponse(map[string]any{
		"path":         relativePath,
		"action":       action,
		"edits":        len(input.Edits),
		"bytes":        len(updated),
		"replacements": len(input.Edits),
	})
}

func grepWithRipgrep(rootPath, relativeRoot string, input grepInput, includePattern *regexp.Regexp, limit int) ([]grepMatch, bool, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, false, err
	}

	args := []string{"--json", "--line-number", "--hidden", "--no-ignore"}
	if input.LiteralText {
		args = append(args, "--fixed-strings")
	}
	if strings.TrimSpace(input.Include) != "" {
		args = append(args, "-g", input.Include)
	}
	searchRoot := "."
	if relativeRoot != "." {
		searchRoot = relativeRoot
	}
	args = append(args, input.Pattern, searchRoot)

	cmd := exec.CommandContext(context.Background(), "rg", args...)
	cmd.Dir = rootPath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}

	matches := make([]grepMatch, 0)
	truncated := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var payload rgJSONMessage
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			continue
		}
		if payload.Type != "match" {
			continue
		}
		path := filepath.ToSlash(payload.Data.Path.Text)
		if includePattern != nil && !includePattern.MatchString(path) {
			continue
		}
		if len(matches) >= limit {
			truncated = true
			continue
		}
		matches = append(matches, grepMatch{
			Path:       path,
			LineNumber: payload.Data.LineNumber,
			Preview:    strings.TrimRight(payload.Data.Lines.Text, "\r\n"),
		})
	}
	stderrBytes, _ := io.ReadAll(stderr)
	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, false, scanErr
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return matches, truncated, nil
		}
		if len(matches) == 0 {
			message := strings.TrimSpace(string(stderrBytes))
			if message == "" {
				message = waitErr.Error()
			}
			return nil, false, fmt.Errorf("ripgrep search failed: %s", message)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].LineNumber < matches[j].LineNumber
	})

	return matches, truncated, nil
}

func grepWithGo(relativeRoot, absoluteRoot string, isDir bool, input grepInput, includePattern *regexp.Regexp, limit int) ([]grepMatch, bool, error) {
	var matcher *regexp.Regexp
	var err error
	if input.LiteralText {
		matcher, err = regexp.Compile(regexp.QuoteMeta(input.Pattern))
	} else {
		matcher, err = regexp.Compile(input.Pattern)
	}
	if err != nil {
		return nil, false, fmt.Errorf("compile grep pattern: %w", err)
	}

	matches := make([]grepMatch, 0)
	truncated := false
	appendMatches := func(fullPath, candidate string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in workspace paths")
		}
		if includePattern != nil && !includePattern.MatchString(candidate) {
			return nil
		}

		contentBytes, err := os.ReadFile(fullPath)
		if err != nil {
			return err
		}
		if isBinaryContent(contentBytes) {
			return nil
		}

		lines := strings.Split(strings.ReplaceAll(string(contentBytes), "\r\n", "\n"), "\n")
		for index, line := range lines {
			if !matcher.MatchString(line) {
				continue
			}
			if len(matches) >= limit {
				truncated = true
				return nil
			}
			matches = append(matches, grepMatch{
				Path:       candidate,
				LineNumber: index + 1,
				Preview:    line,
			})
		}
		return nil
	}

	if !isDir {
		info, err := os.Lstat(absoluteRoot)
		if err != nil {
			return nil, false, err
		}
		err = appendMatches(absoluteRoot, relativeRoot, fs.FileInfoToDirEntry(info))
	} else {
		err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == absoluteRoot {
				return nil
			}
			relPath, relErr := filepath.Rel(absoluteRoot, path)
			if relErr != nil {
				return relErr
			}
			candidate := filepath.ToSlash(relPath)
			if relativeRoot != "." {
				candidate = filepath.ToSlash(filepath.Join(relativeRoot, candidate))
			}
			return appendMatches(path, candidate, entry)
		})
	}
	if err != nil {
		return nil, false, err
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].LineNumber < matches[j].LineNumber
	})
	return matches, truncated, nil
}

func applyExactEdit(ctx context.Context, cfg config.Config, rawPath, oldString, newString string, replaceAll bool) (map[string]any, error) {
	_, relativePath, absolutePath, err := workspacepkg.ResolveFilePath(ctx, cfg, rawPath, false)
	if err != nil {
		return nil, err
	}

	content, action, err := readEditableFile(absolutePath)
	if err != nil {
		return nil, err
	}

	updated, replacements, err := applyExactEditString(content, oldString, newString, replaceAll)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(absolutePath, []byte(updated), 0o644); err != nil {
		return nil, err
	}

	return map[string]any{
		"path":         relativePath,
		"action":       action,
		"replacements": replacements,
		"bytes":        len(updated),
	}, nil
}

func applyExactEditString(content, oldString, newString string, replaceAll bool) (string, int, error) {
	if oldString == "" {
		if content != "" {
			return "", 0, fmt.Errorf("old_string may only be empty when creating a new file or replacing an empty file")
		}
		return newString, 1, nil
	}

	count := strings.Count(content, oldString)
	if count == 0 {
		return "", 0, fmt.Errorf("old_string not found in file")
	}
	if !replaceAll && count != 1 {
		return "", 0, fmt.Errorf("old_string matched %d locations; provide more context or use replace_all", count)
	}
	if replaceAll {
		return strings.ReplaceAll(content, oldString, newString), count, nil
	}
	return strings.Replace(content, oldString, newString, 1), 1, nil
}

func readEditableFile(path string) (string, string, error) {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "created", nil
		}
		return "", "", err
	}
	if isBinaryContent(contentBytes) {
		return "", "", fmt.Errorf("binary files are not supported by edit tools")
	}
	return string(contentBytes), "updated", nil
}

func compileWorkspaceGlob(pattern string) (*regexp.Regexp, error) {
	regex, err := globToRegex(pattern)
	if err != nil {
		return nil, err
	}
	return regexp.Compile("^" + regex + "$")
}

func globToRegex(pattern string) (string, error) {
	var out strings.Builder
	inClass := false

	for index := 0; index < len(pattern); index++ {
		ch := pattern[index]
		switch ch {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				out.WriteString(".*")
				index++
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		case '{':
			end := strings.IndexByte(pattern[index+1:], '}')
			if end < 0 {
				return "", fmt.Errorf("invalid glob pattern %q", pattern)
			}
			parts := strings.Split(pattern[index+1:index+1+end], ",")
			out.WriteString("(?:")
			for partIndex, part := range parts {
				if partIndex > 0 {
					out.WriteByte('|')
				}
				out.WriteString(regexp.QuoteMeta(part))
			}
			out.WriteString(")")
			index += end + 1
		case '[':
			inClass = true
			out.WriteByte(ch)
		case ']':
			inClass = false
			out.WriteByte(ch)
		case '\\':
			out.WriteString("\\\\")
		default:
			if inClass {
				out.WriteByte(ch)
				continue
			}
			if strings.ContainsRune(".+()|^$", rune(ch)) {
				out.WriteByte('\\')
			}
			out.WriteByte(ch)
		}
	}

	if inClass {
		return "", fmt.Errorf("invalid glob pattern %q", pattern)
	}
	return out.String(), nil
}

func defaultRootPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}
	return path
}

func isBinaryContent(content []byte) bool {
	if bytes.IndexByte(content, 0) >= 0 {
		return true
	}
	return !utf8.Valid(content)
}
