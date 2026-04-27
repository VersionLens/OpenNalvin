package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/spf13/cobra"
)

var (
	agentSkillsListJSON   bool
	agentSkillsSearchLimit int
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

var agentSkillsInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a skill into the user skills directory by copying a local source directory",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := filepath.Abs(strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("source must be a directory containing SKILL.md")
		}
		if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
			return fmt.Errorf("source must contain SKILL.md: %w", err)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dest := filepath.Join(home, ".nalvin", "skills", filepath.Base(source))
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("destination already exists: %s", dest)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := copySkillDir(source, dest); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed %s -> %s\n", source, dest)
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
}

func copySkillDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func init() {
	agentSkillsListCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsSearchCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")
	agentSkillsSearchCmd.Flags().IntVar(&agentSkillsSearchLimit, "limit", 25, "Maximum results to return")
	agentSkillsShowCmd.Flags().BoolVar(&agentSkillsListJSON, "json", false, "Emit JSON output")

	agentSkillsCmd.AddCommand(agentSkillsListCmd)
	agentSkillsCmd.AddCommand(agentSkillsSearchCmd)
	agentSkillsCmd.AddCommand(agentSkillsShowCmd)
	agentSkillsCmd.AddCommand(agentSkillsInstallCmd)
	agentCmd.AddCommand(agentSkillsCmd)
}
