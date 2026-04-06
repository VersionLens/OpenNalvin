import { create } from "zustand";
import { api, type ToolDescriptor } from "@/lib/api";

type ToolConfigState = {
  tools: ToolDescriptor[];
  enabledToolIds: string[];
  pinnedToolIds: string[];
  baselineEnabledToolIds: string[];
  baselinePinnedToolIds: string[];
  customizedScopeKey: string | null;
  hasCustomizations: boolean;
  warnings: string[];
  loading: boolean;
  error: string | null;
  loadTools: (runId?: string) => Promise<void>;
  toggleEnabled: (toolId: string) => void;
  togglePinned: (toolId: string) => void;
};

export const useToolConfigStore = create<ToolConfigState>((set, get) => ({
  tools: [],
  enabledToolIds: [],
  pinnedToolIds: [],
  baselineEnabledToolIds: [],
  baselinePinnedToolIds: [],
  customizedScopeKey: null,
  hasCustomizations: false,
  warnings: [],
  loading: false,
  error: null,

  loadTools: async (runId?: string) => {
    const scopeKey = runId ?? "__default__";
    set({ loading: true, error: null });
    try {
      const catalog = await api.listAgentTools(runId);
      const defaultEnabledToolIds = catalog.tools.filter((t) => t.enabled).map((t) => t.id);
      const defaultPinnedToolIds = catalog.tools.filter((t) => t.pinned).map((t) => t.id);
      const validToolIds = new Set(catalog.tools.map((t) => t.id));
      const {
        enabledToolIds,
        pinnedToolIds,
        customizedScopeKey,
        hasCustomizations,
      } = get();
      const preserveCustomizations = hasCustomizations && customizedScopeKey === scopeKey;
      const nextEnabledToolIds = preserveCustomizations
        ? enabledToolIds.filter((id) => validToolIds.has(id))
        : defaultEnabledToolIds;
      const nextPinnedToolIds = preserveCustomizations
        ? pinnedToolIds.filter((id) => validToolIds.has(id) && nextEnabledToolIds.includes(id))
        : defaultPinnedToolIds;
      set({
        tools: catalog.tools,
        enabledToolIds: nextEnabledToolIds,
        pinnedToolIds: nextPinnedToolIds,
        baselineEnabledToolIds: defaultEnabledToolIds,
        baselinePinnedToolIds: defaultPinnedToolIds,
        customizedScopeKey: scopeKey,
        hasCustomizations:
          preserveCustomizations &&
          (nextEnabledToolIds.join("\n") !== defaultEnabledToolIds.join("\n") ||
            nextPinnedToolIds.join("\n") !== defaultPinnedToolIds.join("\n")),
        warnings: catalog.warnings ?? [],
        loading: false,
      });
    } catch (error) {
      set({
        loading: false,
        error: error instanceof Error ? error.message : "Failed to load agent tools",
      });
    }
  },

  toggleEnabled: (toolId: string) => {
    const { enabledToolIds, pinnedToolIds, baselineEnabledToolIds, baselinePinnedToolIds, customizedScopeKey } = get();
    let nextEnabledToolIds: string[];
    let nextPinnedToolIds: string[];
    if (enabledToolIds.includes(toolId)) {
      nextEnabledToolIds = enabledToolIds.filter((id) => id !== toolId);
      nextPinnedToolIds = pinnedToolIds.filter((id) => id !== toolId);
    } else {
      nextEnabledToolIds = [...enabledToolIds, toolId].sort();
      nextPinnedToolIds = pinnedToolIds;
    }
    set({
      enabledToolIds: nextEnabledToolIds,
      pinnedToolIds: nextPinnedToolIds,
      customizedScopeKey: customizedScopeKey ?? "__default__",
      hasCustomizations:
        nextEnabledToolIds.join("\n") !== baselineEnabledToolIds.join("\n") ||
        nextPinnedToolIds.join("\n") !== baselinePinnedToolIds.join("\n"),
    });
  },

  togglePinned: (toolId: string) => {
    const { enabledToolIds, pinnedToolIds, baselineEnabledToolIds, baselinePinnedToolIds, customizedScopeKey } = get();
    if (!enabledToolIds.includes(toolId)) return;
    const nextPinnedToolIds = pinnedToolIds.includes(toolId)
      ? pinnedToolIds.filter((id) => id !== toolId)
      : [...pinnedToolIds, toolId].sort();
    set({
      pinnedToolIds: nextPinnedToolIds,
      customizedScopeKey: customizedScopeKey ?? "__default__",
      hasCustomizations:
        enabledToolIds.join("\n") !== baselineEnabledToolIds.join("\n") ||
        nextPinnedToolIds.join("\n") !== baselinePinnedToolIds.join("\n"),
    });
  },
}));
