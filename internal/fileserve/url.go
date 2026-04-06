package fileserve

import (
	"fmt"
	"net"
	"net/url"
	pathpkg "path"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

func BuildWorkspaceFileURL(cfg config.Config, workspaceName, relativePath, baseOverride string) (string, string, error) {
	if err := workspacepkg.ValidateName(workspaceName); err != nil {
		return "", "", err
	}

	cleanPath, err := cleanURLRelativePath(relativePath)
	if err != nil {
		return "", "", err
	}

	baseURL, err := resolveBaseURL(cfg.Server.Addr, baseOverride)
	if err != nil {
		return "", "", err
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", "", fmt.Errorf("parse base url: %w", err)
	}

	segments := []string{"files", workspaceName}
	segments = append(segments, strings.Split(cleanPath, "/")...)
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(segment))
	}

	basePath := strings.TrimSuffix(parsed.Path, "/")
	baseRawPath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	joinedPath := strings.Join(escaped, "/")
	unescapedPath := strings.Join(segments, "/")
	if basePath == "" || basePath == "/" {
		parsed.Path = "/" + unescapedPath
		parsed.RawPath = "/" + joinedPath
	} else {
		parsed.Path = basePath + "/" + unescapedPath
		if baseRawPath == "" || baseRawPath == "/" {
			parsed.RawPath = "/" + joinedPath
		} else {
			parsed.RawPath = baseRawPath + "/" + joinedPath
		}
	}

	return parsed.String(), baseURL, nil
}

func cleanURLRelativePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if value == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("path must be workspace-relative")
	}

	cleaned := pathpkg.Clean(value)
	if cleaned == "." {
		return "", fmt.Errorf("path must not resolve to the workspace root")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path must stay within the workspace root")
	}
	return cleaned, nil
}

func resolveBaseURL(serverAddr, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		parsed, err := url.Parse(strings.TrimSpace(override))
		if err != nil {
			return "", fmt.Errorf("parse base url override: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" {
			return "", fmt.Errorf("base url override must be absolute, for example http://127.0.0.1:4210")
		}
		return strings.TrimSuffix(parsed.String(), "/"), nil
	}

	addr := strings.TrimSpace(serverAddr)
	if addr == "" {
		return "", fmt.Errorf("server address is not configured")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("parse server address %q: %w", addr, err)
	}

	host = normalizeServeHost(host)
	if port == "" {
		return "http://" + host, nil
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func normalizeServeHost(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
}
