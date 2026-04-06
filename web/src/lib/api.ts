export type HealthResponse = {
  status: string;
};

export type MetaResponse = {
  name: string;
  module: string;
  environment: string;
  version: string;
  commit: string;
  date: string;
  frontend_embedded: boolean;
};

export type WorkspaceSummary = {
  name: string;
  current: boolean;
  db_path: string;
  files_path: string;
  db_exists: boolean;
  files_exists: boolean;
};

export type CurrentWorkspaceResponse = {
  name: string;
  persisted: string;
  db_path: string;
  files_path: string;
  db_exists: boolean;
  files_exists: boolean;
};

export type StoredToolFunction = {
  name?: string;
  arguments?: string;
};

export type StoredToolCall = {
  id?: string;
  type?: string;
  function?: StoredToolFunction;
  estimated_tokens?: number;
  started_at?: string;
};

export type ToolOutputRef = {
  output_id: string;
  tool_call_id?: string;
  size_bytes: number;
  estimated_tokens: number;
  total_lines: number;
  stored_truncated?: boolean;
  inline_truncated?: boolean;
};

export type PlanRef = {
  path?: string;
  source_run_id?: string;
  status?: string;
};

export type AgentRunMessage = {
  role: "user" | "assistant" | "tool";
  message_id?: string;
  content?: string;
  reasoning?: string;
  tool_calls?: StoredToolCall[];
  tool_call_id?: string;
  tool_name?: string;
  content_text?: string;
  content_json?: unknown;
  tool_output_ref?: ToolOutputRef;
  is_error?: boolean;
  ok?: boolean;
  estimated_tokens?: number;
  started_at?: string;
  ended_at?: string;
};

export type AgentRunToolOutputPage = {
  output_id: string;
  tool_call_id?: string;
  tool_name?: string;
  offset: number;
  limit: number;
  start_line: number;
  end_line: number;
  total_lines: number;
  size_bytes: number;
  estimated_tokens: number;
  stored_truncated?: boolean;
  inline_truncated?: boolean;
  content: string;
  truncated: boolean;
};

export type AgentRunToolOutputMatch = {
  line_number: number;
  preview: string;
};

export type AgentRunToolOutputSearchResult = {
  output_id: string;
  tool_call_id?: string;
  tool_name?: string;
  pattern: string;
  literal_text: boolean;
  limit: number;
  total_lines: number;
  size_bytes: number;
  estimated_tokens: number;
  stored_truncated?: boolean;
  inline_truncated?: boolean;
  matches: AgentRunToolOutputMatch[];
  truncated: boolean;
};

export type CompactionToolOutputRef = {
  output_id: string;
  tool_name: string;
  why_it_matters: string;
};

export type CompactionSummary = {
  goal: string;
  constraints?: string[];
  decisions?: string[];
  completed?: string[];
  open?: string[];
  files?: string[];
  tool_outputs?: CompactionToolOutputRef[];
};

export type StoredCompaction = {
  id: string;
  created_at: string;
  trigger: "proactive" | "reactive";
  covered_message_count: number;
  pre_tokens: number;
  post_tokens: number;
  summary: CompactionSummary;
};

export type AgentRunTrace = {
  schema_version: number;
  run_id: string;
  title: string;
  model: string;
  system_prompt?: string;
  metadata?: {
    parent_run_id?: string;
    root_run_id?: string;
    task_name?: string;
    run_kind?: string;
    mode?: string;
    plan_ref?: PlanRef;
    tools?: {
      enabled_ids: string[];
      pinned_ids: string[];
      revealed_ids: string[];
    };
  };
  created_at: string;
  updated_at: string;
  usage: {
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
  };
  estimated_usage?: {
    system_prompt: number;
    user: number;
    assistant: number;
    reasoning: number;
    tool_calls: number;
    tool_results: number;
    total: number;
  };
  messages: AgentRunMessage[];
  compactions?: StoredCompaction[];
};

export type AgentRunSummary = {
  id: string;
  title: string;
  model: string;
  prompt: string;
  parent_run_id?: string;
  root_run_id?: string;
  task_name?: string;
  run_kind: string;
  status: string;
  error?: string;
  duration_ms: number;
  message_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  created_at: string;
  updated_at: string;
};

export type AgentRunRecord = AgentRunSummary & {
  trace: AgentRunTrace;
};

export type ToolSchema = {
  $defs?: Record<string, unknown>;
  type: string;
  properties: Record<string, unknown>;
  required: string[];
  additional_properties?: unknown;
};

export type ToolDescriptor = {
  id: string;
  description?: string;
  keywords?: string[];
  source: string;
  server_name?: string;
  enabled: boolean;
  pinned: boolean;
  visible: boolean;
  default_enabled: boolean;
  default_pinned: boolean;
  schema: ToolSchema;
};

export type ToolCatalogResult = {
  tools: ToolDescriptor[];
  warnings?: string[];
};

export type AgentRunChunk = {
  id: string;
  object: string;
  created: number;
  model: string;
  choices?: Array<{
    index: number;
    delta?: {
      role?: string;
      content?: string;
      reasoning_content?: string;
      tool_calls?: StoredToolCall[];
    };
    finish_reason?: string | null;
  }>;
  error?: {
    message: string;
  };
};

type RequestBody = Record<string, unknown> | string | undefined;

async function request<T>(path: string, init: RequestInit = {}) {
  const response = await fetch(path, {
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      ...(init.headers ?? {}),
    },
    ...init,
  });

  const text = await response.text();
  const payload = text ? safeJsonParse<T>(text) : null;

  if (!response.ok) {
    const message =
      (payload &&
        typeof payload === "object" &&
        "message" in payload &&
        String(payload.message)) ||
      text ||
      response.statusText;
    throw new Error(message);
  }

  return payload;
}

