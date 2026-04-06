# Integration Test: GLM 5.1 vs Qwen 3.5 35B-A3B vs Claude Opus 4.6

**Date:** 2026-04-07
**Task:** Integration analyst — gather intelligence from Slack, GitHub, Linear, and Sentry; build a structured knowledge base; produce an executive summary.
**Workspace:** `integrations-test`

> **Note on Qwen model:** The value recorded in the database is `qwen3.5-27B` but the actual model
> is the **35B A3B MoE variant** (Qwen 3.5 35B-A3B), not the 27B dense model.

**Run IDs:**
- GLM 5.1: `82d9560c-e00c-4b16-a980-4329d3d30416`
- Qwen 3.5 35B-A3B: `376a2c84-91d4-4df2-9166-0fe7cb446036`
- Claude Opus 4.6: `68a12acf-e9bb-4a4a-8a6d-923280e95e26`

---

## High-Level Comparison

| Metric | GLM 5.1 | Qwen 3.5 35B-A3B | Claude Opus 4.6 |
|--------|---------|-------------------|-----------------|
| **Status** | completed | completed | completed |
| **Total wall time** | 6m 18s (378s) | 7m 00s (420s) | **3m 28s (208s)** |
| **Root messages** | 66 | 72 | 60 |
| **Root input tokens** | 75,769 | 190,588 | 481,192 |
| **Root output tokens** | 6,194 | 10,885 | 7,927 |
| **Total input tokens (all runs)** | 129,096 | 253,074 | 930,893 |
| **Total output tokens (all runs)** | 14,767 | 20,892 | 16,965 |
| **Root tool calls** | 35 | 35 | 31 |
| **Total subagent tool calls** | 32 | 38 | 30 |
| **KB nodes created** | 5 | 5 | 5 |
| **KB edges created** | 4 | 4 | 4 |
| **Subagent errors** | 6 soft (Linear project lookups) | 0 hard; many wasted retries | 0 hard; 0 wasted retries |

All three models completed the full 4-phase task. Opus 4.6 was the fastest by a wide margin (3m 28s vs 6-7m) and had zero wasted tool calls, but consumed by far the most input tokens (930K total — the Anthropic API prices input tokens, so this matters for cost). GLM 5.1 was the most token-efficient. Qwen 3.5 was the slowest and least efficient.

---

## Phase 1: Discovery — Tool Search

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| `search_tools` calls | 7 | 9 | 8 |
| Parallel calls | 3 at once | All sequential | 2 at once (linear + sentry) |
| Time in Phase 1 | ~51s | ~60s | ~30s |

All three followed the same pattern: search for integration tools by name, then child agent tools, then KB tools. Opus used 8 calls (one extra `search_tools` for "linear search issues" to find `list_issues` specifically) and parallelized two of them. GLM parallelized three.

---

## Phase 2: Subagent Orchestration

### Spawn & Wait Strategy

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| Spawn pattern | 4 sequential | 4 sequential | 4 sequential |
| Spawn window | 22s | 23s | 12s |
| Wait pattern | **4 parallel** | 4 sequential | **4 parallel** |

**Key finding:** Both GLM 5.1 and Opus 4.6 issued all 4 `wait_agent` calls in a single parallel batch. Qwen 3.5 waited sequentially (slack -> github -> linear -> sentry), meaning it couldn't start KB construction until the last sequential wait returned. This is the #1 reason Qwen was slower.

Opus had the tightest spawn window (12s vs 22-23s for the others), getting all subagents started faster.

### Tool Enablement for Subagents

| Scout | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|-------|---------|-------------------|----------|
| **slack** | `slack_search` | `slack_search` | `slack_search` |
| **github** | `search_repositories`, `get_me`, `search_code`, `search_issues` | `search_repositories`, `search_issues`, `search_code` (missing `get_me`) | `search_repositories`, `get_me`, `search_code` (missing `search_issues`) |
| **linear** | Full Linear tool set | `list_issues`, `research`, `extract_images` | `list_issues`, `research`, `list_comments` |
| **sentry** | Full Sentry tool set | `find_projects`, `find_organizations`, `search_issues`, `search_events` | `find_organizations`, `find_projects`, `search_issues`, `whoami` |

