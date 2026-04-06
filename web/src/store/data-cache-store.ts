import { create } from "zustand";

import type { AgentRunRecord, AgentRunSummary, CurrentWorkspaceResponse, WorkspaceSummary } from "@/lib/api";

type DataCacheState = {
  workspaces: WorkspaceSummary[] | null;
  currentWorkspace: CurrentWorkspaceResponse | null;
  agentRuns: AgentRunSummary[] | null;
  agentRunDetails: Record<string, AgentRunRecord | undefined>;
  setWorkspaces: (workspaces: WorkspaceSummary[]) => void;
  setCurrentWorkspace: (workspace: CurrentWorkspaceResponse | null) => void;
  setAgentRuns: (runs: AgentRunSummary[]) => void;
  setAgentRunDetail: (runId: string, record: AgentRunRecord) => void;
  resetRunData: () => void;
};

export const useDataCacheStore = create<DataCacheState>((set) => ({
  workspaces: null,
  currentWorkspace: null,
  agentRuns: null,
  agentRunDetails: {},
  setWorkspaces: (workspaces) => set({ workspaces }),
  setCurrentWorkspace: (currentWorkspace) => set({ currentWorkspace }),
  setAgentRuns: (agentRuns) => set({ agentRuns }),
  setAgentRunDetail: (runId, record) =>
    set((state) => ({
      agentRunDetails: {
        ...state.agentRunDetails,
        [runId]: record,
      },
    })),
  resetRunData: () => set({ agentRuns: null, agentRunDetails: {} }),
}));

export function resetDataCacheStore() {
  useDataCacheStore.setState({
    workspaces: null,
    currentWorkspace: null,
    agentRuns: null,
    agentRunDetails: {},
  });
}
