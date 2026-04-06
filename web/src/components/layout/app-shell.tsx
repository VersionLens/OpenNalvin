import { Bot, Rows3, Sparkles, Wrench } from "lucide-react";
import { NavLink, useLocation } from "react-router-dom";

import { WorkspaceSwitcher } from "@/components/workspace-switcher";
import { cn } from "@/lib/utils";

const navItems = [
  { to: "/agent", label: "Agent", icon: Bot },
  { to: "/agent-runs", label: "Runs", icon: Rows3 },
  { to: "/agent-tools", label: "Tools", icon: Wrench },
];

type AppShellProps = {
  children: React.ReactNode;
};

export function AppShell({ children }: AppShellProps) {
  const location = useLocation();
  const page = resolvePage(location.pathname);

  return (
    <div className="grain min-h-screen text-foreground">
      <div className="flex min-h-screen w-full flex-col overflow-hidden bg-[#0a0a0a] lg:grid lg:grid-cols-[280px_1fr]">
        <aside className="flex flex-col gap-5 border-b border-white/8 bg-[#171717] px-4 py-5 sm:px-5 lg:border-b-0 lg:border-r lg:border-white/8">
          <div className="flex items-center gap-3 px-2">
            <div className="flex size-11 items-center justify-center rounded-none border border-white/10 bg-white/[0.05] shadow-inner shadow-white/5">
              <Sparkles className="size-5" />
            </div>
            <div>
              <p className="text-lg font-semibold tracking-tight">nalvin</p>
              <p className="text-xs uppercase tracking-[0.22em] text-muted-foreground">Workspace</p>
            </div>
          </div>

          <WorkspaceSwitcher />

          <nav className="rounded-none border border-white/8 bg-black/20 p-2">
            <p className="px-3 pb-2 text-[11px] uppercase tracking-[0.22em] text-muted-foreground">Navigate</p>
            <div className="grid gap-1">
              {navItems.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-3 rounded-none px-3 py-3 text-sm transition",
                      isActive
                        ? "bg-white text-black shadow-[0_12px_32px_rgb(255_255_255_/_0.08)]"
                        : "text-white/72 hover:bg-white/[0.05] hover:text-white",
                    )
                  }
                >
                  <item.icon className="size-4" />
                  {item.label}
                </NavLink>
              ))}
            </div>
          </nav>
        </aside>

        <div className="flex min-w-0 flex-1 flex-col">
          <header className="border-b border-white/8 px-5 py-5 sm:px-6">
            <p className="text-[11px] uppercase tracking-[0.24em] text-muted-foreground">{page.eyebrow}</p>
            <h1 className="mt-3 text-2xl font-semibold tracking-tight sm:text-[2rem]">{page.title}</h1>
            <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">{page.description}</p>
          </header>

          <main className="min-w-0 flex-1 px-4 py-4 sm:px-6 sm:py-6">{children}</main>
        </div>
      </div>
    </div>
  );
}

function resolvePage(pathname: string) {
  if (pathname.startsWith("/agent-runs")) {
    return {
      eyebrow: "Runs",
      title: "Saved agent runs",
      description: "Browse the conversations stored in the current workspace and reopen any run when you need more context.",
    };
  }

  if (pathname.startsWith("/settings")) {
    return {
      eyebrow: "Settings",
      title: "Workspace settings",
      description: "Configuration and environment controls for this workspace.",
    };
  }

  if (pathname.startsWith("/agent-tools")) {
    return {
      eyebrow: "Tools",
      title: "Tool configuration",
      description: "Enable tools for agent runs and pin the ones that should always be visible.",
    };
  }

  return {
    eyebrow: "Agent",
    title: "Agent workspace",
    description: "Start a new run or continue an existing conversation in the active workspace.",
  };
}
