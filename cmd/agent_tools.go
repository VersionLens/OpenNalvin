package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/agent"
	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	agentToolsListRunID string
)

var agentToolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "List and execute agent tools",
}

var agentToolsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available internal and MCP-backed agent tools",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, _, _, err := openWorkspaceDB(cmd.Context())
		if err != nil {
			return err
		}
		defer db.Close()

		result, err := agent.ListTools(cmd.Context(), knowledge.New(db), agentToolsListRunID, agent.ToolSelection{})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(result)
		}

		for _, tool := range result.Tools {
			status := "disabled"
			if tool.Enabled {
				status = "enabled"
			}
			visibility := "hidden"
			if tool.Visible {
				visibility = "visible"
			}
			pin := ""
			if tool.Pinned {
				pin = " pinned"
			}
			source := tool.Source
			if tool.ServerName != "" {
				source += ":" + tool.ServerName
			}
			w.Line("%s [%s %s%s] %s", tool.ID, status, visibility, pin, source)
			if tool.Description != "" {
				w.Line("  %s", tool.Description)
			}
		}
		for _, warning := range result.Warnings {
			w.Line("warning: %s", warning)
		}
		return nil
	},
}

var agentToolsRunCmd = &cobra.Command{
	Use:                "run <tool-id> [tool flags...]",
	Short:              "Execute a tool directly without going through the model",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, _, _, err := openWorkspaceDB(cmd.Context())
		if err != nil {
			return err
		}
		defer db.Close()

		parsed, err := parseToolRunArgs(stripLeadingInheritedFlags(cmd, args))
		if err != nil {
			return err
		}

		result, err := agent.ListTools(cmd.Context(), knowledge.New(db), parsed.runID, agent.ToolSelection{})
		if err != nil {
			return err
		}
		descriptor, ok := findToolDescriptor(result.Tools, parsed.toolID)
		if !ok {
			return fmt.Errorf("tool %q not found", parsed.toolID)
		}

		payload, err := buildToolInput(descriptor.Schema, parsed.jsonArgs, parsed.flagValues)
		if err != nil {
			return err
		}

		runResult, err := agent.InvokeTool(cmd.Context(), knowledge.New(db), parsed.runID, parsed.toolID, payload, agent.ToolSelection{})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(runResult)
		}

		if strings.TrimSpace(runResult.Output) != "" {
			w.Line("%s", runResult.Output)
		}
		if runResult.IsError {
			return fmt.Errorf("tool %s returned an error", parsed.toolID)
		}
		return nil
	},
}

type toolRunArgs struct {
	runID      string
	toolID     string
	jsonArgs   string
	flagValues map[string][]string
}

func init() {
	agentToolsListCmd.Flags().StringVar(&agentToolsListRunID, "run-id", "", "optional run id to resolve tool state from")
	agentToolsCmd.AddCommand(agentToolsListCmd)
	agentToolsCmd.AddCommand(agentToolsRunCmd)
	agentCmd.AddCommand(agentToolsCmd)
}

func parseToolRunArgs(args []string) (toolRunArgs, error) {
	parsed := toolRunArgs{
		flagValues: map[string][]string{},
	}

	for index := 0; index < len(args); index++ {
		token := args[index]
		switch {
		case token == "--run-id":
			index++
			if index >= len(args) {
				return parsed, fmt.Errorf("--run-id requires a value")
			}
			parsed.runID = strings.TrimSpace(args[index])
		case strings.HasPrefix(token, "--run-id="):
			parsed.runID = strings.TrimSpace(strings.TrimPrefix(token, "--run-id="))
		case token == "--json-args":
			index++
			if index >= len(args) {
				return parsed, fmt.Errorf("--json-args requires a value")
			}
			parsed.jsonArgs = args[index]
		case strings.HasPrefix(token, "--json-args="):
			parsed.jsonArgs = strings.TrimPrefix(token, "--json-args=")
		case strings.HasPrefix(token, "--"):
			name, value, consumedNext, err := parseToolFlagToken(token, args, index)
			if err != nil {
				return parsed, err
			}
			parsed.flagValues[name] = append(parsed.flagValues[name], value)
			if consumedNext {
				index++
			}
		default:
			if parsed.toolID == "" {
				parsed.toolID = strings.TrimSpace(token)
				continue
			}
			return parsed, fmt.Errorf("unexpected argument %q", token)
		}
	}

	if parsed.toolID == "" {
		return parsed, fmt.Errorf("tool id is required")
	}
	return parsed, nil
}

func parseToolFlagToken(token string, args []string, index int) (string, string, bool, error) {
	nameValue := strings.TrimPrefix(token, "--")
	if nameValue == "" {
		return "", "", false, fmt.Errorf("invalid flag %q", token)
	}
	if strings.Contains(nameValue, "=") {
		parts := strings.SplitN(nameValue, "=", 2)
		return normalizeFlagName(parts[0]), parts[1], false, nil
	}
	if index+1 < len(args) && !strings.HasPrefix(args[index+1], "--") {
		return normalizeFlagName(nameValue), args[index+1], true, nil
	}
	return normalizeFlagName(nameValue), "true", false, nil
}

