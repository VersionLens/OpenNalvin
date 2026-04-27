package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	workspacepkg "github.com/versionlens/OpenNalvin/internal/workspace"
)

const (
	managedSkillManifestName          = ".nalvin-skill.json"
	defaultRemoteSkillsSearchURL      = "https://skills.sh/api/search"
	defaultRemoteSkillsDirectoryURL   = "https://skills.sh"
	envRemoteSkillsSearchURL          = "NALVIN_SKILLS_REMOTE_SEARCH_URL"
	envRemoteSkillsDirectoryURL       = "NALVIN_SKILLS_REMOTE_DIRECTORY_URL"
	managedSkillExecutionPolicyDocker = "docker_exec"
	managedSkillExecutionPolicyHost   = "host"
)

var (
	validManagedSkillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	nonSkillSlugPattern          = regexp.MustCompile(`[^a-z0-9]+`)
	remoteSkillsFlightChunkRE    = regexp.MustCompile(`self\.__next_f\.push\(\[1,"((?:[^"\\]|\\.)*)"\]`)
	remoteLeaderboardPayloadRE   = regexp.MustCompile(`(?s)\{"initialSkills":\[(.*?)\],"totalSkills":(\d+),"allTimeTotal":(\d+),"view":"([^"]+)"\}`)
)

type managedSkillManifest struct {
	Managed         bool      `json:"managed"`
	ThirdParty      bool      `json:"third_party,omitempty"`
	InstallSource   string    `json:"install_source,omitempty"`
	InstallRef      string    `json:"install_ref,omitempty"`
	DiscoveredPath  string    `json:"discovered_path,omitempty"`
	InstalledAt     time.Time `json:"installed_at,omitempty"`
	LocallyModified bool      `json:"locally_modified,omitempty"`
}

type RemoteSkillDescriptor struct {
	ID                string `json:"id,omitempty"`
	SkillID           string `json:"skill_id,omitempty"`
	Name              string `json:"name"`
	Slug              string `json:"slug,omitempty"`
	Installs          int    `json:"installs,omitempty"`
	InstallsYesterday int    `json:"installs_yesterday,omitempty"`
	Change            int    `json:"change,omitempty"`
	Rank              int    `json:"rank,omitempty"`
	Source            string `json:"source,omitempty"`
	SourceURL         string `json:"source_url,omitempty"`
	DirectoryURL      string `json:"directory_url,omitempty"`
}

type BrowseRemoteSkillsResult struct {
	Query        string                  `json:"query"`
	View         string                  `json:"view,omitempty"`
	Sort         string                  `json:"sort,omitempty"`
	SearchType   string                  `json:"search_type,omitempty"`
	Count        int                     `json:"count"`
	TotalSkills  int                     `json:"total_skills,omitempty"`
	AllTimeTotal int                     `json:"all_time_total,omitempty"`
	SourceFilter string                  `json:"source_filter,omitempty"`
	MinInstalls  int                     `json:"min_installs,omitempty"`
	Skills       []RemoteSkillDescriptor `json:"skills"`
}

type BrowseRemoteSkillsRequest struct {
	Query       string
	View        string
	Sort        string
	Source      string
	MinInstalls int
	Limit       int
}

