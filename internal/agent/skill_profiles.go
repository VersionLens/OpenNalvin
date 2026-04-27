package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

// ResolveSkillProfileRunConfig combines the run's existing provider/tool
// selection with any skill-profile metadata declared by the requested skills.
// Returns the resolved (providerName, fallbackProviders, tools, requestedSkillNames, err).
func ResolveSkillProfileRunConfig(
	ctx context.Context,
	cfg configpkg.Config,
	runKind string,
	requestedSkillNames []string,
	providerName string,
	tools ToolSelection,
) (string, []string, ToolSelection, []string, error) {
	requestedSkillNames = trimStringValues(requestedSkillNames)
	catalog, warnings := loadSkillCatalog(ctx, cfg)
	if len(warnings) > 0 {
		// Keep behavior strict for explicit skill attachment; malformed catalog
		// entries should not be silently ignored during profile resolution.
		return "", nil, ToolSelection{}, nil, fmt.Errorf("load skill catalog: %s", warnings[0])
	}

	var (
		resolvedProvider      string
		resolvedProviderSkill string
		resolvedFallbacks     []string
		resolvedFallbackSkill string
		enabledFromSkills     []string
		pinnedFromSkills      []string
	)

	for _, name := range requestedSkillNames {
		skill, ok := catalog[name]
		if !ok {
			return "", nil, ToolSelection{}, nil, fmt.Errorf("skill %q not found", name)
		}
		if !skillMatchesRunScope(skill, runKind) {
			return "", nil, ToolSelection{}, nil, fmt.Errorf("skill %q is not available for this run kind", name)
		}
		if skill.Profile == nil {
			continue
		}
		profile := applySkillProfileConfigOverrides(cfg, name, *skill.Profile)
		if profile.Provider != "" {
			if resolvedProvider == "" {
				resolvedProvider = profile.Provider
				resolvedProviderSkill = name
			} else if resolvedProvider != profile.Provider {
				return "", nil, ToolSelection{}, nil, fmt.Errorf("conflicting skill profile providers for skills %q and %q", resolvedProviderSkill, name)
			}
		}
		if len(profile.FallbackProviders) > 0 {
			if len(resolvedFallbacks) == 0 {
				resolvedFallbacks = append([]string(nil), profile.FallbackProviders...)
				resolvedFallbackSkill = name
			} else if !reflect.DeepEqual(resolvedFallbacks, profile.FallbackProviders) {
				return "", nil, ToolSelection{}, nil, fmt.Errorf("conflicting skill profile fallback providers for skills %q and %q", resolvedFallbackSkill, name)
			}
		}
		enabledFromSkills = orderedUniqueStrings(enabledFromSkills, profile.PinnedTools, profile.EnabledTools, profile.ExclusiveTools)
		pinnedFromSkills = orderedUniqueStrings(pinnedFromSkills, profile.PinnedTools)
	}

	providerName = strings.TrimSpace(providerName)
	if tools.EnabledToolIDs == nil && len(enabledFromSkills) > 0 {
		tools.EnabledToolIDs = append([]string(nil), enabledFromSkills...)
	}
	if tools.PinnedToolIDs == nil && len(pinnedFromSkills) > 0 {
		tools.PinnedToolIDs = append([]string(nil), pinnedFromSkills...)
	}

	if providerName == "" {
		providerName = resolvedProvider
	}
	if providerName == "" {
		return "", nil, tools, requestedSkillNames, nil
	}
	providerName = normalizeProviderName(providerName)

	var fallbackProviders []string
	if providerName != "" {
		if providerName == resolvedProvider && len(resolvedFallbacks) > 0 {
			next, err := providerFallbackChain(providerName, resolvedFallbacks)
			if err != nil {
				return "", nil, ToolSelection{}, nil, err
			}
			fallbackProviders = next
		} else {
			next, err := providerFallbackChain(providerName, nil)
			if err != nil {
				return "", nil, ToolSelection{}, nil, err
			}
			fallbackProviders = next
		}
	}

	return providerName, fallbackProviders, tools, requestedSkillNames, nil
}

func applySkillProfileConfigOverrides(cfg configpkg.Config, skillName string, profile skillProfile) skillProfile {
	if provider := strings.TrimSpace(cfg.Agent.Subagents.SkillProfileProviders[skillName]); provider != "" {
		profile.Provider = normalizeProviderName(provider)
	}
	if fallbacks, ok := cfg.Agent.Subagents.SkillProfileFallbackProviders[skillName]; ok {
		profile.FallbackProviders = normalizeProviderNames(fallbacks)
	}
	return profile
}

func allExclusiveSkillProfileToolIDs(ctx context.Context, cfg configpkg.Config) []string {
	catalog, _ := loadSkillCatalog(ctx, cfg)
	seen := map[string]struct{}{}
	var ids []string
	for _, skill := range catalog {
		if skill.Profile == nil {
			continue
		}
		for _, id := range skill.Profile.ExclusiveTools {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return trimStringValues(ids)
}

func requestedExclusiveSkillProfileToolIDs(ctx context.Context, cfg configpkg.Config, runKind string, requestedSkillNames []string) []string {
	requestedSkillNames = trimStringValues(requestedSkillNames)
	if len(requestedSkillNames) == 0 {
		return nil
	}
	catalog, _ := loadSkillCatalog(ctx, cfg)
	seen := map[string]struct{}{}
	var ids []string
	for _, name := range requestedSkillNames {
		skill, ok := catalog[name]
		if !ok || skill.Profile == nil || !skillMatchesRunScope(skill, runKind) {
			continue
		}
		for _, id := range skill.Profile.ExclusiveTools {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return trimStringValues(ids)
}
