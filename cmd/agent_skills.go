package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/versionlens/OpenNalvin/internal/agent"
)

var (
	agentSkillsListJSON    bool
	agentSkillsSearchLimit int

	agentSkillsBrowseLimit       int
	agentSkillsBrowseQuery       string
	agentSkillsBrowseView        string
	agentSkillsBrowseSort        string
	agentSkillsBrowseSource      string
	agentSkillsBrowseMinInstalls int

	agentSkillsInstallName    string
	agentSkillsInstallRef     string
	agentSkillsInstallReplace bool

	agentSkillsCreateDescription       string
	agentSkillsCreateResources         []string
	agentSkillsCreateIncludeOpenAIYAML bool
	agentSkillsCreateReplace           bool

	agentSkillsModifyPath       string
	agentSkillsModifyContent    string
	agentSkillsModifyOldString  string
	agentSkillsModifyNewString  string
	agentSkillsModifyReplaceAll bool

	agentSkillsCleanupAll bool
)

var agentSkillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage agent skills",
}

var agentSkillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List embedded, workspace, and user skills",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := agent.StaticListSkills(cmd.Context())
		if err != nil {
			return err
		}
		return renderAgentSkillCatalog(cmd, result, agentSkillsListJSON)
	},
}

var agentSkillsSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search installed skills",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := agent.StaticSearchSkills(cmd.Context(), args[0], agentSkillsSearchLimit)
		if err != nil {
			return err
		}
		return renderAgentSkillCatalog(cmd, result, agentSkillsListJSON)
	},
}

var agentSkillsShowCmd = &cobra.Command{
	Use:   "show <skill>",
	Short: "Show a skill's metadata and SKILL.md body",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := agent.StaticListSkills(cmd.Context())
		if err != nil {
			return err
		}
		name := strings.TrimSpace(args[0])
		for _, skill := range result.Skills {
			if skill.Name == name {
				if agentSkillsListJSON {
					payload, err := json.MarshalIndent(skill, "", "  ")
					if err != nil {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), string(payload))
					return nil
				}
				renderAgentSkillSummary(cmd, skill)
				if path := strings.TrimSpace(skill.Path); path != "" && !strings.HasPrefix(path, "embedded:") {
					if data, err := os.ReadFile(path); err == nil {
						fmt.Fprintln(cmd.OutOrStdout())
						fmt.Fprintln(cmd.OutOrStdout(), string(data))
					}
				}
				return nil
			}
		}
		return fmt.Errorf("skill %q not found", name)
	},
}

var agentSkillsBrowseCmd = &cobra.Command{
	Use:   "browse [query...]",
	Short: "Browse remote installable skills",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		argQuery := strings.TrimSpace(strings.Join(args, " "))
		flagQuery := strings.TrimSpace(agentSkillsBrowseQuery)
		if argQuery != "" && flagQuery != "" {
			return fmt.Errorf("provide either a positional query or --query, not both")
		}
		query := flagQuery
		if query == "" {
			query = argQuery
		}
		result, err := agent.BrowseRemoteSkillsAdvanced(cmd.Context(), agent.BrowseRemoteSkillsRequest{
			Query:       query,
			View:        agentSkillsBrowseView,
			Sort:        agentSkillsBrowseSort,
			Source:      agentSkillsBrowseSource,
			MinInstalls: agentSkillsBrowseMinInstalls,
			Limit:       agentSkillsBrowseLimit,
		})
		if err != nil {
			return err
		}
		return renderRemoteSkillBrowse(cmd, result, agentSkillsListJSON)
	},
}

var agentSkillsInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a managed skill into ~/.nalvin/skills",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := agent.InstallManagedSkill(cmd.Context(), agent.InstallSkillRequest{
			Source:    args[0],
			SkillName: agentSkillsInstallName,
			Ref:       agentSkillsInstallRef,
			Replace:   agentSkillsInstallReplace,
		})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if agentSkillsListJSON {
			payload, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(payload))
			return nil
		}
		status := "installed"
		if result.Replaced {
			status = "replaced"
		}
		fmt.Fprintf(out, "%s %s\n  %s\n", status, result.Name, result.SkillDir)
		return nil
	},
}

var agentSkillsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a managed skill under ~/.nalvin/skills",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := agent.CreateManagedSkill(cmd.Context(), agent.CreateSkillRequest{
			Name:              args[0],
			Description:       agentSkillsCreateDescription,
			Resources:         agentSkillsCreateResources,
			IncludeOpenAIYAML: agentSkillsCreateIncludeOpenAIYAML,
			Replace:           agentSkillsCreateReplace,
		})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if agentSkillsListJSON {
			payload, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(payload))
			return nil
		}
		fmt.Fprintf(out, "created %s\n  %s\n", result.Name, result.SkillDir)
		return nil
	},
}

