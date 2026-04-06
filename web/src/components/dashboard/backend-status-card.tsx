import { Activity, CircleAlert, PackageCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useAppStore } from "@/store/app-store";

export function BackendStatusCard() {
  const { health, meta, state, error, fetchBackendStatus, lastCheckedAt } = useAppStore();

  return (
    <Card className="overflow-hidden">
      <CardHeader className="border-b border-white/8">
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle>Backend status</CardTitle>
            <CardDescription>Live view of the Go server foundation and embedded frontend state.</CardDescription>
          </div>
          <Button variant="outline" size="sm" onClick={() => void fetchBackendStatus()}>
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent className="grid gap-4 pt-6">
        <div className="grid gap-3 sm:grid-cols-3">
          <StatusPill
            icon={Activity}
            label="Health"
            value={health?.status ?? (state === "loading" ? "Checking" : "Unknown")}
          />
          <StatusPill icon={PackageCheck} label="Environment" value={meta?.environment ?? "Pending"} />
          <StatusPill
            icon={meta?.frontend_embedded ? PackageCheck : CircleAlert}
            label="Embedded web"
            value={meta?.frontend_embedded ? "Ready" : "Dev mode"}
          />
        </div>

        <div className="grid gap-3 text-sm text-muted-foreground md:grid-cols-2">
          <InfoRow label="Version" value={meta?.version ?? "dev"} />
          <InfoRow label="Commit" value={meta?.commit ?? "none"} />
          <InfoRow label="Module" value={meta?.module ?? "Waiting for /api/meta"} />
          <InfoRow label="Checked" value={lastCheckedAt ? new Date(lastCheckedAt).toLocaleString() : "Not yet"} />
        </div>

        {error ? (
          <div className="rounded-none border border-red-500/20 bg-red-500/8 px-4 py-3 text-sm text-red-200">{error}</div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function StatusPill({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Activity;
  label: string;
  value: string;
}) {
  return (
    <div className="rounded-none border border-white/8 bg-black/30 p-4">
      <div className="mb-4 flex items-center gap-2 text-muted-foreground">
        <Icon className="size-4" />
        <span className="text-xs uppercase tracking-[0.22em]">{label}</span>
      </div>
      <p className="text-lg font-semibold text-foreground">{value}</p>
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-none border border-white/8 bg-black/20 px-4 py-3">
      <p className="text-xs uppercase tracking-[0.18em] text-muted-foreground">{label}</p>
      <p className="mt-2 break-all text-foreground">{value}</p>
    </div>
  );
}
