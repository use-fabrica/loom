#!/usr/bin/env python3
"""Fetch the LongMemEval_S and LongMemEval_Oracle configs plus the official
evaluation code, at pinned revisions.

Sources (verified 2026-09-01):
- Dataset: https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned
- Official repo: https://github.com/xiaowu0162/LongMemEval (MIT license)

Idempotent: re-running skips files/clone already present at the pinned
revision. All logging goes to stderr; the last stdout line is the fetch
protocol JSON.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
import sys
import tomllib
from pathlib import Path

from huggingface_hub import hf_hub_download

HERE = Path(__file__).resolve().parent
DATA_DIR = HERE / "data"
OFFICIAL_DIR = DATA_DIR / "official"

# Pinned at the tip of the official repo's `main` branch, verified via
# `git ls-remote https://github.com/xiaowu0162/LongMemEval.git main` on
# 2026-09-01.
OFFICIAL_REPO_URL = "https://github.com/xiaowu0162/LongMemEval.git"
OFFICIAL_REPO_SHA = "9e0b455f4ef0e2ab8f2e582289761153549043fc"

# Exact filenames in the xiaowu0162/longmemeval-cleaned HF dataset repo
# (verified via the HF API `siblings` listing on 2026-09-01). Only the S and
# Oracle configs are fetched; the M config is out of scope for this adapter.
DATASET_FILES = ["longmemeval_s_cleaned.json", "longmemeval_oracle.json"]


def log(*args: object) -> None:
    print(*args, file=sys.stderr)


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def load_manifest() -> dict:
    with open(HERE / "benchmark.toml", "rb") as f:
        return tomllib.load(f)


def fetch_dataset(revision: str) -> dict[str, str]:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    files: dict[str, str] = {}
    for filename in DATASET_FILES:
        dest = DATA_DIR / filename
        if dest.exists():
            log(f"[fetch] {filename} already present, skipping download")
        else:
            log(f"[fetch] downloading {filename}@{revision}")
            downloaded = hf_hub_download(
                repo_id="xiaowu0162/longmemeval-cleaned",
                filename=filename,
                repo_type="dataset",
                revision=revision,
                local_dir=str(DATA_DIR),
            )
            dest = Path(downloaded)
        files[filename] = sha256_file(dest)
    return files


def fetch_official_repo() -> None:
    if OFFICIAL_DIR.exists():
        head = subprocess.run(
            ["git", "-C", str(OFFICIAL_DIR), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
        if head == OFFICIAL_REPO_SHA:
            log("[fetch] official repo already checked out at pinned SHA, skipping clone")
            return
        log(f"[fetch] official repo at {head}, expected {OFFICIAL_REPO_SHA}; re-cloning")
        shutil.rmtree(OFFICIAL_DIR)

    log(f"[fetch] cloning {OFFICIAL_REPO_URL}@{OFFICIAL_REPO_SHA}")
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    subprocess.run(["git", "clone", OFFICIAL_REPO_URL, str(OFFICIAL_DIR)], check=True)
    subprocess.run(["git", "-C", str(OFFICIAL_DIR), "checkout", OFFICIAL_REPO_SHA], check=True)


def main() -> None:
    manifest = load_manifest()
    revision = manifest["dataset"]["revision"]

    files = fetch_dataset(revision)
    fetch_official_repo()

    combined_sha256 = hashlib.sha256(json.dumps(files, sort_keys=True).encode("utf-8")).hexdigest()

    result = {
        "name": "longmemeval",
        "revision": revision,
        "sha256": combined_sha256,
        "files": files,
        "official_repo": {"url": OFFICIAL_REPO_URL, "commit": OFFICIAL_REPO_SHA},
    }
    print(json.dumps(result))


if __name__ == "__main__":
    main()
