import { buildChatRows, buildTokenBreakdownRows } from "@/lib/agent-run-display";
import type { AgentRunTrace } from "@/lib/api";

const trace: AgentRunTrace = {
  schema_version: 2,
  run_id: "run_123",
  title: "Trace",
  model: "gpt-test",
  system_prompt: "Be concise.",
  usage: {
    input_tokens: 10,
    output_tokens: 20,
    total_tokens: 30,
  },
  estimated_usage: {
    system_prompt: 20,
    user: 10,
    assistant: 8,
    reasoning: 4,
    tool_calls: 12,
    tool_results: 16,
    total: 70,
  },
  created_at: "2026-03-29T00:00:00Z",
  updated_at: "2026-03-29T00:00:00Z",
  messages: [
    {
      role: "user",
      message_id: "msg_001",
      content: "hello",
      estimated_tokens: 10,
    },
    {
      role: "assistant",
      message_id: "msg_002",
      content: "Looking this up.",
      estimated_tokens: 8,
      tool_calls: [
        {
          id: "call_001",
          type: "function",
          estimated_tokens: 12,
          function: {
            name: "web_fetch_get",
            arguments: '{"url":"https://example.com"}',
          },
        },
      ],
    },
    {
      role: "tool",
      message_id: "msg_003",
      tool_call_id: "call_001",
      tool_name: "web_fetch_get",
      estimated_tokens: 16,
      content_text: '{"ok":true}',
      content_json: { ok: true },
    },
  ],
};

describe("agent-run display helpers", () => {
  it("groups tool calls with their result rows", () => {
    const rows = buildChatRows(trace.messages);

    expect(rows).toHaveLength(3);
    expect(rows[0].kind).toBe("user");
    expect(rows[1].kind).toBe("assistant");
    expect(rows[2].kind).toBe("tool");
    if (rows[2].kind !== "tool") {
      throw new Error("expected tool row");
    }
    expect(rows[2].call?.function?.name).toBe("web_fetch_get");
    expect(rows[2].result?.tool_call_id).toBe("call_001");
  });

  it("builds token breakdown rows including the system prompt", () => {
    const rows = buildChatRows(trace.messages);
    const breakdown = buildTokenBreakdownRows(trace, rows);

    expect(breakdown.map((row) => row.kind)).toEqual(["system", "user", "assistant", "tool"]);
    expect(breakdown[0].estimatedTokens).toBe(20);
    expect(breakdown[3].estimatedTokens).toBe(28);
  });
});
