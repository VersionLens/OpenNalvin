package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
)

// Tools auto-registered from skills' manifest.yaml `commands:` declarations.
//
// When a skill with declared commands is loaded at agent-runtime
// initialisation, each command becomes an ordinary agent tool whose
// dispatcher invokes skill_exec under the hood.

type skillCommandToolInput struct {
	Args    []string `json:"args,omitempty" jsonschema_description:"Argv passed to the command entry inside the skill-runner container."`
	Timeout int      `json:"timeout,omitempty" jsonschema_description:"Optional timeout in seconds; command-manifest default is used when omitted."`
}

func skillCommandToolDescription(skillName string, cmd skillCommandManifest) string {
	base := strings.TrimSpace(cmd.Description)
	if base == "" {
		base = fmt.Sprintf("Run the %s skill's %s command.", skillName, cmd.Name)
	}
	return base + fmt.Sprintf(" (Runs inside the skill-runner container; CWD=/skills/%s; provided by skill %q.)", skillName, skillName)
}

func (rt *agentRuntime) makeSkillCommandTool(skillName string, cmd skillCommandManifest) runtimeTool {
	desc := skillCommandToolDescription(skillName, cmd)
	dispatcher := func(ctx context.Context, input skillCommandToolInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		timeout := input.Timeout
		if timeout <= 0 {
			timeout = cmd.Timeout
		}
		argv := append([]string{cmd.Entry}, input.Args...)
		res, err := rt.runSkillCommand(ctx, skillName, argv, timeout)
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		payload, merr := json.Marshal(res)
		if merr != nil {
			return fantasy.NewTextErrorResponse(merr.Error()), nil
		}
		return fantasy.NewTextResponse(string(payload)), nil
	}
	tool := newParallelAgentTool(cmd.Name, desc, dispatcher)
	return rt.makeTool(cmd.Name, "skill", true, false, tool)
}

// registerSkillCommandTools scans the catalog for every skill with
// manifest-declared commands and registers each as a runtimeTool.
// Collisions are recorded in rt.warnings.
func (rt *agentRuntime) registerSkillCommandTools() {
	if rt.skills == nil {
		return
	}
	for _, skill := range rt.skills.allCatalog() {
		for _, cmd := range skill.Commands {
			tool := rt.makeSkillCommandTool(skill.Name, cmd)
			if _, exists := rt.tools[tool.id]; exists {
				rt.warnings = append(rt.warnings,
					fmt.Sprintf("skill %q command %q: tool id collides with existing tool; skipped", skill.Name, cmd.Name))
				continue
			}
			rt.tools[tool.id] = tool
		}
	}
}

// skillCommandToolIDs returns the tool ids for a skill's declared commands.
func skillCommandToolIDs(skill skillMetadata) []string {
	if len(skill.Commands) == 0 {
		return nil
	}
	out := make([]string, 0, len(skill.Commands))
	for _, c := range skill.Commands {
		out = append(out, c.Name)
	}
	return out
}

// skill_exec — generic fallback tool for running an ad-hoc argv inside a
// skill's container.

type skillExecInput struct {
	Skill   string   `json:"skill" jsonschema_description:"Name of an active skill whose container to exec into."`
	Argv    []string `json:"argv" jsonschema_description:"Command and arguments. CWD is /skills/<skill>."`
	Timeout int      `json:"timeout,omitempty" jsonschema_description:"Optional timeout in seconds."`
}

func (rt *agentRuntime) skillExecTool(ctx context.Context, input skillExecInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	skill := strings.TrimSpace(input.Skill)
	if skill == "" {
		return fantasy.NewTextErrorResponse("skill is required"), nil
	}
	if len(input.Argv) == 0 {
		return fantasy.NewTextErrorResponse("argv is required"), nil
	}
	meta, ok := rt.lookupSkillMetadata(skill)
	if !ok {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("skill %q not found in catalog", skill)), nil
	}
	if len(meta.Commands) == 0 {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("skill %q does not declare any commands; add a manifest.yaml with commands: to enable skill_exec", skill)), nil
	}
	res, err := rt.runSkillCommand(ctx, skill, input.Argv, input.Timeout)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	payload, merr := json.Marshal(res)
	if merr != nil {
		return fantasy.NewTextErrorResponse(merr.Error()), nil
	}
	return fantasy.NewTextResponse(string(payload)), nil
}