**GLM 5.1** enabled the broadest tool sets.
**Opus 4.6** was more surgical — it pinned exactly the tools each subagent needed for its specific tasks, notably including `list_comments` for linear_scout (the task asked for "recent comments on my issues") and `whoami` for sentry_scout. However, it omitted `search_issues` from github_scout.
**Qwen 3.5** was the most limited, critically missing `get_me` from github_scout.

---

## Subagent Timeline

```
GLM 5.1:
22:00:40  slack   START ========================== END 22:02:01  (80s)
22:00:47  github  START ========================= END 22:02:06  (77s)
22:00:56  linear  START ================================= END 22:02:41 (104s)
22:01:02  sentry  START ================================ END 22:02:43 (100s)
          |---- all 4 concurrent ---- last finishes at +2m03s ----|

Qwen 3.5 35B-A3B:
10:36:49  slack   START ======================= END 10:38:01  (70s)
10:36:59  github  START =============================== END 10:38:45 (104s)
10:37:04  linear  START ================================================ END 10:40:10 (185s)
10:37:11  sentry  START ======================== END 10:38:28  (75s)
          |---- all 4 concurrent ---- last finishes at +3m21s ----|

Opus 4.6:
14:26:42  slack   START ==================== END 14:27:39  (57s)
14:26:46  github  START ====================== END 14:27:43  (56s)
14:26:51  linear  START ======================== END 14:27:51  (60s)
14:26:54  sentry  START ================ END 14:27:38  (44s)
          |---- all 4 concurrent ---- last finishes at +1m09s ----|
```

**Opus 4.6 subagents were dramatically faster.** The longest subagent (linear_scout) took only 60s vs 104s (GLM) or 185s (Qwen). The total concurrent subagent phase was 69s for Opus vs 123s (GLM) or 201s (Qwen).

---

## Subagent Performance: slack_scout

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| Duration | 80s | 70s | 54s |
| Messages | 7 | 22 | 7 |
| Tool calls | 3 | 10 | 3 |
| Tools used | `slack_search` x2, `view_tool_output` x1 | `slack_search` x10 | `slack_search` x2, `view_tool_output` x1 |
| Parallel initial searches | Yes | No | Yes |
| Used `view_tool_output` | Yes | **No** | Yes |
| Errors | None | 3 wasted retries | None |

**GLM 5.1 and Opus 4.6 behaved identically:** 2 parallel Slack searches, then `view_tool_output` to read the truncated result. Clean, efficient, correct.

**Qwen 3.5:** 10 separate queries, retried 3x on truncated results, never used `view_tool_output`. Over-searched but produced a longer report.

**Quality comparison:**
- GLM: Focused summary (~1,900 bytes) — key projects, priorities, customer pipeline, strategic direction
- Qwen: Detailed report (~4,700 bytes) — added per-person action items, infrastructure details, customer insights
- Opus: Focused summary (~2,100 bytes) — similar to GLM in scope, with a priorities table ranked by urgency

---

## Subagent Performance: github_scout

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| Duration | 77s | 104s | 55s |
| Messages | 8 | 34 | 17 |
| Tool calls | 4 | 16 | 8 |
| Tools used | `get_me`, `search_repos`, `search_code`, `view_tool_output` | `search_code` x4, `search_repos` x2, `search_issues` x10 | `get_me`, `search_repos` x2, `search_code` x2, `grep_tool_output` x2, `view_tool_output` |
| Used spillover tools | `view_tool_output` x1 | **Never** | `grep_tool_output` x2, `view_tool_output` x1 |
| On-task? | Yes | **No — searched OSS repos** | Yes |

