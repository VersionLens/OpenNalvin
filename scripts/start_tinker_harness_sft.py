#!/usr/bin/env python3
"""Launch a Tinker SFT run for harness-intuition traces."""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import random
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from tinker_cookbook.renderers import ToolCall, TrainOnWhat, get_renderer
from tinker_cookbook.supervised import train
from tinker_cookbook.supervised.data import conversation_to_datum
from tinker_cookbook.supervised.types import SupervisedDataset
from tinker_cookbook.tokenizer_utils import get_tokenizer


def load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key and key not in os.environ:
            os.environ[key] = value


@dataclass
class JsonlChatDataset(SupervisedDataset):
    conversations: list[list[dict[str, Any]]]
    renderer: Any
    max_length: int | None
    batch_size: int
    train_on_what: TrainOnWhat

    def _prepare_conversation(self, conversation: list[dict[str, Any]]) -> list[dict[str, Any]]:
        prepared: list[dict[str, Any]] = []
        for message in conversation:
            copied = dict(message)
            tool_calls = copied.get("tool_calls")
            if tool_calls:
                normalized_calls = []
                for call in tool_calls:
                    call = dict(call)
                    function = dict(call.get("function") or {})
                    if not isinstance(function.get("arguments"), str):
                        function["arguments"] = json.dumps(
                            function.get("arguments") or {},
                            ensure_ascii=False,
                            separators=(",", ":"),
                        )
                    call["function"] = function
                    normalized_calls.append(ToolCall.model_validate(call))
                copied["tool_calls"] = normalized_calls
            prepared.append(copied)
        return prepared

    def get_batch(self, index: int):
        start = index * self.batch_size
        end = start + self.batch_size
        return [
            conversation_to_datum(
                self._prepare_conversation(conversation),
                self.renderer,
                self.max_length,
                self.train_on_what,
            )
            for conversation in self.conversations[start:end]
        ]

    def __len__(self) -> int:
        return (len(self.conversations) + self.batch_size - 1) // self.batch_size

    def set_epoch(self, seed: int = 0):
        rng = random.Random(seed)
        rng.shuffle(self.conversations)


@dataclass
class JsonlChatDatasetBuilder:
    file_path: str
    model_name: str
    renderer_name: str
    max_length: int | None
    batch_size: int
    test_size: int
    shuffle_seed: int
    train_on_what: TrainOnWhat

    def __call__(self):
        path = Path(self.file_path)
        conversations = []
        with path.open("r", encoding="utf-8") as handle:
            for line in handle:
                if not line.strip():
                    continue
                record = json.loads(line)
                conversations.append(record["messages"])

        rng = random.Random(self.shuffle_seed)
        rng.shuffle(conversations)

        eval_conversations = conversations[: self.test_size] if self.test_size else []
        train_conversations = conversations[self.test_size :] if self.test_size else conversations

        tokenizer = get_tokenizer(self.model_name)
        renderer = get_renderer(self.renderer_name, tokenizer)

        train_dataset = JsonlChatDataset(
            train_conversations,
            renderer,
            self.max_length,
            self.batch_size,
            self.train_on_what,
        )
        eval_dataset = (
            JsonlChatDataset(
                eval_conversations,
                renderer,
                self.max_length,
                max(1, len(eval_conversations)),
                self.train_on_what,
            )
            if eval_conversations
            else None
        )
        return train_dataset, eval_dataset


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--data",
        default="export/harness-intuition-v1/tinker-qwen35-4b/conversations.turns.jsonl",
    )
    parser.add_argument("--model", default="Qwen/Qwen3.5-4B")
    parser.add_argument("--renderer", default="qwen3_5")
    parser.add_argument("--log-path", required=True)
    parser.add_argument("--test-size", type=int, default=40)
    parser.add_argument("--batch-size", type=int, default=1)
    parser.add_argument("--max-length", type=int, default=65536)
    parser.add_argument("--learning-rate", type=float, default=1e-4)
    parser.add_argument("--lora-rank", type=int, default=32)
    parser.add_argument("--num-epochs", type=int, default=1)
    parser.add_argument("--save-every", type=int, default=50)
    parser.add_argument("--eval-every", type=int, default=50)
    parser.add_argument("--max-steps", type=int, default=None)
    parser.add_argument("--shuffle-seed", type=int, default=35)
    parser.add_argument("--dotenv", default=".env")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


async def amain() -> None:
    args = parse_args()
    load_dotenv(Path(args.dotenv))
    if not os.environ.get("TINKER_API_KEY") and not args.dry_run:
        raise SystemExit("TINKER_API_KEY is not set; add it to .env or export it first")

    dataset_builder = JsonlChatDatasetBuilder(
        file_path=args.data,
        model_name=args.model,
        renderer_name=args.renderer,
        max_length=args.max_length,
        batch_size=args.batch_size,
        test_size=args.test_size,
        shuffle_seed=args.shuffle_seed,
        train_on_what=TrainOnWhat.LAST_ASSISTANT_MESSAGE,
    )

    train_dataset, eval_dataset = dataset_builder()
    print(
        json.dumps(
            {
                "model": args.model,
                "renderer": args.renderer,
                "data": args.data,
                "train_batches": len(train_dataset),
                "eval_batches": len(eval_dataset) if eval_dataset is not None else 0,
                "batch_size": args.batch_size,
                "max_length": args.max_length,
                "train_on_what": "LAST_ASSISTANT_MESSAGE",
                "log_path": args.log_path,
                "dry_run": args.dry_run,
            },
            indent=2,
        )
    )
    train_dataset.get_batch(0)
    if eval_dataset is not None:
        eval_dataset.get_batch(0)
    print("render_smoke_ok")
    if args.dry_run:
        return

    config = train.Config(
        log_path=args.log_path,
        model_name=args.model,
        renderer_name=args.renderer,
        dataset_builder=dataset_builder,
        learning_rate=args.learning_rate,
        lr_schedule="linear",
        num_epochs=args.num_epochs,
        lora_rank=args.lora_rank,
        evaluator_builders=[],
        infrequent_evaluator_builders=[],
        save_every=args.save_every,
        eval_every=args.eval_every,
        max_steps=args.max_steps,
    )
    await train.main(config)


def main() -> None:
    asyncio.run(amain())


if __name__ == "__main__":
    main()
