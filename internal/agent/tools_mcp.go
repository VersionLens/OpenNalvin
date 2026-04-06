package agent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"charm.land/fantasy"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type mcpServerManager struct {
	clients map[string]*mcpclient.Client
}

type mcpFantasyTool struct {
	id          string
	serverName  string
	toolName    string
	description string
	schema      ToolSchema
	client      *mcpclient.Client
	provider    fantasy.ProviderOptions
}

func (rt *agentRuntime) mcpTools(ctx context.Context) ([]runtimeTool, []string, error) {
	if len(rt.cfg.Agent.MCPServers) == 0 {
		return nil, nil, nil
	}

	manager := &mcpServerManager{clients: map[string]*mcpclient.Client{}}
	tools := make([]runtimeTool, 0)
	warnings := make([]string, 0)

	serverNames := make([]string, 0, len(rt.cfg.Agent.MCPServers))
	for name := range rt.cfg.Agent.MCPServers {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	for _, serverName := range serverNames {
		serverCfg := rt.cfg.Agent.MCPServers[serverName]
		client, err := newMCPClient(ctx, serverCfg)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("mcp server %s unavailable: %v", serverName, err))
			continue
		}
		manager.clients[serverName] = client

		toolList, err := client.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("mcp server %s list tools failed: %v", serverName, err))
			_ = client.Close()
			delete(manager.clients, serverName)
			continue
		}

		for _, remoteTool := range toolList.Tools {
			id := mcpToolID(serverName, remoteTool.Name)
			local := &mcpFantasyTool{
				id:          id,
				serverName:  serverName,
				toolName:    remoteTool.Name,
				description: strings.TrimSpace(remoteTool.Description),
				schema:      mcpToolSchema(remoteTool),
				client:      client,
			}
			tools = append(tools, runtimeTool{
				id:             id,
				tool:           local,
				source:         sourceMCP,
				serverName:     serverName,
				keywords:       rt.mergedToolKeywords(id),
				defaultEnabled: serverCfg.EnabledByDefault,
				defaultPinned:  false,
			})
		}
	}

	if len(manager.clients) > 0 {
		rt.mcpManager = manager
	}

	return tools, warnings, nil
}

func newMCPClient(ctx context.Context, cfg configpkg.MCPServerConfig) (*mcpclient.Client, error) {
	transport := strings.TrimSpace(cfg.Transport)
	var (
		client *mcpclient.Client
		err    error
	)

	switch transport {
	case "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio mcp server command is required")
		}
		opts := make([]mcptransport.StdioOption, 0, 1)
		if cfg.CWD != "" {
			cwd := cfg.CWD
			opts = append(opts, mcptransport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Env = env
				cmd.Dir = cwd
				return cmd, nil
			}))
		}
		client, err = mcpclient.NewStdioMCPClientWithOptions(cfg.Command, stringMapEnv(cfg.Env), cfg.Args, opts...)
	case "streamable_http":
		opts := make([]mcptransport.StreamableHTTPCOption, 0, 2)
		if len(cfg.Headers) > 0 {
			opts = append(opts, mcptransport.WithHTTPHeaders(cfg.Headers))
		}
		if cfg.TimeoutMs > 0 {
			opts = append(opts, mcptransport.WithHTTPTimeout(time.Duration(cfg.TimeoutMs)*time.Millisecond))
		}
		client, err = mcpclient.NewStreamableHttpClient(cfg.URL, opts...)
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", transport)
	}
	if err != nil {
		return nil, err
	}

	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := client.Start(startCtx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("start mcp client: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "nalvin",
		Version: "1.0.0",
		Title:   "nalvin MCP client",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err := client.Initialize(startCtx, initReq); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize mcp client: %w", err)
	}

	return client, nil
}

func (m *mcpServerManager) Close() error {
	var errs []string
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close mcp clients: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (t *mcpFantasyTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:        t.id,
		Description: t.description,
		Parameters:  cloneMap(t.schema.Properties),
		Required:    append([]string(nil), t.schema.Required...),
		Parallel:    true,
	}
}

func (t *mcpFantasyTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	var args any
	if strings.TrimSpace(params.Input) == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(params.Input), &args); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid JSON input: %v", err)), nil
	}

	callResult, err := t.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      t.toolName,
			Arguments: args,
		},
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	payload, err := json.MarshalIndent(callResult, "", "  ")
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	resp := fantasy.NewTextResponse(string(payload))
	resp.IsError = callResult.IsError
	resp.Metadata = string(mustJSON(map[string]any{
		"server_name": t.serverName,
		"tool_name":   t.toolName,
	}))
	return resp, nil
}

func (t *mcpFantasyTool) ProviderOptions() fantasy.ProviderOptions {
	return t.provider
}

func (t *mcpFantasyTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.provider = opts
}

func mcpToolID(serverName, toolName string) string {
	return "mcp__" + encodeMCPToolIDPart(serverName) + "__" + encodeMCPToolIDPart(toolName)
}

func encodeMCPToolIDPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "x"
	}
	if isSafeMCPToolIDPart(value) {
		return value
	}
	return "x" + hex.EncodeToString([]byte(value))
}

func isSafeMCPToolIDPart(value string) bool {
	if value == "" || strings.Contains(value, "__") {
		return false
	}
	for _, c := range []byte(value) {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func mcpToolSchema(tool mcp.Tool) ToolSchema {
	if len(tool.RawInputSchema) > 0 {
		var schema struct {
			Defs                 map[string]any `json:"$defs,omitempty"`
			Type                 string         `json:"type"`
			Properties           map[string]any `json:"properties"`
			Required             []string       `json:"required"`
			AdditionalProperties any            `json:"additionalProperties,omitempty"`
		}
		if err := json.Unmarshal(tool.RawInputSchema, &schema); err == nil {
			return ToolSchema{
				Defs:                 schema.Defs,
				Type:                 schema.Type,
				Properties:           schema.Properties,
				Required:             schema.Required,
				AdditionalProperties: schema.AdditionalProperties,
			}
		}
	}

	return ToolSchema{
		Type:                 tool.InputSchema.Type,
		Properties:           tool.InputSchema.Properties,
		Required:             append([]string(nil), tool.InputSchema.Required...),
		AdditionalProperties: tool.InputSchema.AdditionalProperties,
	}
}

func stringMapEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}
