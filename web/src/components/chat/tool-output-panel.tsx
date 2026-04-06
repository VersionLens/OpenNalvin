import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, type AgentRunToolOutputPage, type AgentRunToolOutputSearchResult, type ToolOutputRef } from "@/lib/api";
import { formatCompactTokens } from "@/lib/utils";

type ToolOutputPanelProps = {
  runId: string;
  outputRef: ToolOutputRef;
  preview?: string;
};

export function ToolOutputPanel({ runId, outputRef, preview }: ToolOutputPanelProps) {
  const [page, setPage] = useState<AgentRunToolOutputPage | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);
  const [pageLoading, setPageLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState<AgentRunToolOutputSearchResult | null>(null);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [searchLoading, setSearchLoading] = useState(false);

  async function loadPage(offset: number) {
    setPageLoading(true);
    setPageError(null);
    try {
      setPage(await api.getAgentRunToolOutput(runId, outputRef.output_id, { offset }));
    } catch (error) {
      setPageError(error instanceof Error ? error.message : "Failed to load stored output");
    } finally {
      setPageLoading(false);
    }
  }

  async function runSearch() {
    if (!query.trim()) return;
    setSearchLoading(true);
    setSearchError(null);
    try {
      setSearch(await api.searchAgentRunToolOutput(runId, outputRef.output_id, { pattern: query, literal_text: true }));
    } catch (error) {
      setSearchError(error instanceof Error ? error.message : "Failed to search stored output");
    } finally {
      setSearchLoading(false);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">Spilled</Badge>
        <Badge variant="outline">{formatCompactBytes(outputRef.size_bytes)}</Badge>
        <Badge variant="outline">{formatCompactTokens(outputRef.estimated_tokens)} total est</Badge>
        <Badge variant="outline">{outputRef.total_lines} lines</Badge>
        {outputRef.stored_truncated ? <Badge variant="destructive">Stored output truncated</Badge> : null}
      </div>

      {preview ? (
        <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Inline preview</Badge>
            <span className="font-mono text-[11px] text-muted-foreground">{outputRef.output_id}</span>
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">{preview}</pre>
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => loadPage(page?.offset ?? 0)} disabled={pageLoading}>
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

      {pageError ? <p className="text-xs text-destructive">{pageError}</p> : null}
      {page ? (
        <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Stored page</Badge>
            <Badge variant="outline">lines {page.start_line || 0}-{page.end_line}</Badge>
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
            {page.content || "(no content in this page)"}
          </pre>
        </div>
      ) : null}

      <div className="flex flex-col gap-2 sm:flex-row">
        <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search stored output" />
        <Button type="button" variant="outline" onClick={runSearch} disabled={searchLoading || !query.trim()}>
          Search
        </Button>
        {search ? (
          <Button type="button" variant="ghost" onClick={() => { setSearch(null); setSearchError(null); }}>
            Clear
          </Button>
        ) : null}
      </div>

      {searchError ? <p className="text-xs text-destructive">{searchError}</p> : null}
      {search ? (
        <div className="space-y-2 rounded-none border border-white/8 bg-black/20 p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">Search</Badge>
            <Badge variant="outline">{search.matches.length} matches</Badge>
            {search.truncated ? <Badge variant="outline">More available</Badge> : null}
          </div>
          <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-muted-foreground">
            {search.matches.length > 0
              ? search.matches.map((m) => `${m.line_number}: ${m.preview}`).join("\n")
              : "No matches"}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

function formatCompactBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) {
    const kb = value / 1024;
    return `${kb % 1 === 0 ? kb.toFixed(0) : kb.toFixed(1)} KB`;
  }
  const mb = value / (1024 * 1024);
  return `${mb % 1 === 0 ? mb.toFixed(0) : mb.toFixed(1)} MB`;
}
