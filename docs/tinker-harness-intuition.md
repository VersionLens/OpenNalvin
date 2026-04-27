# Tinker Harness-Intuition Dataset

This workflow builds a small SFT dataset for teaching open-reasoning Qwen models
how agents should use the harness: load skills, reveal hidden tools,
delegate to sub-agents, and choose local/browser/MCP/docker/news tools.

## Prerequisites

- Install Tinker packages:
  - `uv pip install tinker tinker-cookbook`
- Set `TINKER_API_KEY` before launching Tinker jobs.
- Use Tinker model IDs:
  - `Qwen/Qwen3.5-4B`
  - `Qwen/Qwen3.6-35B-A3B`
- Use Tinker cookbook's `FromConversationFileBuilder` on exported
  `conversations.jsonl` files.

## Baseline Probe

Run a pre-training baseline against the manually served llama.cpp endpoints:

- Mac mini provider: actual served model `Qwen/Qwen3.5-4B`
- Omen provider: actual served model `Qwen/Qwen3.6-35B-A3B`

The model string in `~/.nalvin/config.yaml` does not need to match. The
llama.cpp endpoint is authoritative; record the actual served model in notes.

List the prompt suite:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces baseline-suite'
```

Generate runnable commands:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces baseline-suite --commands --provider macmini --workspace baseline-qwen35-4b --served-model Qwen/Qwen3.5-4B'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces baseline-suite --commands --provider omen --workspace baseline-qwen36-35ba3b --served-model Qwen/Qwen3.6-35B-A3B'
```

Review each baseline run:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace baseline-qwen35-4b agent traces validate <run-id>'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace baseline-qwen35-4b agent traces stats <run-id> --json'
```

Tag observed weaknesses with labels such as `missed-skill`,
`missed-tool-search`, `wrong-tool`, `bad-subagent-use`, `tool-loop`,
`tool-error-recovery`, `spilled-output-mishandled`,
`final-answer-ungrounded`, `overthinking-or-verbosity`, and
`insufficient-reasoning`.

## Curation

Start from historical traces, then generate targeted gap-fill traces for
baseline weaknesses.

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces candidates --status completed --min-messages 6 --min-tool-calls 1 --max-tool-errors 1 --limit 100 --json'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces validate <run-id>'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='--workspace <ws> agent traces curate <run-id...> --quality high --reward 1 --split train --tag harness-intuition-v1 --approve'
```

Target 50-75 approved traces, ideally around 60 train and 12 eval. Preserve
reasoning by default, and keep tool outputs as the summaries the model saw
unless a materialized output is essential.

## Export

Export split-specific curated traces:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces export --out ./export/harness-intuition-v1/train --split train --tag harness-intuition-v1'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces export --out ./export/harness-intuition-v1/eval --split eval --tag harness-intuition-v1'
```

Verify the Tinker conversation shape:

```sh
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces verify-export ./export/harness-intuition-v1/train --json'
PATH=/opt/homebrew/bin:$PATH make cli ARGS='agent traces verify-export ./export/harness-intuition-v1/eval --json'
```

The export writes `conversations.jsonl` plus `manifest.json`. Extra agent
metadata is preserved alongside `messages` for filtering and later RL work.

## Tinker Trial Runs

After `TINKER_API_KEY` is set, start with short one-epoch SFT runs on the same
train export for both target models:

```sh
python -m tinker_cookbook.recipes.chat_sl.train \
  model_name=Qwen/Qwen3.5-4B \
  dataset=./export/harness-intuition-v1/train/conversations.jsonl \
  learning_rate=1e-4 \
  batch_size=1 \
  lora_rank=32 \
  eval_every=20 \
  save_every=20 \
  wandb_project=harness_intuition

python -m tinker_cookbook.recipes.chat_sl.train \
  model_name=Qwen/Qwen3.6-35B-A3B \
  dataset=./export/harness-intuition-v1/train/conversations.jsonl \
  learning_rate=1e-4 \
  batch_size=1 \
  lora_rank=32 \
  eval_every=20 \
  save_every=20 \
  wandb_project=harness_intuition
```

Keep the eval export untouched for post-training harness probes against the
fine-tuned checkpoints and the original non-finetuned Mac mini/Omen baselines.
