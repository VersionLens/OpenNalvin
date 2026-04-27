package agent

import (
	"embed"
	"path"
	"sort"
	"strings"
)

//go:embed skills/*.md
var bundledSkillsFS embed.FS

func listEmbeddedSkillAssetPaths() ([]string, error) {
	entries, err := bundledSkillsFS.ReadDir("skills")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		out = append(out, path.Join("skills", entry.Name()))
	}
	sort.Strings(out)
	return out, nil
}
