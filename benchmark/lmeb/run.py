#!/usr/bin/env python3
"""Runs the LMEB component benchmark against a candidate embedding model.

Loads the tier's task list from subsets.toml (ADR-0004: `selection` names
LMEB's non-overlapping subsets - the only tier that may drive embedding-model
choice; `diagnostic` is LoCoMo/LongMemEval-derived and is reported for
visibility only, never a selection criterion), imports the official LMEB
task registrations cloned by fetch.py into data/official, and drives the
standard MTEB harness the same way the official run_lmeb.py does:
`mteb.get_tasks(...)` + `mteb.MTEB(tasks=...).run(model, ...)`
(scripts/run_lmeb_wo_inst.sh in the official repo). Model loading uses
mteb's own `mteb.get_model()` (portable CPU/GPU sentence-transformers
loading) rather than the official repo's GPU-only STWrapper
(src/embedding_models_wrapper.py asserts n_gpu > 0), since a Component
Benchmark's Subject is an arbitrary candidate embedding model, not
necessarily one that needs the paper's exact inference knobs.

Usage:
    uv run --project benchmark/lmeb python fetch.py   # once, or whenever the
                                                        # pinned revision changes
    uv run --project benchmark/lmeb python run.py \\
        --model sentence-transformers/all-MiniLM-L6-v2 --tier selection

All logging goes to stderr; the last stdout line is the score protocol
JSON: {"metrics": {...}, "dataset": {...}, "judge": null}.
"""
from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import tomllib
from pathlib import Path

HERE = Path(__file__).resolve().parent
DATA_DIR = HERE / "data"
EVAL_DATA_DIR = DATA_DIR / "eval_data"
OFFICIAL_DIR = DATA_DIR / "official"
MANIFEST_PATH = DATA_DIR / "fetch_manifest.json"
OUTPUT_DIR = DATA_DIR / "results"

# Encoding batch size for model.encode() calls. Not paper-tuned (the LMEB
# paper uses per-model batch sizes up to 512 on GPU, see README "Enviroment"
# section) - a small, portable default that runs on CPU or GPU alike; the
# Subject here is an arbitrary candidate model, not one of the paper's 15.
BATCH_SIZE = 32

TIERS = ("selection", "diagnostic", "all")

logging.basicConfig(level=logging.INFO, format="%(levelname)s lmeb.run: %(message)s", stream=sys.stderr)
logger = logging.getLogger("lmeb.run")


def load_subsets() -> dict[str, list[str]]:
    with open(HERE / "subsets.toml", "rb") as f:
        data = tomllib.load(f)
    return {"selection": data["selection"]["tasks"], "diagnostic": data["diagnostic"]["tasks"]}


def load_dataset_provenance() -> dict:
    if not MANIFEST_PATH.exists():
        raise SystemExit(f"{MANIFEST_PATH} not found; run fetch.py first")
    cached = json.loads(MANIFEST_PATH.read_text())
    return {"name": cached["name"], "revision": cached["revision"], "sha256": cached["sha256"]}


def resolve_task_names(tier: str, override: list[str] | None, subsets: dict[str, list[str]]) -> list[str]:
    if override:
        return override
    if tier == "all":
        return [*subsets["selection"], *subsets["diagnostic"]]
    return subsets[tier]


