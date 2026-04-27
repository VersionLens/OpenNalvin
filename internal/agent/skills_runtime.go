package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
	"gopkg.in/yaml.v3"
)

const (
	skillActivationSystem = "system"
	skillActivationManual = "manual"
)

const (
	skillActiveReasonSystem    = "system"
	skillActiveReasonRequested = "requested"
	skillActiveReasonManual    = "manual"
)

const (
	skillScopeRoot  = "root"
	skillScopeChild = "child"
	skillScopeBoth  = "both"
)

const (
	activeSkillPathScheme   = "skill://"
	skillContainerMountRoot = "/skills"
)

// SkillDescriptor is a wire-format snapshot of a skill catalog entry.
type SkillDescriptor struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Activation        string   `json:"activation"`
	RunScopes         []string `json:"run_scopes,omitempty"`
	ToolHints         []string `json:"tool_hints,omitempty"`
	AutorevealTools   []string `json:"autoreveal_tools,omitempty"`
	Source            string   `json:"source,omitempty"`
	Path              string   `json:"path,omitempty"`
	SkillDir          string   `json:"skill_dir,omitempty"`
	HasScripts        bool     `json:"has_scripts,omitempty"`
	ContainerSkillDir string   `json:"container_skill_dir,omitempty"`
	Active            bool     `json:"active"`
}

// SkillCatalogResult is the JSON wire format for skills list/search.
type SkillCatalogResult struct {
	Skills   []SkillDescriptor `json:"skills"`
	Warnings []string          `json:"warnings,omitempty"`
}

type skillMetadata struct {
	Name              string
	Description       string
	Path              string
	Source            string
	SkillDir          string
	Activation        string
	RunScopes         []string
	ToolHints         []string
	AutorevealTools   []string
	Body              string
	HasScripts        bool
	ContainerSkillDir string

	// Extended manifest fields (from manifest.yaml alongside SKILL.md).
	Commands      []skillCommandManifest
	SetupSteps    []skillSetupStep
	ExecImage     string
	ExecNetworkOn bool
	ExecWritable  bool
}

type activeSkill struct {
	Name              string   `json:"name"`
	Reason            string   `json:"reason,omitempty"`
	Source            string   `json:"source,omitempty"`
	Path              string   `json:"path,omitempty"`
	SkillDir          string   `json:"skill_dir,omitempty"`
	Body              string   `json:"body,omitempty"`
	AutorevealTools   []string `json:"autoreveal_tools,omitempty"`
	HasScripts        bool     `json:"has_scripts,omitempty"`
	ContainerSkillDir string   `json:"container_skill_dir,omitempty"`
}

type storedSkillState struct {
	Active []activeSkill `json:"active,omitempty"`
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Activation  string `yaml:"activation,omitempty"`
	RunScopes   any    `yaml:"run_scopes,omitempty"`
	ToolHints   any    `yaml:"tool_hints,omitempty"`
	Metadata    struct {
		Agent struct {
			Activation      string `yaml:"activation,omitempty"`
			RunScopes       any    `yaml:"run_scopes,omitempty"`
			ToolHints       any    `yaml:"tool_hints,omitempty"`
			AutorevealTools any    `yaml:"autoreveal_tools,omitempty"`
		} `yaml:"agent,omitempty"`
	} `yaml:"metadata,omitempty"`
}

type skillManager struct {
	ctx        context.Context
	cfg        configpkg.Config
	runKind    string
	allTools   map[string]struct{}

	mu       sync.Mutex
	catalog  map[string]skillMetadata
	active   map[string]activeSkill
	warnings []string
	loaded   bool
}

func newSkillManager(ctx context.Context, cfg configpkg.Config, runKind string, tools map[string]runtimeTool, stored storedSkillState) *skillManager {
	manager := &skillManager{
		ctx:      ctx,
		cfg:      cfg,
		runKind:  strings.TrimSpace(runKind),
		allTools: map[string]struct{}{},
		catalog:  map[string]skillMetadata{},
		active:   map[string]activeSkill{},
	}
	for id := range tools {
		manager.allTools[id] = struct{}{}
	}
	for _, item := range stored.Active {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		manager.active[name] = activeSkill{
			Name:              name,
			Reason:            item.Reason,
			Source:            item.Source,
			Path:              item.Path,
			SkillDir:          item.SkillDir,
			Body:              item.Body,
			AutorevealTools:   append([]string(nil), item.AutorevealTools...),
			HasScripts:        item.HasScripts,
			ContainerSkillDir: item.ContainerSkillDir,
		}
	}
	return manager
}

