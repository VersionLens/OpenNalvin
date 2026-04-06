import { useEffect } from "react";
import { AlertCircle, RefreshCw } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useDataCacheStore } from "@/store/data-cache-store";
import { useWorkspaceStore } from "@/store/workspace-store";

export function WorkspaceSwitcher() {
  const workspaces = useDataCacheStore((state) => state.workspaces);
  const currentWorkspace = useDataCacheStore((state) => state.currentWorkspace);
  const { load, switchWorkspace, loading, switching, error } = useWorkspaceStore();

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="space-y-3 rounded-none border border-white/8 bg-black/20 p-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <p className="text-xs uppercase tracking-[0.22em] text-muted-foreground">Workspace</p>
          <p className="mt-1 text-sm font-medium">{currentWorkspace?.name ?? "Loading workspace..."}</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-9 rounded-none border border-white/10 bg-white/[0.03] hover:bg-white/[0.08]"
          onClick={() => void load(true)}
          disabled={loading || switching}
        >
          <RefreshCw className={loading ? "size-4 animate-spin" : "size-4"} />
        </Button>
      </div>

      <Select
        value={currentWorkspace?.name ?? ""}
        onValueChange={(value: string) => void switchWorkspace(value)}
        disabled={loading || switching || !workspaces?.length}
      >
        <SelectTrigger className="h-11 rounded-none border-white/10 bg-white/[0.03]">
          <SelectValue placeholder="Select workspace" />
        </SelectTrigger>
        <SelectContent>
          {(workspaces ?? []).map((workspace) => (
            <SelectItem key={workspace.name} value={workspace.name}>
              {workspace.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <p className="text-xs text-muted-foreground">
        {currentWorkspace?.db_exists ? "Workspace database is ready." : "Workspace database will be created on first use."}
      </p>

      {error ? (
        <Alert variant="destructive" className="rounded-none">
          <AlertCircle className="size-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
