import { useEffect } from "react";
import { AlertCircle, LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useToolConfigStore } from "@/store/tool-config-store";

export function AgentToolsPage() {
  const { tools, enabledToolIds, pinnedToolIds, warnings, loading, error, loadTools, toggleEnabled, togglePinned } =
    useToolConfigStore();

  useEffect(() => {
    void loadTools();
  }, [loadTools]);

  const enabledSet = new Set(enabledToolIds);
  const pinnedSet = new Set(pinnedToolIds);

  return (
    <div className="grid gap-6">
      <div className="space-y-3 rounded-none border border-white/8 bg-black/20 p-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p className="text-sm font-medium text-foreground">Tool selection</p>
            <p className="text-xs text-muted-foreground">
              Enable tools for agent runs and pin the ones that should be visible immediately.
            </p>
          </div>
          <Badge variant="outline">
            {enabledToolIds.length} enabled / {pinnedToolIds.length} pinned
          </Badge>
        </div>

        {error ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" />
            <AlertTitle>Could not load tools</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {loading ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <LoaderCircle className="size-4 animate-spin" />
            Loading tool catalog...
          </div>
        ) : null}

        {warnings.length > 0 ? (
          <Alert>
            <AlertCircle className="size-4" />
            <AlertTitle>Tool warnings</AlertTitle>
            <AlertDescription>{warnings.join(" ")}</AlertDescription>
          </Alert>
        ) : null}

        {tools.length > 0 ? (
          <div className="grid gap-3">
            {tools.map((tool) => (
              <div
                key={tool.id}
                className="flex flex-col gap-3 border border-white/8 bg-black/30 p-3 md:flex-row md:items-center md:justify-between"
              >
                <div className="space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs text-foreground">{tool.id}</span>
                    <Badge variant="outline">
                      {tool.server_name ? `${tool.source}:${tool.server_name}` : tool.source}
                    </Badge>
                  </div>
                  {tool.description ? (
                    <p className="text-xs text-muted-foreground">{tool.description}</p>
                  ) : null}
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant={enabledSet.has(tool.id) ? "secondary" : "outline"}
                    onClick={() => toggleEnabled(tool.id)}
                  >
                    {enabledSet.has(tool.id) ? "Enabled" : "Enable"}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant={pinnedSet.has(tool.id) ? "secondary" : "outline"}
                    onClick={() => togglePinned(tool.id)}
                    disabled={!enabledSet.has(tool.id)}
                  >
                    {pinnedSet.has(tool.id) ? "Pinned" : "Pin"}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}
