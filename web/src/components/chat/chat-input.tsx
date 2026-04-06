import { useRef } from "react";
import { Play, Square } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

type ChatInputProps = {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  onStop: () => void;
  isStreaming: boolean;
  placeholder?: string;
};

export function ChatInput({ value, onChange, onSubmit, onStop, isStreaming, placeholder }: ChatInputProps) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      if (!isStreaming && value.trim()) {
        onSubmit();
      }
    }
  }

  return (
    <div className="border-t border-white/8 bg-[#0a0a0a]">
      <div className="mx-auto max-w-3xl px-4 py-4">
        <div className="space-y-3 rounded-none border border-white/8 bg-black/20 p-4">
          <Textarea
            ref={textareaRef}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={placeholder ?? "Ask the agent something... (Enter to send, Shift+Enter for newline)"}
            className="min-h-[80px] rounded-none resize-none"
            disabled={isStreaming}
          />
          <div className="flex justify-end">
            {isStreaming ? (
              <Button variant="outline" onClick={onStop}>
                <Square className="size-4" />
                Stop
              </Button>
            ) : (
              <Button onClick={onSubmit} disabled={!value.trim()}>
                <Play className="size-4" />
                Run agent
              </Button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