type InstallSkillRequest struct {
	Source    string `json:"source"`
	SkillName string `json:"skill_name,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Replace   bool   `json:"replace,omitempty"`
}

type InstallSkillResult struct {
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	SkillDir        string   `json:"skill_dir"`
	Managed         bool     `json:"managed"`
	ThirdParty      bool     `json:"third_party"`
	InstallSource   string   `json:"install_source,omitempty"`
	InstallRef      string   `json:"install_ref,omitempty"`
	ResourcePaths   []string `json:"resource_paths,omitempty"`
	HasScripts      bool     `json:"has_scripts,omitempty"`
	LocallyModified bool     `json:"locally_modified,omitempty"`
	Replaced        bool     `json:"replaced,omitempty"`
}

type CreateSkillRequest struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Resources         []string `json:"resources,omitempty"`
	IncludeOpenAIYAML bool     `json:"include_openai_yaml,omitempty"`
	Replace           bool     `json:"replace,omitempty"`
}

type CreateSkillResult struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	SkillDir      string   `json:"skill_dir"`
	ResourcePaths []string `json:"resource_paths,omitempty"`
}

type ModifySkillEdit struct {
	OldString  string `json:"old_string,omitempty"`
	NewString  string `json:"new_string,omitempty"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type ModifySkillRequest struct {
	Name       string            `json:"name"`
	Path       string            `json:"path,omitempty"`
	Content    string            `json:"content,omitempty"`
	OldString  string            `json:"old_string,omitempty"`
	NewString  string            `json:"new_string,omitempty"`
	ReplaceAll bool              `json:"replace_all,omitempty"`
	Edits      []ModifySkillEdit `json:"edits,omitempty"`
}

type ModifySkillResult struct {
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	AbsolutePath     string   `json:"absolute_path"`
	Action           string   `json:"action"`
	Replacements     int      `json:"replacements,omitempty"`
	Bytes            int      `json:"bytes"`
	LocallyModified  bool     `json:"locally_modified"`
	Validated        bool     `json:"validated,omitempty"`
	SkillName        string   `json:"skill_name,omitempty"`
	SkillDescription string   `json:"skill_description,omitempty"`
	Activation       string   `json:"activation,omitempty"`
	RunScopes        []string `json:"run_scopes,omitempty"`
	AutorevealTools  []string `json:"autoreveal_tools,omitempty"`
}

type remoteSkillsSearchResponse struct {
	Query      string `json:"query"`
	SearchType string `json:"searchType"`
	Count      int    `json:"count"`
	Skills     []struct {
		ID       string `json:"id"`
		SkillID  string `json:"skillId"`
		Name     string `json:"name"`
		Installs int    `json:"installs"`
		Source   string `json:"source"`
	} `json:"skills"`
}

type remoteSkillsLeaderboardPayload struct {
	InitialSkills []struct {
		Source            string `json:"source"`
		SkillID           string `json:"skillId"`
		Name              string `json:"name"`
		Installs          int    `json:"installs"`
		InstallsYesterday int    `json:"installsYesterday"`
		Change            int    `json:"change"`
	} `json:"initialSkills"`
	TotalSkills  int    `json:"totalSkills"`
	AllTimeTotal int    `json:"allTimeTotal"`
	View         string `json:"view"`
}

type installSourceSpec struct {
	Raw      string
	LocalDir string
	RepoURL  string
	Ref      string
	Subpath  string
}

type discoveredSkill struct {
	Name        string
	Description string
	Dir         string
	RelativeDir string
}

var managedSkillReadToolIDs = []string{"view", "list_files", "glob", "grep"}

func browseRemoteSkillsSearch(ctx context.Context, query string, limit int) (BrowseRemoteSkillsResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return BrowseRemoteSkillsResult{}, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 10
	}

	baseURL := strings.TrimSpace(os.Getenv(envRemoteSkillsSearchURL))
	if baseURL == "" {
		baseURL = defaultRemoteSkillsSearchURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return BrowseRemoteSkillsResult{}, fmt.Errorf("parse remote skills url: %w", err)
	}
	values := parsed.Query()
	values.Set("q", query)
	values.Set("limit", fmt.Sprintf("%d", limit))
	parsed.RawQuery = values.Encode()

	resp, err := remoteSkillsHTTPGet(ctx, parsed.String())
	if err != nil {
		return BrowseRemoteSkillsResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return BrowseRemoteSkillsResult{}, fmt.Errorf("remote skill search failed: %s", strings.TrimSpace(string(body)))
	}

	var raw remoteSkillsSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return BrowseRemoteSkillsResult{}, fmt.Errorf("decode remote skill search: %w", err)
	}
	result := BrowseRemoteSkillsResult{
		Query:      strings.TrimSpace(raw.Query),
		SearchType: strings.TrimSpace(raw.SearchType),
		Count:      raw.Count,
		Skills:     make([]RemoteSkillDescriptor, 0, len(raw.Skills)),
	}
	for _, item := range raw.Skills {
		result.Skills = append(result.Skills, remoteSkillDescriptorFromValues(
			strings.TrimSpace(item.ID),
			strings.TrimSpace(item.SkillID),
			strings.TrimSpace(item.Name),
			item.Installs,
			0,
			0,
			strings.TrimSpace(item.Source),
		))
	}
	return result, nil
}

