package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// skillExtendedManifest is an OPTIONAL manifest.yaml that can sit alongside a
// skill's SKILL.md.
//
// A skill that declares commands gets each command auto-registered as an
// agent tool when the skill is activated. The tool's dispatcher runs the
// entry inside the skill-runner container with the skill directory bind-
// mounted at /skills/<name>/.
type skillExtendedManifest struct {
	Name     string                 `yaml:"name,omitempty"`
	Version  string                 `yaml:"version,omitempty"`
	Kind     string                 `yaml:"kind,omitempty"`
	Commands []skillCommandManifest `yaml:"commands,omitempty"`
	Setup    []skillSetupStep       `yaml:"setup,omitempty"`
	Exec     *skillExecConfig       `yaml:"exec,omitempty"`
}

// skillCommandManifest declares one agent-tool-shaped command. Its tool id
// is Name (no prefix). Entry is the path (relative to the skill dir) of
// the binary/script to run inside the container.
type skillCommandManifest struct {
	Name        string `yaml:"name"`
	Entry       string `yaml:"entry"`
	Description string `yaml:"description,omitempty"`
	Timeout     int    `yaml:"timeout,omitempty"`
}

// skillSetupStep runs once per session before the first command is called.
// Each step runs in the container with CWD = skill-dir-inside-container
// (or Cwd, if set, relative to the skill root).
type skillSetupStep struct {
	Run string `yaml:"run"`
	Cwd string `yaml:"cwd,omitempty"`
}

// skillExecConfig lets a skill override container defaults. All fields are
// optional.
type skillExecConfig struct {
	Image    string `yaml:"image,omitempty"`    // default: nalvin/dev
	Network  string `yaml:"network,omitempty"`  // "on" | "off"; default "on"
	Writable bool   `yaml:"writable,omitempty"` // mount skill dir RW (default true when commands are declared)
}

const skillExtendedManifestName = "manifest.yaml"

// readSkillExtendedManifest reads manifest.yaml from skillDir. Returns a
// zero-value manifest (and no error) when the file is absent.
func readSkillExtendedManifest(skillDir string) (skillExtendedManifest, error) {
	if strings.TrimSpace(skillDir) == "" {
		return skillExtendedManifest{}, nil
	}
	path := filepath.Join(skillDir, skillExtendedManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return skillExtendedManifest{}, nil
		}
		return skillExtendedManifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m skillExtendedManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return skillExtendedManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	m.normalise()
	return m, nil
}

func (m *skillExtendedManifest) normalise() {
	out := m.Commands[:0]
	seen := map[string]struct{}{}
	for _, c := range m.Commands {
		c.Name = strings.TrimSpace(c.Name)
		c.Entry = strings.TrimSpace(c.Entry)
		c.Description = strings.TrimSpace(c.Description)
		if c.Name == "" || c.Entry == "" {
			continue
		}
		if _, dup := seen[c.Name]; dup {
			continue
		}
		seen[c.Name] = struct{}{}
		out = append(out, c)
	}
	m.Commands = out

	steps := m.Setup[:0]
	for _, s := range m.Setup {
		s.Run = strings.TrimSpace(s.Run)
		s.Cwd = strings.TrimSpace(s.Cwd)
		if s.Run == "" {
			continue
		}
		steps = append(steps, s)
	}
	m.Setup = steps
}

// hasCommands reports whether the skill declares any agent-tool commands.
func (m skillExtendedManifest) hasCommands() bool {
	return len(m.Commands) > 0
}

// execImage returns the container image to use. Defaults to nalvin/dev.
func (m skillExtendedManifest) execImage() string {
	if m.Exec != nil && strings.TrimSpace(m.Exec.Image) != "" {
		return strings.TrimSpace(m.Exec.Image)
	}
	return "nalvin/dev"
}

// execNetworkOn reports whether network access should be enabled. Default true.
func (m skillExtendedManifest) execNetworkOn() bool {
	if m.Exec == nil || strings.TrimSpace(m.Exec.Network) == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(m.Exec.Network)) {
	case "off", "none", "false", "no", "disabled":
		return false
	}
	return true
}

// execWritable reports whether the skill dir should be mounted RW. Default
// true when commands are declared.
func (m skillExtendedManifest) execWritable() bool {
	if m.Exec == nil {
		return m.hasCommands()
	}
	return m.Exec.Writable || m.hasCommands()
}
