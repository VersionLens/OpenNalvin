import { Bot, Rows3, Sparkles, Wrench } from "lucide-react";
import { NavLink } from "react-router-dom";

import { WorkspaceSwitcher } from "@/components/workspace-switcher";
import { cn } from "@/lib/utils";

const navItems = [
  { to: "/agent", label: "Agent", icon: Bot },
  { to: "/agent-runs", label: "Runs", icon: Rows3 },
  { to: "/agent-tools", label: "Tools", icon: Wrench },
];

type ChatShellProps = {
  children: React.ReactNode;
};

export function ChatShell({ children }: ChatShellProps) {
  return (
    <div className="grain text-foreground">
      <div className="flex h-dvh w-full overflow-hidden bg-[#0a0a0a] lg:grid lg:grid-cols-[280px_1fr]">
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

        <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
          {children}
        </div>
      </div>
    </div>
  );
}
