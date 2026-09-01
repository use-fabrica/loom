#!/usr/bin/env python3
"""Score BEAM answers with the official nugget evaluator.

    uv run --project benchmark/beam python score.py --answers <dir> --tier 128k|500k|all

`--answers <dir>` must contain one file per conversation, named
`<conversation_id>.json`, in the exact shape
`data/official/src/evaluation/run_evaluation.py` (fetched by fetch.py)
consumes as its `answers_directory` argument: the same dict that tier's
`probing_questions` column holds -- one list of two question objects per
memory ability -- with an "llm_response" string added to every question
object. See README.md "Answers-file format" for the full spec and a worked
example.

This reuses the official scoring code cloned into data/official/ (see
fetch.py) so the LLM-judge nugget scoring, the JSON-repair parsing, and the
event-ordering Kendall tau-b computation are the exact code the paper used
(ADR-0003) -- with one deliberate override: the judge LLM passed into every
official evaluate_* call is built from benchmark.toml's [judge].model rather
than the official repo's own hardcoded gpt-4.1-mini client (which run_evaluation.py's
own CLI has no flag to override). Only the aggregation across the two
questions per ability and across conversations -- a trivial, deterministic
mean, taken directly from the official src/evaluation/report_results.py -- is
re-implemented here, so scoring can run against a single harness-provided
answers file instead of a pre-existing "row_name" results tree.
"""
from __future__ import annotations

import argparse
import ast
import contextlib
import json
import os
import sys
import tempfile
from pathlib import Path

import pandas as pd

