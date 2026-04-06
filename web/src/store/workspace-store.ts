import { create } from "zustand";

import { api, type CurrentWorkspaceResponse, type WorkspaceSummary } from "@/lib/api";
import { useDataCacheStore } from "@/store/data-cache-store";

type WorkspaceState = {
  loading: boolean;
  switching: boolean;
  error: string | null;
  load: (force?: boolean) => Promise<void>;
  switchWorkspace: (name: string) => Promise<void>;
};

export const useWorkspaceStore = create<WorkspaceState>((set) => ({
  loading: false,
  switching: false,
  error: null,
  async load(force = false) {
    const cache = useDataCacheStore.getState();
    if (!force && cache.workspaces !== null && cache.currentWorkspace !== null) {
      return;
    }

    set({ loading: true, error: null });
    try {
      const [workspaces, currentWorkspace] = await Promise.all([api.listWorkspaces(), api.getCurrentWorkspace()]);
      useDataCacheStore.getState().setWorkspaces(workspaces);
      useDataCacheStore.getState().setCurrentWorkspace(currentWorkspace);
      set({ loading: false });
    } catch (error) {
      set({
        loading: false,
        error: error instanceof Error ? error.message : "Failed to load workspaces",
      });
    }
  },
  async switchWorkspace(name) {
    set({ switching: true, error: null });
    try {
      const currentWorkspace = await api.switchWorkspace(name);
      const workspaces = await api.listWorkspaces();
      useDataCacheStore.getState().setCurrentWorkspace(currentWorkspace);
      useDataCacheStore.getState().setWorkspaces(workspaces);
      useDataCacheStore.getState().resetRunData();
      set({ switching: false });
    } catch (error) {
      set({
        switching: false,
        error: error instanceof Error ? error.message : "Failed to switch workspace",
      });
    }
  },
}));
