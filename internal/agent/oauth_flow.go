package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	configpkg "github.com/versionlens/OpenNalvin/internal/config"
)

type OAuthFlowResult struct {
	ServerName string   `json:"server_name"`
	ToolNames  []string `json:"tool_names"`
}

func RunOAuthFlow(ctx context.Context, w io.Writer, serverName string, cfg configpkg.MCPServerConfig) (*OAuthFlowResult, error) {
	if cfg.OAuth == nil {
		return nil, fmt.Errorf("mcp server %q has no OAuth configuration", serverName)
	}
	if cfg.Transport != "streamable_http" {
		return nil, fmt.Errorf("OAuth is only supported for streamable_http transport, got %q", cfg.Transport)
	}

	callbackPort := cfg.OAuth.CallbackPort
	redirectURI := fmt.Sprintf("http://localhost:%d/oauth/callback", callbackPort)
	tokenStore := newFileTokenStore(oauthTokenDir(), serverName)

	oauthCfg := mcpclient.OAuthConfig{
		ClientID:     cfg.OAuth.ClientID,
		ClientSecret: cfg.OAuth.ClientSecret,
		RedirectURI:  redirectURI,
		TokenStore:   tokenStore,
		PKCEEnabled:  true,
	}

	opts := make([]mcptransport.StreamableHTTPCOption, 0, 2)
	if len(cfg.Headers) > 0 {
		opts = append(opts, mcptransport.WithHTTPHeaders(cfg.Headers))
	}
	if cfg.TimeoutMs > 0 {
		opts = append(opts, mcptransport.WithHTTPTimeout(time.Duration(cfg.TimeoutMs)*time.Millisecond))
	}

	// Try connecting with any cached token first.
	needsAuth, handler, tryErr := tryExistingToken(ctx, cfg.URL, oauthCfg, opts)
	if tryErr != nil && !needsAuth {
		return nil, tryErr
	}
	if !needsAuth {
		fmt.Fprintf(w, "Already authenticated with %s (cached token valid).\n", serverName)
		// Create a fresh client for tool listing since the probe client is closed.
		return verifyAndListTools(ctx, w, cfg.URL, oauthCfg, opts, serverName)
	}

	if handler == nil {
		// No typed OAuth error available (e.g. stale token detected by string match).
		// Create a fresh handler so the auth flow can proceed.
		handler = mcptransport.NewOAuthHandler(oauthCfg)
		handler.SetBaseURL(cfg.URL)
	}

	// Dynamic client registration if no client ID is configured.
	if handler.GetClientID() == "" {
		fmt.Fprintf(w, "Registering dynamic OAuth client with %s...\n", serverName)
		if regErr := handler.RegisterClient(ctx, "nalvin"); regErr != nil {
			return nil, fmt.Errorf(
				"dynamic client registration failed for %s: %w\n\n"+
					"This server does not support automatic registration.\n"+
					"Options:\n"+
					"  1. Add an oauth.client_id in config for this server\n"+
					"  2. Use API key/PAT via headers instead of OAuth:\n"+
					"     nalvin mcp add %s --url %s --header \"Authorization=Bearer <token>\" --overwrite",
				serverName, regErr, serverName, cfg.URL)
		}
		fmt.Fprintf(w, "Registered as client %s\n", handler.GetClientID())
	}

	// Generate PKCE verifier + challenge.
	codeVerifier, err := mcpclient.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate code verifier: %w", err)
	}
	codeChallenge := mcpclient.GenerateCodeChallenge(codeVerifier)

	state, err := mcpclient.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	authURL, err := handler.GetAuthorizationURL(ctx, state, codeChallenge)
	if err != nil {
		return nil, fmt.Errorf("get authorization URL: %w", err)
	}

	// Start local callback server.
	codeCh := make(chan callbackResult, 1)
	srv, err := startCallbackServer(callbackPort, codeCh)
	if err != nil {
		return nil, fmt.Errorf("start callback server on port %d: %w", callbackPort, err)
	}
	defer srv.Close()

	fmt.Fprintf(w, "Opening browser for %s authorization...\n", serverName)
	fmt.Fprintf(w, "If the browser does not open, visit:\n  %s\n\n", authURL)
	_ = openBrowser(authURL)

	// Wait for the OAuth callback.
	fmt.Fprintf(w, "Waiting for authorization callback on http://localhost:%d/oauth/callback ...\n", callbackPort)
	var result callbackResult
	select {
	case result = <-codeCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if result.err != "" {
		return nil, fmt.Errorf("OAuth callback error: %s — %s", result.err, result.errDescription)
	}
	if result.code == "" {
		return nil, fmt.Errorf("OAuth callback returned empty authorization code")
	}

	// Exchange code for token.
	fmt.Fprintf(w, "Exchanging authorization code for token...\n")
	if err := handler.ProcessAuthorizationResponse(ctx, result.code, result.state, codeVerifier); err != nil {
		return nil, fmt.Errorf("process authorization response: %w", err)
	}
	fmt.Fprintf(w, "Token saved successfully.\n\n")

	// Verify by reconnecting with the new token.
	fmt.Fprintf(w, "Verifying connection...\n")
	return verifyAndListTools(ctx, w, cfg.URL, oauthCfg, opts, serverName)
}

