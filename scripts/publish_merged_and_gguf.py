#!/usr/bin/env python3
"""Merge a PEFT LoRA into a HF base model, convert to GGUF, and publish.

This script is intentionally resumable: each stage skips existing artifacts
unless --force is supplied.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
from pathlib import Path

from huggingface_hub import HfApi


def load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        os.environ.setdefault(key.strip(), value.strip().strip('"').strip("'"))


def run(cmd: list[str], *, cwd: Path | None = None) -> None:
    print("+", " ".join(cmd), flush=True)
    subprocess.run(cmd, cwd=cwd, check=True)


def write_readme(path: Path, *, title: str, base_model: str, adapter_repo: str, kind: str) -> None:
    path.write_text(
        f"""---
base_model: {base_model}
tags:
- nalvin
- harness-intuition
- tinker
- lora
- sft
- qwen
- {kind}
---

# {title}

This model was produced from the harness-intuition Tinker SFT run.

- Base model: `{base_model}`
- Adapter source: `{adapter_repo}`
- Dataset: `<your-hf-namespace>/harness-intuition-v1`
- Merge/quantization script: `scripts/publish_merged_and_gguf.py`

The LoRA was merged into the base weights before publishing this artifact.
""",
        encoding="utf-8",
    )


def prepare_adapter(adapter_dir: Path, base_model: str, work_dir: Path) -> Path:
    prepared = work_dir / "adapter"
    if prepared.exists():
        shutil.rmtree(prepared)
    shutil.copytree(adapter_dir, prepared)
    cfg_path = prepared / "adapter_config.json"
    cfg = json.loads(cfg_path.read_text())
    cfg["base_model_name_or_path"] = base_model
    cfg_path.write_text(json.dumps(cfg, indent=2) + "\n")
    return prepared


def merge(args: argparse.Namespace, prepared_adapter: Path) -> None:
    if (args.merged_dir / "model.safetensors.index.json").exists() and not args.force:
        print(f"merged model exists: {args.merged_dir}")
        return
    args.merged_dir.mkdir(parents=True, exist_ok=True)
    code = r"""
import os
from pathlib import Path
import torch
from transformers import AutoModelForCausalLM, AutoTokenizer
from peft import PeftModel

base_model = os.environ["BASE_MODEL"]
adapter_dir = os.environ["ADAPTER_DIR"]
out_dir = os.environ["MERGED_DIR"]

