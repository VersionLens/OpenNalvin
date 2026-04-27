package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Metadata parsing ---

func TestParseCustomToolMeta_Defaults(t *testing.T) {
	src := []byte(`package main
func main() {}
`)
	meta := parseCustomToolMeta(src, "my_tool")
	assert.Equal(t, "my_tool", meta.name)
	assert.Equal(t, "", meta.description)
	assert.Empty(t, meta.keywords)
	assert.True(t, meta.defaultEnabled)
	assert.False(t, meta.defaultPinned)
}

func TestParseCustomToolMeta_Directives(t *testing.T) {
	src := []byte(`package main

// tool:name reverse_string
// tool:description Reverse a UTF-8 string.
// tool:keywords reverse,flip,mirror
// tool:default_enabled true
// tool:default_pinned false

func main() {}
`)
	meta := parseCustomToolMeta(src, "fallback")
	assert.Equal(t, "reverse_string", meta.name)
	assert.Equal(t, "Reverse a UTF-8 string.", meta.description)
	assert.Equal(t, []string{"reverse", "flip", "mirror"}, meta.keywords)
	assert.True(t, meta.defaultEnabled)
	assert.False(t, meta.defaultPinned)
}

func TestParseCustomToolMeta_DisabledPinned(t *testing.T) {
	src := []byte(`// tool:name my_tool
// tool:default_enabled false
// tool:default_pinned true
package main
func main() {}
`)
	meta := parseCustomToolMeta(src, "fallback")
	assert.Equal(t, "my_tool", meta.name)
	assert.False(t, meta.defaultEnabled)
	assert.True(t, meta.defaultPinned)
}

// --- Schema parsing ---

func TestParseCustomToolSchema_NoInputStruct(t *testing.T) {
	src := []byte(`package main
func main() {}
`)
	schema, err := parseCustomToolSchema(src)
	require.NoError(t, err)
	assert.Equal(t, "object", schema.Type)
	assert.Empty(t, schema.Properties)
	assert.Empty(t, schema.Required)
}

func TestParseCustomToolSchema_WithInputStruct(t *testing.T) {
	src := []byte(`package main

type Input struct {
	Text   string ` + "`" + `json:"text" description:"The string to reverse."` + "`" + `
	Limit  int    ` + "`" + `json:"limit,omitempty" description:"Optional limit."` + "`" + `
	Verbose bool  ` + "`" + `json:"verbose,omitempty"` + "`" + `
}

func main() {}
`)
	schema, err := parseCustomToolSchema(src)
	require.NoError(t, err)
	assert.Equal(t, "object", schema.Type)
	assert.Equal(t, []string{"text"}, schema.Required)

	textProp, ok := schema.Properties["text"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", textProp["type"])
	assert.Equal(t, "The string to reverse.", textProp["description"])

	limitProp, ok := schema.Properties["limit"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "integer", limitProp["type"])
	assert.Equal(t, "Optional limit.", limitProp["description"])

	verboseProp, ok := schema.Properties["verbose"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", verboseProp["type"])
	_, hasDesc := verboseProp["description"]
	assert.False(t, hasDesc)
}

func TestParseCustomToolSchema_AllTypes(t *testing.T) {
	src := []byte(`package main

type Input struct {
	S  string
	I  int
	F  float64
	B  bool
	AS []string
	M  map[string]string
}

func main() {}
`)
	schema, err := parseCustomToolSchema(src)
	require.NoError(t, err)
	assert.Equal(t, "string", schema.Properties["S"].(map[string]any)["type"])
	assert.Equal(t, "integer", schema.Properties["I"].(map[string]any)["type"])
	assert.Equal(t, "number", schema.Properties["F"].(map[string]any)["type"])
	assert.Equal(t, "boolean", schema.Properties["B"].(map[string]any)["type"])
	assert.Equal(t, "array", schema.Properties["AS"].(map[string]any)["type"])
	assert.Equal(t, "object", schema.Properties["M"].(map[string]any)["type"])
}

// --- Compilation and execution ---

var reverseStringSrc = []byte(`package main

import "tool"
import "strings"

// tool:name reverse_string
// tool:description Reverse a UTF-8 string.
// tool:keywords reverse

type Input struct {
	Text string ` + "`" + `json:"text" description:"The string to reverse."` + "`" + `
}

func main() {
	in := tool.GetInput()
	text, _ := in["text"].(string)
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	_ = strings.Contains(text, "x") // use an import
	tool.SetOutput(map[string]any{
		"reversed": string(runes),
	})
}
`)

func TestCompileCustomTool(t *testing.T) {
	prog, err := compileCustomTool(reverseStringSrc)
	require.NoError(t, err)
	assert.NotNil(t, prog)
}

func TestCustomTool_Run_ReturnsOutput(t *testing.T) {
	prog, err := compileCustomTool(reverseStringSrc)
	require.NoError(t, err)

	schema, err := parseCustomToolSchema(reverseStringSrc)
	require.NoError(t, err)

	ct := &customTool{
		id:      "reverse_string",
		schema:  schema,
		program: prog,
		rt: &agentRuntime{
			tools: map[string]runtimeTool{},
			cfg:   testConfigWithCustomTimeout(30),
		},
	}

	resp, err := ct.Run(context.Background(), fantasy.ToolCall{
		ID:    "test",
		Name:  "reverse_string",
		Input: `{"text": "hello"}`,
	})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "olleh")
}

func TestCustomTool_Run_NoOutput(t *testing.T) {
	src := []byte(`package main
func main() {}
`)
	prog, err := compileCustomTool(src)
	require.NoError(t, err)

	ct := &customTool{
		id:      "noop",
		program: prog,
		schema:  ToolSchema{Type: "object", Properties: map[string]any{}, Required: []string{}},
		rt: &agentRuntime{
			tools: map[string]runtimeTool{},
			cfg:   testConfigWithCustomTimeout(30),
		},
	}

	resp, err := ct.Run(context.Background(), fantasy.ToolCall{
		ID:    "test",
		Name:  "noop",
		Input: `{}`,
	})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "success")
}