var agentSkillsModifyCmd = &cobra.Command{
	Use:   "modify <name>",
	Short: "Modify files inside a managed skill",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := agent.ModifyManagedSkill(cmd.Context(), agent.ModifySkillRequest{
			Name:       args[0],
			Path:       agentSkillsModifyPath,
			Content:    agentSkillsModifyContent,
			OldString:  agentSkillsModifyOldString,
			NewString:  agentSkillsModifyNewString,
			ReplaceAll: agentSkillsModifyReplaceAll,
		})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if agentSkillsListJSON {
			payload, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(payload))
			return nil
		}
		fmt.Fprintf(out, "%s %s\n  %s\n", result.Action, result.Path, result.AbsolutePath)
		return nil
	},
}

var agentSkillsCleanupCmd = &cobra.Command{
	Use:   "cleanup-containers",
	Short: "Remove skill-runner containers for the current workspace or all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, paths, err := activeWorkspaceConfig(cmd.Context())
		if err != nil {
			return err
		}
		result, err := agent.CleanupSkillContainers(cmd.Context(), cfg, paths.Name, paths.FilesPath, agentSkillsCleanupAll)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if agentSkillsListJSON {
			payload, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(payload))
			return nil
		}
		if len(result.Removed) == 0 {
			if result.All {
				fmt.Fprintln(out, "no skill-runner containers found")
			} else {
				fmt.Fprintf(out, "no skill-runner containers found for workspace %s\n", result.Workspace)
			}
			return nil
		}
		for _, name := range result.Removed {
			fmt.Fprintf(out, "removed %s\n", name)
		}
		return nil
	},
}

func renderAgentSkillCatalog(cmd *cobra.Command, result agent.SkillCatalogResult, asJSON bool) error {
	if asJSON {
		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(payload))
		return nil
	}
	for _, skill := range result.Skills {
		renderAgentSkillSummary(cmd, skill)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", w)
	}
	return nil
}

func renderAgentSkillSummary(cmd *cobra.Command, skill agent.SkillDescriptor) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s [%s]", skill.Name, skill.Activation)
	if skill.Active {
		fmt.Fprint(out, " active")
	}
	if skill.Managed {
		fmt.Fprint(out, " managed")
	}
	if skill.ThirdParty {
		fmt.Fprint(out, " third-party")
	}
	fmt.Fprintln(out)
	if skill.Description != "" {
		fmt.Fprintf(out, "  %s\n", skill.Description)
	}
	if skill.SkillDir != "" {
		fmt.Fprintf(out, "  dir: %s\n", skill.SkillDir)
	}
	if len(skill.AutorevealTools) > 0 {
		fmt.Fprintf(out, "  autoreveal: %s\n", strings.Join(skill.AutorevealTools, ", "))
	}
	if len(skill.ToolHints) > 0 {
		fmt.Fprintf(out, "  hints: %s\n", strings.Join(skill.ToolHints, ", "))
	}
	if skill.HasScripts {
		fmt.Fprintln(out, "  has_scripts: true")
	}
	if len(skill.ResourcePaths) > 0 {
		fmt.Fprintf(out, "  resources: %s\n", strings.Join(skill.ResourcePaths, ", "))
	}
	if skill.InstallSource != "" {
		ref := skill.InstallRef
		if ref == "" {
			fmt.Fprintf(out, "  installed from: %s\n", skill.InstallSource)
		} else {
			fmt.Fprintf(out, "  installed from: %s @ %s\n", skill.InstallSource, ref)
		}
	}
}

func renderRemoteSkillBrowse(cmd *cobra.Command, result agent.BrowseRemoteSkillsResult, asJSON bool) error {
	out := cmd.OutOrStdout()
	if asJSON {
		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(payload))
		return nil
	}
	header := "Remote Skills"
	if result.View != "" {
		header += " • " + result.View
	} else if result.Query != "" {
		header += fmt.Sprintf(" • search %q", result.Query)
	}
	fmt.Fprintln(out, header)
	if len(result.Skills) == 0 {
		fmt.Fprintln(out, "No remote skills matched.")
		return nil
	}
	for index, skill := range result.Skills {
		prefix := fmt.Sprintf("%d.", index+1)
		if skill.Rank > 0 {
			prefix = fmt.Sprintf("#%d", skill.Rank)
		}
		fmt.Fprintf(out, "%s %s", prefix, skill.Name)
		if skill.Source != "" {
			fmt.Fprintf(out, " [%s]", skill.Source)
		}
		if skill.Installs > 0 {
			fmt.Fprintf(out, " [%d installs]", skill.Installs)
		}
		fmt.Fprintln(out)
		if skill.DirectoryURL != "" {
			fmt.Fprintf(out, "  page: %s\n", skill.DirectoryURL)
		}
		if skill.SourceURL != "" {
			fmt.Fprintf(out, "  repo: %s\n", skill.SourceURL)
		}
	}
	return nil
}

