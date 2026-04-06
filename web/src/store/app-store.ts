import { create } from "zustand";

import { getHealth, getMeta, type HealthResponse, type MetaResponse } from "@/lib/api";

type BackendStatus = {
  health: HealthResponse | null;
  meta: MetaResponse | null;
  state: "idle" | "loading" | "ready" | "error";
  error: string | null;
  lastCheckedAt: string | null;
  fetchBackendStatus: () => Promise<void>;
};

const initialState = {
  health: null,
  meta: null,
  state: "idle" as const,
  error: null,
  lastCheckedAt: null,
};

export const useAppStore = create<BackendStatus>((set) => ({
  ...initialState,
  async fetchBackendStatus() {
    set({ state: "loading", error: null });

    try {
      const [health, meta] = await Promise.all([getHealth(), getMeta()]);
      set({
        health,
        meta,
        state: "ready",
        error: null,
        lastCheckedAt: new Date().toISOString(),
      });
    } catch (error) {
      set({
        state: "error",
        error: error instanceof Error ? error.message : "Unknown error",
      });
    }
  },
}));

export function resetAppStore() {
  useAppStore.setState(initialState);
}