func (sm *skillManager) initialize() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.loaded {
		return
	}
	sm.loaded = true
	sm.loadCatalogLocked()
	sm.activateSystemSkillsLocked()
}

func (sm *skillManager) loadCatalogLocked() {
	catalog, warnings := loadSkillCatalog(sm.ctx, sm.cfg)
	for name, skill := range catalog {
		if len(skill.AutorevealTools) == 0 {
			continue
		}
		filtered := make([]string, 0, len(skill.AutorevealTools))
		for _, id := range skill.AutorevealTools {
			if _, ok := sm.allTools[id]; !ok {
				warnings = append(warnings, fmt.Sprintf("skill %q autoreveal_tools includes unknown tool %q", name, id))
				continue
			}
			filtered = append(filtered, id)
		}
		skill.AutorevealTools = filtered
		catalog[name] = skill
	}
	sm.catalog = catalog
	sm.warnings = append(sm.warnings, warnings...)
}

func (sm *skillManager) activateSystemSkillsLocked() {
	for _, skill := range sm.catalog {
		if skill.Activation != skillActivationSystem {
			continue
		}
		if !skillMatchesRunScope(skill, sm.runKind) {
			continue
		}
		sm.activateLocked(skill, skillActiveReasonSystem)
	}
}

func (sm *skillManager) activateLocked(skill skillMetadata, reason string) {
	if _, ok := sm.active[skill.Name]; ok {
		return
	}
	sm.active[skill.Name] = activeSkill{
		Name:              skill.Name,
		Reason:            reason,
		Source:            skill.Source,
		Path:              skill.Path,
		SkillDir:          skill.SkillDir,
		Body:              strings.TrimSpace(skill.Body),
		AutorevealTools:   append([]string(nil), skill.AutorevealTools...),
		HasScripts:        skill.HasScripts,
		ContainerSkillDir: skill.ContainerSkillDir,
	}
}

func (sm *skillManager) activateManual(name string) (skillMetadata, error) {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	skill, ok := sm.catalog[strings.TrimSpace(name)]
	if !ok {
		return skillMetadata{}, fmt.Errorf("skill %q not found", name)
	}
	if !skillMatchesRunScope(skill, sm.runKind) {
		return skillMetadata{}, fmt.Errorf("skill %q is not available for this run kind", name)
	}
	sm.activateLocked(skill, skillActiveReasonManual)
	return skill, nil
}

func (sm *skillManager) deactivate(name string) bool {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	name = strings.TrimSpace(name)
	if _, ok := sm.active[name]; !ok {
		return false
	}
	delete(sm.active, name)
	return true
}

func (sm *skillManager) storedState() storedSkillState {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	names := make([]string, 0, len(sm.active))
	for name := range sm.active {
		names = append(names, name)
	}
	sort.Strings(names)
	state := storedSkillState{Active: make([]activeSkill, 0, len(names))}
	for _, name := range names {
		state.Active = append(state.Active, sm.active[name])
	}
	return state
}

func (sm *skillManager) activeMetadata() []activeSkill {
	return sm.storedState().Active
}

func (sm *skillManager) metadataByName(name string) (skillMetadata, bool) {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	meta, ok := sm.catalog[strings.TrimSpace(name)]
	return meta, ok
}

func (sm *skillManager) activeSkillByName(name string) (activeSkill, bool) {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	item, ok := sm.active[strings.TrimSpace(name)]
	return item, ok
}

func (sm *skillManager) allCatalog() []skillMetadata {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	out := make([]skillMetadata, 0, len(sm.catalog))
	for _, meta := range sm.catalog {
		out = append(out, meta)
	}
	return out
}

func (sm *skillManager) catalogWarnings() []string {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return append([]string(nil), sm.warnings...)
}

func (sm *skillManager) skillSummaries() []SkillDescriptor {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	names := make([]string, 0, len(sm.catalog))
	for name := range sm.catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SkillDescriptor, 0, len(names))
	for _, name := range names {
		skill := sm.catalog[name]
		_, active := sm.active[name]
		out = append(out, describeSkill(skill, active))
	}
	return out
}