func browseRemoteSkillsLeaderboard(ctx context.Context, view, sortBy, sourceFilter string, minInstalls, limit int) (BrowseRemoteSkillsResult, error) {
	if view == "" {
		view = "all-time"
	}
	baseURL := strings.TrimSpace(os.Getenv(envRemoteSkillsDirectoryURL))
	if baseURL == "" {
		baseURL = defaultRemoteSkillsDirectoryURL
	}
	pageURL, err := remoteSkillsLeaderboardURL(baseURL, view)
	if err != nil {
		return BrowseRemoteSkillsResult{}, err
	}
	resp, err := remoteSkillsHTTPGet(ctx, pageURL)
	if err != nil {
		return BrowseRemoteSkillsResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return BrowseRemoteSkillsResult{}, fmt.Errorf("remote skill leaderboard failed: %s", strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return BrowseRemoteSkillsResult{}, err
	}
	payload, err := parseRemoteSkillsLeaderboardHTML(string(body), view)
	if err != nil {
		return BrowseRemoteSkillsResult{}, err
	}
	result := BrowseRemoteSkillsResult{
		View:         payload.View,
		Sort:         sortBy,
		TotalSkills:  payload.TotalSkills,
		AllTimeTotal: payload.AllTimeTotal,
		SourceFilter: strings.TrimSpace(sourceFilter),
		MinInstalls:  minInstalls,
		Skills:       make([]RemoteSkillDescriptor, 0, len(payload.InitialSkills)),
	}
	for index, item := range payload.InitialSkills {
		descriptor := remoteSkillDescriptorFromValues(
			"",
			strings.TrimSpace(item.SkillID),
			strings.TrimSpace(item.Name),
			item.Installs,
			item.InstallsYesterday,
			item.Change,
			strings.TrimSpace(item.Source),
		)
		descriptor.Rank = index + 1
		result.Skills = append(result.Skills, descriptor)
	}
	result.Skills = filterAndSortRemoteSkills(result.Skills, sortBy, sourceFilter, minInstalls, true)
	if limit > 0 && len(result.Skills) > limit {
		result.Skills = result.Skills[:limit]
	}
	result.Count = len(result.Skills)
	return result, nil
}

func remoteSkillsHTTPGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func remoteSkillsLeaderboardURL(baseURL, view string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse remote skills directory url: %w", err)
	}
	switch normalizeRemoteSkillsView(view) {
	case "", "all-time":
		parsed.Path = "/"
	case "trending":
		parsed.Path = "/trending"
	case "hot":
		parsed.Path = "/hot"
	default:
		return "", fmt.Errorf("unsupported remote skills view %q", view)
	}
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func parseRemoteSkillsLeaderboardHTML(html, requestedView string) (remoteSkillsLeaderboardPayload, error) {
	for _, match := range remoteSkillsFlightChunkRE.FindAllStringSubmatch(html, -1) {
		if len(match) < 2 {
			continue
		}
		decoded, err := strconv.Unquote(`"` + match[1] + `"`)
		if err != nil {
			continue
		}
		payloadMatch := remoteLeaderboardPayloadRE.FindStringSubmatch(decoded)
		if len(payloadMatch) != 5 {
			continue
		}
		itemsJSON := "[" + payloadMatch[1] + "]"
		var skills []struct {
			Source            string `json:"source"`
			SkillID           string `json:"skillId"`
			Name              string `json:"name"`
			Installs          int    `json:"installs"`
			InstallsYesterday int    `json:"installsYesterday"`
			Change            int    `json:"change"`
		}
		if err := json.Unmarshal([]byte(itemsJSON), &skills); err != nil {
			continue
		}
		totalSkills, err := strconv.Atoi(payloadMatch[2])
		if err != nil {
			continue
		}
		allTimeTotal, err := strconv.Atoi(payloadMatch[3])
		if err != nil {
			continue
		}
		view := strings.TrimSpace(payloadMatch[4])
		if requestedView != "" && normalizeRemoteSkillsView(view) != normalizeRemoteSkillsView(requestedView) {
			continue
		}
		return remoteSkillsLeaderboardPayload{
			InitialSkills: skills,
			TotalSkills:   totalSkills,
			AllTimeTotal:  allTimeTotal,
			View:          view,
		}, nil
	}
	return remoteSkillsLeaderboardPayload{}, fmt.Errorf("decode remote skills leaderboard")
}

func remoteSkillDescriptorFromValues(id, skillID, name string, installs, installsYesterday, change int, source string) RemoteSkillDescriptor {
	slug := strings.TrimSpace(skillID)
	if slug == "" {
		slug = strings.TrimSpace(name)
	}
	return RemoteSkillDescriptor{
		ID:                strings.TrimSpace(id),
		SkillID:           strings.TrimSpace(skillID),
		Name:              strings.TrimSpace(name),
		Slug:              slug,
		Installs:          installs,
		InstallsYesterday: installsYesterday,
		Change:            change,
		Source:            source,
		SourceURL:         remoteSkillSourceURL(source),
		DirectoryURL:      remoteSkillDirectoryURL(source, slug),
	}
}

func filterAndSortRemoteSkills(skills []RemoteSkillDescriptor, sortBy, sourceFilter string, minInstalls int, preserveRank bool) []RemoteSkillDescriptor {
	filtered := make([]RemoteSkillDescriptor, 0, len(skills))
	for _, skill := range skills {
		if sourceFilter != "" && !strings.Contains(strings.ToLower(skill.Source), sourceFilter) {
			continue
		}
		if minInstalls > 0 && skill.Installs < minInstalls {
			continue
		}
		filtered = append(filtered, skill)
	}
	switch sortBy {
	case "", "rank", "relevance":
		if !preserveRank {
			return filtered
		}
	case "installs":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Installs != filtered[j].Installs {
				return filtered[i].Installs > filtered[j].Installs
			}
			return filtered[i].Name < filtered[j].Name
		})
	case "change":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Change != filtered[j].Change {
				return filtered[i].Change > filtered[j].Change
			}
			if filtered[i].Installs != filtered[j].Installs {
				return filtered[i].Installs > filtered[j].Installs
			}
			return filtered[i].Name < filtered[j].Name
		})
	case "name":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Name != filtered[j].Name {
				return filtered[i].Name < filtered[j].Name
			}
			return filtered[i].Source < filtered[j].Source
		})
	case "source":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Source != filtered[j].Source {
				return filtered[i].Source < filtered[j].Source
			}
			return filtered[i].Name < filtered[j].Name
		})
	}
	return filtered
}

func normalizeRemoteSkillsView(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "", "all-time", "alltime", "top":
		if value == "" {
			return ""
		}
		return "all-time"
	case "trending":
		return "trending"
	case "hot":
		return "hot"
	default:
		return value
	}
}

func normalizeRemoteSkillsSort(raw string, hasQuery bool) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		if hasQuery {
			return "installs"
		}
		return "rank"
	}
	switch value {
	case "rank", "relevance", "installs", "change", "name", "source":
		return value
	default:
		return value
	}
}

// BrowseRemoteSkills returns search results for the given query (or the
// default leaderboard when query is empty).
func BrowseRemoteSkills(ctx context.Context, query string, limit int) (BrowseRemoteSkillsResult, error) {
	return BrowseRemoteSkillsAdvanced(ctx, BrowseRemoteSkillsRequest{
		Query: query,
		Limit: limit,
	})
}