function expectPayload<T>(payload: T | null, message: string) {
  if (payload === null) {
    throw new Error(message);
  }
  return payload;
}

function safeJsonParse<T>(value: string) {
  try {
    return JSON.parse(value) as T;
  } catch {
    return null;
  }
}

function serializeBody(body: RequestBody) {
  if (body === undefined) {
    return undefined;
  }
  return typeof body === "string" ? body : JSON.stringify(body);
}

export function streamAgentRun(
  params: {
    message: string;
    run_id?: string;
    title?: string;
    model?: string;
    system_prompt?: string;
    enabled_tool_ids?: string[];
    pinned_tool_ids?: string[];
  },
  onChunk: (chunk: AgentRunChunk) => void,
) {
  const controller = new AbortController();

  const done = (async () => {
    const response = await fetch("/v1/chat/completions", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        messages: [{ role: "user", content: params.message }],
        stream: true,
        run_id: params.run_id,
        title: params.title,
        model: params.model,
        system_prompt: params.system_prompt,
        enabled_tool_ids: params.enabled_tool_ids,
        pinned_tool_ids: params.pinned_tool_ids,
      }),
      signal: controller.signal,
    });

    if (!response.ok) {
      const text = await response.text();
      const parsed = safeJsonParse<{ message?: string }>(text);
      throw new Error(parsed?.message ?? text ?? response.statusText);
    }

    const reader = response.body?.getReader();
    if (!reader) {
      throw new Error("Streaming not supported by this browser");
    }

    const decoder = new TextDecoder();
    let buffer = "";

    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split("\n\n");
      buffer = parts.pop() ?? "";
      for (const part of parts) {
        const dataLines = part
          .split("\n")
          .filter((line) => line.startsWith("data: "))
          .map((line) => line.slice(6));
        for (const line of dataLines) {
          if (line === "[DONE]") {
            return;
          }
          const chunk = safeJsonParse<AgentRunChunk>(line);
          if (chunk) {
            onChunk(chunk);
          }
        }
      }
    }
  })();

  return {
    abort: () => controller.abort(),
    done,
  };
}

export const api = {
  getHealth() {
    return request<HealthResponse>("/api/health").then((payload) =>
      expectPayload(payload, "Missing health response"),
    );
  },
  getMeta() {
    return request<MetaResponse>("/api/meta").then((payload) =>
      expectPayload(payload, "Missing metadata response"),
    );
  },
  async listWorkspaces() {
    const payload = await request<{ workspaces?: WorkspaceSummary[] }>(
      "/api/workspaces",
    );
    return payload?.workspaces ?? [];
  },
  async getCurrentWorkspace() {
    const payload = await request<CurrentWorkspaceResponse>(
      "/api/workspace/current",
    );
    return expectPayload(payload, "Missing current workspace response");
  },
  async switchWorkspace(name: string) {
    const payload = await request<CurrentWorkspaceResponse>(
      "/api/workspace/switch",
      {
        method: "POST",
        body: serializeBody({ name }),
      },
    );
    return expectPayload(payload, "Missing switched workspace response");
  },
  async listAgentRuns(status?: string) {
    const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
    const payload = await request<{ runs?: AgentRunSummary[] }>(
      `/api/agent-runs${suffix}`,
    );
    return payload?.runs ?? [];
  },
  async getAgentRun(runId: string) {
    const payload = await request<AgentRunRecord>(`/api/agent-runs/${runId}`);
    return expectPayload(payload, `Missing run payload for ${runId}`);
  },
  async getAgentRunToolOutput(
    runId: string,
    outputId: string,
    params: { offset?: number; limit?: number } = {},
  ) {
    const query = new URLSearchParams();
    if (params.offset !== undefined) {
      query.set("offset", String(params.offset));
    }
    if (params.limit !== undefined) {
      query.set("limit", String(params.limit));
    }
    const suffix = query.size > 0 ? `?${query.toString()}` : "";
    const payload = await request<AgentRunToolOutputPage>(
      `/api/agent-runs/${runId}/tool-outputs/${outputId}${suffix}`,
    );
    return expectPayload(
      payload,
      `Missing tool output payload for ${outputId}`,
    );
  },
  async searchAgentRunToolOutput(
    runId: string,
    outputId: string,
    params: { pattern: string; literal_text?: boolean; limit?: number },
  ) {
    const query = new URLSearchParams();
    query.set("pattern", params.pattern);
    if (params.literal_text !== undefined) {
      query.set("literal_text", String(params.literal_text));
    }
    if (params.limit !== undefined) {
      query.set("limit", String(params.limit));
    }
    const payload = await request<AgentRunToolOutputSearchResult>(
      `/api/agent-runs/${runId}/tool-outputs/${outputId}/grep?${query.toString()}`,
    );
    return expectPayload(
      payload,
      `Missing tool output search payload for ${outputId}`,
    );
  },
  async listAgentTools(runId?: string) {
    const suffix = runId ? `?run_id=${encodeURIComponent(runId)}` : "";
    const payload = await request<ToolCatalogResult>(
      `/api/agent-tools${suffix}`,
    );
    return expectPayload(payload, "Missing agent tools payload");
  },
  async implementPlan(runId: string, body: Record<string, unknown> = {}) {
    const payload = await request<{ run?: AgentRunRecord }>(
      `/api/agent-runs/${runId}/implement-plan`,
      {
        method: "POST",
        body: serializeBody(body),
      },
    );
    return expectPayload(payload?.run ?? null, `Missing implementation run for ${runId}`);
  },
};

export function getHealth() {
  return api.getHealth();
}

export function getMeta() {
  return api.getMeta();
}