func (sm *skillManager) search(query string, limit int) []SkillDescriptor {
	sm.initialize()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if limit <= 0 {
		limit = 25
	}
	query = strings.TrimSpace(strings.ToLower(query))
	skills := make([]skillMetadata, 0, len(sm.catalog))
	for _, skill := range sm.catalog {
		if !skillMatchesRunScope(skill, sm.runKind) {
			continue
		}
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	out := make([]SkillDescriptor, 0)
	for _, skill := range skills {
		if query != "" {
			if !strings.Contains(strings.ToLower(skill.Name), query) &&
				!strings.Contains(strings.ToLower(skill.Description), query) &&
				!strings.Contains(strings.ToLower(strings.Join(skill.ToolHints, " ")), query) {
				continue
			}
		}
		_, active := sm.active[skill.Name]
		out = append(out, describeSkill(skill, active))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func loadSkillCatalog(ctx context.Context, cfg configpkg.Config) (map[string]skillMetadata, []string) {
	catalog := map[string]skillMetadata{}
	var warnings []string
	register := func(skill skillMetadata) {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			return
		}
		if _, exists := catalog[name]; exists {
			return
		}
		catalog[name] = skill
	}

	// Workspace skills.
	if paths, err := workspacepkg.ActivePaths(ctx, cfg); err == nil {
		for _, dir := range workspaceSkillDirsFor(paths.FilesPath, cfg) {
			skills, nextWarnings := loadSkillDirectory(dir, "workspace")
			warnings = append(warnings, nextWarnings...)
			for _, skill := range skills {
				register(skill)
			}
		}
	}

	// User skills.
	for _, dir := range userSkillDirs(cfg) {
		skills, nextWarnings := loadSkillDirectory(dir, "user")
		warnings = append(warnings, nextWarnings...)
		for _, skill := range skills {
			register(skill)
		}
	}

	// Embedded skills.
	assetPaths, err := listEmbeddedSkillAssetPaths()
	if err != nil {
		return catalog, append(warnings, err.Error())
	}
	for _, assetPath := range assetPaths {
		data, readErr := bundledSkillsFS.ReadFile(assetPath)
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("embedded skill %s: %v", assetPath, readErr))
			continue
		}
		skill, parseErr := parseSkillMarkdown(string(data), "embedded:"+assetPath, "system")
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("embedded skill %s: %v", assetPath, parseErr))
			continue
		}
		register(skill)
	}

	return catalog, warnings
}

func userSkillDirs(cfg configpkg.Config) []string {
	configured := strings.TrimSpace(cfg.Agent.Skills.UserDir)
	if configured != "" {
		return []string{expandHome(configured)}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".nalvin", "skills")}
}

func workspaceSkillDirsFor(filesPath string, cfg configpkg.Config) []string {
	configured := strings.TrimSpace(cfg.Agent.Skills.WorkspaceDir)
	if configured != "" {
		return []string{configured}
	}
	return []string{filepath.Join(filesPath, ".nalvin", "skills")}
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

func loadSkillDirectory(rootDir, source string) ([]skillMetadata, []string) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("skills dir %s: %v", rootDir, err)}
	}
	var skills []skillMetadata
	var warnings []string
	for _, entry := range entries {
		skillDir := filepath.Join(rootDir, entry.Name())
		info, statErr := os.Stat(skillDir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		skillPath := filepath.Join(skillDir, "SKILL.md")
		data, readErr := os.ReadFile(skillPath)
		if readErr != nil {
			continue
		}
		skill, parseErr := parseSkillMarkdown(string(data), skillPath, source)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", skillPath, parseErr))
			continue
		}
		skill.SkillDir = skillDir

		// Pick up optional manifest.yaml for declared commands and exec config.
		ext, extErr := readSkillExtendedManifest(skillDir)
		if extErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", skillDir, extErr))
		} else {
			skill.Commands = ext.Commands
			skill.SetupSteps = ext.Setup
			skill.ExecImage = ext.execImage()
			skill.ExecNetworkOn = ext.execNetworkOn()
			skill.ExecWritable = ext.execWritable()
			if len(skill.Commands) > 0 {
				skill.HasScripts = true
				skill.ContainerSkillDir = filepath.ToSlash(filepath.Join(skillContainerMountRoot, filepath.Base(skillDir)))
			}
		}
		skills = append(skills, skill)
	}
	return skills, warnings
}

func parseSkillMarkdown(raw, resolvedPath, source string) (skillMetadata, error) {
	frontmatterRaw, body, err := splitMarkdownFrontmatter(raw)
	if err != nil {
		return skillMetadata{}, err
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatterRaw), &meta); err != nil {
		return skillMetadata{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return skillMetadata{}, fmt.Errorf("missing skill name")
	}
	description := strings.TrimSpace(meta.Description)
	if description == "" {
		return skillMetadata{}, fmt.Errorf("missing skill description")
	}
	activation := normalizeSkillActivation(firstNonEmptyString(
		strings.TrimSpace(meta.Metadata.Agent.Activation),
		strings.TrimSpace(meta.Activation),
		skillActivationManual,
	))
	runScopes := normalizeSkillScopes(meta.Metadata.Agent.RunScopes, meta.RunScopes)
	toolHints := normalizeSkillStringList(meta.Metadata.Agent.ToolHints, meta.ToolHints)
	autoreveal := normalizeSkillStringList(meta.Metadata.Agent.AutorevealTools)
	return skillMetadata{
		Name:            name,
		Description:     description,
		Path:            resolvedPath,
		Source:          source,
		SkillDir:        filepath.Dir(resolvedPath),
		Activation:      activation,
		RunScopes:       runScopes,
		ToolHints:       toolHints,
		AutorevealTools: autoreveal,
		Body:            strings.TrimSpace(body),
	}, nil
}