// tryExistingToken probes whether a cached token works. Returns (needsAuth, handler, err).
// If needsAuth is true, handler may be non-nil for use in the auth flow.
func tryExistingToken(ctx context.Context, url string, oauthCfg mcpclient.OAuthConfig, opts []mcptransport.StreamableHTTPCOption) (bool, *mcptransport.OAuthHandler, error) {
	client, err := mcpclient.NewOAuthStreamableHttpClient(url, oauthCfg, opts...)
	if err != nil {
		return false, nil, fmt.Errorf("create OAuth MCP client: %w", err)
	}
	defer client.Close()

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := client.Start(probeCtx); err != nil {
		if mcpclient.IsOAuthAuthorizationRequiredError(err) {
			return true, mcpclient.GetOAuthHandler(err), nil
		}
		return false, nil, fmt.Errorf("start mcp client: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "nalvin", Version: "1.0.0", Title: "nalvin MCP client"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err := client.Initialize(probeCtx, initReq); err != nil {
		if mcpclient.IsOAuthAuthorizationRequiredError(err) {
			return true, mcpclient.GetOAuthHandler(err), nil
		}
		// Token exists but server rejects it for a non-OAuth reason — treat as needing auth.
		if strings.Contains(err.Error(), "no valid token available") || strings.Contains(err.Error(), "authorization required") {
			return true, nil, nil
		}
		return false, nil, fmt.Errorf("initialize mcp client: %w", err)
	}

	return false, nil, nil
}

func verifyAndListTools(ctx context.Context, w io.Writer, url string, oauthCfg mcpclient.OAuthConfig, opts []mcptransport.StreamableHTTPCOption, serverName string) (*OAuthFlowResult, error) {
	client, err := mcpclient.NewOAuthStreamableHttpClient(url, oauthCfg, opts...)
	if err != nil {
		return nil, fmt.Errorf("create verification client: %w", err)
	}
	defer client.Close()

	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := client.Start(startCtx); err != nil {
		return nil, fmt.Errorf("verify start: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "nalvin", Version: "1.0.0", Title: "nalvin MCP client"}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	if _, err := client.Initialize(startCtx, initReq); err != nil {
		return nil, fmt.Errorf("verify initialize: %w", err)
	}

	toolList, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("verify list tools: %w", err)
	}

	names := make([]string, 0, len(toolList.Tools))
	for _, t := range toolList.Tools {
		names = append(names, t.Name)
	}

	fmt.Fprintf(w, "Connected to %s — %d tools available:\n", serverName, len(names))
	for _, name := range names {
		fmt.Fprintf(w, "  mcp__%s__%s\n", serverName, name)
	}

	return &OAuthFlowResult{
		ServerName: serverName,
		ToolNames:  names,
	}, nil
}

type callbackResult struct {
	code           string
	state          string
	err            string
	errDescription string
}

func startCallbackServer(port int, ch chan<- callbackResult) (*http.Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			ch <- callbackResult{err: errCode, errDescription: q.Get("error_description")}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, "<html><body><h2>Authorization failed: %s</h2><p>%s</p><p>You can close this tab.</p></body></html>",
				errCode, q.Get("error_description"))
			return
		}
		ch <- callbackResult{code: q.Get("code"), state: q.Get("state")}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body><h2>Authorization successful!</h2><p>You can close this tab and return to the terminal.</p></body></html>")
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return srv, nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	return exec.Command(cmd, args...).Start()
}

// MCPServerNames returns sorted MCP server names from config for CLI use.
func MCPServerNames(cfg configpkg.Config) []string {
	names := make([]string, 0, len(cfg.Agent.MCPServers))
	for name := range cfg.Agent.MCPServers {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && strings.Compare(s[j-1], s[j]) > 0; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// TestMCPServer connects to an MCP server, lists tools, and returns the result.
func TestMCPServer(ctx context.Context, w io.Writer, serverName string, cfg configpkg.MCPServerConfig) (*OAuthFlowResult, error) {
	client, err := newMCPClient(ctx, serverName, cfg)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	toolList, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	names := make([]string, 0, len(toolList.Tools))
	for _, t := range toolList.Tools {
		names = append(names, t.Name)
	}

	fmt.Fprintf(w, "Connected to %s — %d tools available:\n", serverName, len(names))
	for _, name := range names {
		fmt.Fprintf(w, "  mcp__%s__%s\n", serverName, name)
	}

	return &OAuthFlowResult{
		ServerName: serverName,
		ToolNames:  names,
	}, nil
}
