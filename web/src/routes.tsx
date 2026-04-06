import { Outlet } from "react-router-dom";
import type { RouteObject } from "react-router-dom";

import { AppShell } from "@/components/layout/app-shell";
import { ChatShell } from "@/components/layout/chat-shell";
import { AgentChatPage } from "@/pages/agent-chat-page";
import { AgentRunPage } from "@/pages/agent-run-page";
import { AgentRunsPage } from "@/pages/agent-runs-page";
import { AgentToolsPage } from "@/pages/agent-tools-page";
import { NotFoundPage } from "@/pages/not-found-page";
import { SettingsPage } from "@/pages/settings-page";

function ChatShellLayout() {
  return (
    <ChatShell>
      <Outlet />
    </ChatShell>
  );
}

function ShellLayout() {
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}

export const appRoutes: RouteObject[] = [
  {
    path: "/",
    element: <ChatShellLayout />,
    children: [
      {
        index: true,
        element: <AgentChatPage />,
      },
      {
        path: "agent",
        element: <AgentChatPage />,
      },
      {
        path: "agent/:runId",
        element: <AgentChatPage />,
      },
    ],
  },
  {
    path: "/",
    element: <ShellLayout />,
    children: [
      {
        path: "settings",
        element: <SettingsPage />,
      },
      {
        path: "agent-tools",
        element: <AgentToolsPage />,
      },
      {
        path: "agent-runs",
        element: <AgentRunsPage />,
      },
      {
        path: "agent-runs/:runId",
        element: <AgentRunPage />,
      },
      {
        path: "*",
        element: <NotFoundPage />,
      },
    ],
  },
];