from _common import (
    ABILITY_KEYS,
    DATA_DIR,
    MANIFEST_PATH,
    OFFICIAL_DIR,
    TIER_TO_SPLIT,
    load_benchmark_toml,
    log,
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--answers", required=True, type=Path, help="directory of <conversation_id>.json answer files")
    parser.add_argument("--tier", required=True, choices=["128k", "500k", "all"])
    return parser.parse_args()


def find_parquet(split: str) -> Path:
    matches = sorted((DATA_DIR / "beam" / "data").glob(f"{split}-*.parquet"))
    if not matches:
        raise FileNotFoundError(f"no {split} parquet shard under data/beam/data/; run fetch.py first")
    return matches[0]


def prepare_official_llm_config(openai_api_key: str) -> None:
    """Populate the official repo's llms_config.json so importing src.llm (a
    side effect of importing src.evaluation.run_evaluation) succeeds.

    Only the "gpt" entry is ever exercised by this script: we pass our own
    ChatOpenAI (built from benchmark.toml's judge.model) into every official
    evaluate_* call instead of the module's own hardcoded gpt_llm. The
    llama/qwen entries exist only because src/llm.py unconditionally builds
    all three clients at import time; they are never invoked, so
    placeholder-but-well-formed values are safe. This file lives inside the
    gitignored data/official/ checkout -- never committed.
    """
    config_path = OFFICIAL_DIR / "src" / "llms_config.json"
    if not config_path.parent.exists():
        raise FileNotFoundError(f"{OFFICIAL_DIR} is missing src/; run fetch.py first")
    placeholder = {"model_url": "http://unused.invalid", "model_name": "unused", "api_key": "unused"}
    config_path.write_text(
        json.dumps({"llama": placeholder, "qwen": placeholder, "gpt": {"api_key": openai_api_key}}, indent=2),
        encoding="utf-8",
    )


def load_official_run_evaluation():
    sys.path.insert(0, str(OFFICIAL_DIR))
    from src.evaluation.run_evaluation import run_evaluation  # type: ignore[import-not-found]

    return run_evaluation


def validate_ability_keys(payload: dict, source: str) -> None:
    missing = [k for k in ABILITY_KEYS if k not in payload]
    if missing:
        raise ValueError(f"{source} is missing memory abilities: {missing}")


def load_conversation_answers(answers_dir: Path, conversation_id: str) -> Path:
    path = answers_dir / f"{conversation_id}.json"
    if not path.exists():
        raise FileNotFoundError(f"missing answers file for conversation {conversation_id!r}: {path}")
    return path


def score_conversation(
    conversation_id: str,
    probing_questions: dict,
    answers_path: Path,
    judge,
    run_evaluation_fn,
    tmp_root: Path,
) -> dict[str, float]:
    conv_tmp = tmp_root / conversation_id
    conv_tmp.mkdir(parents=True, exist_ok=True)
    pq_path = conv_tmp / "probing_questions.json"
    pq_path.write_text(json.dumps(probing_questions, ensure_ascii=False, indent=2), encoding="utf-8")
    output_path = conv_tmp / "evaluation.json"

    run_evaluation_fn(
        probing_questions_address=str(pq_path),
        answers_directory=str(answers_path),
        output_address=str(output_path),
        model=judge,
    )

    raw = json.loads(output_path.read_text(encoding="utf-8"))
    scores: dict[str, float] = {}
    for ability in ABILITY_KEYS:
        objects = raw.get(ability)
        if not objects:
            raise ValueError(f"conversation {conversation_id}: official evaluator returned no results for {ability!r}")
        # src/evaluation/report_results.py: event_ordering is scored by the
        # Kendall tau-b coefficient (tau_norm); every other ability by the
        # mean nugget-rubric judge score (llm_judge_score).
        metric_key = "tau_norm" if ability == "event_ordering" else "llm_judge_score"
        values = [float(obj[metric_key]) for obj in objects]
        scores[ability] = sum(values) / len(values)
    return scores


def score_tier(tier: str, answers_dir: Path, judge, run_evaluation_fn, tmp_root: Path) -> dict[str, float]:
    split = TIER_TO_SPLIT[tier]
    parquet_path = find_parquet(split)
    log(f"[{tier}] reading {parquet_path.relative_to(DATA_DIR)}")
    frame = pd.read_parquet(parquet_path, engine="pyarrow", columns=["conversation_id", "probing_questions"])

    per_conversation: list[dict[str, float]] = []
    for row in frame.itertuples(index=False):
        conversation_id = str(row.conversation_id)
        try:
            probing_questions = ast.literal_eval(row.probing_questions)
        except (ValueError, SyntaxError) as exc:
            raise ValueError(f"[{tier}] conversation {conversation_id}: malformed probing_questions column: {exc}") from exc
        validate_ability_keys(probing_questions, f"[{tier}] conversation {conversation_id} probing_questions")

        answers_path = load_conversation_answers(answers_dir, conversation_id)
        answers = json.loads(answers_path.read_text(encoding="utf-8"))
        validate_ability_keys(answers, f"[{tier}] conversation {conversation_id} answers ({answers_path})")
        for ability in ABILITY_KEYS:
            if len(answers[ability]) != len(probing_questions[ability]):
                raise ValueError(
                    f"[{tier}] conversation {conversation_id} ability {ability!r}: "
                    f"{len(answers[ability])} answer(s) vs {len(probing_questions[ability])} probing question(s)"
                )

        log(f"[{tier}] scoring conversation {conversation_id} ({len(per_conversation) + 1}/{len(frame)})")
        per_conversation.append(
            score_conversation(conversation_id, probing_questions, answers_path, judge, run_evaluation_fn, tmp_root)
        )

    tier_scores = {
        ability: sum(c[ability] for c in per_conversation) / len(per_conversation) for ability in ABILITY_KEYS
    }
    tier_scores["overall"] = sum(tier_scores[a] for a in ABILITY_KEYS) / len(ABILITY_KEYS)
    return tier_scores


def main() -> int:
    args = parse_args()
    tiers = sorted(TIER_TO_SPLIT) if args.tier == "all" else [args.tier]

    if not MANIFEST_PATH.exists():
        log(f"{MANIFEST_PATH} missing; run fetch.py first")
        return 1
    if not (OFFICIAL_DIR / "src" / "evaluation" / "run_evaluation.py").exists():
        log(f"{OFFICIAL_DIR} is missing the official evaluation code; run fetch.py first")
        return 1
    if not args.answers.is_dir():
        log(f"--answers {args.answers} is not a directory")
        return 1

    openai_api_key = os.environ.get("OPENAI_API_KEY", "")
    if not openai_api_key:
        log(
            "OPENAI_API_KEY must be set: it authenticates both the pinned judge "
            "and the official evaluation module's import-time client construction"
        )
        return 1

    config = load_benchmark_toml()
    judge_model = config.get("judge", {}).get("model", "")
    prompt_version = config.get("judge", {}).get("prompt_version", "")
    if not judge_model:
        log("benchmark.toml [judge].model is empty")
        return 1

    try:
        with tempfile.TemporaryDirectory(prefix="beam-score-") as tmp_dir, contextlib.redirect_stdout(sys.stderr):
            # Everything the official code (and its nltk/model downloads)
            # prints goes to sys.stdout by default; redirect it to stderr for
            # the duration of this block so our own final print() below is
            # the only thing on stdout, per the harness's "last stdout line
            # is JSON" contract.
            tmp_root = Path(tmp_dir)
            prepare_official_llm_config(openai_api_key)
            run_evaluation_fn = load_official_run_evaluation()

            from langchain_openai import ChatOpenAI

            judge = ChatOpenAI(model=judge_model, temperature=0)

            metrics: dict[str, float] = {}
            for tier in tiers:
                tier_scores = score_tier(tier, args.answers, judge, run_evaluation_fn, tmp_root)
                for key, value in tier_scores.items():
                    metrics[f"{tier}.{key}"] = value
    except Exception as exc:
        log(f"score failed: {exc}")
        return 1

    manifest = json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))
    result = {
        "metrics": metrics,
        "dataset": {"name": manifest["name"], "revision": manifest["revision"], "sha256": manifest["sha256"]},
        "judge": {"model": judge_model, "prompt_version": prompt_version},
    }
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
