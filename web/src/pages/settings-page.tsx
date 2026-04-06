import { FolderCog, ShieldCheck, Workflow } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";

export function SettingsPage() {
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_320px]">
      <Card>
        <CardHeader>
          <CardTitle>Workspace settings</CardTitle>
          <CardDescription>
            Placeholder space for config editing, runtime toggles, profiles, and feature flags as the project grows.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <SettingRow
            icon={FolderCog}
            title="Config source"
            description="`~/.config/nalvin/config.yaml`, `.env`, environment variables, and flags are already wired into the backend foundation."
          />
          <Separator />
          <SettingRow
            icon={Workflow}
            title="App modes"
            description="The first cut exposes `app.env`, `server.addr`, and `server.allowed_origins`. This page can later grow into a proper settings editor."
          />
          <Separator />
          <SettingRow
            icon={ShieldCheck}
            title="Production readiness"
            description="The Go binary can serve the built SPA once `bun run build:web` is followed by a fresh `go build`."
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Next additions</CardTitle>
          <CardDescription>Good early candidates for the next pairing session.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm text-muted-foreground">
          <p>Structured CLI subcommands for real workflows.</p>
          <p>Feature-scoped API modules under `/api`.</p>
          <p>Persisted user preferences and theme controls.</p>
          <p>Data tables, toasts, command bar, and auth if needed.</p>
        </CardContent>
      </Card>
    </div>
  );
}

function SettingRow({
  icon: Icon,
  title,
  description,
}: {
  icon: typeof FolderCog;
  title: string;
  description: string;
}) {
  return (
    <div className="flex gap-4">
      <div className="flex size-11 shrink-0 items-center justify-center rounded-none border border-white/10 bg-white/6">
        <Icon className="size-5" />
      </div>
      <div className="space-y-1">
        <h3 className="font-medium text-foreground">{title}</h3>
        <p className="text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}