model = AutoModelForCausalLM.from_pretrained(
    base_model,
    torch_dtype=torch.bfloat16,
    device_map="cpu",
    low_cpu_mem_usage=True,
    trust_remote_code=True,
)
model = PeftModel.from_pretrained(model, adapter_dir)
model = model.merge_and_unload()
model.save_pretrained(out_dir, safe_serialization=True, max_shard_size="5GB")
tok = AutoTokenizer.from_pretrained(base_model, trust_remote_code=True)
tok.save_pretrained(out_dir)
"""
    env = os.environ.copy()
    env.update(
        {
            "BASE_MODEL": args.base_model,
            "ADAPTER_DIR": str(prepared_adapter),
            "MERGED_DIR": str(args.merged_dir),
            "HF_HUB_DISABLE_XET": "1",
        }
    )
    subprocess.run([str(args.python), "-c", code], check=True, env=env)
    write_readme(
        args.merged_dir / "README.md",
        title=args.merged_title,
        base_model=args.base_model,
        adapter_repo=args.adapter_repo,
        kind="merged",
    )


def upload_folder(api: HfApi, folder: Path, repo_id: str, private: bool) -> None:
    api.create_repo(repo_id, repo_type="model", private=private, exist_ok=True)
    api.upload_folder(
        repo_id=repo_id,
        repo_type="model",
        folder_path=str(folder),
        commit_message="Upload merged harness-intuition artifact",
    )


def patch_llama_cpp_tokenizer_hashes(llama_cpp: Path) -> None:
    """Patch missing upstream tokenizer hashes needed by current Qwen releases."""
    converter = llama_cpp / "convert_hf_to_gguf.py"
    if not converter.exists():
        raise SystemExit(f"missing llama.cpp converter: {converter}")

    text = converter.read_text(encoding="utf-8")
    qwen35_4b_hash = "1444df51289cfa8063b96f0e62b1125440111bc79a52003ea14b6eac7016fd5f"
    if qwen35_4b_hash in text:
        return

    needle = (
        '    if chkhsh == "d30d75d9059f1aa2c19359de71047b3ae408c70875e8a3ccf8c5fba56c9d8af4":\n'
        '        # ref: https://huggingface.co/Qwen/Qwen3.5-9B-Instruct\n'
        '        res = "qwen35"\n'
    )
    patch = needle + (
        f'    if chkhsh == "{qwen35_4b_hash}":\n'
        '        # ref: https://huggingface.co/Qwen/Qwen3.5-4B\n'
        '        res = "qwen35"\n'
    )
    if needle not in text:
        print("warning: qwen35 tokenizer patch anchor not found; leaving llama.cpp converter unchanged", flush=True)
        return
    converter.write_text(text.replace(needle, patch), encoding="utf-8")


def convert_gguf(args: argparse.Namespace) -> None:
    patch_llama_cpp_tokenizer_hashes(args.llama_cpp)
    if args.f16_gguf.exists() and not args.force:
        print(f"f16/bf16 gguf exists: {args.f16_gguf}")
    else:
        args.f16_gguf.parent.mkdir(parents=True, exist_ok=True)
        run(
            [
                str(args.python),
                str(args.llama_cpp / "convert_hf_to_gguf.py"),
                str(args.merged_dir),
                "--outfile",
                str(args.f16_gguf),
                "--outtype",
                args.gguf_outtype,
            ]
        )
    if args.q4_gguf.exists() and not args.force:
        print(f"q4 gguf exists: {args.q4_gguf}")
    else:
        args.q4_gguf.parent.mkdir(parents=True, exist_ok=True)
        run([str(args.quantize), str(args.f16_gguf), str(args.q4_gguf), args.quant])
    write_readme(
        args.gguf_upload_dir / "README.md",
        title=args.gguf_title,
        base_model=args.base_model,
        adapter_repo=args.adapter_repo,
        kind="gguf",
    )
    if args.gguf_upload_file.exists() or args.gguf_upload_file.is_symlink():
        args.gguf_upload_file.unlink()
    try:
        os.link(args.q4_gguf, args.gguf_upload_file)
    except OSError:
        shutil.copy2(args.q4_gguf, args.gguf_upload_file)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-model", required=True)
    parser.add_argument("--adapter-dir", type=Path, required=True)
    parser.add_argument("--adapter-repo", required=True)
    parser.add_argument("--work-dir", type=Path, required=True)
    parser.add_argument("--merged-repo", required=True)
    parser.add_argument("--gguf-repo", required=True)
    parser.add_argument("--merged-title", required=True)
    parser.add_argument("--gguf-title", required=True)
    parser.add_argument("--python", type=Path, required=True)
    parser.add_argument("--llama-cpp", type=Path, required=True)
    parser.add_argument("--private", action="store_true", default=True)
    parser.add_argument("--public", dest="private", action="store_false")
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--skip-merge", action="store_true")
    parser.add_argument("--skip-upload-merged", action="store_true")
    parser.add_argument("--skip-gguf", action="store_true")
    parser.add_argument("--skip-upload-gguf", action="store_true")
    parser.add_argument("--gguf-outtype", default="bf16")
    parser.add_argument("--quant", default="Q4_K_M")
    args = parser.parse_args()

    load_dotenv(Path(".env"))
    token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGING_FACE_ACCESS_TOKEN")
    if not token:
        raise SystemExit("HF_TOKEN is required")
    os.environ.setdefault("HF_HUB_DISABLE_XET", "1")

    args.work_dir.mkdir(parents=True, exist_ok=True)
    args.merged_dir = args.work_dir / "merged-hf"
    args.gguf_dir = args.work_dir / "gguf"
    args.gguf_upload_dir = args.work_dir / "gguf-upload"
    args.f16_gguf = args.gguf_dir / "model.bf16.gguf"
    args.q4_gguf = args.gguf_dir / f"model.{args.quant.lower()}.gguf"
    args.gguf_upload_file = args.gguf_upload_dir / args.q4_gguf.name
    args.quantize = args.llama_cpp / "build" / "bin" / "llama-quantize"
    args.gguf_upload_dir.mkdir(parents=True, exist_ok=True)

    prepared_adapter = prepare_adapter(args.adapter_dir, args.base_model, args.work_dir)
    if not args.skip_merge:
        merge(args, prepared_adapter)

    api = HfApi(token=token)
    if not args.skip_upload_merged:
        upload_folder(api, args.merged_dir, args.merged_repo, args.private)
    if not args.skip_gguf:
        convert_gguf(args)
    if not args.skip_upload_gguf:
        upload_folder(api, args.gguf_upload_dir, args.gguf_repo, args.private)

    print("merged:", f"https://huggingface.co/{args.merged_repo}")
    print("gguf:", f"https://huggingface.co/{args.gguf_repo}")


if __name__ == "__main__":
    main()