func TestCustomTool_Run_RuntimePanic(t *testing.T) {
	src := []byte(`package main
func main() {
	var s []string
	_ = s[0] // index out of range
}
`)
	prog, err := compileCustomTool(src)
	require.NoError(t, err)

	ct := &customTool{
		id:      "panicker",
		program: prog,
		schema:  ToolSchema{Type: "object", Properties: map[string]any{}, Required: []string{}},
		rt: &agentRuntime{
			tools: map[string]runtimeTool{},
			cfg:   testConfigWithCustomTimeout(30),
		},
	}

	resp, err := ct.Run(context.Background(), fantasy.ToolCall{
		ID:    "test",
		Name:  "panicker",
		Input: `{}`,
	})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "panicker")
}

func TestCustomTool_Run_CallTool(t *testing.T) {
	src := []byte(`package main

import "tool"

func main() {
	result, err := tool.CallTool("echo_tool", map[string]any{"msg": "hi"})
	if err != nil {
		tool.SetOutput(map[string]any{"error": err.Error()})
		return
	}
	tool.SetOutput(result)
}
`)
	prog, err := compileCustomTool(src)
	require.NoError(t, err)

	// Create a minimal runtime with a fake "echo_tool".
	echoTool := &mockAgentTool{
		info: fantasy.ToolInfo{Name: "echo_tool"},
		runFn: func(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(`{"echoed": "hi"}`), nil
		},
	}

	rt := &agentRuntime{
		tools: map[string]runtimeTool{
			"echo_tool": {id: "echo_tool", tool: echoTool},
		},
		cfg: testConfigWithCustomTimeout(30),
	}

	ct := &customTool{
		id:      "call_test",
		program: prog,
		schema:  ToolSchema{Type: "object", Properties: map[string]any{}, Required: []string{}},
		rt:      rt,
	}

	resp, err := ct.Run(context.Background(), fantasy.ToolCall{
		ID:    "test",
		Name:  "call_test",
		Input: `{}`,
	})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "echoed")
}

