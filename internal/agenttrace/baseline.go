package agenttrace

import "strings"

type BaselinePrompt struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	Prompt   string   `json:"prompt"`
}

type BaselineSuiteOptions struct {
	Category string
	Contains string
	Limit    int
}

func BaselineSuite(opts BaselineSuiteOptions) []BaselinePrompt {
	category := strings.ToLower(strings.TrimSpace(opts.Category))
	contains := strings.ToLower(strings.TrimSpace(opts.Contains))
	out := make([]BaselinePrompt, 0, len(defaultBaselinePrompts))
	for _, prompt := range defaultBaselinePrompts {
		if category != "" && strings.ToLower(prompt.Category) != category {
			continue
		}
		if contains != "" {
			haystack := strings.ToLower(prompt.ID + " " + prompt.Category + " " + strings.Join(prompt.Tags, " ") + " " + prompt.Prompt)
			if !strings.Contains(haystack, contains) {
				continue
			}
		}
		out = append(out, prompt)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out
}

func BaselineCategories() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, prompt := range defaultBaselinePrompts {
		if _, ok := seen[prompt.Category]; ok {
			continue
		}
		seen[prompt.Category] = struct{}{}
		out = append(out, prompt.Category)
	}
	return out
}

var defaultBaselinePrompts = []BaselinePrompt{
	{
		ID:       "skill-curator-read",
		Category: "skill-lifecycle",
		Tags:     []string{"skill", "skill-curator", "no-web"},
		Prompt:   "Activate the skill-curator skill. Create a managed skill named baseline-managed-read with scripts and references. Then use view on skill://baseline-managed-read/SKILL.md and summarize whether it looks like a valid skill. Do not use web/http/browser tools for skill:// paths.",
	},
	{
		ID:       "skill-curator-guardrail",
		Category: "skill-lifecycle",
		Tags:     []string{"skill", "guardrail", "tool-error-recovery"},
		Prompt:   "Activate the skill-curator skill. Create a managed skill named baseline-managed-guardrail with references. Then try to replace baseline-managed-guardrail SKILL.md with the exact text BASELINE READY using modify_skill. If modify_skill fails, say it failed and include the error. Finally view skill://baseline-managed-guardrail/SKILL.md and report whether the file still looks like a valid skill. Do not use web/http/browser tools for skill:// paths.",
	},
	{
		ID:       "find-skill-news",
		Category: "skill-lifecycle",
		Tags:     []string{"skill", "news", "tool-selection"},
		Prompt:   "Find a relevant skill for collecting current headlines, activate it if useful, then summarize the top technology headlines. Use the skill or its tools if they are available, and keep the answer concise.",
	},
	{
		ID:       "find-skill-files",
		Category: "skill-lifecycle",
		Tags:     []string{"skill", "files", "workspace"},
		Prompt:   "Find a relevant skill for batching file analysis, activate it if useful, then count how many Go files are in the current workspace root and one level below. Explain briefly which tool path you chose.",
	},
	{
		ID:       "hidden-news-tools",
		Category: "tool-discovery",
		Tags:     []string{"search-tools", "news"},
		Prompt:   "Use search_tools to reveal the hidden news tools, then use the appropriate revealed tools to fetch the latest technology headlines and one relevant discussion source. Summarize the result in five bullets.",
	},
	{
		ID:       "hidden-git-tools",
		Category: "tool-discovery",
		Tags:     []string{"search-tools", "git"},
		Prompt:   "Use search_tools with the query git to reveal Git-related tools. Then inspect the current repository status and the latest commit summary using the right tool or shell fallback. Report what changed and do not modify files.",
	},
	{
		ID:       "hidden-docker-tools",
		Category: "tool-discovery",
		Tags:     []string{"search-tools", "docker"},
		Prompt:   "Use search_tools to find docker-related tools. Report which docker tools are available, then inspect whether a development container can mount the current workspace. Do not create a long-running container unless the tool requires it for inspection.",
	},
	{
		ID:       "tool-choice-no-browser",
		Category: "tool-discovery",
		Tags:     []string{"wrong-tool", "no-browser"},
		Prompt:   "List the files in the current workspace root and tell me what you see. Use local file or shell tools, not browser or web tools.",
	},
	{
		ID:       "subagent-simple",
		Category: "subagents",
		Tags:     []string{"subagent", "wait"},
		Prompt:   "Spawn one child agent named baseline_child. Ask the child to reply with exactly: child ok. Wait for the child to finish, then report the child's answer exactly.",
	},
	{
		ID:       "subagent-two-researchers",
		Category: "subagents",
		Tags:     []string{"subagent", "parallel", "synthesis"},
		Prompt:   "Use two spawned child agents to research today's AI news. One child should cover model releases and one child should cover policy or industry news. Wait for both children and synthesize a concise combined digest.",
	},
	{
		ID:       "subagent-verifier",
		Category: "subagents",
		Tags:     []string{"subagent", "verification", "workspace"},
		Prompt:   "Investigate the current backend workspace like a debugging task. Use a local tool to print the workspace path and list top-level files. Spawn a verification child agent to independently list top-level files, then compare the results.",
	},
	{
		ID:       "subagent-browser-delegate",
		Category: "subagents",
		Tags:     []string{"subagent", "browser"},
		Prompt:   "Inspect the currently open browser tab. If a browser automation skill or child provider is appropriate, delegate the browser-specific inspection to a child agent and summarize the page title and visible purpose.",
	},
	{
		ID:       "shell-deterministic",
		Category: "shell-docker-workspace",
		Tags:     []string{"shell", "single-tool-call"},
		Prompt:   "Use the shell tool exactly once. In that shell call, compute sha256 of the string nalvin-baseline and print only the hash. Then answer with the hash and no extra commentary.",
	},
	{
		ID:       "workspace-path-files",
		Category: "shell-docker-workspace",
		Tags:     []string{"workspace", "files"},
		Prompt:   "Use local workspace tools to print the current workspace path and list the root directory. Then identify whether this looks like a Go repo, a web repo, or both.",
	},
	{
		ID:       "docker-container-check",
		Category: "shell-docker-workspace",
		Tags:     []string{"docker", "workspace-mount"},
		Prompt:   "Create or inspect a development container from the current workspace using the available docker workflow. Verify that the workspace is mounted and report the container name, mount path, and one command output from inside the container.",
	},
	{
		ID:       "shell-error-recovery",
		Category: "shell-docker-workspace",
		Tags:     []string{"tool-error-recovery", "shell"},
		Prompt:   "Use a shell command that first attempts to read a clearly nonexistent file named ./definitely-not-here-baseline.txt, then recovers by listing the current directory. Explain the failure and the recovery in two sentences.",
	},
	{
		ID:       "browser-open-tab",
		Category: "browser-mcp",
		Tags:     []string{"browser", "mcp"},
		Prompt:   "Inspect the currently open browser tab and tell me the page title, URL, and main visible purpose. Use browser tools if available; do not guess from memory.",
	},
	{
		ID:       "browser-no-local-files",
		Category: "browser-mcp",
		Tags:     []string{"browser", "wrong-tool"},
		Prompt:   "Tell me what files are in the current workspace root. This is not a browser task, so avoid browser tools unless no local inspection tool exists.",
	},
	{
		ID:       "github-mcp-review",
		Category: "browser-mcp",
		Tags:     []string{"mcp", "github", "review"},
		Prompt:   "If GitHub MCP tools are available, inspect the most recent accessible pull request or issue and summarize what kind of repository activity it represents. If they are not available, say so and use a local git status fallback.",
	},
	{
		ID:       "tool-search-mcp",
		Category: "browser-mcp",
		Tags:     []string{"mcp", "tool-search"},
		Prompt:   "Use tool discovery to determine whether MCP or app connector tools are available for this task. Report the most relevant connector-style tool family and why it is or is not useful here.",
	},
	{
		ID:       "news-rss-reddit",
		Category: "news-research",
		Tags:     []string{"news", "rss", "reddit"},
		Prompt:   "Use the available news workflow to fetch one AI headline source and one discussion source. Return a concise cross-source digest with exactly three bullets and include the source names.",
	},
	{
		ID:       "news-current-specific",
		Category: "news-research",
		Tags:     []string{"news", "search"},
		Prompt:   "Search current news for a recent OpenAI, Anthropic, Google DeepMind, or Qwen model update. Summarize the top result, including date and source if available.",
	},
	{
		ID:       "research-parallel",
		Category: "news-research",
		Tags:     []string{"research", "parallel", "subagent"},
		Prompt:   "Prepare a concise cross-source situation digest about a current geopolitical or technology topic. Use parallel child agents if helpful, and distinguish confirmed facts from uncertainty.",
	},
	{
		ID:       "research-no-overbrowse",
		Category: "news-research",
		Tags:     []string{"research", "tool-choice"},
		Prompt:   "Summarize what this repository appears to implement based only on local files and agent context. Do not browse the public web for this local-code question.",
	},
	{
		ID:       "git-status-summary",
		Category: "git-code-eval",
		Tags:     []string{"git", "status"},
		Prompt:   "Inspect git status and the latest commit. Report modified tracked files, untracked files if any, and the latest commit subject. Do not modify the worktree.",
	},
	{
		ID:       "codebase-find-entrypoint",
		Category: "git-code-eval",
		Tags:     []string{"codebase", "rg"},
		Prompt:   "Find the CLI entrypoint for agent trace commands in this repo. Use fast local search, identify the main file and command names, and give a short explanation.",
	},
	{
		ID:       "code-eval-shell-once",
		Category: "git-code-eval",
		Tags:     []string{"shell", "reasoning"},
		Prompt:   "Use the shell tool exactly once to count Go test files under cmd and internal. Then answer with the counts per top-level directory and one sentence about coverage shape.",
	},
	{
		ID:       "spilled-output-page",
		Category: "file-tool-output",
		Tags:     []string{"spilled-output", "output-paging"},
		Prompt:   "Run a local command that produces more than 200 lines of harmless output, then summarize it without pasting everything. If output is spilled or summarized, inspect only the useful portion before answering.",
	},
	{
		ID:       "grep-large-output",
		Category: "file-tool-output",
		Tags:     []string{"grep", "large-output"},
		Prompt:   "Search the repository for occurrences of agent traces. Summarize the most relevant files and avoid dumping long match lists.",
	},
	{
		ID:       "torrent-search-only",
		Category: "failure-recovery",
		Tags:     []string{"torrent", "guardrail", "search-only"},
		Prompt:   "Use torrent search only. Do not download anything. Search for Ubuntu 24.04 Desktop ISO torrents and summarize titles, sizes, and whether they look like legitimate Linux ISO results.",
	},
	{
		ID:       "bad-tool-argument-recovery",
		Category: "failure-recovery",
		Tags:     []string{"tool-error-recovery", "arguments"},
		Prompt:   "Try to use an available local inspection tool with a too-specific path that may not exist, then recover by inspecting the current directory. Report the error and the corrected path choice.",
	},
}
