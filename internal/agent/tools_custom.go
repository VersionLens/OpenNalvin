package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/open2b/scriggo"
	"github.com/open2b/scriggo/native"
)

// customToolContextKey is the context key used to pass per-invocation state
// into a Scriggo program via RunOptions.Context.
type customToolContextKey struct{}

// customToolState holds per-invocation state accessible to native tool functions.
type customToolState struct {
	input     map[string]any
	output    any
	hasOutput bool
	rt        *agentRuntime
	toolID    string
}

// callTool invokes another registered tool from within a custom tool.
func (s *customToolState) callTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if name == s.toolID {
		return nil, fmt.Errorf("custom tool cannot call itself recursively")
	}
	rt, ok := s.rt.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	inputJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal args: %w", err)
	}
	resp, err := rt.tool.Run(ctx, fantasy.ToolCall{
		ID:    "custom_calltool_" + name,
		Name:  name,
		Input: string(inputJSON),
	})
	if err != nil {
		return nil, err
	}
	if resp.IsError {
		return nil, fmt.Errorf("tool %s error: %s", name, resp.Content)
	}
	var result map[string]any
	if jsonErr := json.Unmarshal([]byte(resp.Content), &result); jsonErr != nil {
		return map[string]any{"output": resp.Content}, nil
	}
	return result, nil
}

// customToolNativePackage returns the "tool" package exposed to all custom tool scripts.
// Functions use native.Env to retrieve per-invocation state from the run context.
func customToolNativePackage() native.Package {
	return native.Package{
		Name: "tool",
		Declarations: native.Declarations{
			// GetInput returns the tool's JSON input as a map. Use type assertions to
			// access individual fields: text := tool.GetInput()["text"].(string)
			"GetInput": func(env native.Env) map[string]any {
				state, ok := env.Context().Value(customToolContextKey{}).(*customToolState)
				if !ok || state == nil {
					return map[string]any{}
				}
				return state.input
			},
			// SetOutput sets the tool's return value. The value must be
			// JSON-serializable. If called multiple times, the last value wins.
			"SetOutput": func(env native.Env, v any) {
				state, ok := env.Context().Value(customToolContextKey{}).(*customToolState)
				if !ok || state == nil {
					return
				}
				state.output = v
				state.hasOutput = true
			},
			// CallTool invokes another registered tool by ID. Returns the parsed JSON
			// response as a map, or an error if the tool fails.
			"CallTool": func(env native.Env, name string, args map[string]any) (map[string]any, error) {
				state, ok := env.Context().Value(customToolContextKey{}).(*customToolState)
				if !ok || state == nil {
					return nil, fmt.Errorf("no tool state in context")
				}
				return state.callTool(env.Context(), name, args)
			},
			// Logf writes a debug log line. Useful for tracing tool execution.
			"Logf": func(format string, args ...any) {
				fmt.Fprintf(os.Stderr, "[custom tool] "+format+"\n", args...)
			},
		},
	}
}

// customTool implements fantasy.AgentTool for a Scriggo-compiled custom tool.
type customTool struct {
	id          string
	description string
	keywords    []string
	schema      ToolSchema
	program     *scriggo.Program
	rt          *agentRuntime
	provider    fantasy.ProviderOptions
}

func (ct *customTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:        ct.id,
		Description: ct.description,
		Parameters:  schemaToParameters(ct.schema),
		Required:    ct.schema.Required,
		Parallel:    true,
	}
}

func (ct *customTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var input map[string]any
	if call.Input != "" {
		_ = json.Unmarshal([]byte(call.Input), &input)
	}
	if input == nil {
		input = map[string]any{}
	}

	state := &customToolState{
		input:  input,
		rt:     ct.rt,
		toolID: ct.id,
	}

	timeout := time.Duration(ct.rt.cfg.Agent.CustomTools.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(
		context.WithValue(ctx, customToolContextKey{}, state),
		timeout,
	)
	defer cancel()

	runErr := ct.program.Run(&scriggo.RunOptions{Context: ctx})
	if runErr != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("custom tool %q error: %v", ct.id, runErr)), nil
	}

	if state.hasOutput {
		resp, err := jsonToolResponse(state.output)
		if err != nil {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("custom tool %q: serialize output: %v", ct.id, err)), nil
		}
		return resp, nil
	}
	resp, _ := jsonToolResponse(map[string]any{"success": true})
	return resp, nil
}

func (ct *customTool) ProviderOptions() fantasy.ProviderOptions {
	return ct.provider
}

func (ct *customTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	ct.provider = opts
}

// schemaToParameters converts a ToolSchema into the flat property map expected
// by fantasy.ToolInfo.Parameters: {"fieldName": {"type": "string", ...}, ...}.
// runtime.go wraps this in the outer ToolSchema{Type:"object", Properties:...}
// envelope, so we must not double-wrap here.
func schemaToParameters(schema ToolSchema) map[string]any {
	if schema.Properties == nil {
		return map[string]any{}
	}
	return schema.Properties
}