// BrowseRemoteSkillsAdvanced supports view/sort/source/min_installs filters.
func BrowseRemoteSkillsAdvanced(ctx context.Context, req BrowseRemoteSkillsRequest) (BrowseRemoteSkillsResult, error) {
	query := strings.TrimSpace(req.Query)
	view := normalizeRemoteSkillsView(req.View)
	sortBy := normalizeRemoteSkillsSort(req.Sort, query != "")
	sourceFilter := strings.TrimSpace(strings.ToLower(req.Source))
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	if query == "" {
		if view == "" {
			view = "all-time"
		}
		return browseRemoteSkillsLeaderboard(ctx, view, sortBy, sourceFilter, req.MinInstalls, limit)
	}

	result, err := browseRemoteSkillsSearch(ctx, query, limit)
	if err != nil {
		return BrowseRemoteSkillsResult{}, err
	}
	result.Sort = sortBy
	result.SourceFilter = strings.TrimSpace(req.Source)
	result.MinInstalls = req.MinInstalls
	result.Skills = filterAndSortRemoteSkills(result.Skills, sortBy, sourceFilter, req.MinInstalls, false)
	if len(result.Skills) > limit {
		result.Skills = result.Skills[:limit]
	}
	result.Count = len(result.Skills)
	return result, nil
}

