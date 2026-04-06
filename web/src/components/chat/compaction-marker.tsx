import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import type { StoredCompaction } from "@/lib/api";
import { formatCompactTokens } from "@/lib/utils";

type CompactionMarkerProps = {
  compaction: StoredCompaction;
};

export function CompactionMarker({ compaction }: CompactionMarkerProps) {
  const { summary } = compaction;
  const tokenDelta = compaction.post_tokens - compaction.pre_tokens;
  const pct =
    compaction.pre_tokens > 0 ? Math.round((compaction.post_tokens / compaction.pre_tokens) * 100) : 0;

  return (
    <div className="relative flex items-center gap-3 py-1">
      <div className="h-px flex-1 bg-violet-500/30" />
      <Accordion type="single" collapsible className="flex-none">
        <AccordionItem value="compaction" className="border-none">
          <AccordionTrigger className="flex items-center gap-2 rounded-none border border-violet-500/40 bg-violet-500/8 px-3 py-1.5 text-xs text-violet-300 hover:bg-violet-500/12 hover:no-underline [&>svg]:ml-1">
            <span className="font-mono uppercase tracking-[0.14em]">Checkpoint</span>
            <Badge variant="outline" className="border-violet-500/40 text-violet-300 capitalize">
              {compaction.trigger}
            </Badge>
            <Badge variant="outline" className="border-violet-500/40 font-mono text-violet-300">
              {compaction.covered_message_count} msgs
            </Badge>
            <Badge variant="outline" className="border-violet-500/40 font-mono text-violet-300">
              {formatCompactTokens(compaction.pre_tokens)} &rarr; {formatCompactTokens(compaction.post_tokens)}{" "}
              ({tokenDelta <= 0 ? "" : "+"}{formatCompactTokens(tokenDelta)}, {pct}%)
            </Badge>
          </AccordionTrigger>
          <AccordionContent className="border border-t-0 border-violet-500/40 bg-violet-500/5 px-4 pb-4 pt-3">
            <div className="space-y-3 text-xs">
              {summary.goal ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Goal</p>
                  <p className="text-foreground">{summary.goal}</p>
                </div>
              ) : null}
              {summary.constraints?.length ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Constraints</p>
                  <ul className="space-y-0.5 text-muted-foreground">
                    {summary.constraints.map((c, i) => <li key={i}>{c}</li>)}
                  </ul>
                </div>
              ) : null}
              {summary.decisions?.length ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Decisions</p>
                  <ul className="space-y-0.5 text-muted-foreground">
                    {summary.decisions.map((d, i) => <li key={i}>{d}</li>)}
                  </ul>
                </div>
              ) : null}
              {summary.completed?.length ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Completed</p>
                  <ul className="space-y-0.5 text-muted-foreground">
                    {summary.completed.map((c, i) => <li key={i}>{c}</li>)}
                  </ul>
                </div>
              ) : null}
              {summary.open?.length ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Open</p>
                  <ul className="space-y-0.5 text-muted-foreground">
                    {summary.open.map((o, i) => <li key={i}>{o}</li>)}
                  </ul>
                </div>
              ) : null}
              {summary.files?.length ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Files</p>
                  <ul className="space-y-0.5 font-mono text-muted-foreground">
                    {summary.files.map((f, i) => <li key={i}>{f}</li>)}
                  </ul>
                </div>
              ) : null}
              {summary.tool_outputs?.length ? (
                <div>
                  <p className="mb-1 text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Stored outputs</p>
                  <ul className="space-y-1">
                    {summary.tool_outputs.map((ref) => (
                      <li key={ref.output_id} className="flex flex-wrap items-baseline gap-2">
                        <span className="font-mono text-violet-300">{ref.output_id}</span>
                        <span className="text-muted-foreground">{ref.tool_name}</span>
                        <span className="text-muted-foreground">&mdash; {ref.why_it_matters}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              <p className="font-mono text-[10px] text-muted-foreground/50">{compaction.id}</p>
            </div>
          </AccordionContent>
        </AccordionItem>
      </Accordion>
      <div className="h-px flex-1 bg-violet-500/30" />
    </div>
  );
}