func init() {
	agentSkillsListCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsSearchCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsSearchCmd.Flags().IntVar(&agentSkillsSearchLimit, "limit", 25, "Maximum results to return")
	agentSkillsShowCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")

	agentSkillsBrowseCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsBrowseCmd.Flags().IntVar(&agentSkillsBrowseLimit, "limit", 10, "max remote skills to return")
	agentSkillsBrowseCmd.Flags().StringVarP(&agentSkillsBrowseQuery, "query", "q", "", "remote skill search query")
	agentSkillsBrowseCmd.Flags().StringVar(&agentSkillsBrowseView, "view", "", "leaderboard view: all-time, trending, or hot")
	agentSkillsBrowseCmd.Flags().StringVar(&agentSkillsBrowseSort, "sort", "", "sort by rank, installs, change, name, source, or relevance")
	agentSkillsBrowseCmd.Flags().StringVar(&agentSkillsBrowseSource, "source", "", "filter remote results by source substring")
	agentSkillsBrowseCmd.Flags().IntVar(&agentSkillsBrowseMinInstalls, "min-installs", 0, "filter out remote skills below this install count")

	agentSkillsInstallCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsInstallCmd.Flags().StringVar(&agentSkillsInstallName, "skill", "", "exact skill name when source contains multiple skills")
	agentSkillsInstallCmd.Flags().StringVar(&agentSkillsInstallRef, "ref", "", "branch, tag, or revision to install from")
	agentSkillsInstallCmd.Flags().BoolVar(&agentSkillsInstallReplace, "replace", false, "replace an existing managed skill directory")

	agentSkillsCreateCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsCreateCmd.Flags().StringVar(&agentSkillsCreateDescription, "description", "", "skill description and trigger guidance")
	agentSkillsCreateCmd.Flags().StringArrayVar(&agentSkillsCreateResources, "resource", nil, "optional resource to scaffold (scripts, references, assets)")
	agentSkillsCreateCmd.Flags().BoolVar(&agentSkillsCreateIncludeOpenAIYAML, "include-openai-yaml", false, "create agents/openai.yaml")
	agentSkillsCreateCmd.Flags().BoolVar(&agentSkillsCreateReplace, "replace", false, "replace an existing managed skill directory")
	_ = agentSkillsCreateCmd.MarkFlagRequired("description")

	agentSkillsModifyCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsModifyCmd.Flags().StringVar(&agentSkillsModifyPath, "path", "SKILL.md", "managed-skill-relative path to modify")
	agentSkillsModifyCmd.Flags().StringVar(&agentSkillsModifyContent, "content", "", "full replacement file contents")
	agentSkillsModifyCmd.Flags().StringVar(&agentSkillsModifyOldString, "old-string", "", "exact text to replace")
	agentSkillsModifyCmd.Flags().StringVar(&agentSkillsModifyNewString, "new-string", "", "replacement text")
	agentSkillsModifyCmd.Flags().BoolVar(&agentSkillsModifyReplaceAll, "replace-all", false, "replace every exact match")

	agentSkillsCleanupCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsCleanupCmd.Flags().BoolVar(&agentSkillsCleanupAll, "all", false, "remove skill-runner containers for all workspaces")

	agentSkillsCmd.AddCommand(agentSkillsListCmd)
	agentSkillsCmd.AddCommand(agentSkillsSearchCmd)
	agentSkillsCmd.AddCommand(agentSkillsShowCmd)
	agentSkillsCmd.AddCommand(agentSkillsBrowseCmd)
	agentSkillsCmd.AddCommand(agentSkillsInstallCmd)
	agentSkillsCmd.AddCommand(agentSkillsCreateCmd)
	agentSkillsCmd.AddCommand(agentSkillsModifyCmd)
	agentSkillsCmd.AddCommand(agentSkillsCleanupCmd)
	agentCmd.AddCommand(agentSkillsCmd)
}
