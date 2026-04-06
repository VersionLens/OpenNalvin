import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useChat } from "@ai-sdk/react";
import { DefaultChatTransport } from "ai";

import { ChatMessageList } from "@/components/chat/chat-message-list";
import { ChatInput } from "@/components/chat/chat-input";
import { useToolConfigStore } from "@/store/tool-config-store";

const transport = new DefaultChatTransport({ api: "/api/chat" });

export function AgentChatPage() {
  const { runId = "" } = useParams();
  const [input, setInput] = useState("");
  const { enabledToolIds, pinnedToolIds, hasCustomizations, loadTools } = useToolConfigStore();

  useEffect(() => {
    void loadTools(runId || undefined);
  }, [loadTools, runId]);

  // The backend is stateful: each run stores its full conversation history by
  // run_id. To maintain a continuous conversation across multiple user turns,
  // we pass the run_id from the previous assistant response so the backend
  // appends to the same run instead of starting a new one.
  //
  // The AI SDK sets each assistant message's id to the messageId from the
  // `{"type":"start","messageId":"<run_id>"}` stream part emitted by our Go
  // handler. We capture it here and forward it on the next request.
  const activeRunId = useRef<string | undefined>(runId || undefined);

  const { messages, sendMessage, status, stop } = useChat({
    transport,
    onFinish: ({ message }) => {
      if (message.role === "assistant" && message.id) {
        activeRunId.current = message.id;
      }
    },
  });

  const isStreaming = status === "streaming" || status === "submitted";

  function handleSubmit() {
    const trimmed = input.trim();
    if (!trimmed || isStreaming) return;
    setInput("");

    // Capture the run_id from the last assistant message before sending, so
    // the closure reads the up-to-date value at call time.
    const runIdForRequest = activeRunId.current;

    void sendMessage(
      { text: trimmed },
      {
        body: {
          run_id: runIdForRequest,
          enabled_tool_ids: hasCustomizations ? enabledToolIds : undefined,
          pinned_tool_ids: hasCustomizations ? pinnedToolIds : undefined,
        },
      },
    );
  }

  return (
    <div className="flex h-full flex-col">
      <ChatMessageList messages={messages} status={status} />
      <ChatInput
        value={input}
        onChange={setInput}
        onSubmit={handleSubmit}
        onStop={stop}
        isStreaming={isStreaming}
        placeholder={runId ? "Continue the saved conversation..." : "Ask the agent something..."}
      />
    </div>
  );
}