// InstallManagedSkill installs a skill from a local path or git URL into
// ~/.nalvin/skills.
func InstallManagedSkill(ctx context.Context, req InstallSkillRequest) (InstallSkillResult, error) {
	spec, err := parseInstallSource(req.Source, req.Ref)
	if err != nil {
		return InstallSkillResult{}, err
	}
	localDir, cleanup, err := materializeInstallSource(ctx, spec)
	if err != nil {
		return InstallSkillResult{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	discovered, err := discoverSkillsInSource(localDir)
	if err != nil {
		return InstallSkillResult{}, err
	}
	selected, err := selectDiscoveredSkill(discovered, spec.Subpath, req.SkillName)
	if err != nil {
		return InstallSkillResult{}, err
	}

	rootDir, err := managedSkillsRoot()
	if err != nil {
		return InstallSkillResult{}, err
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return InstallSkillResult{}, err
	}
	installDirName := managedSkillDirNameFor(selected.Name)
	destinationDir := filepath.Join(rootDir, installDirName)
	replaced := false
	if _, err := os.Stat(destinationDir); err == nil {
		if !req.Replace {
			return InstallSkillResult{}, fmt.Errorf("managed skill already exists: %s", installDirName)
		}
		if err := os.RemoveAll(destinationDir); err != nil {
			return InstallSkillResult{}, fmt.Errorf("remove existing managed skill %q: %w", installDirName, err)
		}
		replaced = true
	} else if !os.IsNotExist(err) {
		return InstallSkillResult{}, err
	}

	if err := copySkillPayload(selected.Dir, destinationDir); err != nil {
		return InstallSkillResult{}, err
	}
	manifest := managedSkillManifest{
		Managed:        true,
		ThirdParty:     true,
		InstallSource:  strings.TrimSpace(req.Source),
		InstallRef:     strings.TrimSpace(spec.Ref),
		DiscoveredPath: selected.RelativeDir,
		InstalledAt:    time.Now().UTC(),
	}
	if err := writeManagedSkillManifest(destinationDir, manifest); err != nil {
		return InstallSkillResult{}, err
	}

	skill, err := loadManagedSkillByName(selected.Name)
	if err != nil {
		return InstallSkillResult{}, err
	}
	return InstallSkillResult{
		Name:            skill.Name,
		Path:            skill.Path,
		SkillDir:        skill.SkillDir,
		Managed:         skill.Managed,
		ThirdParty:      skill.ThirdParty,
		InstallSource:   skill.InstallSource,
		InstallRef:      skill.InstallRef,
		ResourcePaths:   append([]string(nil), skill.ResourcePaths...),
		HasScripts:      skill.HasScripts,
		LocallyModified: skill.LocallyModified,
		Replaced:        replaced,
	}, nil
}

// CreateManagedSkill scaffolds a brand-new managed skill under ~/.nalvin/skills.
func CreateManagedSkill(_ context.Context, req CreateSkillRequest) (CreateSkillResult, error) {
	name := strings.TrimSpace(req.Name)
	if !validManagedSkillNamePattern.MatchString(name) {
		return CreateSkillResult{}, fmt.Errorf("name must be lowercase hyphen-case")
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return CreateSkillResult{}, fmt.Errorf("description is required")
	}

	rootDir, err := managedSkillsRoot()
	if err != nil {
		return CreateSkillResult{}, err
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return CreateSkillResult{}, err
	}
	skillDir := filepath.Join(rootDir, name)
	if _, err := os.Stat(skillDir); err == nil {
		if !req.Replace {
			return CreateSkillResult{}, fmt.Errorf("managed skill already exists: %s", name)
		}
		if err := os.RemoveAll(skillDir); err != nil {
			return CreateSkillResult{}, fmt.Errorf("remove existing managed skill %q: %w", name, err)
		}
	} else if !os.IsNotExist(err) {
		return CreateSkillResult{}, err
	}

	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return CreateSkillResult{}, err
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(renderManagedSkillMarkdown(name, description)), 0o644); err != nil {
		return CreateSkillResult{}, err
	}

	resourceSet := normalizeManagedSkillResources(req.Resources)
	for _, resource := range resourceSet {
		if err := os.MkdirAll(filepath.Join(skillDir, resource), 0o755); err != nil {
			return CreateSkillResult{}, err
		}
	}
	if req.IncludeOpenAIYAML {
		openAIPath := filepath.Join(skillDir, "agents", "openai.yaml")
		if err := os.MkdirAll(filepath.Dir(openAIPath), 0o755); err != nil {
			return CreateSkillResult{}, err
		}
		if err := os.WriteFile(openAIPath, []byte(renderManagedOpenAIYAML(name, description)), 0o644); err != nil {
			return CreateSkillResult{}, err
		}
	}

	manifest := managedSkillManifest{
		Managed:     true,
		InstalledAt: time.Now().UTC(),
	}
	if err := writeManagedSkillManifest(skillDir, manifest); err != nil {
		return CreateSkillResult{}, err
	}

	skill, err := loadManagedSkillByName(name)
	if err != nil {
		return CreateSkillResult{}, err
	}
	return CreateSkillResult{
		Name:          skill.Name,
		Path:          skill.Path,
		SkillDir:      skill.SkillDir,
		ResourcePaths: append([]string(nil), skill.ResourcePaths...),
	}, nil
}

// ModifyManagedSkill applies an exact-match or full-content edit to a managed
// skill file. SKILL.md edits are validated before being written.
func ModifyManagedSkill(_ context.Context, req ModifySkillRequest) (ModifySkillResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return ModifySkillResult{}, fmt.Errorf("name is required")
	}
	skill, err := loadManagedSkillByName(name)
	if err != nil {
		return ModifySkillResult{}, err
	}
	if !skill.Managed {
		return ModifySkillResult{}, fmt.Errorf("skill %q is not a managed skill", name)
	}

	relativePath, err := normalizeManagedSkillEditablePath(req.Path)
	if err != nil {
		return ModifySkillResult{}, err
	}
	absolutePath := filepath.Join(skill.SkillDir, filepath.FromSlash(relativePath))

	var (
		action         string
		replacements   int
		content        string
		validatedSkill *skillMetadata
	)
	switch {
	case req.Content != "":
		content = req.Content
		if _, statErr := os.Stat(absolutePath); statErr == nil {
			action = "updated"
		} else if os.IsNotExist(statErr) {
			action = "created"
		} else if statErr != nil {
			return ModifySkillResult{}, statErr
		}
	case len(req.Edits) > 0:
		current, currentAction, err := readEditableFile(absolutePath)
		if err != nil {
			return ModifySkillResult{}, err
		}
		action = currentAction
		content = current
		for index, edit := range req.Edits {
			next, count, err := applyExactEditString(content, edit.OldString, edit.NewString, edit.ReplaceAll)
			if err != nil {
				return ModifySkillResult{}, fmt.Errorf("edit %d failed: %w", index+1, err)
			}
			content = next
			replacements += count
		}
	default:
		current, currentAction, err := readEditableFile(absolutePath)
		if err != nil {
			return ModifySkillResult{}, err
		}
		action = currentAction
		content, replacements, err = applyExactEditString(current, req.OldString, req.NewString, req.ReplaceAll)
		if err != nil {
			return ModifySkillResult{}, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return ModifySkillResult{}, err
	}
	if relativePath == "SKILL.md" {
		validated, err := validateManagedSkillMarkdown(content, absolutePath, skill.Source)
		if err != nil {
			return ModifySkillResult{}, err
		}
		validatedSkill = &validated
	}
	if err := writeManagedSkillFile(absolutePath, content, relativePath == "SKILL.md"); err != nil {
		return ModifySkillResult{}, err
	}
	if err := markManagedSkillLocallyModified(skill.SkillDir); err != nil {
		return ModifySkillResult{}, err
	}

	result := ModifySkillResult{
		Name:            skill.Name,
		Path:            relativePath,
		AbsolutePath:    absolutePath,
		Action:          action,
		Replacements:    replacements,
		Bytes:           len(content),
		LocallyModified: true,
	}
	if validatedSkill != nil {
		result.Validated = true
		result.SkillName = validatedSkill.Name
		result.SkillDescription = validatedSkill.Description
		result.Activation = validatedSkill.Activation
		result.RunScopes = append([]string(nil), validatedSkill.RunScopes...)
		result.AutorevealTools = append([]string(nil), validatedSkill.AutorevealTools...)
	}
	return result, nil
}

// RemoveManagedSkill removes a managed skill directory under ~/.nalvin/skills.
// (OpenNalvin extension; not present upstream.)
func RemoveManagedSkill(_ context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	skill, err := loadManagedSkillByName(name)
	if err != nil {
		return err
	}
	if !skill.Managed {
		return fmt.Errorf("skill %q is not a managed skill", name)
	}
	rootDir, err := managedSkillsRoot()
	if err != nil {
		return err
	}
	if !pathWithinRoot(rootDir, skill.SkillDir) {
		return fmt.Errorf("refusing to remove %q: not under managed skills root", skill.SkillDir)
	}
	return os.RemoveAll(skill.SkillDir)
}

func enrichManagedSkillMetadata(skill *skillMetadata, skillDir, source string) []string {
	if skill == nil {
		return nil
	}
	skill.SkillDir = strings.TrimSpace(skillDir)
	if skill.SkillDir == "" {
		skill.SkillDir = filepath.Dir(skill.Path)
	}

	resources := detectManagedSkillResources(skill.SkillDir)
	skill.ResourcePaths = resources
	skill.HasScripts = containsManagedSkillResource(resources, "scripts")

	managedRoot, err := managedSkillsRoot()
	if err != nil {
		return nil
	}
	if !pathWithinRoot(managedRoot, skill.SkillDir) {
		if skill.HasScripts {
			skill.ExecutionPolicy = managedSkillExecutionPolicyHost
		}
		return nil
	}

	skill.Managed = true
	skill.ContainerSkillDir = filepath.ToSlash(filepath.Join(skillContainerMountRoot, filepath.Base(skill.SkillDir)))
	skill.ReadTools = managedSkillReadTools()
	skill.SkillPathExamples = managedSkillPathExamples(skill.Name)
	if skill.HasScripts {
		skill.ExecutionPolicy = managedSkillExecutionPolicyDocker
	}

	manifest, err := readManagedSkillManifest(skill.SkillDir)
	if err != nil {
		return []string{fmt.Sprintf("managed skill %s: %v", skill.SkillDir, err)}
	}
	if manifest.Managed {
		skill.Managed = true
	}
	skill.ThirdParty = manifest.ThirdParty
	skill.LocallyModified = manifest.LocallyModified
	skill.InstallSource = strings.TrimSpace(manifest.InstallSource)
	skill.InstallRef = strings.TrimSpace(manifest.InstallRef)
	if !skill.ThirdParty && source == "user" && skill.InstallSource != "" {
		skill.ThirdParty = true
	}
	return nil
}

func managedSkillsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nalvin", "skills"), nil
}

