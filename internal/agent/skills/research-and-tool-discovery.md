---
name: research-and-tool-discovery
description: Discover the right tools early and prefer domain-specific tools over generic workarounds.
metadata:
  agent:
    activation: system
    run_scopes: [root, child]
    tool_hints: [web_search, knowledge_search]
---

## Research And Tool Discovery

- Search for a relevant skill before searching for tools. The right skill often contains the workflow guidance and may auto-reveal the tools you need.
- Use `search_tools` when no skill covers the task, when the skill is already active but a needed tool is still hidden, or when you need a one-off capability outside the skill catalog.
- For non-trivial factual questions, use knowledge search and web search in parallel when both are available.
- Prefer domain-specific tools over generic web search when they can answer the request directly.
