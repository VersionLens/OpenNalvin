import type React from "react";
import { useState } from "react";

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  api,
  type AgentRunMessage,
  type AgentRunToolOutputPage,
  type AgentRunToolOutputSearchResult,
  type AgentRunTrace,
  type StoredCompaction,
} from "@/lib/api";
import {
  buildChatRows,
  buildTokenBreakdownRows,
  type TokenBreakdownRow,
} from "@/lib/agent-run-display";
import { formatCompactTokens, stringifyJson } from "@/lib/utils";

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 10000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.round(ms / 1000)}s`;
}

function messageDurationMs(message: { started_at?: string; ended_at?: string }): number | undefined {
  if (!message.started_at || !message.ended_at) return undefined;
  const ms = new Date(message.ended_at).getTime() - new Date(message.started_at).getTime();
  return ms >= 0 ? ms : undefined;
}

type AgentRunChatProps = {
  trace: AgentRunTrace;
};

const ROLE_STYLES = {
  system: {
    dot: "bg-amber-500/80",
    segment: "bg-amber-500/80",
    bar: "bg-amber-400/80",
  },
  user: {
    dot: "bg-blue-500",
    segment: "bg-blue-500",
    bar: "bg-blue-400",
  },
  assistant: {
    dot: "bg-emerald-500",
    segment: "bg-emerald-500",
    bar: "bg-emerald-400",
  },
  tool: {
    dot: "bg-slate-400",
    segment: "bg-slate-400",
    bar: "bg-slate-300",
  },
} as const;

export function AgentRunChat({ trace }: AgentRunChatProps) {
  const rows = buildChatRows(trace.messages);
  const tokenRows = buildTokenBreakdownRows(trace, rows);

  // Build a map from covered_message_count -> compaction, so we can insert
  // a marker after the last row whose sourceIndex < covered_message_count.
  const compactionByBoundary = new Map<number, StoredCompaction>();
  for (const c of trace.compactions ?? []) {
    compactionByBoundary.set(c.covered_message_count, c);
  }

  // For each row index, determine if a compaction marker should follow it.
  // A marker for covered_message_count=N goes after the last row where sourceIndex < N.
  function compactionAfterRow(rowIndex: number): StoredCompaction | null {
    const currentSourceIndex = rows[rowIndex]?.sourceIndex ?? -1;
    const nextSourceIndex = rows[rowIndex + 1]?.sourceIndex ?? Infinity;
    for (const [boundary, compaction] of compactionByBoundary) {
      if (currentSourceIndex < boundary && nextSourceIndex >= boundary) {
        return compaction;
      }
    }
    return null;
  }

  return (
    <div className="space-y-4">
      {trace.system_prompt ? (
        <div id="system-prompt" className="sr-only" />
      ) : null}
      {trace.estimated_usage?.total ? (
        <TokenBreakdown trace={trace} rows={tokenRows} />
      ) : null}

      {rows.flatMap((row, index) => {
        let rowElement: React.ReactNode = null;

        if (row.kind === "user") {
          rowElement = (
            <div key={row.key} id={row.anchorId} className="flex justify-end">
              <div className="max-w-[85%] rounded-none bg-primary px-5 py-4 text-sm text-primary-foreground shadow-lg shadow-white/8">
                <div className="flex items-center justify-between gap-3">
                  <Badge variant="secondary">User</Badge>
                  {row.message.estimated_tokens ? (
                    <Badge variant="outline">
                      {formatCompactTokens(row.message.estimated_tokens)} est
                    </Badge>
                  ) : null}
                </div>
                <p className="mt-3 whitespace-pre-wrap leading-relaxed">
                  {row.message.content}
                </p>
              </div>
            </div>
          );
        } else if (row.kind === "assistant") {
          const hasBody = Boolean(row.message.content || row.message.reasoning);
          if (hasBody) {
            rowElement = (
              <Card
                key={row.key}
                id={row.anchorId}
                className="border-white/10 bg-white/[0.04]"
              >
                <CardContent className="space-y-4 p-5">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary">Assistant</Badge>
                    {row.toolCallCount > 0 ? (
                      <Badge variant="outline">
                        {row.toolCallCount} tool
                        {row.toolCallCount === 1 ? "" : "s"}
                      </Badge>
                    ) : null}
                    {row.message.estimated_tokens ? (
                      <Badge variant="outline">
                        {formatCompactTokens(row.message.estimated_tokens)} est
                      </Badge>
                    ) : null}
                    {(() => {
                      const dur = messageDurationMs(row.message);
                      return dur !== undefined ? (
                        <Badge variant="outline" className="font-mono text-muted-foreground">
                          {formatDuration(dur)}
                        </Badge>
                      ) : null;
                    })()}
                  </div>

                  {row.message.content ? (
                    <p className="whitespace-pre-wrap text-sm leading-relaxed">
                      {row.message.content}
                    </p>
                  ) : null}

                  {row.message.reasoning ? (
                    <Accordion type="single" collapsible>
                      <AccordionItem
                        value={`reasoning-${index}`}
                        className="border-white/8"
                      >
                        <AccordionTrigger className="py-2 text-xs uppercase tracking-[0.16em] text-muted-foreground">
                          Reasoning
                        </AccordionTrigger>
                        <AccordionContent>
                          <pre className="overflow-x-auto whitespace-pre-wrap rounded-none border border-white/8 bg-black/25 p-3 text-xs text-muted-foreground">
                            {row.message.reasoning}
                          </pre>
                        </AccordionContent>
                      </AccordionItem>
                    </Accordion>
                  ) : null}
                </CardContent>
              </Card>
            );
          }
        } else {
          rowElement = (
            <Card
              key={row.key}
              id={row.anchorId}
              className="border-white/10 bg-white/[0.04]"
            >
              <CardContent className="space-y-4 p-5">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="secondary">Tool</Badge>
                  <Badge
                    variant={row.result?.is_error ? "destructive" : "outline"}
                  >
                    {row.call?.function?.name || row.result?.tool_name || "tool"}
                  </Badge>
                  {row.call?.id ? (
                    <span className="font-mono text-[11px] text-muted-foreground">
                      {row.call.id}
                    </span>
                  ) : null}
                  {(() => {
                    const dur = row.result ? messageDurationMs(row.result) : undefined;
                    return dur !== undefined ? (
                      <Badge variant="outline" className="font-mono text-muted-foreground">
                        {formatDuration(dur)}
                      </Badge>
                    ) : null;
                  })()}
                </div>

                {row.call ? (
                  <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant="outline">Call</Badge>
                      {row.call.estimated_tokens ? (
                        <Badge variant="outline">
                          {formatCompactTokens(row.call.estimated_tokens)} est
                        </Badge>
                      ) : null}
                    </div>
                    {row.call.function?.arguments ? (
                      <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
                        {row.call.function.arguments}
                      </pre>
                    ) : (
                      <p className="text-xs text-muted-foreground">
                        No arguments
                      </p>
                    )}
                  </div>
                ) : null}

                {row.result ? (
                  <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge
                        variant={row.result.is_error ? "destructive" : "outline"}
                      >
                        {row.result.is_error ? "Error" : "Result"}
                      </Badge>
                      {row.result.estimated_tokens ? (
                        <Badge variant="outline">
                          {formatCompactTokens(row.result.estimated_tokens)} est
                        </Badge>
                      ) : null}
                    </div>
                    <RevealedToolsBadges contentJson={row.result.content_json} />
                    {row.result.tool_output_ref ? (
                      <ToolOutputPanel runId={trace.run_id} result={row.result} />
                    ) : row.result.content_json ? (
                      <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
                        {stringifyJson(row.result.content_json)}
                      </pre>
                    ) : row.result.content_text ? (
                      <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
                        {row.result.content_text}
                      </pre>
                    ) : (
                      <p className="text-xs text-muted-foreground">
                        No tool output
                      </p>
                    )}
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

        const compaction = compactionAfterRow(index);
        return [
          rowElement,
          compaction ? (
            <CompactionMarker key={`compaction-${compaction.id}`} compaction={compaction} />
          ) : null,
        ];
      })}
    </div>
  );
}

function CompactionMarker({ compaction }: { compaction: StoredCompaction }) {
  const { summary } = compaction;
  const tokenDelta = compaction.post_tokens - compaction.pre_tokens;
  const pct = compaction.pre_tokens > 0
    ? Math.round((compaction.post_tokens / compaction.pre_tokens) * 100)
    : 0;

  return (
    <div className="relative flex items-center gap-3 py-1">
      <div className="h-px flex-1 bg-violet-500/30" />
      <Accordion type="single" collapsible className="flex-none">
        <AccordionItem value="compaction" className="border-none">
          <AccordionTrigger className="flex items-center gap-2 rounded-none border border-violet-500/40 bg-violet-500/8 px-3 py-1.5 text-xs text-violet-300 hover:bg-violet-500/12 hover:no-underline [&>svg]:ml-1">
            <span className="font-mono uppercase tracking-[0.14em]">
              Checkpoint
            </span>
            <Badge variant="outline" className="border-violet-500/40 text-violet-300 capitalize">
              {compaction.trigger}
            </Badge>
            <Badge variant="outline" className="border-violet-500/40 font-mono text-violet-300">
              {compaction.covered_message_count} msgs
            </Badge>
            <Badge variant="outline" className="border-violet-500/40 font-mono text-violet-300">
              {formatCompactTokens(compaction.pre_tokens)} &rarr; {formatCompactTokens(compaction.post_tokens)}
              {" "}({tokenDelta <= 0 ? "" : "+"}{formatCompactTokens(tokenDelta)}, {pct}%)
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

function RevealedToolsBadges({ contentJson }: { contentJson: unknown }) {
  if (!contentJson || typeof contentJson !== "object") return null;
  const obj = contentJson as Record<string, unknown>;
  const ids = obj.revealed_tool_ids;
  if (!Array.isArray(ids) || ids.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-none border border-emerald-500/30 bg-emerald-500/8 px-3 py-2">
      <span className="text-[11px] uppercase tracking-[0.14em] text-emerald-400">
        Revealed
      </span>
      {ids.map((id) => (
        <Badge
          key={String(id)}
          variant="outline"
          className="border-emerald-500/40 font-mono text-emerald-300"
        >
          {String(id)}
        </Badge>
      ))}
    </div>
  );
}

function ToolOutputPanel({
  runId,
  result,
}: {
  runId: string;
  result: AgentRunMessage;
}) {
  const ref = result.tool_output_ref;
  const [page, setPage] = useState<AgentRunToolOutputPage | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);
  const [pageLoading, setPageLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState<AgentRunToolOutputSearchResult | null>(
    null,
  );
  const [searchError, setSearchError] = useState<string | null>(null);
  const [searchLoading, setSearchLoading] = useState(false);

  if (!ref) {
    return null;
  }
  const outputRef = ref;

  const summary = extractToolOutputSummary(result.content_json);
  const preview = summary?.preview || result.content_text || "";

  async function loadPage(offset: number) {
    setPageLoading(true);
    setPageError(null);
    try {
      setPage(
        await api.getAgentRunToolOutput(runId, outputRef.output_id, { offset }),
      );
    } catch (error) {
      setPageError(
        error instanceof Error ? error.message : "Failed to load stored output",
      );
    } finally {
      setPageLoading(false);
    }
  }

  async function runSearch() {
    if (!query.trim()) {
      return;
    }
    setSearchLoading(true);
    setSearchError(null);
    try {
      setSearch(
        await api.searchAgentRunToolOutput(runId, outputRef.output_id, {
          pattern: query,
          literal_text: true,
        }),
      );
    } catch (error) {
      setSearchError(
        error instanceof Error
          ? error.message
          : "Failed to search stored output",
      );
    } finally {
      setSearchLoading(false);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">Spilled</Badge>
        <Badge variant="outline">
          {formatCompactBytes(outputRef.size_bytes)}
        </Badge>
        <Badge variant="outline">
          {formatCompactTokens(outputRef.estimated_tokens)} total est
        </Badge>
        <Badge variant="outline">{outputRef.total_lines} lines</Badge>
        {outputRef.stored_truncated ? (
          <Badge variant="destructive">Stored output truncated</Badge>
        ) : null}
      </div>

      {preview ? (
        <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Inline preview</Badge>
            <span className="font-mono text-[11px] text-muted-foreground">
              {outputRef.output_id}
            </span>
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
            {preview}
          </pre>
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => loadPage(page?.offset ?? 0)}
          disabled={pageLoading}
        >
          {page ? "Reload stored output" : "Load stored output"}
        </Button>
        {page ? (
          <>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => loadPage(Math.max(0, page.offset - page.limit))}
              disabled={pageLoading || page.offset <= 0}
            >
              Previous page
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => loadPage(page.end_line)}
              disabled={pageLoading || !page.truncated}
            >
              Next page
            </Button>
          </>
        ) : null}
      </div>

      {pageError ? (
        <p className="text-xs text-destructive">{pageError}</p>
      ) : null}
      {page ? (
        <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Stored page</Badge>
            <Badge variant="outline">
              lines {page.start_line || 0}-{page.end_line}
            </Badge>
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
            {page.content || "(no content in this page)"}
          </pre>
        </div>
      ) : null}

      <div className="flex flex-col gap-2 sm:flex-row">
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search stored output"
        />
        <Button
          type="button"
          variant="outline"
          onClick={runSearch}
          disabled={searchLoading || !query.trim()}
        >
          Search
        </Button>
        {search ? (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setSearch(null);
              setSearchError(null);
            }}
          >
            Clear
          </Button>
        ) : null}
      </div>

      {searchError ? (
        <p className="text-xs text-destructive">{searchError}</p>
      ) : null}
      {search ? (
        <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Search</Badge>
            <Badge variant="outline">{search.matches.length} matches</Badge>
            {search.truncated ? (
              <Badge variant="outline">More available</Badge>
            ) : null}
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
            {search.matches.length > 0
              ? search.matches
                  .map((match) => `${match.line_number}: ${match.preview}`)
                  .join("\n")
              : "No matches"}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

function TokenBreakdown({
  trace,
  rows,
}: {
  trace: AgentRunTrace;
  rows: TokenBreakdownRow[];
}) {
  const totalTokens = trace.estimated_usage?.total ?? 0;
  const messageCount = trace.messages.length;
  const maxRowTokens = Math.max(...rows.map((row) => row.estimatedTokens), 1);
  const roleTotals = rows.reduce<Record<string, number>>((acc, row) => {
    acc[row.kind] = (acc[row.kind] ?? 0) + row.estimatedTokens;
    return acc;
  }, {});

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>~{formatCompactTokens(totalTokens)} est. tokens</span>
        <div className="flex flex-wrap items-center gap-3">
          {Object.entries(roleTotals).map(([role, tokens]) => (
            <span key={role} className="flex items-center gap-1.5">
              <span
                className={`inline-block size-2 rounded-full ${ROLE_STYLES[role as keyof typeof ROLE_STYLES].dot}`}
              />
              <span className="capitalize">{role}</span>
              <span>{Math.round((tokens / totalTokens) * 100) || 0}%</span>
            </span>
          ))}
        </div>
        <span>{messageCount} messages</span>
      </div>

      <div className="flex h-7 w-full overflow-hidden rounded-none border border-white/8 bg-white/[0.03] p-0.5">
        {rows.map((row) => (
          <button
            key={row.key}
            type="button"
            className={`min-w-[3px] transition-opacity hover:opacity-80 ${ROLE_STYLES[row.kind].segment}`}
            style={{
              width: `${Math.max((row.estimatedTokens / totalTokens) * 100, 0.4)}%`,
            }}
            title={`${row.label} • ${formatCompactTokens(row.estimatedTokens)} est`}
            onClick={() => scrollToMessage(row.anchorId)}
          />
        ))}
      </div>

      <Accordion type="single" collapsible>
        <AccordionItem
          value="token-breakdown"
          className="rounded-none border border-white/8 bg-white/[0.03]"
        >
          <AccordionTrigger className="px-4 py-3 text-sm text-muted-foreground hover:no-underline">
            <span className="truncate">
              {Object.entries(roleTotals)
                .sort(([, left], [, right]) => right - left)
                .map(
                  ([role, tokens]) =>
                    `${role} ${Math.round((tokens / totalTokens) * 100) || 0}%`,
                )
                .join(" · ")}
            </span>
          </AccordionTrigger>
          <AccordionContent className="space-y-1 px-4 pb-4">
            {rows.map((row, index) => (
              <button
                key={row.key}
                type="button"
                className="flex w-full items-center gap-3 rounded-none px-2 py-2 text-left hover:bg-white/4"
                onClick={() => scrollToMessage(row.anchorId)}
              >
                <span className="w-8 shrink-0 font-mono text-[11px] text-muted-foreground">
                  {row.kind === "system" ? "sys" : `#${index}`}
                </span>
                <span
                  className={`inline-block size-2 shrink-0 rounded-full ${ROLE_STYLES[row.kind].dot}`}
                />
                <span
                  className="min-w-0 w-56 shrink-0 truncate text-sm text-muted-foreground"
                  title={row.label}
                >
                  {row.label}
                </span>
                <div className="h-2 flex-1 overflow-hidden rounded-full bg-white/[0.08]">
                  <div
                    className={`h-full rounded-full ${ROLE_STYLES[row.kind].bar}`}
                    style={{
                      width: `${Math.max((row.estimatedTokens / maxRowTokens) * 100, 2)}%`,
                    }}
                  />
                </div>
                <span className="w-14 shrink-0 text-right text-xs text-muted-foreground">
                  {formatCompactTokens(row.estimatedTokens)}
                </span>
                <span className="w-12 shrink-0 text-right font-mono text-xs text-muted-foreground/60">
                  {row.durationMs !== undefined ? formatDuration(row.durationMs) : ""}
                </span>
              </button>
            ))}
          </AccordionContent>
        </AccordionItem>
      </Accordion>
    </div>
  );
}

function scrollToMessage(anchorId: string | null) {
  if (typeof window === "undefined" || anchorId === null) {
    return;
  }
  window.document
    .getElementById(anchorId)
    ?.scrollIntoView({ behavior: "smooth", block: "center" });
}

function extractToolOutputSummary(value: unknown): { preview?: string } | null {
  if (!value || typeof value !== "object") {
    return null;
  }
  if (!("spilled" in value) || !("preview" in value)) {
    return null;
  }
  return value as { preview?: string };
}

function formatCompactBytes(value: number) {
  if (value < 1024) {
    return `${value} B`;
  }
  if (value < 1024 * 1024) {
    const kb = value / 1024;
    return `${kb % 1 === 0 ? kb.toFixed(0) : kb.toFixed(1)} KB`;
  }
  const mb = value / (1024 * 1024);
  return `${mb % 1 === 0 ? mb.toFixed(0) : mb.toFixed(1)} MB`;
}
