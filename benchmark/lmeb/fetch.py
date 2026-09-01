#!/usr/bin/env python3
"""Fetches the LMEB dataset and the official LMEB evaluation code (the MTEB
task registrations), at pinned revisions.

Sources (verified 2026-09-01):
- Paper: Zhao et al., *LMEB: Long-horizon Memory Embedding Benchmark*,
  arXiv:2603.12572. <https://arxiv.org/abs/2603.12572>
- Official repo: <https://github.com/KaLM-Embedding/LMEB> (Apache-2.0 for the
  evaluation code itself; per-dataset content licenses vary, see README.md).
- Dataset: <https://huggingface.co/datasets/KaLM-Embedding/LMEB>, pinned at
  the revision recorded in benchmark.toml [dataset].revision.

LMEB is not pip-installable: its 22 datasets are registered as MTEB tasks by
importing its `src` package (see run.py), so the whole repo is cloned rather
than vendored (ADR-0003). The dataset is the repo's own queries/corpus/qrels
tree, laid out as <MemType>/<Dataset>/<subset>/*.jsonl - exactly what the
cloned data/official/src/abstasks/LocalRetrieval.py expects under
LOCAL_DATA_PREFIX - so it is fetched as one snapshot rather than file-by-file
(there is no single "the dataset file" the way LoCoMo/LongMemEval have one).

Idempotent: re-running skips work that is already done. All logging goes to
stderr; the last stdout line is the fetch protocol JSON
({"name", "revision", "sha256"}). The full per-file manifest is cached at
data/fetch_manifest.json so run.py (and repeat fetches) don't have to re-hash
a multi-thousand-file tree on every invocation.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
import sys
import tomllib
from pathlib import Path

from huggingface_hub import snapshot_download

HERE = Path(__file__).resolve().parent
DATA_DIR = HERE / "data"
EVAL_DATA_DIR = DATA_DIR / "eval_data"
OFFICIAL_DIR = DATA_DIR / "official"
MANIFEST_PATH = DATA_DIR / "fetch_manifest.json"

DATASET_REPO_ID = "KaLM-Embedding/LMEB"

# Pinned at the tip of the official repo's `main` branch (the "Update README
# with MTEB support for LMEB" commit, which lands within a minute of the HF
# dataset revision above being published - both are the same release).
# Verified via the GitHub commits API on 2026-09-01: no commit after this one
# exists on `main` as of that date.
OFFICIAL_REPO_URL = "https://github.com/KaLM-Embedding/LMEB.git"
OFFICIAL_REPO_SHA = "a02ae842598183ed162fc90c58ffaae5eec89f12"


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


def hash_tree(root: Path) -> dict[str, str]:
    """sha256 of every file under root, keyed by root-relative posix path."""
    files: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        if path.is_file():
            files[path.relative_to(root).as_posix()] = sha256_file(path)
    return files


def fetch_dataset(revision: str) -> dict[str, str]:
    if MANIFEST_PATH.exists():
        cached = json.loads(MANIFEST_PATH.read_text())
        if cached.get("revision") == revision and cached.get("files"):
            log(f"[fetch] eval_data already present at revision {revision}, skipping download")
            return cached["files"]

    DATA_DIR.mkdir(parents=True, exist_ok=True)
    log(f"[fetch] downloading {DATASET_REPO_ID}@{revision} into {EVAL_DATA_DIR}")
    snapshot_download(
        repo_id=DATASET_REPO_ID,
        repo_type="dataset",
        revision=revision,
        local_dir=str(EVAL_DATA_DIR),
    )
    log("[fetch] hashing downloaded tree (cached in data/fetch_manifest.json)")
    return hash_tree(EVAL_DATA_DIR)


def current_commit(repo_dir: Path) -> str | None:
    if not (repo_dir / ".git").exists():
        return None
    proc = subprocess.run(
        ["git", "-C", str(repo_dir), "rev-parse", "HEAD"],
        capture_output=True,
        text=True,
    )
    return proc.stdout.strip() if proc.returncode == 0 else None


def fetch_official_repo() -> None:
    if current_commit(OFFICIAL_DIR) == OFFICIAL_REPO_SHA:
        log(f"[fetch] official repo already checked out at {OFFICIAL_REPO_SHA}, skipping clone")
        return
    if OFFICIAL_DIR.exists():
        shutil.rmtree(OFFICIAL_DIR)
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    log(f"[fetch] cloning {OFFICIAL_REPO_URL}@{OFFICIAL_REPO_SHA}")
    subprocess.run(["git", "clone", OFFICIAL_REPO_URL, str(OFFICIAL_DIR)], check=True)
    subprocess.run(["git", "-C", str(OFFICIAL_DIR), "checkout", OFFICIAL_REPO_SHA], check=True)
    got = current_commit(OFFICIAL_DIR)
    if got != OFFICIAL_REPO_SHA:
        raise RuntimeError(f"official checkout landed on {got!r}, expected {OFFICIAL_REPO_SHA!r}")
    log(f"[fetch] checked out {OFFICIAL_REPO_SHA}")


def main() -> None:
    manifest = load_manifest()
    revision = manifest["dataset"]["revision"]

    files = fetch_dataset(revision)
    fetch_official_repo()

    combined_sha256 = hashlib.sha256(json.dumps(files, sort_keys=True).encode("utf-8")).hexdigest()

    result = {
        "name": "lmeb",
        "revision": revision,
        "sha256": combined_sha256,
        "file_count": len(files),
        "official_repo": {"url": OFFICIAL_REPO_URL, "commit": OFFICIAL_REPO_SHA},
    }
    MANIFEST_PATH.write_text(json.dumps({**result, "files": files}))
    print(json.dumps(result))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 - protocol requires a clean nonzero exit
        log(f"fetch failed: {exc}")
        sys.exit(1)
