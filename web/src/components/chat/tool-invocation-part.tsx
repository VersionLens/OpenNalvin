import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import type { DynamicToolUIPart } from "ai";
import { stringifyJson } from "@/lib/utils";

type ToolInvocationPartProps = {
  part: DynamicToolUIPart;
};

export function ToolInvocationPart({ part }: ToolInvocationPartProps) {
  return (
    <Card className="border-white/10 bg-white/[0.04]">
      <CardContent className="space-y-4 p-5">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="secondary">Tool</Badge>
          <Badge variant="outline">{part.toolName}</Badge>
          <span className="font-mono text-[11px] text-muted-foreground">{part.toolCallId}</span>
        </div>

        {part.input !== undefined ? (
          <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
            <Badge variant="outline">Call</Badge>
            <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
              {typeof part.input === "string" ? part.input : stringifyJson(part.input)}
            </pre>
          </div>
        ) : null}

        {part.state === "output-available" ? (
          <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
            <Badge variant="outline">Result</Badge>
            <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
              {typeof part.output === "string" ? part.output : stringifyJson(part.output)}
            </pre>
          </div>
        ) : part.state === "output-error" ? (
          <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
            <Badge variant="destructive">Error</Badge>
            <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
              {part.errorText}
            </pre>
          </div>
        ) : (
          <div className="rounded-none border border-dashed border-white/8 bg-black/10 p-3 text-xs text-muted-foreground">
            Awaiting tool result.
          </div>
        )}
      </CardContent>
    </Card>
  );
}
