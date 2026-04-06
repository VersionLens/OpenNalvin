import { render, screen } from "@testing-library/react";

import { AgentRunChat } from "@/components/agent-run-chat";
import type { AgentRunTrace } from "@/lib/api";

function createTrace(messages: AgentRunTrace["messages"]): AgentRunTrace {
  return {
    schema_version: 2,
    run_id: "run_123",
    title: "Test run",
    model: "gpt-test",
    usage: {
      input_tokens: 10,
      output_tokens: 20,
      total_tokens: 30,
    },
    estimated_usage: {
      system_prompt: 0,
      user: 10,
      assistant: 8,
      reasoning: 4,
      tool_calls: 12,
      tool_results: 16,
      total: 50,
    },
    created_at: "2026-03-29T00:00:00Z",
    updated_at: "2026-03-29T00:00:00Z",
    messages,
  };
}

describe("AgentRunChat", () => {
  it("renders a tool call and matching result in one card", () => {
    render(
      <AgentRunChat
        trace={createTrace([
          {
            role: "assistant",
            message_id: "msg_001",
            content: "Looking this up now.",
            tool_calls: [
              {
                id: "call_001",
                type: "function",
                estimated_tokens: 1200,
                function: {
                  name: "mcp.exa.web_search_exa",
                  arguments: '{"query":"example domains"}',
                },
              },
            ],
          },
          {
            role: "tool",
            message_id: "msg_002",
            tool_call_id: "call_001",
            tool_name: "mcp.exa.web_search_exa",
            estimated_tokens: 3000,
            content_text: '{"hits":[{"title":"Example Domains"}]}',
            content_json: { hits: [{ title: "Example Domains" }] },
          },
        ])}
      />,
    );

    expect(screen.getByText("~50 est. tokens")).toBeInTheDocument();
    expect(screen.getByText("2 messages")).toBeInTheDocument();
    expect(screen.getByText("Call")).toBeInTheDocument();
    expect(screen.getByText("Result")).toBeInTheDocument();
    expect(screen.getByText("mcp.exa.web_search_exa")).toBeInTheDocument();
    expect(screen.getByText("call_001")).toBeInTheDocument();
    expect(screen.getByText("1.2k est")).toBeInTheDocument();
    expect(screen.getByText("3k est")).toBeInTheDocument();
  });

  it("renders parsed tool JSON once instead of showing raw and parsed output", () => {
    render(
      <AgentRunChat
        trace={createTrace([
          {
            role: "tool",
            message_id: "msg_001",
            tool_name: "search_tools",
            content_text: '{"foo":"bar"}',
            content_json: { foo: "bar" },
          },
        ])}
      />,
    );

    expect(screen.queryByText('{"foo":"bar"}')).not.toBeInTheDocument();
    expect(screen.getByText(/"foo": "bar"/)).toBeInTheDocument();
  });

  it("shows unmatched streamed tool calls without requiring a result yet", () => {
    render(
      <AgentRunChat
        trace={createTrace([
          {
            role: "assistant",
            message_id: "msg_001",
            tool_calls: [
              {
                id: "call_002",
                type: "function",
                function: {
                  name: "web_fetch_get",
                  arguments: '{"url":"https://example.com"}',
                },
              },
            ],
          },
        ])}
      />,
    );

    expect(screen.getByText("web_fetch_get")).toBeInTheDocument();
    expect(screen.getByText("Awaiting tool result.")).toBeInTheDocument();
  });

  it("renders spilled tool output metadata and inline preview", () => {
    render(
      <AgentRunChat
        trace={createTrace([
          {
            role: "tool",
            message_id: "msg_001",
            tool_name: "web_fetch_get",
            content_text: '{"spilled":true}',
            content_json: {
              spilled: true,
              output_id: "out_001",
              preview: "first line\nsecond line",
            },
            tool_output_ref: {
              output_id: "out_001",
              size_bytes: 4096,
              estimated_tokens: 1200,
              total_lines: 120,
              inline_truncated: true,
            },
          },
        ])}
      />,
    );

    expect(screen.getByText("Spilled")).toBeInTheDocument();
    expect(screen.getByText("4 KB")).toBeInTheDocument();
    expect(screen.getByText("1.2k total est")).toBeInTheDocument();
    expect(screen.getByText("120 lines")).toBeInTheDocument();
    expect(screen.getByText("Inline preview")).toBeInTheDocument();
    expect(screen.getByText("out_001")).toBeInTheDocument();
    expect(screen.getByText("Load stored output")).toBeInTheDocument();
  });
});
