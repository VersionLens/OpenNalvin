# Finetuning Plan: Qwen 3.5 for Agent Behavior

**Goal:** Finetune [Qwen3.5-9B](https://huggingface.co/Qwen/Qwen3.5-9B) and [Qwen3.5-4B](https://huggingface.co/Qwen/Qwen3.5-4B) to match or exceed Claude Opus 4.6 on agentic tool-calling tasks — specifically the orchestration, spillover handling, parallel calling, and subagent management patterns that define high-quality agent runs.

**Target models:** Both are dense models with a hybrid architecture (Gated DeltaNet + grouped-query attention every 4th layer). Despite being 15-70x smaller than Opus, the behavioral gaps identified below are primarily about learned patterns, not raw capability — making them strong finetuning targets.

**Strategy:** Use Opus 4.6 as the gold-standard trace generator and GLM 5.1 as the synthetic data workhorse (cheaper, fast, good enough tool-calling behavior to generate diverse scenarios).

---

## 1. Behavioral Gaps to Close

From the [three-model comparison](integration-test-glm5.1-vs-qwen3.5-35b-vs-opus4.6.md), Qwen 3.5 has five distinct behavioral failures, ranked by impact:

### Gap 1: Never Uses Spillover Tools (Critical)

**What happens:** When a tool result is inline-truncated, the system returns a JSON envelope with `inline_truncated: true`, `output_id: "<uuid>"`, and an explicit message: *"Full output stored separately. Use view_tool_output or grep_tool_output with the output_id to inspect it."* Qwen ignores this entirely — across 4 subagents and ~20 truncated results, it never once called `view_tool_output` or `grep_tool_output`.

**Instead it does:**
- Retries the identical tool call (sentry_scout x3, slack_scout x3)
- Searches for alternative queries to avoid the truncation
- Moves on without the data

**Why this matters:** The spillover system is load-bearing. Many MCP tools return large results (5K-40K tokens). Without reading them, the model operates on incomplete data and wastes calls on retries.

**Target behavior (from Opus):**
1. Detect `inline_truncated: true` + `output_id` in tool result
2. If the data matters, call `grep_tool_output(output_id, pattern)` first (efficient for known patterns)
3. If grep returns 0 or the content is unstructured, fall back to `view_tool_output(output_id, offset, limit)`
4. Never retry an identical call just because the result was truncated

### Gap 2: No Parallel Tool Calling (High)

**What happens:** Qwen issues every tool call sequentially, one per assistant turn. Across the entire run (root + 4 subagents, 73 tool calls total), exactly zero were parallel.

**Where parallelism should occur:**
- `wait_agent` calls (all 4 should be in one batch)
- Independent `search_tools` calls during discovery
- Independent data-fetching calls within subagents (e.g., `slack_search` x2 for different queries)
- `list_comments` across independent issues
- `kb_create_edge` calls that don't depend on each other

**Target behavior (from Opus):** Issue multiple `tool_calls` in a single assistant message when the calls are independent. Opus parallelized 14 of its 31 root-level calls (45%) and used heavy parallelism within subagents (linear_scout comment checks in pairs).

### Gap 3: Off-Task Subagent Behavior (High)

**What happens:** The github_scout searched for open bugs in facebook/react, pytorch/pytorch, rails/rails, and tensorflow/tensorflow instead of focusing on VersionLens repos. The task said "search recent open issues across repos mentioning 'bug' (limit 5)" — Qwen interpreted "across repos" as "across all of GitHub."

**Why this matters:** Subagents operate with minimal supervision. An off-task subagent wastes its entire budget and produces garbage that the root agent may uncritically incorporate into KB nodes.

**Target behavior:** Stay scoped to the organization/context established by earlier calls. If `search_repositories` found VersionLens repos, subsequent issue searches should target those repos, not random OSS projects.

### Gap 4: Retrying Identical Failing Calls (Medium)

**What happens:** Sentry_scout called `search_issues` with the exact same parameters 4 times before changing them. No learning between attempts.

**Target behavior:** If a call returns unexpected results, vary the parameters on the next attempt. Analyze what went wrong before retrying.

### Gap 5: Poor Subagent Tool Enablement (Medium)

**What happens:** Root agent didn't enable `get_me` for github_scout (explicitly mentioned in the task prompt), and gave linear_scout only 3 tools including the non-useful `extract_images`.

**Target behavior:** Map task requirements to specific tools. If the task says "find who the authenticated user is", enable `get_me`. If the task says "recent comments on my issues", enable `list_comments`.

---

## 2. Training Data Architecture

### 2.1 Data Format

Each training example is a complete agent run trace in the model's chat format:

```
system: <system prompt with tool definitions>
user: <task prompt>
assistant: <reasoning + tool_calls[]>
tool: <tool result for call 1>
tool: <tool result for call 2>
assistant: <reasoning + tool_calls[]>
...
assistant: <final response>
```

For parallel tool calls, the assistant message contains multiple entries in `tool_calls[]`, and each gets its own `tool` response message.

Key structural requirements:
- Tool call format must match Qwen 3.5's native function-calling schema
- Reasoning/thinking tokens should be present (Qwen 3.5 supports a `reasoning` field)
- Truncated tool results must include the exact envelope our system produces (see Section 2.3)

### 2.2 Data Sources

We use three sources of training data, in order of priority:

#### Source A: Opus 4.6 Gold Traces (highest quality, lowest volume)

Real Opus runs on diverse tasks. These are the ground truth for correct behavior.

**How to collect:**
1. Design 50-100 diverse agent tasks covering the tool catalog (see Section 3)
2. Run each with `--provider anthropic`
3. Extract traces from `agent_runs.trace` column
4. Filter to runs where status=completed and no wasted calls
5. Convert Anthropic message format → Qwen chat format

**What they teach:** Correct orchestration patterns, parallel calling, spillover handling, subagent tool enablement, executive summary quality.

**Expected volume:** ~50-100 root traces + ~200-400 subagent traces = ~300-500 training conversations.

#### Source B: GLM 5.1 Synthetic Traces (good quality, high volume)

GLM already handles spillover (`view_tool_output`) and parallel calls correctly. It's cheaper and faster than Opus, making it ideal for generating bulk training data.

**How to collect:**
1. Run the same 50-100 tasks with GLM 5.1
2. Use Opus traces as reference to identify where GLM diverged
3. Keep GLM traces where behavior matches Opus patterns
4. Discard or edit traces where GLM makes errors (e.g., its linear_scout failures)

**What they teach:** Volume for the core patterns (parallel calls, spillover handling). GLM's traces are structurally correct even where content quality is lower.

**Expected volume:** ~300-500 training conversations (same tasks, different model).

#### Source C: Targeted Synthetic Examples (surgical, hand-crafted)

For behaviors that neither Opus nor GLM demonstrate in sufficient variety, we generate synthetic traces:

**2.2c.1 — Truncation Recovery Drills:**
Generate 200+ examples where tool results are truncated, and the correct next action is `view_tool_output` or `grep_tool_output`. Vary:
- The tool that produced the truncation (slack_search, search_code, list_issues, etc.)
- The output_id format
- Whether grep or view is the better choice
- The content being retrieved

**2.2c.2 — Parallel Call Batching:**
Generate 200+ examples of assistant messages with 2-5 parallel tool calls. Vary:
- The combination of tools (search_tools pairs, wait_agent batches, comment lookups)
- The reasoning for why they're independent
- The subsequent handling of multiple tool results

**2.2c.3 — Off-Task Correction (DPO pairs):**
For DPO/preference training, generate pairs:
- **Chosen:** github_scout searches within VersionLens repos
- **Rejected:** github_scout searches facebook/react, pytorch, etc.

**2.2c.4 — Retry Variation:**
Generate examples where the first call returns unexpected results and the model varies its parameters on the second attempt (rather than retrying identically).

**Expected volume:** ~600-800 targeted examples.

### 2.3 Truncation Envelope Format

Every synthetic truncated tool result must use our exact envelope so the model learns to recognize it:

```json
{
  "ok": true,
  "tool_name": "slack_search",
  "size_bytes": 17286,
  "estimated_tokens": 4082,
  "total_lines": 89,
  "stored_truncated": false,
  "inline_truncated": true,
  "output_id": "a6d229c2-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "preview": "<first ~500 chars of the output>",
  "preview_truncated": true,
  "message": "Full output stored separately. Use view_tool_output or grep_tool_output with the output_id to inspect it."
}
```

The model must learn: when it sees `"inline_truncated": true`, the next action should involve the `output_id`.

---

## 3. Task Design for Data Generation

### 3.1 Task Categories

Each task should exercise a different combination of behaviors:

| Category | Example Task | Key Behaviors Tested |
|----------|-------------|---------------------|
| **Multi-integration sweep** | "Gather data from all integrations and build a KB" | Subagent spawning, parallel waits, KB construction |
| **Deep single-integration** | "Find all urgent Linear issues, get comments, check assignees" | Sequential tool chaining, truncation handling |
| **Cross-integration correlation** | "Compare Sentry errors with recent GitHub PRs" | Subagent coordination, data synthesis |
| **Search-heavy** | "Search Slack for discussions about [topic] across last month" | Truncation handling, query refinement |
| **Exploration** | "Discover what tools are available and document them" | `search_tools`, parallel discovery |
| **Error-recovery** | Tasks where some tools will return errors or empty results | Retry variation, fallback strategies |
| **Large result handling** | Tasks targeting tools known to return 10K+ tokens | Spillover handling, `grep_tool_output` |

### 3.2 Task Template Variables

To maximize diversity without manual effort, templatize tasks:

```
"Search {integration} for {query_a} and {query_b} (limit {n} each). 
 Compile a structured summary."

Variables:
- integration: slack, github, linear, sentry
- query_a/b: drawn from a pool of 20+ realistic queries
- n: 3, 5, 10, 20
```

### 3.3 Generating Tasks with GLM 5.1

Use GLM to generate diverse tasks:

```
Prompt to GLM:
"You are a task designer for an AI agent testing framework. 
Generate 20 diverse agent tasks that would exercise:
- Parallel tool calling (multiple independent lookups)
- Handling truncated/large tool results  
- Subagent spawning and coordination
- Error recovery when tools return unexpected results
- Staying on-task within a defined scope

Available integrations: Slack, GitHub (MCP), Linear (MCP), Sentry (MCP), 
Knowledge Base, Child Agents.
Each task should be 3-8 sentences and include specific, testable objectives."
```

Then run each generated task with both Opus (gold) and GLM (bulk) to get paired traces.

---

## 4. Finetuning Strategy

### 4.1 Model Architecture and Unsloth Configuration

Both target models (Qwen3.5-9B, Qwen3.5-4B) are dense models with a hybrid architecture: Gated DeltaNet linear attention (3 of every 4 layers) + grouped-query attention (every 4th layer).

**Critical: Unsloth recommends AGAINST QLoRA (4-bit) for Qwen 3.5.** Their docs state: *"It is not recommended to do QLoRA (4-bit) training on the Qwen3.5 models, no matter MoE or dense, due to higher than normal quantization differences."* Use **bf16 LoRA** instead.

**Recommended Unsloth LoRA configuration:**
```python
model = FastLanguageModel.from_pretrained(
    model_name="Qwen/Qwen3.5-9B",  # or Qwen3.5-4B
    max_seq_length=98304,  # 96K
    dtype=torch.bfloat16,
    load_in_4bit=False,  # DO NOT use 4-bit for Qwen3.5
)
model = FastLanguageModel.get_peft_model(
    model,
    r=16,
    lora_alpha=16,
    lora_dropout=0,
    bias="none",
    target_modules=["q_proj", "k_proj", "v_proj", "o_proj",
                     "gate_proj", "up_proj", "down_proj"],
    use_gradient_checkpointing="unsloth",
)
```

**Sequence length requirements — why 65K:**

Our actual agent traces are long. Measured from the three-model comparison run:

| Run Type | Trace Token Range | With System Prompt + Tool Defs |
|----------|-------------------|-------------------------------|
| Root run (orchestrator) | 47K-61K tokens | ~50K-65K tokens |
| Subagent (large, e.g. slack/github) | 20K-40K tokens | ~25K-45K tokens |
| Subagent (small, e.g. sentry) | 5K-13K tokens | ~8K-18K tokens |

A max_seq_length of 8192 would truncate every root trace and most subagent traces, losing the critical end-of-sequence behavior (KB construction, executive summary). We target **96K** to cover the full root + subagent traces with headroom for the system prompt and tool definitions.

Both Qwen3.5-9B and 4B natively support 262K context, so the architecture handles it — the constraint is VRAM.

**VRAM requirements (bf16 LoRA with Unsloth gradient checkpointing):**

| Model | seq_len 2K (baseline) | seq_len 32K (subagents) | seq_len 96K (target) |
|-------|----------------------|------------------------|---------------------|
| Qwen3.5-4B | ~10 GB | ~24 GB | ~60 GB |
| Qwen3.5-9B | ~22 GB | ~48 GB | ~80+ GB |

VRAM scales roughly linearly with sequence length due to KV cache and activation memory. At our 96K target:

| Model | Min GPU |
|-------|---------|
| Qwen3.5-4B | 1x A100 80GB (comfortable) |
| Qwen3.5-9B | 1x A100 80GB |

Both fit on a single A100 80GB with Unsloth's gradient checkpointing at batch size 1. The 9B model weights are ~18GB in bf16; LoRA adds negligible overhead; the remaining ~60GB covers activations and KV cache at 96K sequence length. Requires transformers v5 (Unsloth default).

### 4.2 Training Phases

#### Phase 1: SFT on Core Tool Patterns

**Data:** Source C (targeted synthetic examples) — ~600-800 examples focused purely on:
- Truncation → `view_tool_output`/`grep_tool_output` response
- Parallel tool call formatting
- Retry variation

**Goal:** Teach the mechanical behaviors first. This is the lowest-risk intervention because these patterns are orthogonal to the model's existing knowledge.

**Training settings (Unsloth):**
```python
trainer = SFTTrainer(
    model=model,
    train_dataset=dataset,
    args=TrainingArguments(
        per_device_train_batch_size=1,
        gradient_accumulation_steps=4,
        num_train_epochs=3,
        learning_rate=2e-4,
        bf16=True,
        max_seq_length=98304,  # 96K
    ),
)
```

**Evaluation:** Run 10 agent tasks where tools will be truncated. Measure: does the model call `view_tool_output`? Does it issue parallel calls?

#### Phase 2: SFT on Full Agent Traces

**Data:** Source A (Opus gold traces) + Source B (GLM traces where behavior matches Opus) — ~600-1000 examples.

**Goal:** Teach end-to-end orchestration: discovery → spawn → wait → KB build → summarize. Including correct tool enablement for subagents.

**Training settings:** Same as Phase 1 but reduce to 1-2 epochs (longer traces, risk of overfitting to specific task patterns). Full 96K context for root traces.

**Evaluation:** Run the full integration analyst task. Compare against Opus baseline on:
- Tool call count (waste ratio)
- Parallel call percentage
- Spillover tool usage
- Subagent tool enablement correctness
- Executive summary quality (human eval)

#### Phase 3: DPO/ORPO on Preference Pairs

**Data:** Source C off-task correction pairs + any cases from Phase 2 where the model still exhibits bad patterns.

**Pair construction:**
- **Chosen:** Correct tool call sequence from Opus trace
- **Rejected:** Qwen's original bad behavior (retries, off-task, no spillover)

This is specifically for:
- Off-task → on-task (github_scout scope)
- Identical retry → varied retry
- Ignoring truncation → reading truncation

**Training settings:** DPO beta=0.1, 1 epoch, same LoRA config as Phase 1.

### 4.3 What NOT to Finetune

Preserve Qwen's existing strengths:
- **The `research` tool recovery** in linear_scout was smart — don't train this away
- **Reasoning/thinking** — Qwen produces useful `reasoning` traces; don't suppress these
- **General knowledge and language** — we're only changing tool-calling behavior, not the base model's world knowledge

---

## 5. Data Generation Pipeline

### 5.1 Tooling Required

Build a pipeline that:

1. **Runs agent tasks** via `make cli ARGS="--workspace <ws> agent run --provider <p> -p '<prompt>'"` 
2. **Extracts traces** from SQLite: `SELECT trace FROM agent_runs WHERE id = ?`
3. **Converts trace format** from our internal JSON to Qwen's chat template
4. **Filters bad traces** — discard runs with status != completed or with off-task behavior
5. **Generates truncation drills** — template-based synthetic examples
6. **Generates DPO pairs** — pair Opus chosen traces with Qwen rejected traces for the same task
7. **Validates format** — ensure all training examples parse correctly in the target format

### 5.2 Trace Conversion: Internal → Qwen Format

Our trace format:
```json
{
  "messages": [
    {"role": "assistant", "content": "...", "reasoning": "...", "tool_calls": [...]},
    {"role": "tool", "tool_call_id": "...", "tool_name": "...", "content_text": "..."}
  ]
}
```

Qwen 3.5 chat format:
```json
{
  "messages": [
    {"role": "system", "content": "<system prompt with tool defs>"},
    {"role": "user", "content": "<task>"},
    {"role": "assistant", "content": "<think>...</think>\n<tool_call>\n{...}\n</tool_call>"},
    {"role": "tool", "name": "...", "content": "..."},
    ...
  ]
}
```

Key mappings:
- Our `reasoning` field → Qwen's `<think>` block
- Our `tool_calls[]` array → Qwen's `<tool_call>` blocks (multiple for parallel)
- Our `content_text` → Qwen's tool `content`
- System prompt must include full tool definitions in Qwen's function-calling schema

### 5.3 System Prompt Extraction

Each training example needs the system prompt that was active during the run, including tool definitions. Extract from:
- `agent_run_turns.system_prompt` — the system prompt text
- `agent_run_turns.enabled_tool_ids` / `pinned_tool_ids` — determines which tools were in scope
- The tool catalog (from `agent tools list --json`) — for full schema definitions

For subagent traces, the tool set is scoped by what the root agent enabled via `spawn_agent`.

### 5.4 Mock Tool Results for Synthetic Data

For Source C (targeted synthetic examples), we need to generate realistic tool results without hitting real APIs. Approach:

1. **Replay real results:** Use tool results from Opus/GLM runs, varying the content slightly
2. **Truncation simulation:** Take a real large result, truncate it, and wrap it in the spillover envelope
3. **Error simulation:** Generate realistic error responses (e.g., `{"error": "Project not found"}`)
4. **Empty result simulation:** `{"issues": [], "hasNextPage": false}`

GLM 5.1 can generate diverse mock tool results:
```
Prompt to GLM:
"Generate a realistic Slack search result for query 'engineering priorities' 
with 5 messages. Include channel names, author IDs, message content about 
software development priorities, and timestamps. Format as JSON matching 
this schema: {count, has_more, messages: [{author, channel, content, permalink, ts}]}"
```

---

## 6. Evaluation Framework

### 6.1 Automated Metrics

Run the same integration analyst task (and 10+ other diverse tasks) after each training phase. Measure:

| Metric | How to Measure | Opus Baseline | Qwen Pre-FT | Target |
|--------|---------------|---------------|-------------|--------|
| **Spillover tool usage** | Count `view_tool_output` + `grep_tool_output` calls / truncated results | 6/8 (75%) | 0/20 (0%) | >60% |
| **Parallel call ratio** | Parallel calls / total calls | 14/31 (45%) | 0/73 (0%) | >30% |
| **Wasted calls** | Identical retries + off-task calls | 0 | ~15 | <3 |
| **Wait pattern** | Are wait_agent calls parallel? | Yes | No | Yes |
| **Tool enablement accuracy** | Do subagents have the tools they need? | 90% | 60% | >85% |
| **Total wall time** | End-to-end duration | 208s | 420s | <300s |
| **Total tool calls** | Root + subagent | 61 | 73 | <65 |

### 6.2 Human Evaluation

For 20 runs post-finetuning, have a human evaluator rate:
- Executive summary quality (1-5): actionability, accuracy, cross-source synthesis
- KB node quality (1-5): relevance, completeness, correct scope
- Subagent task adherence (pass/fail): did each scout stay on-task?

### 6.3 Regression Testing

Ensure finetuning doesn't degrade:
- General instruction following (run a non-agent benchmark like MT-Bench)
- Reasoning quality (the `research` tool recovery pattern should still work)
- Response quality for non-tool-calling conversations

---

## 7. Stretch Goals: Exceeding Opus

Once Qwen matches Opus on the core behaviors, there are areas where it could potentially surpass it:

### 7.1 Sentry Coverage Breadth

Opus only scanned `app-nalvin-com` and missed the more critical `nalvin-gcp` issues. GLM found `nalvin-gcp` by doing an org-wide search. Train Qwen to check multiple projects when a Sentry org has several.

### 7.2 Subagent Spawn Parallelism

None of the three models parallelized `spawn_agent` calls (all sequential). Since spawns are independent, they could theoretically be batched. This is a novel behavior not in any reference trace — we'd need to construct synthetic examples.

### 7.3 KB Node Attributes

Opus added structured attributes to nodes and edges (e.g., `{source: "linear", in_progress_count: 11}`). Train Qwen to do the same — this makes the KB more queryable and useful.

### 7.4 Cost Efficiency

If the finetuned Qwen 3.5 matches Opus quality with 3-4x fewer input tokens (as a 35B model inherently uses less context), it becomes a much more cost-effective option for running agent tasks at scale.

---

## 8. Data and Compute Estimate

### Training Data

| Source | Examples |
|--------|----------|
| Opus gold traces (50-100 tasks × root + subagents) | ~300-500 |
| GLM bulk traces (same tasks, filtered) | ~300-500 |
| Targeted synthetic drills (truncation, parallel, retry) | ~600-800 |
| DPO preference pairs | ~200 |
| **Total** | **~1,500-2,000** |

### Compute (Unsloth, bf16 LoRA, 96K seq_len)

| Model | GPU |
|-------|-----|
| Qwen3.5-4B | 1x A100 80GB |
| Qwen3.5-9B | 1x A100 80GB |

All phases (SFT Phase 1 + SFT Phase 2 + DPO Phase 3) run on a **single GPU** with Unsloth. No multi-GPU or distributed training required. Unsloth's custom gradient checkpointing (`use_gradient_checkpointing="unsloth"`) is mandatory for staying within VRAM budgets.

### API Cost for Trace Generation

~50-100 Opus 4.6 runs at ~500K input tokens each ≈ 25-50M input tokens. At Anthropic batch API pricing this is the primary dollar cost.

---

## 9. Risks and Mitigations

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| **Overfitting to integration analyst task** | High | Diverse task set (50+ tasks across categories), hold out test tasks |
| **Degraded reasoning** | Medium | Track MT-Bench, preserve thinking tokens, conservative LR |
| **Router instability (MoE-specific)** | Medium | Monitor expert utilization during training, use lower LR for router |
| **Truncation over-triggering** | Low | Include examples where results are NOT truncated and no spillover is needed |
| **Loss of `research` tool smarts** | Low | Include Qwen's good linear_scout trace in training set |
| **Format mismatch** | Medium | Validate every example against Qwen's tokenizer before training |

---

## Appendix: Inspecting Agent Runs with SQLite

Every agent run is stored in `~/.nalvin/workspace-db/<workspace>.sqlite`. The examples below use the `integrations-test` workspace.

### Database schema overview

```
agent_runs              — one row per run (root or child)
agent_run_turns         — one row per user turn within a run
agent_run_events        — SSE events emitted during the run
agent_run_tool_outputs  — spillover tool outputs stored separately
```

The `trace` column on `agent_runs` is a JSON blob containing the full message history including tool calls, tool results, reasoning, and timing.

### List recent runs

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT id, status, model, duration_ms, message_count,
         input_tokens, output_tokens,
         substr(prompt, 1, 80) AS prompt_preview
  FROM agent_runs
  WHERE run_kind = 'root'
  ORDER BY updated_at DESC
  LIMIT 10;
"
```

### Get a run and all its subagent runs

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT id, model, task_name, run_kind, status,
         duration_ms, message_count, input_tokens, output_tokens
  FROM agent_runs
  WHERE id = '<RUN_ID>' OR root_run_id = '<RUN_ID>'
  ORDER BY created_at;
"
```

### Extract the full tool call sequence from a run

```sql
sqlite3 -json ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT
    json_extract(tc.value, '$.function.name') AS tool,
    json_extract(tc.value, '$.function.arguments') AS args,
    CASE WHEN json_array_length(json_extract(m.value, '$.tool_calls')) > 1
         THEN 'parallel' ELSE 'sequential' END AS mode
  FROM agent_runs ar,
    json_each(json_extract(ar.trace, '$.messages')) m,
    json_each(json_extract(m.value, '$.tool_calls')) tc
  WHERE ar.id = '<RUN_ID>'
    AND json_extract(m.value, '$.role') = 'assistant'
    AND json_extract(m.value, '$.tool_calls') IS NOT NULL;
"
```

### Count tool calls per tool per subagent

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT
    ar.task_name,
    json_extract(tc.value, '$.function.name') AS tool,
    COUNT(*) AS calls
  FROM agent_runs ar,
    json_each(json_extract(ar.trace, '$.messages')) m,
    json_each(json_extract(m.value, '$.tool_calls')) tc
  WHERE ar.root_run_id = '<ROOT_RUN_ID>'
    AND json_extract(m.value, '$.role') = 'assistant'
  GROUP BY ar.task_name, tool
  ORDER BY ar.task_name, calls DESC;
"
```

### Find truncated tool results (candidates for spillover handling analysis)

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT
    ar.task_name,
    json_extract(m.value, '$.tool_name') AS tool,
    json_extract(m.value, '$.content_json.output_id') AS output_id,
    json_extract(m.value, '$.content_json.estimated_tokens') AS est_tokens,
    json_extract(m.value, '$.content_json.inline_truncated') AS truncated
  FROM agent_runs ar,
    json_each(json_extract(ar.trace, '$.messages')) m
  WHERE (ar.id = '<RUN_ID>' OR ar.root_run_id = '<RUN_ID>')
    AND json_extract(m.value, '$.role') = 'tool'
    AND json_extract(m.value, '$.content_json.inline_truncated') = 1;
"
```

### Extract the final assistant message (executive summary)

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT json_extract(m.value, '$.content')
  FROM agent_runs ar,
    json_each(json_extract(ar.trace, '$.messages')) m
  WHERE ar.id = '<RUN_ID>'
    AND json_extract(m.value, '$.role') = 'assistant'
    AND json_extract(m.value, '$.content') IS NOT NULL
    AND json_extract(m.value, '$.content') != ''
  ORDER BY ROWID DESC
  LIMIT 1;
"
```

### Extract the full trace as formatted JSON

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT json_extract(trace, '$.messages')
  FROM agent_runs
  WHERE id = '<RUN_ID>';
" | python3 -m json.tool > trace.json
```

### Measure trace size (for sequence length planning)

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT
    ar.model,
    ar.task_name,
    ar.message_count,
    length(ar.trace) AS trace_bytes,
    length(ar.trace) / 4 AS approx_tokens
  FROM agent_runs ar
  WHERE ar.id = '<RUN_ID>' OR ar.root_run_id = '<RUN_ID>'
  ORDER BY length(ar.trace) DESC;
"
```

### Compare parallel vs sequential tool calling across models

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT
    ar.model,
    ar.task_name,
    COUNT(*) AS total_calls,
    SUM(CASE WHEN json_array_length(json_extract(m.value, '$.tool_calls')) > 1
             THEN 1 ELSE 0 END) AS parallel_calls,
    ROUND(100.0 * SUM(CASE WHEN json_array_length(json_extract(m.value, '$.tool_calls')) > 1
             THEN 1 ELSE 0 END) / COUNT(*), 1) AS parallel_pct
  FROM agent_runs ar,
    json_each(json_extract(ar.trace, '$.messages')) m,
    json_each(json_extract(m.value, '$.tool_calls')) tc
  WHERE ar.root_run_id IN ('<OPUS_ID>', '<GLM_ID>', '<QWEN_ID>')
    AND json_extract(m.value, '$.role') = 'assistant'
  GROUP BY ar.model, ar.task_name
  ORDER BY ar.model, ar.task_name;
"
```

### View spillover tool outputs for a run

```sql
sqlite3 ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT output_id, tool_name, size_bytes, estimated_tokens,
         total_lines, stored_truncated, inline_truncated
  FROM agent_run_tool_outputs
  WHERE run_id = '<RUN_ID>'
  ORDER BY created_at;
"
```

### Get spawn_agent tool enablement (what tools each subagent was given)

```sql
sqlite3 -json ~/.nalvin/workspace-db/integrations-test.sqlite "
  SELECT
    json_extract(tc.value, '$.function.arguments') AS spawn_args
  FROM agent_runs ar,
    json_each(json_extract(ar.trace, '$.messages')) m,
    json_each(json_extract(m.value, '$.tool_calls')) tc
  WHERE ar.id = '<ROOT_RUN_ID>'
    AND json_extract(m.value, '$.role') = 'assistant'
    AND json_extract(tc.value, '$.function.name') = 'spawn_agent';
" | python3 -m json.tool
```