func readManagedSkillManifest(skillDir string) (managedSkillManifest, error) {
	path := filepath.Join(skillDir, managedSkillManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return managedSkillManifest{Managed: true}, nil
		}
		return managedSkillManifest{}, err
	}
	var manifest managedSkillManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return managedSkillManifest{}, fmt.Errorf("parse %s: %w", managedSkillManifestName, err)
	}
	if !manifest.Managed {
		manifest.Managed = true
	}
	return manifest, nil
}

func writeManagedSkillManifest(skillDir string, manifest managedSkillManifest) error {
	manifest.Managed = true
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(skillDir, managedSkillManifestName), data, 0o644)
}

func markManagedSkillLocallyModified(skillDir string) error {
	manifest, err := readManagedSkillManifest(skillDir)
	if err != nil {
		return err
	}
	manifest.Managed = true
	manifest.LocallyModified = true
	return writeManagedSkillManifest(skillDir, manifest)
}

func detectManagedSkillResources(skillDir string) []string {
	candidates := []string{"scripts", "references", "assets"}
	out := make([]string, 0, len(candidates)+1)
	for _, candidate := range candidates {
		info, err := os.Stat(filepath.Join(skillDir, candidate))
		if err == nil && info.IsDir() {
			out = append(out, candidate)
		}
	}
	openAIPath := filepath.Join(skillDir, "agents", "openai.yaml")
	if info, err := os.Stat(openAIPath); err == nil && !info.IsDir() {
		out = append(out, "agents/openai.yaml")
	}
	return out
}

func parseInstallSource(rawSource, requestedRef string) (installSourceSpec, error) {
	rawSource = strings.TrimSpace(rawSource)
	if rawSource == "" {
		return installSourceSpec{}, fmt.Errorf("source is required")
	}
	if local, ok, err := resolveLocalInstallSource(rawSource); err != nil {
		return installSourceSpec{}, err
	} else if ok {
		return installSourceSpec{Raw: rawSource, LocalDir: local, Ref: strings.TrimSpace(requestedRef)}, nil
	}

	if parsedURL, err := url.Parse(rawSource); err == nil && parsedURL.Scheme != "" && parsedURL.Host != "" {
		spec, err := parseGitURLInstallSource(parsedURL)
		if err != nil {
			return installSourceSpec{}, err
		}
		if strings.TrimSpace(requestedRef) != "" {
			spec.Ref = strings.TrimSpace(requestedRef)
		}
		spec.Raw = rawSource
		return spec, nil
	}

	if strings.Count(rawSource, "/") == 1 && !strings.Contains(rawSource, " ") {
		return installSourceSpec{
			Raw:     rawSource,
			RepoURL: "https://github.com/" + rawSource + ".git",
			Ref:     strings.TrimSpace(requestedRef),
		}, nil
	}

	if strings.HasSuffix(rawSource, ".git") {
		return installSourceSpec{Raw: rawSource, RepoURL: rawSource, Ref: strings.TrimSpace(requestedRef)}, nil
	}

	return installSourceSpec{}, fmt.Errorf("unsupported skill source %q", rawSource)
}