func splitMarkdownFrontmatter(raw string) (string, string, error) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", "", fmt.Errorf("missing YAML frontmatter")
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", "", fmt.Errorf("unterminated YAML frontmatter")
	}
	return rest[:end], rest[end+5:], nil
}

func normalizeSkillActivation(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case skillActivationSystem:
		return skillActivationSystem
	default:
		return skillActivationManual
	}
}

func normalizeSkillScopes(values ...any) []string {
	var out []string
	seen := map[string]struct{}{}
	addValue := func(item string) {
		switch strings.TrimSpace(strings.ToLower(item)) {
		case skillScopeRoot, skillScopeChild, skillScopeBoth:
			val := strings.TrimSpace(strings.ToLower(item))
			if _, ok := seen[val]; ok {
				return
			}
			seen[val] = struct{}{}
			out = append(out, val)
		}
	}
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			addValue(typed)
		case []string:
			for _, v := range typed {
				addValue(v)
			}
		case []any:
			for _, v := range typed {
				if s, ok := v.(string); ok {
					addValue(s)
				}
			}
		}
	}
	if len(out) == 0 {
		return []string{skillScopeBoth}
	}
	return out
}

func normalizeSkillStringList(values ...any) []string {
	var items []string
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			items = append(items, strings.Split(typed, ",")...)
		case []string:
			items = append(items, typed...)
		case []any:
			for _, v := range typed {
				if s, ok := v.(string); ok {
					items = append(items, s)
				}
			}
		}
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, value := range items {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func skillMatchesRunScope(skill skillMetadata, runKind string) bool {
	scope := skillScopeRoot
	if strings.TrimSpace(runKind) == RunKindChild {
		scope = skillScopeChild
	}
	for _, value := range skill.RunScopes {
		if value == skillScopeBoth || value == scope {
			return true
		}
	}
	return false
}

func describeSkill(skill skillMetadata, active bool) SkillDescriptor {
	return SkillDescriptor{
		Name:              skill.Name,
		Description:       skill.Description,
		Activation:        skill.Activation,
		RunScopes:         append([]string(nil), skill.RunScopes...),
		ToolHints:         append([]string(nil), skill.ToolHints...),
		AutorevealTools:   append([]string(nil), skill.AutorevealTools...),
		Source:            skill.Source,
		Path:              skill.Path,
		SkillDir:          skill.SkillDir,
		HasScripts:        skill.HasScripts,
		ContainerSkillDir: skill.ContainerSkillDir,
		Active:            active,
	}
}

// StaticListSkills returns the union catalog without requiring a runtime.
func StaticListSkills(ctx context.Context) (SkillCatalogResult, error) {
	cfg, _ := configpkg.FromContext(ctx)
	catalog, warnings := loadSkillCatalog(ctx, cfg)
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	skills := make([]SkillDescriptor, 0, len(names))
	for _, name := range names {
		skills = append(skills, describeSkill(catalog[name], false))
	}
	return SkillCatalogResult{Skills: skills, Warnings: warnings}, nil
}

// StaticSearchSkills searches the union catalog by query.
func StaticSearchSkills(ctx context.Context, query string, limit int) (SkillCatalogResult, error) {
	cfg, _ := configpkg.FromContext(ctx)
	catalog, warnings := loadSkillCatalog(ctx, cfg)
	if limit <= 0 {
		limit = 25
	}
	q := strings.TrimSpace(strings.ToLower(query))
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	skills := make([]SkillDescriptor, 0)
	for _, name := range names {
		skill := catalog[name]
		if q != "" {
			if !strings.Contains(strings.ToLower(skill.Name), q) &&
				!strings.Contains(strings.ToLower(skill.Description), q) &&
				!strings.Contains(strings.ToLower(strings.Join(skill.ToolHints, " ")), q) {
				continue
			}
		}
		skills = append(skills, describeSkill(skill, false))
		if len(skills) >= limit {
			break
		}
	}
	return SkillCatalogResult{Skills: skills, Warnings: warnings}, nil
}
