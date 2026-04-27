#!/usr/bin/env python3
"""Consolidate exported agent trace JSONL shards into one deduplicated JSONL.

The script is intentionally metadata-preserving: every output record keeps its
original `messages` and `agent_metadata`, then adds consolidation metadata with
the source files/categories that contained the record. This lets us clean up
ad-hoc export folders without losing provenance.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


DEFAULT_SKIP_DIRS = {
    "consolidated",
    "archive",
    "__pycache__",
}

PREFERRED_SOURCE_PRIORITY = {
    "all-plus-deepseek": 100,
    "all-plus-docker-skillrunner": 90,
    "all-plus-multiturn": 80,
    "all-plus-gapfill": 70,
    "all": 60,
    "code-glm": 50,
    "docker-skillrunner-glm": 50,
    "gapfill-glm": 50,
    "multiturn-web-glm": 50,
    "deepseek-opencode-policy": 50,
    "combined": 40,
    "glm": 30,
    "kimi": 30,
    "initial-five": 20,
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Consolidate exported agent trace JSONL shards.",
    )
    parser.add_argument(
        "--root",
        type=Path,
        default=Path("export"),
        help="Export root to scan. Defaults to ./export.",
    )
    parser.add_argument(
        "--out",
        type=Path,
        default=Path("export/harness-intuition-v1/consolidated"),
        help="Output directory for conversations.jsonl and manifest.json.",
    )
    parser.add_argument(
        "--include",
        action="append",
        default=[],
        help="Additional conversations.jsonl path to include. Repeatable.",
    )
    return parser.parse_args()


def category_for(path: Path, root: Path) -> str:
    rel = path.relative_to(root)
    parts = rel.parts
    if parts == ("conversations.jsonl",):
        return "initial-five"
    if "raw-exports" in parts:
        idx = parts.index("raw-exports")
        if idx + 1 < len(parts):
            return parts[idx + 1]
    if "latest-batch" in parts:
        idx = parts.index("latest-batch")
        if idx + 1 < len(parts):
            return parts[idx + 1]
    if len(parts) >= 2:
        return parts[-2]
    return "uncategorized"


def should_scan(path: Path, out_dir: Path) -> bool:
    if path.name != "conversations.jsonl":
        return False
    try:
        path.relative_to(out_dir)
        return False
    except ValueError:
        pass
    return not any(part in DEFAULT_SKIP_DIRS for part in path.parts)


def discover_inputs(root: Path, out_dir: Path, includes: list[str]) -> list[Path]:
    paths = {path for path in root.rglob("conversations.jsonl") if should_scan(path, out_dir)}
    for item in includes:
        paths.add(Path(item))
    return sorted(path.resolve() for path in paths if path.exists())


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def record_key(record: dict[str, Any]) -> str:
    metadata = record.get("agent_metadata") or {}
    for field in ("curation_id", "source_run_id", "id"):
        value = metadata.get(field)
        if isinstance(value, str) and value.strip():
            return f"{field}:{value.strip()}"
    digest = hashlib.sha256(canonical_json(record.get("messages", [])).encode("utf-8")).hexdigest()
    return f"messages_sha256:{digest}"


def source_priority(categories: set[str]) -> int:
    return max((PREFERRED_SOURCE_PRIORITY.get(category, 0) for category in categories), default=0)


def merge_record(existing: dict[str, Any] | None, incoming: dict[str, Any]) -> dict[str, Any]:
    if existing is None:
        return incoming

    existing_meta = existing.setdefault("agent_metadata", {})
    incoming_meta = incoming.get("agent_metadata") or {}
    existing_consolidation = existing_meta.setdefault("consolidation", {})
    incoming_consolidation = incoming_meta.get("consolidation") or {}

    existing_sources = set(existing_consolidation.get("source_files") or [])
    incoming_sources = set(incoming_consolidation.get("source_files") or [])
    existing_categories = set(existing_consolidation.get("categories") or [])
    incoming_categories = set(incoming_consolidation.get("categories") or [])

    merged_sources = sorted(existing_sources | incoming_sources)
    merged_categories = sorted(existing_categories | incoming_categories)

    existing_score = source_priority(existing_categories)
    incoming_score = source_priority(incoming_categories)
    winner = incoming if incoming_score > existing_score else existing
    winner_meta = winner.setdefault("agent_metadata", {})
    winner_consolidation = winner_meta.setdefault("consolidation", {})
    winner_consolidation["source_files"] = merged_sources
    winner_consolidation["categories"] = merged_categories
    winner_consolidation["duplicate_count"] = len(merged_sources)
    return winner


def load_records(paths: list[Path], root: Path) -> tuple[dict[str, dict[str, Any]], dict[str, int], list[str]]:
    records: dict[str, dict[str, Any]] = {}
    source_counts: dict[str, int] = {}
    errors: list[str] = []

    for path in paths:
        category = category_for(path, root)
        rel_path = str(path.relative_to(root.resolve()) if path.is_relative_to(root.resolve()) else path)
        with path.open("r", encoding="utf-8") as handle:
            for line_number, line in enumerate(handle, start=1):
                line = line.strip()
                if not line:
                    continue
                source_counts[rel_path] = source_counts.get(rel_path, 0) + 1
                try:
                    record = json.loads(line)
                except json.JSONDecodeError as exc:
                    errors.append(f"{rel_path}:{line_number}: {exc}")
                    continue
                if not isinstance(record, dict) or "messages" not in record:
                    errors.append(f"{rel_path}:{line_number}: missing messages")
                    continue

                metadata = record.setdefault("agent_metadata", {})
                consolidation = metadata.setdefault("consolidation", {})
                consolidation["source_files"] = [rel_path]
                consolidation["categories"] = [category]
                consolidation["duplicate_count"] = 1

                key = record_key(record)
                records[key] = merge_record(records.get(key), record)

    return records, source_counts, errors


def sort_key(item: tuple[str, dict[str, Any]]) -> tuple[str, str, str]:
    key, record = item
    metadata = record.get("agent_metadata") or {}
    provider = str(metadata.get("provider") or "")
    title = str(metadata.get("title") or metadata.get("prompt") or "")
    return provider, title, key


def write_outputs(
    records: dict[str, dict[str, Any]],
    source_counts: dict[str, int],
    errors: list[str],
    inputs: list[Path],
    root: Path,
    out_dir: Path,
) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    conversations_path = out_dir / "conversations.jsonl"
    manifest_path = out_dir / "manifest.json"

    category_counts: Counter[str] = Counter()
    provider_counts: Counter[str] = Counter()
    tag_counts: Counter[str] = Counter()
    split_counts: Counter[str] = Counter()
    duplicate_histogram: Counter[int] = Counter()

    with conversations_path.open("w", encoding="utf-8") as handle:
        for _, record in sorted(records.items(), key=sort_key):
            metadata = record.get("agent_metadata") or {}
            consolidation = metadata.get("consolidation") or {}
            for category in consolidation.get("categories") or []:
                category_counts[str(category)] += 1
            for tag in metadata.get("tags") or []:
                tag_counts[str(tag)] += 1
            provider_counts[str(metadata.get("provider") or "unknown")] += 1
            split_counts[str(metadata.get("split") or "unspecified")] += 1
            duplicate_histogram[int(consolidation.get("duplicate_count") or 1)] += 1
            handle.write(json.dumps(record, ensure_ascii=False, separators=(",", ":")) + "\n")

    manifest = {
        "created_at": datetime.now(timezone.utc).isoformat(),
        "root": str(root),
        "output": {
            "conversations": str(conversations_path),
            "manifest": str(manifest_path),
        },
        "input_files": [str(path.relative_to(root.resolve()) if path.is_relative_to(root.resolve()) else path) for path in inputs],
        "source_line_counts": dict(sorted(source_counts.items())),
        "record_count": len(records),
        "category_counts": dict(sorted(category_counts.items())),
        "provider_counts": dict(sorted(provider_counts.items())),
        "split_counts": dict(sorted(split_counts.items())),
        "tag_counts": dict(sorted(tag_counts.items())),
        "duplicate_source_count_histogram": dict(sorted((str(k), v) for k, v in duplicate_histogram.items())),
        "errors": errors,
    }
    manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()
    root = args.root.resolve()
    out_dir = args.out.resolve()
    inputs = discover_inputs(root, out_dir, args.include)
    records, source_counts, errors = load_records(inputs, root)
    write_outputs(records, source_counts, errors, inputs, root, out_dir)
    print(f"inputs={len(inputs)} records={len(records)} errors={len(errors)} out={out_dir}")
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
