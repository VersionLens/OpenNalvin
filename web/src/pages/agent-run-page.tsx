import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { AlertCircle, ChevronLeft, RefreshCw } from "lucide-react";

import { AgentRunChat } from "@/components/agent-run-chat";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import { formatCompactTokens, formatDate } from "@/lib/utils";
import { useDataCacheStore } from "@/store/data-cache-store";

export function AgentRunPage() {
  const { runId = "" } = useParams();
  const record = useDataCacheStore((state) => state.agentRunDetails[runId]);
  const setAgentRunDetail = useDataCacheStore((state) => state.setAgentRunDetail);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  async function load(force = false) {
    if (!force && record) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      setAgentRunDetail(runId, await api.getAgentRun(runId));
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : "Failed to load run");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, [runId]);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-3">
        <Button asChild variant="outline" size="sm">
          <Link to="/agent-runs">
            <ChevronLeft className="size-4" />
            Back to runs
          </Link>
        </Button>
        <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={loading}>
          <RefreshCw className="size-4" />
          Reload
        </Button>
        {record ? (
          <Button asChild size="sm">
            <Link to={`/agent/${record.id}`}>Continue in Agent</Link>
          </Button>
        ) : null}
      </div>

      {loading ? <AgentRunDetailSkeleton /> : null}

      {error ? (
        <Alert variant="destructive">
          <AlertCircle className="size-4" />
          <AlertTitle>Could not load run</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      {record ? (
        <>
          <Card>
            <CardHeader className="gap-4">
              <div className="space-y-1">
                <CardTitle>{record.title || "Untitled run"}</CardTitle>
                <CardDescription>{record.id}</CardDescription>
              </div>
              <div className="flex flex-wrap gap-2">
                <Badge variant="secondary">{record.status}</Badge>
                <Badge variant="outline">{record.model}</Badge>
                <Badge variant="outline">{record.run_kind}</Badge>
                {record.task_name ? <Badge variant="outline">{record.task_name}</Badge> : null}
                <Badge variant="outline">{record.message_count} msgs</Badge>
                <Badge variant="outline">{formatCompactTokens(record.total_tokens)} tokens</Badge>
                {record.trace.estimated_usage ? <Badge variant="outline">{formatCompactTokens(record.trace.estimated_usage.total)} est</Badge> : null}
                {record.trace.compactions?.length ? (
                  <Badge variant="outline" className="border-violet-500/40 text-violet-300">
                    {record.trace.compactions.length} compaction{record.trace.compactions.length === 1 ? "" : "s"}
                  </Badge>
                ) : null}
              </div>
            </CardHeader>
            <CardContent className="grid gap-4 text-sm text-muted-foreground sm:grid-cols-2 xl:grid-cols-4">
              <div>
                <p className="text-[11px] uppercase tracking-[0.16em]">Created</p>
                <p className="mt-1 text-foreground">{formatDate(record.created_at)}</p>
              </div>
              <div>
                <p className="text-[11px] uppercase tracking-[0.16em]">Updated</p>
                <p className="mt-1 text-foreground">{formatDate(record.updated_at)}</p>
              </div>
              <div>
                <p className="text-[11px] uppercase tracking-[0.16em]">Input tokens</p>
                <p className="mt-1 text-foreground">{formatCompactTokens(record.input_tokens)}</p>
              </div>
              <div>
                <p className="text-[11px] uppercase tracking-[0.16em]">Total tokens</p>
                <p className="mt-1 text-foreground">{formatCompactTokens(record.total_tokens)}</p>
              </div>
              <div>
                <p className="text-[11px] uppercase tracking-[0.16em]">Output tokens</p>
                <p className="mt-1 text-foreground">{formatCompactTokens(record.output_tokens)}</p>
              </div>
              {record.trace.estimated_usage ? (
                <>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">Estimated total</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.total)}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">System prompt</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.system_prompt)}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">User</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.user)}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">Assistant</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.assistant)}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">Reasoning</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.reasoning)}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">Tool calls</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.tool_calls)}</p>
                  </div>
                  <div>
                    <p className="text-[11px] uppercase tracking-[0.16em]">Tool results</p>
                    <p className="mt-1 text-foreground">{formatCompactTokens(record.trace.estimated_usage.tool_results)}</p>
                  </div>
                </>
              ) : null}
              {record.parent_run_id ? (
                <div>
                  <p className="text-[11px] uppercase tracking-[0.16em]">Parent run</p>
                  <p className="mt-1 text-foreground">
                    <Link to={`/agent-runs/${record.parent_run_id}`} className="hover:underline">
                      {record.parent_run_id}
                    </Link>
                  </p>
                </div>
              ) : null}
              {record.root_run_id && record.root_run_id !== record.id ? (
                <div>
                  <p className="text-[11px] uppercase tracking-[0.16em]">Root run</p>
                  <p className="mt-1 text-foreground">
                    <Link to={`/agent-runs/${record.root_run_id}`} className="hover:underline">
                      {record.root_run_id}
                    </Link>
                  </p>
                </div>
              ) : null}
            </CardContent>
          </Card>

          <AgentRunChat trace={record.trace} />
        </>
      ) : null}
    </div>
  );
}

function AgentRunDetailSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-36 w-full rounded-none" />
      <Skeleton className="h-64 w-full rounded-none" />
    </div>
  );
}