func resolveLocalInstallSource(raw string) (string, bool, error) {
	source := raw
	if strings.HasPrefix(source, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		source = filepath.Join(home, strings.TrimPrefix(source, "~"))
	}
	if !filepath.IsAbs(source) && !strings.HasPrefix(source, ".") {
		if _, err := os.Stat(source); os.IsNotExist(err) {
			return "", false, nil
		}
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(absolute)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("skill source path must be a directory")
	}
	return absolute, true, nil
}

func parseGitURLInstallSource(parsed *url.URL) (installSourceSpec, error) {
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	switch host {
	case "github.com":
		parts := splitURLPathParts(parsed.Path)
		if len(parts) < 2 {
			return installSourceSpec{}, fmt.Errorf("github source must include owner and repo")
		}
		spec := installSourceSpec{
			RepoURL: "https://github.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git") + ".git",
		}
		if len(parts) >= 4 && parts[2] == "tree" {
			spec.Ref = parts[3]
			if len(parts) > 4 {
				spec.Subpath = filepath.ToSlash(filepath.Join(parts[4:]...))
			}
		}
		return spec, nil
	case "gitlab.com":
		parts := splitURLPathParts(parsed.Path)
		if len(parts) < 2 {
			return installSourceSpec{}, fmt.Errorf("gitlab source must include owner and repo")
		}
		spec := installSourceSpec{
			RepoURL: "https://gitlab.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git") + ".git",
		}
		if len(parts) >= 5 && parts[2] == "-" && parts[3] == "tree" {
			spec.Ref = parts[4]
			if len(parts) > 5 {
				spec.Subpath = filepath.ToSlash(filepath.Join(parts[5:]...))
			}
		}
		return spec, nil
	default:
		return installSourceSpec{RepoURL: parsed.String()}, nil
	}
}

func splitURLPathParts(raw string) []string {
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "/")
}

func materializeInstallSource(ctx context.Context, spec installSourceSpec) (string, func(), error) {
	if spec.LocalDir != "" {
		return spec.LocalDir, nil, nil
	}
	tmpDir, err := os.MkdirTemp("", "nalvin-skill-source-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	cloneOpts := &gogit.CloneOptions{
		URL: spec.RepoURL,
	}
	if strings.TrimSpace(spec.Ref) == "" {
		cloneOpts.Depth = 1
	}
	repo, err := gogit.PlainCloneContext(ctx, tmpDir, false, cloneOpts)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if strings.TrimSpace(spec.Ref) != "" {
		rev, err := repo.ResolveRevision(plumbing.Revision(spec.Ref))
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("resolve git ref %q: %w", spec.Ref, err)
		}
		worktree, err := repo.Worktree()
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if err := worktree.Checkout(&gogit.CheckoutOptions{Hash: *rev}); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("checkout git ref %q: %w", spec.Ref, err)
		}
	}
	return tmpDir, cleanup, nil
}

func discoverSkillsInSource(root string) ([]discoveredSkill, error) {
	roots := []string{
		root,
		filepath.Join(root, "skills"),
		filepath.Join(root, ".agents", "skills"),
		filepath.Join(root, ".nalvin", "skills"),
		filepath.Join(root, ".codex", "skills"),
		filepath.Join(root, ".claude", "skills"),
	}
	seenDirs := map[string]struct{}{}
	out := make([]discoveredSkill, 0)

	for _, candidateRoot := range roots {
		if _, ok := seenDirs[filepath.Clean(candidateRoot)]; ok {
			continue
		}
		seenDirs[filepath.Clean(candidateRoot)] = struct{}{}

		skillPath := filepath.Join(candidateRoot, "SKILL.md")
		if _, err := os.Stat(skillPath); err == nil {
			data, err := os.ReadFile(skillPath)
			if err != nil {
				return nil, err
			}
			meta, err := parseSkillMarkdown(string(data), skillPath, "remote")
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, candidateRoot)
			if err != nil {
				rel = "."
			}
			out = append(out, discoveredSkill{
				Name:        meta.Name,
				Description: meta.Description,
				Dir:         candidateRoot,
				RelativeDir: filepath.ToSlash(rel),
			})
		}

		entries, err := os.ReadDir(candidateRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(candidateRoot, entry.Name())
			skillPath := filepath.Join(dir, "SKILL.md")
			if _, err := os.Stat(skillPath); err != nil {
				continue
			}
			data, err := os.ReadFile(skillPath)
			if err != nil {
				return nil, err
			}
			meta, err := parseSkillMarkdown(string(data), skillPath, "remote")
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, dir)
			if err != nil {
				rel = entry.Name()
			}
			out = append(out, discoveredSkill{
				Name:        meta.Name,
				Description: meta.Description,
				Dir:         dir,
				RelativeDir: filepath.ToSlash(rel),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].RelativeDir < out[j].RelativeDir
	})
	return out, nil
}

