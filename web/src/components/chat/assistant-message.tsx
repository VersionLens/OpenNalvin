import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import type { ReasoningUIPart, TextUIPart } from "ai";

type AssistantMessageProps = {
  parts: Array<TextUIPart | ReasoningUIPart>;
};

export function AssistantMessage({ parts }: AssistantMessageProps) {
  const textParts = parts.filter((p): p is TextUIPart => p.type === "text");
  const reasoningParts = parts.filter((p): p is ReasoningUIPart => p.type === "reasoning");

  const isReasoningStreaming = reasoningParts.some((p) => p.state === "streaming");
  const hasText = textParts.some((p) => p.text.length > 0);
  const combinedReasoning = reasoningParts.map((p) => p.text).join("");

  // Track whether text has ever started to know when to auto-close reasoning.
  const textStartedRef = useRef(false);
  const [userOpen, setUserOpen] = useState(false);

  useEffect(() => {
    if (hasText && !textStartedRef.current) {
      textStartedRef.current = true;
      setUserOpen(false);
    }
  }, [hasText]);

  // Force-open while reasoning is streaming and text hasn't started.
  // Once text starts, controlled by userOpen.
  const isReasoningOpen = isReasoningStreaming && !hasText ? true : userOpen;

  if (textParts.length === 0 && reasoningParts.length === 0) {
    return null;
  }

  return (
    <div className="space-y-3 py-1">
      {combinedReasoning ? (
        <Accordion
          type="single"
          collapsible
          value={isReasoningOpen ? "reasoning" : ""}
          onValueChange={(v) => {
            // Only allow user toggle once streaming is done
            if (!isReasoningStreaming) {
              setUserOpen(v === "reasoning");
            }
          }}
        >
          <AccordionItem value="reasoning" className="border-white/8">
            <AccordionTrigger className="py-2 text-xs uppercase tracking-[0.16em] text-muted-foreground">
              Reasoning
              {isReasoningStreaming ? (
                <span className="ml-2 inline-block size-1.5 animate-pulse rounded-full bg-current" />
              ) : null}
            </AccordionTrigger>
            <AccordionContent>
              <pre className="overflow-x-auto whitespace-pre-wrap rounded-none border border-white/8 bg-black/25 p-3 text-xs text-muted-foreground">
                {combinedReasoning}
              </pre>
            </AccordionContent>
          </AccordionItem>
        </Accordion>
      ) : null}

      {textParts.map((part, i) =>
        part.text ? (
          <div
            key={i}
            className="prose prose-invert prose-sm max-w-none leading-relaxed
              prose-headings:font-semibold prose-headings:tracking-tight
              prose-code:rounded-none prose-code:bg-white/8 prose-code:px-1.5 prose-code:py-0.5 prose-code:text-xs prose-code:font-mono prose-code:before:content-none prose-code:after:content-none
              prose-pre:rounded-none prose-pre:border prose-pre:border-white/8 prose-pre:bg-black/30
              prose-a:text-blue-400 prose-a:no-underline hover:prose-a:underline
              prose-hr:border-white/10"
          >
            <Markdown>{part.text}</Markdown>
          </div>
        ) : null,
      )}
    </div>
  );
}
