import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";

import { appRoutes } from "@/routes";
import { resetDataCacheStore, useDataCacheStore } from "@/store/data-cache-store";

describe("app routes", () => {
  beforeEach(() => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);

      if (url.endsWith("/api/workspaces")) {
        return new Response(
          JSON.stringify({
            workspaces: [
              {
                name: "default",
                current: true,
                db_path: "/tmp/default.db",
                files_path: "/tmp/default",
                db_exists: true,
                files_exists: true,
              },
            ],
          }),
          { status: 200 },
        );
      }

      if (url.endsWith("/api/workspace/current")) {
        return new Response(
          JSON.stringify({
            name: "default",
            persisted: "default",
            db_path: "/tmp/default.db",
            files_path: "/tmp/default",
            db_exists: true,
            files_exists: true,
          }),
          { status: 200 },
        );
      }

      if (url.includes("/api/agent-runs")) {
        return new Response(JSON.stringify({ runs: [] }), { status: 200 });
      }

      return new Response("not found", { status: 404 });
    });

    vi.stubGlobal("fetch", fetchMock);
    resetDataCacheStore();
    useDataCacheStore.setState({
      workspaces: [
        {
          name: "default",
          current: true,
          db_path: "/tmp/default.db",
          files_path: "/tmp/default",
          db_exists: true,
          files_exists: true,
        },
      ],
      currentWorkspace: {
        name: "default",
        persisted: "default",
        db_path: "/tmp/default.db",
        files_path: "/tmp/default",
        db_exists: true,
        files_exists: true,
      },
    });
  });

  afterEach(() => {
    resetDataCacheStore();
    vi.unstubAllGlobals();
  });

  it("renders the root route as the agent chat", async () => {
    const router = createMemoryRouter(appRoutes, {
      initialEntries: ["/"],
    });

    render(<RouterProvider router={router} />);

    expect(await screen.findByPlaceholderText(/ask the agent something/i)).toBeInTheDocument();
  });

  it("renders the settings page", async () => {
    const router = createMemoryRouter(appRoutes, {
      initialEntries: ["/settings"],
    });

    render(<RouterProvider router={router} />);

    expect(await screen.findByText(/placeholder space for config editing/i)).toBeInTheDocument();
  });

  it("renders the not found state", async () => {
    const router = createMemoryRouter(appRoutes, {
      initialEntries: ["/missing"],
    });

    render(<RouterProvider router={router} />);

    expect(await screen.findByText("404")).toBeInTheDocument();
  });
});