func stripLeadingInheritedFlags(cmd *cobra.Command, args []string) []string {
	if cmd == nil || len(args) == 0 {
		return append([]string(nil), args...)
	}

	inherited := cmd.InheritedFlags()
	if inherited == nil || inherited.NFlag()+lenFlagSet(inherited) == 0 {
		return append([]string(nil), args...)
	}

	out := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		token := args[index]
		flag, skipNext, ok := inheritedFlagMatch(inherited, token, args, index)
		if !ok {
			out = append(out, args[index:]...)
			return out
		}
		if flag == nil {
			out = append(out, args[index:]...)
			return out
		}
		if skipNext {
			index++
		}
	}

	return out
}

func inheritedFlagMatch(set *pflag.FlagSet, token string, args []string, index int) (*pflag.Flag, bool, bool) {
	if set == nil || token == "" {
		return nil, false, false
	}
	if token == "--" || token == "-" {
		return nil, false, false
	}

	if strings.HasPrefix(token, "--") {
		nameValue := strings.TrimPrefix(token, "--")
		name := nameValue
		hasExplicitValue := false
		if split := strings.Index(nameValue, "="); split >= 0 {
			name = nameValue[:split]
			hasExplicitValue = true
		}
		flag := set.Lookup(name)
		if flag == nil {
			return nil, false, false
		}
		return flag, shouldSkipFlagValue(flag, hasExplicitValue, args, index), true
	}

	if strings.HasPrefix(token, "-") {
		nameValue := strings.TrimPrefix(token, "-")
		if nameValue == "" {
			return nil, false, false
		}
		hasExplicitValue := false
		if split := strings.Index(nameValue, "="); split >= 0 {
			nameValue = nameValue[:split]
			hasExplicitValue = true
		}
		if len(nameValue) != 1 {
			return nil, false, false
		}
		var matched *pflag.Flag
		set.VisitAll(func(flag *pflag.Flag) {
			if matched == nil && flag.Shorthand == nameValue {
				matched = flag
			}
		})
		if matched == nil {
			return nil, false, false
		}
		return matched, shouldSkipFlagValue(matched, hasExplicitValue, args, index), true
	}

	return nil, false, false
}

func shouldSkipFlagValue(flag *pflag.Flag, hasExplicitValue bool, args []string, index int) bool {
	if flag == nil || hasExplicitValue || flag.NoOptDefVal != "" {
		return false
	}
	if index+1 >= len(args) {
		return false
	}
	next := args[index+1]
	return !strings.HasPrefix(next, "-")
}

func lenFlagSet(set *pflag.FlagSet) int {
	if set == nil {
		return 0
	}
	count := 0
	set.VisitAll(func(*pflag.Flag) {
		count++
	})
	return count
}

func normalizeFlagName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), "-", "_")
}

func findToolDescriptor(tools []agent.ToolDescriptor, id string) (agent.ToolDescriptor, bool) {
	for _, tool := range tools {
		if tool.ID == id {
			return tool, true
		}
	}
	return agent.ToolDescriptor{}, false
}

func buildToolInput(schema agent.ToolSchema, jsonArgs string, flagValues map[string][]string) (map[string]any, error) {
	payload := map[string]any{}
	if strings.TrimSpace(jsonArgs) != "" {
		if err := json.Unmarshal([]byte(jsonArgs), &payload); err != nil {
			return nil, fmt.Errorf("parse --json-args: %w", err)
		}
	}

	for name, values := range flagValues {
		property, _ := schema.Properties[name].(map[string]any)
		value, err := coerceToolFlagValues(property, values)
		if err != nil {
			return nil, fmt.Errorf("parse --%s: %w", strings.ReplaceAll(name, "_", "-"), err)
		}
		payload[name] = value
	}

	return payload, nil
}

func coerceToolFlagValues(property map[string]any, values []string) (any, error) {
	schemaType, _ := property["type"].(string)

	switch schemaType {
	case "array":
		items, _ := property["items"].(map[string]any)
		out := make([]any, 0, len(values))
		for _, value := range values {
			item, err := coerceSingleToolFlagValue(items, value)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case "object":
		if len(values) != 1 {
			return nil, fmt.Errorf("nested object values should be passed with --json-args")
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(values[0]), &parsed); err != nil {
			return nil, fmt.Errorf("expected JSON object")
		}
		return parsed, nil
	default:
		if len(values) == 0 {
			return "", nil
		}
		return coerceSingleToolFlagValue(property, values[len(values)-1])
	}
}

func coerceSingleToolFlagValue(property map[string]any, value string) (any, error) {
	schemaType, _ := property["type"].(string)
	switch schemaType {
	case "boolean":
		return strconv.ParseBool(value)
	case "integer":
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		return number, nil
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, err
		}
		return number, nil
	case "object":
		var parsed map[string]any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, fmt.Errorf("expected JSON object")
		}
		return parsed, nil
	case "array":
		var parsed []any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, fmt.Errorf("expected JSON array")
		}
		return parsed, nil
	default:
		return value, nil
	}
}