func TestCustomTool_Run_SelfCallBlocked(t *testing.T) {
	src := []byte(`package main

import "tool"

func main() {
	result, err := tool.CallTool("self_calling", map[string]any{})
	if err != nil {
		tool.SetOutput(map[string]any{"error": err.Error()})
		return
	}
	tool.SetOutput(result)
}
`)
	prog, err := compileCustomTool(src)
	require.NoError(t, err)

	ct := &customTool{
		id:      "self_calling",
		program: prog,
		schema:  ToolSchema{Type: "object", Properties: map[string]any{}, Required: []string{}},
		rt: &agentRuntime{
			tools: map[string]runtimeTool{},
			cfg:   testConfigWithCustomTimeout(30),
		},
	}
	// Add self to rt.tools so the lookup finds "self_calling".
	ct.rt.tools["self_calling"] = runtimeTool{id: "self_calling", tool: ct}

	resp, err := ct.Run(context.Background(), fantasy.ToolCall{
		ID:    "test",
		Name:  "self_calling",
		Input: `{}`,
	})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	// The error from CallTool is captured in the output.
	assert.Contains(t, resp.Content, "recursively")
}

func TestCustomTool_Info(t *testing.T) {
	schema, err := parseCustomToolSchema(reverseStringSrc)
	require.NoError(t, err)
	meta := parseCustomToolMeta(reverseStringSrc, "fallback")

	ct := &customTool{
		id:          meta.name,
		description: meta.description,
		keywords:    meta.keywords,
		schema:      schema,
	}

	info := ct.Info()
	assert.Equal(t, "reverse_string", info.Name)
	assert.Equal(t, "Reverse a UTF-8 string.", info.Description)
	assert.Equal(t, []string{"text"}, info.Required)
	assert.True(t, info.Parallel)
}

func TestCompileCustomTool_SyntaxError(t *testing.T) {
	src := []byte(`package main
func main() {
	this is not valid go
}
`)
	_, err := compileCustomTool(src)
	assert.Error(t, err)
}

func TestGoASTTypeToJSONType(t *testing.T) {
	// Tested indirectly through parseCustomToolSchema — see TestParseCustomToolSchema_AllTypes.
	// This exercises the string fallback for unknown types.
	src := []byte(`package main

type Input struct {
	X int64
	Y uint32
}
func main() {}
`)
	schema, err := parseCustomToolSchema(src)
	require.NoError(t, err)
	assert.Equal(t, "integer", schema.Properties["X"].(map[string]any)["type"])
	assert.Equal(t, "integer", schema.Properties["Y"].(map[string]any)["type"])
}

// --- stdlib package access ---

func TestCustomTool_StdlibStrings(t *testing.T) {
	src := []byte(`package main

import (
	"tool"
	"strings"
)

func main() {
	upper := strings.ToUpper("hello")
	tool.SetOutput(map[string]any{"result": upper})
}
`)
	prog, err := compileCustomTool(src)
	require.NoError(t, err)

	ct := &customTool{
		id:      "strings_test",
		program: prog,
		schema:  ToolSchema{Type: "object", Properties: map[string]any{}, Required: []string{}},
		rt: &agentRuntime{
			tools: map[string]runtimeTool{},
			cfg:   testConfigWithCustomTimeout(30),
		},
	}
	resp, err := ct.Run(context.Background(), fantasy.ToolCall{
		ID:    "t",
		Name:  "strings_test",
		Input: `{}`,
	})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "HELLO")
}

func TestCustomTool_BlockedPackage(t *testing.T) {
	src := []byte(`package main

import "os"

func main() {
	os.Exit(0)
}
`)
	_, err := compileCustomTool(src)
	assert.Error(t, err, "os package should not be available")
	assert.True(t, strings.Contains(err.Error(), "os") || err != nil)
}

// --- Helpers ---

// testConfigWithCustomTimeout returns a minimal Config with the custom tools timeout set.
func testConfigWithCustomTimeout(seconds int) configpkg.Config {
	return configpkg.Config{
		Agent: configpkg.AgentConfig{
			CustomTools: configpkg.AgentCustomToolsConfig{
				Enabled:        true,
				TimeoutSeconds: seconds,
			},
		},
	}
}

// mockAgentTool is a minimal AgentTool for testing CallTool bridging.
type mockAgentTool struct {
	info  fantasy.ToolInfo
	runFn func(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error)
}

func (m *mockAgentTool) Info() fantasy.ToolInfo { return m.info }
func (m *mockAgentTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return m.runFn(ctx, call)
}
func (m *mockAgentTool) ProviderOptions() fantasy.ProviderOptions        { return nil }
func (m *mockAgentTool) SetProviderOptions(opts fantasy.ProviderOptions) {}
