#!/usr/bin/env python3
"""Fetches the LoCoMo dataset and the official evaluation code.

Primary sources (see README.md for the full list):
- Repo: https://github.com/snap-research/locomo, pinned to the commit in
  benchmark.toml [dataset].revision.
- Dataset file: data/locomo10.json in that repo (confirmed from the repo's
  README: "The dataset can be found in the `./data/locomo10.json` file").
- License: LICENSE.txt at the repo root is CC BY-NC 4.0 International and
  covers the whole repository (there is no separate code license). The
  dataset must never be committed or redistributed (ADR-0002). The cloned
  copy of the repo under data/official/ is used only to invoke its scoring
  code in-process from score.py; it is never vendored into this repo (see
  ADR-0003) and stays gitignored alongside data/locomo10.json.

Idempotent: re-running skips work that is already done and correct.
Protocol: last stdout line is {"name", "revision", "sha256"}; all other
output goes to stderr; exits non-zero on failure.
"""
import hashlib
import json
import shutil
import subprocess
import sys
import urllib.request
from pathlib import Path

REPO_URL = "https://github.com/snap-research/locomo"
REVISION = "3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376"
DATASET_NAME = "locomo10"
RAW_URL = f"https://raw.githubusercontent.com/snap-research/locomo/{REVISION}/data/locomo10.json"

HERE = Path(__file__).resolve().parent
DATA_DIR = HERE / "data"
DATASET_PATH = DATA_DIR / "locomo10.json"
OFFICIAL_DIR = DATA_DIR / "official"


def log(msg: str) -> None:
    print(msg, file=sys.stderr)


def sha256_of(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def fetch_dataset() -> None:
    if DATASET_PATH.exists():
        log(f"dataset already present at {DATASET_PATH}")
        return
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    log(f"downloading {RAW_URL}")
    tmp = DATASET_PATH.with_suffix(".json.tmp")
    urllib.request.urlretrieve(RAW_URL, tmp)
    tmp.rename(DATASET_PATH)
    log(f"saved to {DATASET_PATH}")


def current_commit(repo_dir: Path) -> str | None:
    if not (repo_dir / ".git").exists():
        return None
    proc = subprocess.run(
        ["git", "-C", str(repo_dir), "rev-parse", "HEAD"],
        capture_output=True,
        text=True,
    )
    return proc.stdout.strip() if proc.returncode == 0 else None


def clone_official() -> None:
    if current_commit(OFFICIAL_DIR) == REVISION:
        log(f"official repo already checked out at {REVISION}")
        return
    if OFFICIAL_DIR.exists():
        shutil.rmtree(OFFICIAL_DIR)
    OFFICIAL_DIR.mkdir(parents=True, exist_ok=True)
    log(f"cloning {REPO_URL} at {REVISION} into {OFFICIAL_DIR}")
    subprocess.run(["git", "init", "-q", str(OFFICIAL_DIR)], check=True)
    subprocess.run(
        ["git", "-C", str(OFFICIAL_DIR), "remote", "add", "origin", REPO_URL],
        check=True,
    )
    subprocess.run(
        ["git", "-C", str(OFFICIAL_DIR), "fetch", "-q", "--depth", "1", "origin", REVISION],
        check=True,
    )
    subprocess.run(["git", "-C", str(OFFICIAL_DIR), "checkout", "-q", "FETCH_HEAD"], check=True)
    got = current_commit(OFFICIAL_DIR)
    if got != REVISION:
        raise RuntimeError(f"official checkout landed on {got!r}, expected {REVISION!r}")
    log(f"checked out {REVISION}")


def main() -> None:
    fetch_dataset()
    clone_official()
    result = {
        "name": DATASET_NAME,
        "revision": REVISION,
        "sha256": sha256_of(DATASET_PATH),
    }
    print(json.dumps(result))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 - protocol requires a clean nonzero exit
        log(f"fetch failed: {exc}")
        sys.exit(1)