**Opus 4.6:** Called `get_me` first (correct), then searched repos for "nalvin" (found 9), then tried two code searches. When results were truncated, it first tried `grep_tool_output` with regex patterns to find relevant content (2 attempts, 0 matches — the patterns were too specific for the JSON format), then fell back to `view_tool_output` to read raw content. More tool calls than GLM (8 vs 4) but methodical — it tried the smart approach (grep) before brute-force (view).

**GLM 5.1:** Clean and efficient — 4 calls, all on-task.

**Qwen 3.5 (major failure):** 16 calls, went completely off-task searching for generic bugs in facebook/react, pytorch, tensorflow, rails. Never used spillover tools. The worst subagent performance across all runs.

---

## Subagent Performance: linear_scout

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| Duration | 104s | 185s | 58s |
| Messages | 32 | 12 | 27 |
| Tool calls | 20 | 5 | 15 |
| Key tools | `get_user` x2, `get_team`, `list_cycles` x3, `get_project` x6 (all failed) | `list_issues` x3, `research` x2 | `list_issues` x3, `list_comments` x10, `view_tool_output` x2 |
| Found actual issues? | **No** (meta-report only) | **Yes** (9 issues via `research`) | **Yes** (11 issues via `list_issues`) |
| Used `view_tool_output` | No | No | Yes (x2) |
| Checked comments? | No | No (via `research`, which found none) | **Yes** (10 individual `list_comments` calls, all empty) |
| Parallel calls | Minimal | None | **Heavy** (comments fetched in pairs) |

**Opus 4.6 (best linear_scout):** The most thorough execution. Called `list_issues` with correct state filter (no quoting bug), got truncated results, used `view_tool_output` to read them. Then fetched priority 1 and priority 2 issues in parallel. Then systematically checked comments on 10 issues (in parallel pairs of 2). Found 11 in-progress issues with full details and correctly noted that 3 urgent items were unassigned. This is the only scout that actually fulfilled all three parts of the task: (a) in-progress issues, (b) high-priority issues, (c) recent comments.

**Qwen 3.5 (good recovery):** Only 5 calls, but smart use of `research` tool recovered from a state filter bug. Found 9 in-progress issues. Didn't individually check comments.

**GLM 5.1 (weakest linear_scout):** Explored workspace metadata extensively but never actually listed issues. Wasted 6 calls on non-existent projects. The resulting "My Active Work" node was a workspace overview (team members, labels, cycles) without any specific issue content.

---

## Subagent Performance: sentry_scout

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| Duration | 100s | 75s | 42s |
| Messages | 11 | 16 | 9 |
| Tool calls | 5 | 7 | 4 |
| Tools used | `whoami`, `find_orgs`, `find_projects`, `search_issues` x2 | `find_orgs`, `find_projects`, `search_issues` x5 | `whoami` + `find_orgs` (parallel), `find_projects`, `search_issues` |
| Parallel first calls | Yes (`whoami` + `find_orgs`) | No | Yes (`whoami` + `find_orgs`) |
| Wasted retries | 0 | 3 | 0 |
| Project scoped | `nalvin-gcp` + `app-nalvin-com` | `app-nalvin-com` (after 3 retries) | `app-nalvin-com` |

**Opus 4.6:** Cleanest execution — 4 calls, zero waste. `whoami` + `find_organizations` in parallel, then `find_projects`, then `search_issues` scoped to `app-nalvin-com` with correct parameters on the first try. Found 5 unresolved issues (472 total events).

**GLM 5.1:** Also clean (5 calls), and covered both `nalvin-gcp` (backend issues, 147 events) and `app-nalvin-com` (found 0 there). The nalvin-gcp coverage was more operationally valuable.

**Qwen 3.5:** Retried the same query 3 times before fixing the project parameter. 7 calls for the same result Opus got in 4.

**Quality:** GLM found the more critical backend issues (nalvin-gcp). Opus and Qwen both focused on app-nalvin-com (frontend/SSR issues). For a production health report, GLM's coverage was better, though Opus's was clean and accurate for what it covered.

---

## Phase 3: Knowledge Base Construction

| | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|--|---------|-------------------|----------|
| Node creation | 5 sequential | 5 sequential | 5 sequential |
| Edge creation | 4 sequential | 4 sequential | 4 sequential |
| Edge attributes | None | None | **Yes** (`{integration: "slack"}` etc.) |
| Phase 4 verification | Read all 5 nodes + listed edges | Read all 5 nodes | Listed nodes + listed edges (parallel) |

**Opus distinction:** Only model to add structured `attributes` to KB edges (tagging each with the source integration). It also added richer attributes to nodes (e.g., `{source: "linear", user: "Pascal Chatterjee", in_progress_count: 11, urgent_count: 5}`). This makes the KB more queryable.

For verification, Opus listed nodes + edges in parallel rather than reading each node individually — faster but less thorough verification.

---

## Phase 4: Executive Summary Quality

### GLM 5.1
- Sections: Organization Overview, What's Hot, Strategic Landscape, Codebase Snapshot, KB Structure
- Led with actionable items (production health, engineering blockers)
- Correctly identified #1 blocker: Jira/Linear sync
- Concrete numbers: 147 events/24h, 43% cycle completion, ARR figures
- ASCII diagram of KB structure
- ~3,300 characters

### Qwen 3.5 35B-A3B
- Table-based structure with KB node inventory
- Key findings organized by scout source
- "Strategic Insights" section with forward-looking recommendations
- Contains off-task GitHub data (React/PyTorch bugs) without flagging it
- Listed Linear tools as only 3 available (its own limited view)
- ~3,800 characters

### Claude Opus 4.6
- Clean two-part structure: Knowledge Base Built (table) + Key Findings + Recommended Actions
- Prioritized action table (P0-P4) with source attribution
- Identified risk flags: "sole engineer bottleneck", "KB v2 Urgent items stale 7+ days"
- Cross-referenced findings across integrations (e.g., Jira/Linear sync appears in both Slack and Linear data)
- Concrete and specific: "368 error events on /integrations", "3 unassigned urgent items (VL-4389/90/91)"
- ~2,800 characters — most concise of the three

**Verdict:** Opus produced the most actionable and operationally useful summary. It synthesized across sources (noting that Jira/Linear sync showed up in both Slack discussions and Linear priorities), identified risks that neither other model flagged (sole engineer bottleneck, stale urgent items), and provided a clear prioritized action table. GLM was close behind with a solid summary. Qwen's summary was the weakest due to carrying forward quality issues from its scouts.

---

## Behavioral Patterns

### Tool Output Handling (inline truncation)

| Behavior | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|----------|---------|-------------------|----------|
| Used `view_tool_output` | Yes (2x across scouts) | **Never** | Yes (4x across scouts) |
| Used `grep_tool_output` | No | **Never** | Yes (2x in github_scout) |
| Response to truncation | Read the spillover | Retry or re-search | Read spillover; try grep first, then view |

Opus demonstrated the most sophisticated truncation handling: it tried `grep_tool_output` first to extract specific content from large outputs, falling back to `view_tool_output` when grep patterns didn't match. This is the most efficient approach for large spillover outputs.

Qwen never used either spillover tool — the most significant capability gap across all three models.

### Parallel Tool Calling

| Pattern | GLM 5.1 | Qwen 3.5 35B-A3B | Opus 4.6 |
|---------|---------|-------------------|----------|
| Discovery phase | 3 parallel | All sequential | 2 parallel |
| wait_agent | **4 parallel** | 4 sequential | **4 parallel** |
| Within subagents | Moderate (pairs) | Rare | **Heavy** (pairs throughout) |
| KB verification | Sequential reads | Sequential reads | **Parallel** (list nodes + edges) |

Opus used parallel tool calling most aggressively, especially within subagents — the linear_scout fetched comments in parallel pairs, and the sentry/slack scouts parallelized their initial discovery calls. This is the primary reason Opus subagents were ~40% faster than GLM's despite doing equivalent work.

### Error Recovery

**GLM 5.1:** Reasonable but expensive — linear_scout tried 6 project names before giving up. Recovered from the wrong approach but lost time.

