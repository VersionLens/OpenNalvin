import type { AgentRunMessage, AgentRunTrace, StoredToolCall } from "@/lib/api";

export type ChatRow =
  | {
      kind: "user";
      key: string;
      message: AgentRunMessage;
      sourceIndex: number;
      anchorId: string;
    }
  | {
      kind: "assistant";
      key: string;
      message: AgentRunMessage;
      toolCallCount: number;
      sourceIndex: number;
      anchorId: string;
    }
  | {
      kind: "tool";
      key: string;
      call: StoredToolCall | null;
      result: AgentRunMessage | null;
      sourceIndex: number;
      anchorId: string;
    };

export type TokenBreakdownRow = {
  key: string;
  kind: "system" | "user" | "assistant" | "tool";
  label: string;
  estimatedTokens: number;
  anchorId: string | null;
  durationMs?: number;
};

export function buildChatRows(messages: AgentRunMessage[]): ChatRow[] {
  const rows: ChatRow[] = [];

  for (let index = 0; index < messages.length; index += 1) {
    const message = messages[index];

    if (message.role === "user") {
      rows.push({
        kind: "user",
        key: message.message_id ?? `user-${index}`,
        message,
        sourceIndex: index,
        anchorId: `message-${message.message_id ?? `user-${index}`}`,
      });
      continue;
    }

    if (message.role === "assistant") {
      const toolCalls = message.tool_calls ?? [];
      const trailingToolMessages: AgentRunMessage[] = [];
      let cursor = index + 1;

      while (cursor < messages.length && messages[cursor].role === "tool") {
        trailingToolMessages.push(messages[cursor]);
        cursor += 1;
      }

      rows.push({
        kind: "assistant",
        key: message.message_id ?? `assistant-${index}`,
        message,
        toolCallCount: toolCalls.length,
        sourceIndex: index,
        anchorId: `message-${message.message_id ?? `assistant-${index}`}`,
      });

      if (toolCalls.length > 0) {
        const toolResultsByID = new Map<string, AgentRunMessage>();
        const unmatchedResults: AgentRunMessage[] = [];

        for (const toolMessage of trailingToolMessages) {
          if (toolMessage.tool_call_id) {
            toolResultsByID.set(toolMessage.tool_call_id, toolMessage);
          } else {
            unmatchedResults.push(toolMessage);
          }
        }

        toolCalls.forEach((call, toolIndex) => {
          rows.push({
            kind: "tool",
            key: call.id ?? `${message.message_id ?? index}-tool-${toolIndex}`,
            call,
            result: call.id ? (toolResultsByID.get(call.id) ?? null) : (unmatchedResults.shift() ?? null),
            sourceIndex: index,
            anchorId: `message-${call.id ?? `${message.message_id ?? index}-tool-${toolIndex}`}`,
          });
        });

        for (const toolMessage of trailingToolMessages) {
          const isMatched = toolMessage.tool_call_id ? toolCalls.some((call) => call.id === toolMessage.tool_call_id) : false;
          if (isMatched) {
            continue;
          }
          rows.push({
            kind: "tool",
            key: toolMessage.message_id ?? `tool-${index}-${rows.length}`,
            call: null,
            result: toolMessage,
            sourceIndex: index,
            anchorId: `message-${toolMessage.message_id ?? `tool-${index}-${rows.length}`}`,
          });
        }

        index = cursor - 1;
        continue;
      }

      continue;
    }

    rows.push({
      kind: "tool",
      key: message.message_id ?? `tool-${index}`,
      call: null,
      result: message,
      sourceIndex: index,
      anchorId: `message-${message.message_id ?? `tool-${index}`}`,
    });
  }

  return rows;
}

export function buildTokenBreakdownRows(trace: AgentRunTrace, rows: ChatRow[]): TokenBreakdownRow[] {
  const breakdownRows: TokenBreakdownRow[] = [];

  if (trace.system_prompt && (trace.estimated_usage?.system_prompt ?? 0) > 0) {
    breakdownRows.push({
      key: "system-prompt",
      kind: "system",
      label: previewText(trace.system_prompt, 72),
      estimatedTokens: trace.estimated_usage?.system_prompt ?? 0,
      anchorId: "system-prompt",
    });
  }

  for (const row of rows) {
    if (row.kind === "user") {
      breakdownRows.push({
        key: row.key,
        kind: "user",
        label: previewText(row.message.content ?? "", 72) || "User message",
        estimatedTokens: row.message.estimated_tokens ?? 0,
        anchorId: row.anchorId,
        durationMs: messageDurationMs(row.message),
      });
      continue;
    }

    if (row.kind === "assistant") {
      const label = row.message.content
        ? previewText(row.message.content, 72)
        : row.message.reasoning
          ? previewText(row.message.reasoning, 72)
          : "Assistant step";

      breakdownRows.push({
        key: row.key,
        kind: "assistant",
        label,
        estimatedTokens: row.message.estimated_tokens ?? 0,
        anchorId: row.anchorId,
        durationMs: messageDurationMs(row.message),
      });
      continue;
    }

    const toolName = row.call?.function?.name || row.result?.tool_name || "tool";
    const toolTokens = (row.call?.estimated_tokens ?? 0) + (row.result?.estimated_tokens ?? 0);
    breakdownRows.push({
      key: row.key,
      kind: "tool",
      label: toolName,
      estimatedTokens: toolTokens,
      anchorId: row.anchorId,
      durationMs: row.result ? messageDurationMs(row.result) : undefined,
    });
  }

  return breakdownRows;
}

function messageDurationMs(message: { started_at?: string; ended_at?: string }): number | undefined {
  if (!message.started_at || !message.ended_at) return undefined;
  const ms = new Date(message.ended_at).getTime() - new Date(message.started_at).getTime();
  return ms >= 0 ? ms : undefined;
}

function previewText(value: string, maxLen: number) {
  const normalized = value.replace(/\s+/g, " ").trim();
  if (!normalized) {
    return "";
  }
  if (normalized.length <= maxLen) {
    return normalized;
  }
  return `${normalized.slice(0, maxLen - 1)}…`;
}