// customToolMeta holds parsed metadata from // tool: comment directives.
type customToolMeta struct {
	name           string
	description    string
	keywords       []string
	defaultEnabled bool
	defaultPinned  bool
}

// parseCustomToolMeta scans source lines for // tool:key value directives.
// defaultName is used when no // tool:name directive is present.
func parseCustomToolMeta(src []byte, defaultName string) customToolMeta {
	meta := customToolMeta{
		name:           defaultName,
		defaultEnabled: true,
	}
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "// tool:") {
			continue
		}
		rest := strings.TrimPrefix(line, "// tool:")
		key, val, _ := strings.Cut(rest, " ")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			if val != "" {
				meta.name = val
			}
		case "description":
			meta.description = val
		case "keywords":
			for _, kw := range strings.Split(val, ",") {
				if kw = strings.TrimSpace(kw); kw != "" {
					meta.keywords = append(meta.keywords, kw)
				}
			}
		case "default_enabled":
			meta.defaultEnabled = val != "false"
		case "default_pinned":
			meta.defaultPinned = val == "true"
		}
	}
	return meta
}

// parseCustomToolSchema parses the top-level `type Input struct` from the
// source to build a JSON schema for the tool's parameters. Returns an empty
// schema (no parameters) if no Input struct is found.
func parseCustomToolSchema(src []byte) (ToolSchema, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		return ToolSchema{}, fmt.Errorf("parse: %w", err)
	}
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "Input" {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return buildSchemaFromStruct(structType), nil
		}
	}
	return ToolSchema{Type: "object", Properties: map[string]any{}, Required: []string{}}, nil
}

func buildSchemaFromStruct(s *ast.StructType) ToolSchema {
	props := map[string]any{}
	var required []string
	for _, field := range s.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		fieldName := field.Names[0].Name
		jsonName := fieldName
		optional := false
		description := ""
		if field.Tag != nil {
			tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
			if j := tag.Get("json"); j != "" {
				parts := strings.SplitN(j, ",", 2)
				if parts[0] != "" && parts[0] != "-" {
					jsonName = parts[0]
				}
				if len(parts) > 1 && strings.Contains(parts[1], "omitempty") {
					optional = true
				}
			}
			if d := tag.Get("description"); d != "" {
				description = d
			}
		}
		prop := map[string]any{"type": goASTTypeToJSONType(field.Type)}
		if description != "" {
			prop["description"] = description
		}
		props[jsonName] = prop
		if !optional {
			required = append(required, jsonName)
		}
	}
	if required == nil {
		required = []string{}
	}
	return ToolSchema{Type: "object", Properties: props, Required: required}
}

func goASTTypeToJSONType(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		switch e.Name {
		case "string":
			return "string"
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64":
			return "integer"
		case "float32", "float64":
			return "number"
		case "bool":
			return "boolean"
		default:
			return "string"
		}
	case *ast.ArrayType:
		return "array"
	case *ast.MapType:
		return "object"
	case *ast.StarExpr:
		return goASTTypeToJSONType(e.X)
	default:
		return "string"
	}
}

// customTools scans the custom tools directory and returns compiled runtimeTools.
// It returns a (possibly empty) list of tools and any non-fatal warnings.
func (rt *agentRuntime) customTools() ([]runtimeTool, []string) {
	cfg := rt.cfg.Agent.CustomTools
	if !cfg.Enabled {
		return nil, nil
	}

	dir := cfg.Dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, []string{fmt.Sprintf("custom tools: get home dir: %v", err)}
		}
		dir = filepath.Join(home, ".nalvin", "custom-tools")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("custom tools: read dir %s: %v", dir, err)}
	}

	var tools []runtimeTool
	var warnings []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}

		filePath := filepath.Join(dir, name)
		src, err := os.ReadFile(filePath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("custom tool %s: read file: %v", name, err))
			continue
		}

		defaultName := strings.TrimSuffix(name, ".go")
		meta := parseCustomToolMeta(src, defaultName)

		schema, err := parseCustomToolSchema(src)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("custom tool %s: parse: %v", name, err))
			continue
		}

		program, err := compileCustomTool(src)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("custom tool %s: compile: %v", name, err))
			continue
		}

		ct := &customTool{
			id:          meta.name,
			description: meta.description,
			keywords:    meta.keywords,
			schema:      schema,
			program:     program,
			rt:          rt,
		}
		tools = append(tools, runtimeTool{
			id:             ct.id,
			tool:           ct,
			source:         sourceCustom,
			keywords:       ct.keywords,
			defaultEnabled: meta.defaultEnabled,
			defaultPinned:  meta.defaultPinned,
		})
	}

	return tools, warnings
}

// compileCustomTool compiles a custom tool's Go source with the tool API and
// safe standard library packages available for import.
func compileCustomTool(src []byte) (*scriggo.Program, error) {
	opts := &scriggo.BuildOptions{
		Packages: native.CombinedImporter{
			native.Packages{"tool": customToolNativePackage()},
			customToolPackages(),
		},
	}
	return scriggo.Build(scriggo.Files{"main.go": src}, opts)
}