def apply_limit(tasks: list, limit: int) -> None:
    """Cap queries evaluated per MTEB hf_subset, for fast smoke runs. The
    full corpus stays intact - only the number of queries searched is capped
    - so the real retrieval path is still exercised end to end, just over
    fewer queries. Mirrors the exact data shape LocalRetrieval.load_data()
    populates: self.queries[hf_subset][split], self.relevant_docs[hf_subset]
    [split], both dict[query_id, ...]."""
    for task in tasks:
        task.load_data()
        for hf_subset, by_split in task.queries.items():
            for split, queries in by_split.items():
                if len(queries) <= limit:
                    continue
                kept_ids = list(queries.keys())[:limit]
                task.queries[hf_subset][split] = {qid: queries[qid] for qid in kept_ids}
                relevant = task.relevant_docs.get(hf_subset, {}).get(split, {})
                task.relevant_docs[hf_subset][split] = {
                    qid: relevant[qid] for qid in kept_ids if qid in relevant
                }
        logger.info("Applied --limit=%d to %s (%d hf_subsets)", limit, task.metadata.name, len(task.queries))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--model", required=True, help="HF embedding model id or local path (the Subject under test)")
    parser.add_argument("--tier", choices=TIERS, default="selection", help="which subsets.toml task set to run (default: selection)")
    parser.add_argument("--tasks", help="comma-separated exact MTEB task names; overrides --tier's subsets.toml list")
    parser.add_argument("--limit", type=int, default=None, help="cap queries evaluated per MTEB hf_subset, for a fast smoke run")
    args = parser.parse_args()

    if not OFFICIAL_DIR.exists():
        raise SystemExit(f"{OFFICIAL_DIR} not found; run fetch.py first")
    if not EVAL_DATA_DIR.exists():
        raise SystemExit(f"{EVAL_DATA_DIR} not found; run fetch.py first")

    # data/official/src/abstasks/LocalRetrieval.py reads LOCAL_DATA_PREFIX at
    # *import time* into a module-level constant, so this must be set before
    # `import src` below (setdefault: a caller-exported override still wins).
    os.environ.setdefault("LOCAL_DATA_PREFIX", str(EVAL_DATA_DIR))

    sys.path.insert(0, str(OFFICIAL_DIR))
    import datasets as hf_datasets

    hf_datasets.disable_progress_bar()
    hf_datasets.logging.set_verbosity_error()

    import src  # noqa: F401 - side effect: registers LMEB's 22 tasks into mteb's task registry
    import mteb

    subsets = load_subsets()
    override = [t.strip() for t in args.tasks.split(",") if t.strip()] if args.tasks else None
    task_names = resolve_task_names(args.tier, override, subsets)

    logger.info("Subject (embedding model): %s", args.model)
    logger.info("Tier: %s -> %d MTEB tasks: %s", args.tier, len(task_names), ", ".join(task_names))
    if args.limit:
        logger.info("Smoke-run limit: %d queries per hf_subset", args.limit)

    tasks = mteb.get_tasks(tasks=task_names)
    found = {t.metadata.name for t in tasks}
    missing = sorted(set(task_names) - found)
    if missing:
        raise SystemExit(f"MTEB could not resolve tasks: {missing}")

    if args.limit:
        apply_limit(tasks, args.limit)

    model = mteb.get_model(args.model)

    evaluation = mteb.MTEB(tasks=tasks)
    results = evaluation.run(
        model,
        output_folder=str(OUTPUT_DIR),
        verbosity=0,
        overwrite_results=True,
        encode_kwargs={"batch_size": BATCH_SIZE},
    )

    selection_set = set(subsets["selection"])
    diagnostic_set = set(subsets["diagnostic"])
    per_task: dict[str, float] = {}
    for result in results:
        per_task[result.task_name] = result.get_score(splits=["test"])
        logger.info("%s: ndcg@10=%.4f", result.task_name, per_task[result.task_name])

    metrics: dict[str, float] = {f"{name}.ndcg@10": round(score, 4) for name, score in per_task.items()}
    selection_scores = [s for n, s in per_task.items() if n in selection_set]
    diagnostic_scores = [s for n, s in per_task.items() if n in diagnostic_set]
    if selection_scores:
        metrics["selection.mean_ndcg@10"] = round(sum(selection_scores) / len(selection_scores), 4)
    if diagnostic_scores:
        metrics["diagnostic.mean_ndcg@10"] = round(sum(diagnostic_scores) / len(diagnostic_scores), 4)

    output = {
        "metrics": metrics,
        "dataset": load_dataset_provenance(),
        "judge": None,
    }
    print(json.dumps(output))


if __name__ == "__main__":
    main()