func selectDiscoveredSkill(candidates []discoveredSkill, subpath, requestedName string) (discoveredSkill, error) {
	if len(candidates) == 0 {
		return discoveredSkill{}, fmt.Errorf("no installable skills found in source")
	}
	filtered := candidates
	if strings.TrimSpace(subpath) != "" {
		subpath = filepath.ToSlash(strings.Trim(strings.TrimSpace(subpath), "/"))
		next := make([]discoveredSkill, 0, len(candidates))
		for _, candidate := range candidates {
			rel := strings.Trim(filepath.ToSlash(candidate.RelativeDir), "/")
			if rel == subpath || strings.HasPrefix(rel, subpath+"/") {
				next = append(next, candidate)
			}
		}
		filtered = next
	}
	if requestedName != "" {
		requestedName = strings.TrimSpace(strings.ToLower(requestedName))
		for _, candidate := range filtered {
			if strings.ToLower(candidate.Name) == requestedName || strings.ToLower(filepath.Base(candidate.RelativeDir)) == requestedName {
				return candidate, nil
			}
		}
		return discoveredSkill{}, fmt.Errorf("skill %q not found in source", requestedName)
	}
	if len(filtered) == 1 {
		return filtered[0], nil
	}
	names := make([]string, 0, len(filtered))
	for _, candidate := range filtered {
		names = append(names, fmt.Sprintf("%s (%s)", candidate.Name, candidate.RelativeDir))
	}
	sort.Strings(names)
	return discoveredSkill{}, fmt.Errorf("source contains multiple skills; choose one with skill_name: %s", strings.Join(names, ", "))
}

func copySkillPayload(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	items := []string{"SKILL.md", "scripts", "references", "assets"}
	for _, item := range items {
		src := filepath.Join(srcDir, item)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if _, err := workspacepkg.CopyTree(src, filepath.Join(dstDir, item), true); err != nil {
			return fmt.Errorf("copy %s: %w", item, err)
		}
	}
	openAIPath := filepath.Join(srcDir, "agents", "openai.yaml")
	if _, err := os.Stat(openAIPath); err == nil {
		if _, err := workspacepkg.CopyTree(openAIPath, filepath.Join(dstDir, "agents", "openai.yaml"), true); err != nil {
			return fmt.Errorf("copy agents/openai.yaml: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func loadManagedSkillByName(name string) (skillMetadata, error) {
	rootDir, err := managedSkillsRoot()
	if err != nil {
		return skillMetadata{}, err
	}
	skills, warnings := loadSkillDirectory(rootDir, "user")
	if len(warnings) > 0 {
		return skillMetadata{}, fmt.Errorf("%s", strings.Join(warnings, "; "))
	}
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, name) && skill.Managed {
			return skill, nil
		}
	}
	return skillMetadata{}, fmt.Errorf("managed skill %q not found", name)
}

func normalizeManagedSkillEditablePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "SKILL.md", nil
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(filepath.ToSlash(raw), "/") {
		return "", fmt.Errorf("path must be skill-relative")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path must stay within the managed skill root")
	}
	if cleaned == "SKILL.md" || cleaned == "agents/openai.yaml" {
		return cleaned, nil
	}
	for _, prefix := range []string{"scripts/", "references/", "assets/"} {
		if strings.HasPrefix(cleaned, prefix) {
			return cleaned, nil
		}
	}
	return "", fmt.Errorf("path %q is not editable through modify_skill", raw)
}

func normalizeManagedSkillResources(values []string) []string {
	allowed := map[string]struct{}{
		"scripts":    {},
		"references": {},
		"assets":     {},
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func renderManagedSkillMarkdown(name, description string) string {
	return strings.TrimSpace(fmt.Sprintf(`---
name: %s
description: %s
---

## When To Use

- Use this skill when %s

## Workflow

- Inspect the task and confirm the triggering conditions match this skill.
- Follow the skill's reusable steps before falling back to generic tooling.
`, name, description, description)) + "\n"
}

func renderManagedOpenAIYAML(name, description string) string {
	return strings.TrimSpace(fmt.Sprintf(`
name: %s
description: %s
`, name, description)) + "\n"
}

func validateManagedSkillMarkdown(content, resolvedPath, source string) (skillMetadata, error) {
	skill, err := parseSkillMarkdown(content, resolvedPath, source)
	if err != nil {
		return skillMetadata{}, fmt.Errorf("invalid SKILL.md: %w", err)
	}
	return skill, nil
}

func writeManagedSkillFile(path, content string, atomic bool) error {
	if !atomic {
		return os.WriteFile(path, []byte(content), 0o644)
	}
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".nalvin-skill-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(0o644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func managedSkillDirNameFor(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = nonSkillSlugPattern.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "skill"
	}
	return name
}

func managedSkillReadTools() []string {
	return append([]string(nil), managedSkillReadToolIDs...)
}

func managedSkillPathExamples(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return []string{
		activeSkillPathScheme + name + "/SKILL.md",
		activeSkillPathScheme + name + "/references",
		activeSkillPathScheme + name + "/scripts",
	}
}

func remoteSkillSourceURL(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return source
	}
	return "https://github.com/" + source
}

func remoteSkillDirectoryURL(source, slug string) string {
	source = strings.TrimSpace(source)
	slug = strings.TrimSpace(slug)
	if source == "" && slug == "" {
		return ""
	}
	baseURL := strings.TrimSpace(os.Getenv(envRemoteSkillsDirectoryURL))
	if baseURL == "" {
		baseURL = defaultRemoteSkillsDirectoryURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/")
	}
	segments := make([]string, 0, 4)
	for _, part := range strings.Split(source, "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments = append(segments, url.PathEscape(part))
	}
	if slug != "" {
		segments = append(segments, url.PathEscape(slug))
	}
	parsed.Path = path.Join("/", path.Join(segments...))
	return parsed.String()
}

func pathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func containsManagedSkillResource(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
