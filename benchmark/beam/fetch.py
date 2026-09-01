#!/usr/bin/env python3
"""Fetch the BEAM benchmark: the 128K and 500K dataset tiers, plus the
official evaluation code, pinned to the revisions recorded in benchmark.toml
and _common.py.

Idempotent: re-running skips a dataset shard already present at the pinned
revision (huggingface_hub.snapshot_download resolves by content hash) and
skips the git clone when data/official/ is already checked out at
GIT_REVISION.

All downloads land in data/ (gitignored, never committed).

Primary sources read to establish these pins (2026-09-01):
- Paper: https://arxiv.org/abs/2510.27246
- Code:  https://github.com/mohammadtavakoli78/BEAM
- Data:  https://huggingface.co/datasets/Mohammadta/BEAM
See README.md for the full provenance write-up (judge model, tier naming,
licensing, answers-file format).
"""
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import subprocess
from pathlib import Path

from huggingface_hub import snapshot_download

from _common import (
    DATA_DIR,
    GIT_REPO_URL,
    GIT_REVISION,
    HF_REPO_ID,
    MANIFEST_PATH,
    OFFICIAL_DIR,
    TIER_TO_SPLIT,
    hf_revision,
    log,
)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fetch_dataset(revision: str) -> list[Path]:
    """Download only the 128K ("100K" split) and 500K parquet shards.

    Session decision: the 1M tier (also part of this same HF dataset) is
    planned next; the 10M tier -- a separate `Mohammadta/BEAM-10M` dataset --
    is a later milestone. Neither is fetched here.
    """
    allow_patterns = [f"data/{split}-*.parquet" for split in TIER_TO_SPLIT.values()]
    log(f"Fetching {HF_REPO_ID}@{revision} tiers={sorted(TIER_TO_SPLIT)} -> {DATA_DIR / 'beam'}")
    local_dir = snapshot_download(
        repo_id=HF_REPO_ID,
        repo_type="dataset",
        revision=revision,
        allow_patterns=allow_patterns,
        local_dir=DATA_DIR / "beam",
    )
    files = sorted(Path(local_dir).glob("data/*.parquet"))
    if not files:
        raise RuntimeError(f"no parquet shards matched {allow_patterns} under {local_dir}")
    for f in files:
        log(f"  {f.relative_to(DATA_DIR)} ({f.stat().st_size:,} bytes)")
    return files


def clone_official_repo() -> None:
    """Idempotently clone the official BEAM repo at GIT_REVISION into data/official/."""
    if (OFFICIAL_DIR / ".git").exists():
        head = subprocess.run(
            ["git", "-C", str(OFFICIAL_DIR), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
        if head == GIT_REVISION:
            log(f"data/official already at {GIT_REVISION[:12]}, skipping clone")
            return
        log(f"data/official at {head[:12]} != pinned {GIT_REVISION[:12]}; re-cloning")
        shutil.rmtree(OFFICIAL_DIR)

    OFFICIAL_DIR.parent.mkdir(parents=True, exist_ok=True)
    log(f"Cloning {GIT_REPO_URL}@{GIT_REVISION[:12]} -> {OFFICIAL_DIR}")
    subprocess.run(
        ["git", "clone", "--quiet", "--no-checkout", GIT_REPO_URL, str(OFFICIAL_DIR)],
        check=True,
    )
    subprocess.run(
        ["git", "-C", str(OFFICIAL_DIR), "checkout", "--quiet", GIT_REVISION],
        check=True,
    )


def write_manifest(revision: str, parquet_files: list[Path]) -> dict:
    entries = sorted(
        ({"path": str(p.relative_to(DATA_DIR)), "sha256": sha256_file(p)} for p in parquet_files),
        key=lambda e: e["path"],
    )
    combined = hashlib.sha256(json.dumps(entries, sort_keys=True).encode("utf-8")).hexdigest()
    manifest = {
        "name": "beam",
        "revision": revision,
        "sha256": combined,
        "tiers": sorted(TIER_TO_SPLIT),
        "official_repo": {"url": GIT_REPO_URL, "revision": GIT_REVISION},
        "files": entries,
    }
    MANIFEST_PATH.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return manifest


def main() -> int:
    argparse.ArgumentParser(description=__doc__).parse_args()
    DATA_DIR.mkdir(parents=True, exist_ok=True)

    try:
        revision = hf_revision()
        parquet_files = fetch_dataset(revision)
        clone_official_repo()
        manifest = write_manifest(revision, parquet_files)
    except Exception as exc:  # surface any failure with a nonzero exit
        log(f"fetch failed: {exc}")
        return 1

    log(
        f"OK: {len(manifest['files'])} parquet file(s) across tiers {manifest['tiers']}, "
        f"official repo @ {GIT_REVISION[:12]}, dataset sha256={manifest['sha256'][:12]}"
    )
    print(json.dumps({"name": manifest["name"], "revision": manifest["revision"], "sha256": manifest["sha256"]}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
