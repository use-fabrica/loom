#!/usr/bin/env python3
"""Score a hypothesis file against LongMemEval_S or LongMemEval_Oracle by
invoking the official evaluate_qa.py (cloned into data/official/ by
fetch.py) with the judge pinned in benchmark.toml.

Usage:
    uv run --project benchmark/longmemeval python score.py \\
        --answers path/to/hypotheses.jsonl --config S [--retrieval path/to/retrieval.jsonl]

--answers must be a JSONL file, one object per line:
    {"question_id": "<id>", "hypothesis": "<model answer text>"}
This is exactly the format required by the official evaluate_qa.py (see
README "Testing Your System" / evaluate_qa.py's hypotheses loader).

Requires OPENAI_API_KEY (and optionally OPENAI_ORGANIZATION) in the
environment: the official evaluate_qa.py calls the OpenAI API directly for
judging.

All logging goes to stderr; the last stdout line is the score protocol
JSON.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
import tomllib
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
DATA_DIR = HERE / "data"
EVALUATE_QA = DATA_DIR / "official" / "src" / "evaluation" / "evaluate_qa.py"

CONFIG_TO_FILE = {
    "S": "longmemeval_s_cleaned.json",
    "Oracle": "longmemeval_oracle.json",
}

# Short key from the official evaluate_qa.py's model_zoo dict (src/evaluation
# /evaluate_qa.py:11-15 at the pinned SHA) that resolves to the judge model
# pinned in benchmark.toml. See README for the exact source citation.
JUDGE_SHORT_KEY = "gpt-4o"

# Order matches the official print_qa_metrics.py's type2acc dict literal.
QUESTION_TYPES = [
    "single-session-user",
    "single-session-preference",
    "single-session-assistant",
    "multi-session",
    "temporal-reasoning",
    "knowledge-update",
]

RETRIEVAL_SESSION_METRICS = ["recall_all@5", "ndcg_any@5", "recall_all@10", "ndcg_any@10"]
RETRIEVAL_TURN_METRICS = RETRIEVAL_SESSION_METRICS + ["recall_all@50", "ndcg_any@50"]


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


def run_official_evaluate_qa(answers_path: Path, ref_path: Path) -> Path:
    if not EVALUATE_QA.exists():
        raise SystemExit(f"{EVALUATE_QA} not found; run fetch.py first")

    proc = subprocess.run(
        [sys.executable, str(EVALUATE_QA), JUDGE_SHORT_KEY, str(answers_path), str(ref_path)],
        capture_output=True,
        text=True,
    )
    for line in proc.stdout.splitlines():
        log("[evaluate_qa.py]", line)
    for line in proc.stderr.splitlines():
        log("[evaluate_qa.py:stderr]", line)
    if proc.returncode != 0:
        raise SystemExit(f"evaluate_qa.py failed with exit code {proc.returncode}")

    return Path(f"{answers_path}.eval-results-{JUDGE_SHORT_KEY}")


def score_qa(config_name: str, answers_path: Path, ref_path: Path, judge_model: str) -> dict[str, float]:
    result_file = run_official_evaluate_qa(answers_path, ref_path)
    entries = [json.loads(line) for line in result_file.read_text().splitlines() if line.strip()]
    if not entries:
        raise SystemExit(f"{result_file} contains no scored entries")

    for entry in entries:
        got = entry["autoeval_label"]["model"]
        if got != judge_model:
            raise SystemExit(
                f"judge mismatch: evaluate_qa.py used {got!r}, benchmark.toml pins {judge_model!r}"
            )

    ref_data = {e["question_id"]: e for e in json.loads(ref_path.read_text())}
    type_acc: dict[str, list[int]] = {t: [] for t in QUESTION_TYPES}
    abstention_acc: list[int] = []
    for entry in entries:
        ref_entry = ref_data[entry["question_id"]]
        label = 1 if entry["autoeval_label"]["label"] else 0
        type_acc[ref_entry["question_type"]].append(label)
        if "_abs" in entry["question_id"]:
            abstention_acc.append(label)

    for qtype, scores in type_acc.items():
        if not scores:
            raise SystemExit(f"no scored answers for question_type={qtype!r}; check --answers coverage")
    if not abstention_acc:
        raise SystemExit("no scored abstention (_abs) answers; check --answers coverage")

    metrics: dict[str, float] = {}
    all_scores: list[int] = []
    task_scores: list[float] = []
    for qtype, scores in type_acc.items():
        mean = float(np.mean(scores))
        metrics[f"{config_name}.{qtype.replace('-', '_')}"] = mean
        all_scores.extend(scores)
        task_scores.append(mean)
    metrics[f"{config_name}.task_averaged"] = float(np.mean(task_scores))
    metrics[f"{config_name}.overall"] = float(np.mean(all_scores))
    metrics[f"{config_name}.abstention"] = float(np.mean(abstention_acc))
    return metrics


def score_retrieval(config_name: str, retrieval_path: Path) -> dict[str, float]:
    """Aggregate an official-format retrieval log (as produced by the
    official src/retrieval/run_retrieval.py) the same way
    print_retrieval_metrics.py does, namespaced under the config."""
    entries = [json.loads(line) for line in retrieval_path.read_text().splitlines() if line.strip()]
    entries = [e for e in entries if "_abs" not in e["question_id"]]
    if not entries:
        raise SystemExit(f"{retrieval_path} contains no non-abstention retrieval entries")

    all_metrics = [e["retrieval_results"]["metrics"] for e in entries]
    metrics: dict[str, float] = {}
    for name in RETRIEVAL_SESSION_METRICS:
        values = [m["session"][name] for m in all_metrics]
        metrics[f"{config_name}.retrieval.session.{name}"] = float(np.mean(values))
    for name in RETRIEVAL_TURN_METRICS:
        values = [m["turn"][name] for m in all_metrics]
        metrics[f"{config_name}.retrieval.turn.{name}"] = float(np.mean(values))
    return metrics


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--answers", required=True, type=Path, help="JSONL hypothesis file")
    parser.add_argument("--config", required=True, choices=sorted(CONFIG_TO_FILE))
    parser.add_argument(
        "--retrieval",
        type=Path,
        default=None,
        help="optional official-format retrieval log (print_retrieval_metrics.py "
        "input shape); adds session/turn recall+ndcg metrics",
    )
    args = parser.parse_args()

    manifest = load_manifest()
    judge_model = manifest["judge"]["model"]
    prompt_version = manifest["judge"]["prompt_version"]
    revision = manifest["dataset"]["revision"]

    ref_filename = CONFIG_TO_FILE[args.config]
    ref_path = DATA_DIR / ref_filename
    if not ref_path.exists():
        raise SystemExit(f"{ref_path} not found; run fetch.py first")

    if not args.answers.exists():
        raise SystemExit(f"{args.answers} not found")

    metrics = score_qa(args.config, args.answers.resolve(), ref_path, judge_model)
    if args.retrieval is not None:
        metrics.update(score_retrieval(args.config, args.retrieval))

    result = {
        "metrics": metrics,
        "dataset": {
            "name": ref_filename,
            "revision": revision,
            "sha256": sha256_file(ref_path),
        },
        "judge": {
            "model": judge_model,
            "prompt_version": prompt_version,
        },
    }
    print(json.dumps(result))


if __name__ == "__main__":
    main()
