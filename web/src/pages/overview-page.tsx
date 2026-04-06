import { ArrowUpRight, Layers3, Radar, Server } from "lucide-react";

import { BackendStatusCard } from "@/components/dashboard/backend-status-card";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

const tracks = [
  {
    icon: Server,
    title: "CLI + API foundation",
    description: "Cobra, Viper, Chi, and embedded SPA delivery are in place for the next layer of functionality.",
  },
  {
    icon: Layers3,
    title: "Composable front-end shell",
    description: "A Bun/Vite app with React Router, Zustand, and shadcn primitives ready to absorb real product flows.",
  },
  {
    icon: Radar,
    title: "Intentional dark system",
    description: "Neutral black tokens, glass surfaces, and compact cards tuned for tool-heavy dashboards instead of landing pages.",
  },
];

export function OverviewPage() {
  return (
    <div className="grid gap-6">
      <section className="grid gap-4 xl:grid-cols-[1.35fr_0.95fr]">
        <Card className="overflow-hidden bg-gradient-to-br from-white/[0.08] via-white/[0.03] to-transparent">
          <CardHeader className="gap-4">
            <div className="inline-flex w-fit items-center rounded-none border border-white/10 bg-white/8 px-3 py-1 text-xs uppercase tracking-[0.24em] text-muted-foreground">
              Foundation layer
            </div>
            <div className="max-w-2xl space-y-3">
              <CardTitle className="text-4xl leading-tight sm:text-5xl">
                A black-first workspace for the next evolution of <span className="text-white/70">nalvin</span>.
              </CardTitle>
              <CardDescription className="max-w-xl text-base leading-7 text-white/72">
                This starter intentionally begins as an app shell, not a marketing page. The goal is to give us a strong surface for CLI features, HTTP modules, and richer workflows without repainting the foundation later.
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-3">
            <Button size="lg">
              Explore routes
              <ArrowUpRight className="size-4" />
            </Button>
            <Button variant="ghost" size="lg">
              Extend the API
            </Button>
          </CardContent>
        </Card>

        <BackendStatusCard />
      </section>

      <section className="grid gap-4 lg:grid-cols-3">
        {tracks.map(({ icon: Icon, title, description }) => (
          <Card key={title}>
            <CardHeader>
              <div className="flex size-12 items-center justify-center rounded-none border border-white/10 bg-white/6">
                <Icon className="size-5" />
              </div>
              <CardTitle>{title}</CardTitle>
              <CardDescription>{description}</CardDescription>
            </CardHeader>
          </Card>
        ))}
      </section>
    </div>
  );
}
