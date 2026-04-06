import { create } from "zustand";

import type { AgentRunChunk, AgentRunMessage, AgentRunTrace, StoredToolCall } from "@/lib/api";

export type AgentStatus = "idle" | "running" | "done" | "error";

type AgentRunState = {
  status: AgentStatus;
  runId: string | null;
  error: string | null;
  baseTrace: AgentRunTrace | null;
  streamingContent: string;
  streamingReasoning: string;
  streamingToolCalls: StoredToolCall[];
  abortFn: (() => void) | null;
  seedTrace: (trace: AgentRunTrace | null) => void;
  setAbortFn: (fn: (() => void) | null) => void;
  handleChunk: (chunk: AgentRunChunk) => void;
  finishWithTrace: (trace: AgentRunTrace) => void;
  setError: (message: string) => void;
  reset: () => void;
};

const initialState = {
  status: "idle" as AgentStatus,
  runId: null as string | null,
  error: null as string | null,
  baseTrace: null as AgentRunTrace | null,
  streamingContent: "",
  streamingReasoning: "",
  streamingToolCalls: [] as StoredToolCall[],
  abortFn: null as (() => void) | null,
};

export const useAgentStore = create<AgentRunState>((set) => ({
  ...initialState,
  seedTrace: (trace) =>
    set({
      baseTrace: trace,
      runId: trace?.run_id ?? null,
      status: "idle",
      error: null,
      streamingContent: "",
      streamingReasoning: "",
      streamingToolCalls: [],
    }),
  setAbortFn: (abortFn) => set({ abortFn }),
  handleChunk: (chunk) =>
    set((state) => {
      if (chunk.error?.message) {
        return {
          ...state,
          status: "error",
          error: chunk.error.message,
        };
      }

      const choice = chunk.choices?.[0];
      const delta = choice?.delta;
      const nextToolCalls = [...state.streamingToolCalls];

      for (const call of delta?.tool_calls ?? []) {
        if (call.id) {
          nextToolCalls.push(call);
          continue;
        }
        if (nextToolCalls.length === 0) {
          nextToolCalls.push(call);
          continue;
        }
        const last = { ...nextToolCalls[nextToolCalls.length - 1] };
        last.function = {
          ...last.function,
          arguments: `${last.function?.arguments ?? ""}${call.function?.arguments ?? ""}`,
        };
        nextToolCalls[nextToolCalls.length - 1] = last;
      }

      if (choice?.finish_reason) {
        return {
          ...state,
          runId: chunk.id || state.runId,
          status: choice.finish_reason === "stop" ? "done" : "error",
          streamingContent: "",
          streamingReasoning: "",
          streamingToolCalls: [],
        };
      }

      return {
        ...state,
        status: "running",
        runId: chunk.id || state.runId,
        streamingContent: `${state.streamingContent}${delta?.content ?? ""}`,
        streamingReasoning: `${state.streamingReasoning}${delta?.reasoning_content ?? ""}`,
        streamingToolCalls: nextToolCalls,
      };
    }),
  finishWithTrace: (trace) =>
    set({
      status: "done",
      runId: trace.run_id,
      baseTrace: trace,
      streamingContent: "",
      streamingReasoning: "",
      streamingToolCalls: [],
      error: null,
    }),
  setError: (error) => set({ status: "error", error }),
  reset: () => set(initialState),
}));

export function buildLiveTrace(trace: AgentRunTrace | null, content: string, reasoning: string, toolCalls: StoredToolCall[]) {
  if (!trace && !content && !reasoning && toolCalls.length === 0) {
    return null;
  }

  const messages = [...(trace?.messages ?? [])] as AgentRunMessage[];
  if (content || reasoning || toolCalls.length > 0) {
    messages.push({
      role: "assistant",
      content,
      reasoning,
      tool_calls: toolCalls,
    });
  }

  return {
    schema_version: trace?.schema_version ?? 1,
    run_id: trace?.run_id ?? "",
    title: trace?.title ?? "",
    model: trace?.model ?? "",
    system_prompt: trace?.system_prompt ?? "",
    usage: trace?.usage ?? { input_tokens: 0, output_tokens: 0, total_tokens: 0 },
    created_at: trace?.created_at ?? new Date().toISOString(),
    updated_at: trace?.updated_at ?? new Date().toISOString(),
    messages,
  } satisfies AgentRunTrace;
}
