"""Shared constants and helpers for the BEAM benchmark adapter (fetch.py, score.py).

Primary sources read to establish the pins below (2026-09-01) -- see README.md
for the full provenance write-up:
- Paper: https://arxiv.org/abs/2510.27246
  (Tavakoli et al., "Beyond a Million Tokens: Benchmarking and Enhancing
  Long-Term Memory in LLMs", ICLR 2026)
- Code:  https://github.com/mohammadtavakoli78/BEAM
- Data:  https://huggingface.co/datasets/Mohammadta/BEAM
"""
from __future__ import annotations

import sys
import tomllib
from pathlib import Path

BEAM_DIR = Path(__file__).resolve().parent
DATA_DIR = BEAM_DIR / "data"
OFFICIAL_DIR = DATA_DIR / "official"
MANIFEST_PATH = DATA_DIR / "manifest.json"
BENCHMARK_TOML_PATH = BEAM_DIR / "benchmark.toml"

# --- Hugging Face dataset -----------------------------------------------
# The pinned commit itself lives in benchmark.toml's [dataset].revision (the
# harness-facing manifest is the single source of truth for it); this module
# only fixes the repo id and the tier -> split/file-prefix mapping.
HF_REPO_ID = "Mohammadta/BEAM"

# our tier name -> HF split name / parquet filename prefix (see
# `data/{split}-*.parquet` under https://huggingface.co/datasets/Mohammadta/BEAM).
# The dataset's own split names (and the official repo's `chats/100K/` local
# copy) say "100K" where the paper prose and its own README say "128K" for
# the very same tier -- see README.md "Tier naming" for the full explanation.
# Only the two tiers below are fetched (session decision: 1M next, 10M is a
# later milestone via the separate `Mohammadta/BEAM-10M` dataset).
TIER_TO_SPLIT = {"128k": "100K", "500k": "500K"}

# --- Official evaluation code --------------------------------------------
GIT_REPO_URL = "https://github.com/mohammadtavakoli78/BEAM.git"
# main @ GitHub, read via `gh api repos/mohammadtavakoli78/BEAM/commits/main`
# on 2026-09-01.
GIT_REVISION = "b2da22eac88bb0874c64665f13457eb99835774a"

# The ten memory abilities: exact ability keys as used throughout the
# official code (src/evaluation/report_results.py `column_names`, and every
# probing_questions.json / evaluate_* dispatch in src/evaluation/*.py).
ABILITY_KEYS = (
    "abstention",
    "contradiction_resolution",
    "event_ordering",
    "information_extraction",
    "instruction_following",
    "knowledge_update",
    "multi_session_reasoning",
    "preference_following",
    "summarization",
    "temporal_reasoning",
)


def log(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


def load_benchmark_toml() -> dict:
    with BENCHMARK_TOML_PATH.open("rb") as fh:
        return tomllib.load(fh)


def hf_revision() -> str:
    """The pinned Mohammadta/BEAM commit, read from benchmark.toml [dataset].revision."""
    return load_benchmark_toml()["dataset"]["revision"]
