# Running Qwen 3.5 with llama.cpp

This guide covers running Qwen 3.5 models (dense and MoE variants) as a nalvin provider via llama.cpp's OpenAI-compatible server.

## llama-server setup

### Recommended command

```bash
./build/bin/llama-server \
  --host 0.0.0.0 --port 1234 \
  -m /path/to/Qwen3.5-35B-A3B.gguf \
  --jinja \
  --reasoning-format deepseek \
  -fa \
  -ngl 99 \
  -c 196000 \
  -n -1 \
  --no-context-shift \
  --temp 0.6 --top-k 20 --top-p 0.95 --min-p 0
```

### Flag reference

| Flag | Purpose |
|------|---------|
| `--jinja` | Enables Jinja chat template engine. Required for tool calling to work. |
| `--reasoning-format deepseek` | Parses `<think>` tags into `reasoning_content` for Qwen 3.5 thinking models. |
| `-fa` | Flash attention. Recommended for performance. |
| `-ngl 99` | Offload all layers to GPU. |
| `-c 196000` | Context window size in tokens. Adjust to your VRAM. |
| `-n -1` | Unlimited generation tokens per response. Without this, the model may be silently truncated. |
| `--no-context-shift` | Prevents context shifting issues in long multi-turn conversations. |
| `--temp 0.6` | Sampling temperature. Qwen recommends 0.6 for thinking mode with precise tasks. |
| `--top-k 20` | Top-k sampling. Qwen recommended value. |
| `--top-p 0.95` | Top-p (nucleus) sampling. Qwen recommended value. |
| `--min-p 0` | Minimum probability threshold. Qwen recommended value. |

### Important notes

- **Do not use greedy decoding** (temp=0). Qwen's official guidance warns this causes performance degradation and endless repetitions.
- **Do not set `--presence-penalty`** on the server when using tool calling. While Qwen recommends `presence_penalty=1.5` for text generation, it interferes with the structured token patterns needed for tool call JSON.
- **Chat template matters.** The default template embedded in some Qwen 3.5 GGUF files has known bugs that cause tool calling to stall. Use the [unsloth GGUF](https://huggingface.co/unsloth/Qwen3.5-35B-A3B-GGUF) which includes fixes, or provide a corrected template via `--chat-template-file`.

## nalvin provider config

```yaml
providers:
  llamacpp:
    base_url: http://localhost:1234/v1
    api_key: not-set
    model: qwen3.5-35B-A3B
```

No `type` field is needed — nalvin defaults to `openai_compat` when `base_url` is set.

Run an agent:

```bash
nalvin --workspace myproject agent run --provider llamacpp -p "your prompt"
```

## Model variants

Two Qwen 3.5 variants have been tested:

| Model | Params | Active | Tool calling | Notes |
|-------|--------|--------|-------------|-------|
| Qwen3.5-27B (dense) | 27B | 27B | Works for 3-7 step tasks | Slower, sometimes stops after tool discovery |
| Qwen3.5-35B-A3B (MoE) | 35B | 3B | Works for 3-10+ step tasks | Faster, better multi-step reliability |

The **MoE variant is recommended** for agentic workloads. It generates tokens faster (less active params) and handles longer tool-call sequences more reliably.

## Known limitations

### Premature stopping

Both Qwen 3.5 variants occasionally emit stop tokens mid-task, ending the agent loop before all steps are completed. This is a model-level behavior, not a nalvin bug. It manifests as:

- The model creates a todo list, discovers tools, but stops before using them
- The model completes some steps but stops before finishing the rest
- Runs with identical prompts sometimes succeed and sometimes fail

Contributing factors:
- The llama.cpp chat template parsing for thinking models + tool calling is still maturing (see [llama.cpp #20260](https://github.com/ggml-org/llama.cpp/issues/20260))
- GGUF quantization may degrade tool-calling reliability compared to full-precision weights

### Todo-based continuation (automatic retry)

nalvin mitigates premature stopping with a todo-continuation mechanism. When the model creates a todo list via `todowrite` but the agent loop ends with pending or in-progress items, the runtime automatically:

1. Injects a user message: "You have incomplete todo items. Continue working on the remaining steps."
2. Re-invokes the model with the full conversation history
3. Repeats up to 5 times

This significantly improves task completion rates for local models:

| Workflow | Without retries | With retries |
|----------|----------------|-------------|
| Create 3 files + shell loop | ~33% | ~67% |
| News research + digest | Rarely completes | Completes reliably |

The mechanism only triggers when the model has actually called `todowrite` — if the model stops before creating any todos, no retry occurs. This is why the system prompt strongly encourages calling `todowrite` as the first action for multi-step tasks.

The retry limit is defined in `internal/agent/run.go` as `maxTodoContinuations`.

### Reliability by task complexity

Based on testing with Qwen3.5-35B-A3B (MoE, Q4_K_XL quantization):

| Task type | Example | Reliability |
|-----------|---------|------------|
| Single tool discovery + use | "Create a file" | High (~90%) |
| Multi-tool discovery + sequential use | "Create a KB node, then list nodes" | High (~85%) |
| Multi-tool with web fetch + synthesis | "Fetch Wikipedia article, write summary" | High (~80%) |
| Multi-phase with shell composition | "Create files, then shell loop to build index" | Medium (~67% with retries) |
| Multi-source research + write | "Collect news from 2 categories, write digest" | Medium-High (~75% with retries) |

## Tuning tips

- **Use `--verbose`** on agent runs to see tool calls, timing, and debug output in real time.
- **Inspect runs in the database** after each run to see the full trace:
  ```bash
  sqlite3 ~/.nalvin/workspace-db/<workspace>.sqlite \
    "SELECT id, status, duration_ms, message_count FROM agent_runs ORDER BY updated_at DESC LIMIT 5;"
  ```
- **Start simple.** Verify basic tool calling works (file create + read) before attempting complex multi-step workflows.
- **Reduce pinned tools** if the model seems confused. Fewer visible tools = less schema in the prompt = better focus. Override per-run with `--unpin-tool`.
- **Pin specific tools** for tasks where you know what the model needs, skipping the discovery phase: `--pin-tool write --pin-tool shell`.
