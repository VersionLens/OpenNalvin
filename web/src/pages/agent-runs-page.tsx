import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { AlertCircle, RefreshCw } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import { formatCompactTokens, formatDate } from "@/lib/utils";
import { useDataCacheStore } from "@/store/data-cache-store";

export function AgentRunsPage() {
  const navigate = useNavigate();
  const runs = useDataCacheStore((state) => state.agentRuns);
  const setAgentRuns = useDataCacheStore((state) => state.setAgentRuns);
  const currentWorkspace = useDataCacheStore((state) => state.currentWorkspace);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  async function load(force = false) {
    if (!force && runs !== null) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      setAgentRuns(await api.listAgentRuns());
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : "Failed to load agent runs");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, [currentWorkspace?.name]);

  return (
    <Card>
      <CardHeader className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1">
          <CardTitle>Agent Runs</CardTitle>
          <CardDescription>
            Browse every saved agent run in <span className="text-foreground">{currentWorkspace?.name ?? "the active workspace"}</span>.
          </CardDescription>
        </div>
        <div className="flex gap-2">
          <Button asChild variant="secondary" size="sm">
            <Link to="/agent">New chat</Link>
          </Button>
          <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={loading}>
            <RefreshCw className="size-4" />
            Reload
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" />
            <AlertTitle>Could not load agent runs</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {loading ? <AgentRunsSkeleton /> : null}

        {!loading && (runs ?? []).length === 0 ? (
          <div className="rounded-none border border-dashed border-white/10 p-8 text-center text-sm text-muted-foreground">
            No agent runs have been saved in this workspace yet.
          </div>
        ) : null}

        {!loading && (runs ?? []).length > 0 ? (
          <div className="rounded-none border border-white/8 bg-black/20">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Run</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Usage</TableHead>
                  <TableHead>Updated</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(runs ?? []).map((run) => (
                  <TableRow key={run.id} className="cursor-pointer hover:bg-white/4" onClick={() => navigate(`/agent-runs/${run.id}`)}>
                    <TableCell>
                      <div className="space-y-1">
                        <Link
                          to={`/agent-runs/${run.id}`}
                          className="font-medium hover:underline"
                          onClick={(event) => event.stopPropagation()}
                        >
                          {run.title || "Untitled run"}
                        </Link>
                        <p className="max-w-xl text-xs text-muted-foreground">{run.prompt}</p>
                        <div className="flex flex-wrap gap-2 text-[11px] text-muted-foreground">
                          <span>{run.run_kind}</span>
                          {run.task_name ? <span>{run.task_name}</span> : null}
                          {run.parent_run_id ? <span>parent: {run.parent_run_id}</span> : null}
                        </div>
                        <p className="font-mono text-[11px] text-muted-foreground">{run.id}</p>
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-2">
                        <Badge variant="secondary">{run.status}</Badge>
                        <Badge variant="outline">{run.model}</Badge>
                      </div>
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {run.message_count} msgs • {formatCompactTokens(run.total_tokens)} tokens
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">{formatDate(run.updated_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function AgentRunsSkeleton() {
  return (
    <div className="space-y-3">
      {Array.from({ length: 4 }).map((_, index) => (
        <div key={index} className="rounded-none border border-white/8 p-4">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="mt-3 h-4 w-72" />
          <Skeleton className="mt-3 h-4 w-28" />
        </div>
      ))}
    </div>
  );
}
