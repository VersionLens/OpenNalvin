import { resetAppStore, useAppStore } from "@/store/app-store";

describe("app store", () => {
  afterEach(() => {
    resetAppStore();
    vi.unstubAllGlobals();
  });

  it("hydrates backend status from the API", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/api/health")) {
        return new Response(JSON.stringify({ status: "ok" }), { status: 200 });
      }
      if (url.endsWith("/api/meta")) {
        return new Response(
          JSON.stringify({
            name: "nalvin",
            module: "github.com/versionlens/OpenNalvin",
            environment: "test",
            version: "dev",
            commit: "none",
            date: "unknown",
            frontend_embedded: false,
          }),
          { status: 200 },
        );
      }

      return new Response("not found", { status: 404 });
    });

    vi.stubGlobal("fetch", fetchMock);

    await useAppStore.getState().fetchBackendStatus();

    const state = useAppStore.getState();
    expect(state.state).toBe("ready");
    expect(state.health?.status).toBe("ok");
    expect(state.meta?.environment).toBe("test");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