**Qwen 3.5:** Mixed — smart pivot to `research` tool for Linear, but retried identical failing Sentry calls 3x. Inconsistent recovery strategy.

**Opus 4.6:** Zero errors requiring recovery. Every tool call either succeeded or the model adapted immediately (e.g., trying grep patterns before falling back to raw view). No wasted retries anywhere across all subagents.

---

## Cost Analysis (approximate)

Using approximate per-token pricing for context:

| Model | Total Input Tokens | Total Output Tokens | Speed (wall time) |
|-------|-------------------|--------------------|--------------------|
| GLM 5.1 | 129,096 | 14,767 | 378s |
| Qwen 3.5 35B-A3B | 253,074 | 20,892 | 420s |
| Claude Opus 4.6 | 930,893 | 16,965 | 208s |

Opus consumed **7.2x more input tokens than GLM** and **3.7x more than Qwen**, largely because it's a much larger context model and the Anthropic API counts tokens differently. However, it completed in roughly half the wall time and produced the highest quality output with zero wasted work.

GLM 5.1 offers the best efficiency/quality tradeoff — similar quality to Opus at 1/7th the input token cost, though 1.8x slower.

---

## Summary: Strengths and Weaknesses

### GLM 5.1
**Strengths:**
- Most token-efficient (7x fewer input tokens than Opus)
- Understood spillover/`view_tool_output` mechanism
- Used parallel tool calls effectively (4 parallel waits)
- Broadest tool enablement for subagents
- Good executive summary with actionable focus

**Weaknesses:**
- linear_scout never found actual issues (meta-report only) — biggest quality gap
- linear_scout wasted 6 calls on non-existent projects
- Slower than Opus (1.8x)

### Qwen 3.5 35B-A3B
**Strengths:**
- linear_scout found actual issues via smart `research` tool recovery
- Slack scout gathered the most comprehensive data (10 queries)

**Weaknesses:**
- **Never used `view_tool_output`** despite ~20 truncated results (most critical issue)
- github_scout went completely off-task (searched OSS repos)
- Retried identical failing calls (sentry_scout 3x)
- Sequential waits wasted time
- Slowest overall, highest output token count
- Executive summary carried forward data quality issues

### Claude Opus 4.6
**Strengths:**
- **Fastest** (3m 28s — nearly 2x faster than either other model)
- **Zero wasted tool calls** — no retries, no errors, no off-task behavior
- **Best linear_scout** — found all in-progress issues, checked all comments, identified unassigned urgents
- Most sophisticated spillover handling (`grep_tool_output` before `view_tool_output`)
- Heaviest parallel tool calling (within and across subagents)
- Best executive summary — cross-referenced sources, prioritized action table, risk flags
- Structured KB attributes on nodes and edges

**Weaknesses:**
- **7x more input tokens** than GLM — significantly more expensive
- Sentry scout only covered `app-nalvin-com` (GLM found the more critical `nalvin-gcp` issues)
- Omitted `search_issues` from github_scout tool enablement
- Verification phase was lighter (listed nodes rather than reading each one back)

---

## Recommendations for the Agent Framework

1. **Teach truncation recovery:** Inject a hint when `inline_truncated=true` is returned, pointing to `view_tool_output`/`grep_tool_output`. Qwen never discovered these tools exist for this purpose.

2. **Validate subagent tool enablement:** The root should verify that tools mentioned in the task prompt are actually enabled on the relevant subagent. All three models missed at least one tool.

3. **Guard against off-task behavior:** Qwen's github_scout searching facebook/react suggests subagent task prompts need explicit scope constraints (e.g., "only search within VersionLens repos").

4. **Parallel wait pattern:** Consider a `wait_all_agents` tool or document the parallel `wait_agent` pattern more prominently. 2 of 3 models discovered it independently, but Qwen did not.

5. **Encourage `grep_tool_output`:** Opus was the only model to try grep before view — this is the ideal pattern for large outputs. The tool description could emphasize this workflow.
