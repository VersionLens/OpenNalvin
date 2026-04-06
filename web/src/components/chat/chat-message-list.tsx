import { useEffect, useRef } from "react";
import { LoaderCircle } from "lucide-react";
import type { UIMessage, ChatStatus } from "ai";

import { UserMessage } from "@/components/chat/user-message";
import { AssistantMessage } from "@/components/chat/assistant-message";
import { ToolInvocationPart } from "@/components/chat/tool-invocation-part";
import type { TextUIPart, ReasoningUIPart, DynamicToolUIPart } from "ai";

type ChatMessageListProps = {
  messages: UIMessage[];
  status: ChatStatus;
};

export function ChatMessageList({ messages, status }: ChatMessageListProps) {
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, status]);

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-4 px-4 py-6">
        {messages.map((message) => {
          if (message.role === "user") {
            const textParts = message.parts.filter((p): p is TextUIPart => p.type === "text");
            const text = textParts.map((p) => p.text).join("");
            return <UserMessage key={message.id} text={text} />;
          }

          if (message.role === "assistant") {
            // Group consecutive text/reasoning parts together; tool parts break
            // the sequence. This preserves the natural order so that pre-tool
            // and post-tool text are rendered as separate blocks rather than
            // collapsing into a single duplicated-looking response.
            type ContentGroup = { type: "content"; parts: Array<TextUIPart | ReasoningUIPart>; key: number };
            type ToolGroup = { type: "tool"; part: DynamicToolUIPart };
            const groups: Array<ContentGroup | ToolGroup> = [];
            let currentContent: Array<TextUIPart | ReasoningUIPart> | null = null;
            let groupKey = 0;

            for (const part of message.parts) {
              if (part.type === "text" || part.type === "reasoning") {
                if (!currentContent) {
                  currentContent = [];
                  groups.push({ type: "content", parts: currentContent, key: groupKey++ });
                }
                currentContent.push(part as TextUIPart | ReasoningUIPart);
              } else if (part.type === "dynamic-tool") {
                currentContent = null;
                groups.push({ type: "tool", part: part as DynamicToolUIPart });
              }
            }

            return (
              <div key={message.id} className="space-y-4">
                {groups.map((group) =>
                  group.type === "content" ? (
                    <AssistantMessage key={group.key} parts={group.parts} />
                  ) : (
                    <ToolInvocationPart key={group.part.toolCallId} part={group.part} />
                  ),
                )}
              </div>
            );
          }

          return null;
        })}

        {status === "submitted" ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" />
            <span>Thinking…</span>
          </div>
        ) : null}

        <div ref={bottomRef} />
      </div>
    </div>
  );
}
