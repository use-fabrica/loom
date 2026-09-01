#!/usr/bin/env python3
"""Scores LoCoMo QA answers with the official lexical metric and/or a pinned
LLM judge.

Primary sources (see README.md "Sources" for the full list):
- Official scorer: snap-research/locomo task_eval/evaluation.py,
  `eval_question_answering`, imported unmodified from the clone fetch.py
  makes at data/official/ (pinned revision, see benchmark.toml). The LoCoMo
  paper's QA task (Maharana et al. 2024, arXiv:2402.17753, Section 4.1 /
  Table 2) is scored with a single word-overlap F1 metric per category.
  `evaluation.py` also defines `bert_score` and `rougel_score`/`rl`
  functions, but neither is reachable from `eval_question_answering`; a
  repo-wide grep confirms `bert_score(` has no call site at all, and
  `rougel_score`/`rl` are only called from `eval_dialogue_system`, which
  backs LoCoMo's separate multimodal-dialogue-generation task. So despite
  the benchmark harness's generic "lexical" scoring category, the
  LoCoMo QA metric this script reports is F1 only -- BERTScore and ROUGE-L
  are not part of the official QA evaluation and are not computed here.
  (`evaluation.py` still imports `bert_score` unconditionally at module
  level, so the package remains a runtime dependency even though the
  function is never called along this path.)
- Judge prompt/model: judge_prompt.md, adapted from LongMemEval; model
  pinned in benchmark.toml [judge].model.

Category numbering: the released dataset labels each question with an
integer `category` (1-5) but no name. We inferred the mapping below from
evidence-count statistics over data/locomo10.json and from
`eval_question_answering`'s own branches (category 1 splits the answer
into comma-separated sub-answers and averages partial F1 -- the paper's
multi-hop task; category 5 is checked against "no information
available"/"not mentioned" -- the paper's adversarial task; categories
2-4 are scored as single free-text spans). See README.md "Category
mapping" for the full evidence and citations.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import sys
import tomllib
from pathlib import Path

HERE = Path(__file__).resolve().parent
DATA_DIR = HERE / "data"
DATASET_PATH = DATA_DIR / "locomo10.json"
OFFICIAL_DIR = DATA_DIR / "official"
JUDGE_PROMPT_PATH = HERE / "judge_prompt.md"
BENCHMARK_TOML_PATH = HERE / "benchmark.toml"

DATASET_NAME = "locomo10"

CATEGORY_NAMES = {
    1: "multi_hop",
    2: "temporal",
    3: "open_domain",
    4: "single_hop",
    5: "adversarial",
}
ADVERSARIAL_CATEGORY = 5

STANDARD_MARKER = "## Standard prompt"
ADVERSARIAL_MARKER = "## Adversarial prompt"


def log(msg: str) -> None:
    print(msg, file=sys.stderr)


def sha256_of(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def load_benchmark_toml() -> dict:
    with open(BENCHMARK_TOML_PATH, "rb") as f:
        return tomllib.load(f)


def load_dataset() -> list[dict]:
    if not DATASET_PATH.exists():
        raise FileNotFoundError(f"{DATASET_PATH} missing; run fetch.py first")
    with open(DATASET_PATH) as f:
        return json.load(f)


def question_id(sample_id: str, idx: int) -> str:
    return f"{sample_id}::{idx}"


def iter_questions(dataset: list[dict]):
    """Yields (qid, qa_dict) for every question, in dataset file order.

    The dataset has no native question id; we mint one as
    "<sample_id>::<index-within-conversation>", which is stable across runs
    since JSON preserves list order. --answers files must key on this id.
    """
    for conv in dataset:
        for idx, qa in enumerate(conv["qa"]):
            yield question_id(conv["sample_id"], idx), qa


def load_answers(path: Path) -> dict[str, str]:
    with open(path) as f:
        return json.load(f)


def official_qa_records(dataset, answers):
    """Builds the list of dicts `eval_question_answering` expects, restricted
    to question ids present in `answers`.

    Category-5 (adversarial) items in this dataset release carry
    `adversarial_answer`, not `answer`. `eval_question_answering` reads
    `line['answer']` unconditionally before branching on category, so we
    backfill it here; the adversarial branch never actually reads the
    value, it only inspects the prediction text.
    """
    records = []
    missing = []
    for qid, qa in iter_questions(dataset):
        if qid not in answers:
            missing.append(qid)
            continue
        records.append(
            {
                "question": qa["question"],
                "answer": qa.get("answer", qa.get("adversarial_answer", "")),
                "category": qa["category"],
                "evidence": qa.get("evidence", []),
                "prediction": answers[qid],
            }
        )
    return records, missing


def run_lexical(dataset, answers, total_questions):
    sys.path.insert(0, str(OFFICIAL_DIR))
    from task_eval.evaluation import eval_question_answering  # noqa: E402

    records, missing = official_qa_records(dataset, answers)
    if missing:
        log(f"lexical: {len(missing)}/{total_questions} dataset questions have no answer entry, skipping them")
    if not records:
        raise RuntimeError("no answers matched dataset question ids")

    all_ems, _lens, _all_recall = eval_question_answering(records, eval_key="prediction")

    by_category: dict[int, list[float]] = {}
    for rec, score in zip(records, all_ems):
        by_category.setdefault(rec["category"], []).append(score)

    metrics = {}
    for cat, name in CATEGORY_NAMES.items():
        scores = by_category.get(cat, [])
        if scores:
            metrics[f"lexical.f1_{name}"] = sum(scores) / len(scores)
    metrics["lexical.f1_overall"] = sum(all_ems) / len(all_ems)
    metrics["lexical.coverage"] = len(records) / total_questions
    return metrics


def load_judge_templates() -> tuple[str, str]:
    text = JUDGE_PROMPT_PATH.read_text()
    std_start = text.index(STANDARD_MARKER) + len(STANDARD_MARKER)
    adv_start = text.index(ADVERSARIAL_MARKER)
    standard = text[std_start:adv_start].strip()
    adversarial = text[adv_start + len(ADVERSARIAL_MARKER):].strip()
    return standard, adversarial


def run_judge(dataset, answers, total_questions, model):
    from openai import OpenAI

    client = OpenAI()
    standard_tpl, adversarial_tpl = load_judge_templates()

    records = []
    missing = []
    for qid, qa in iter_questions(dataset):
        if qid not in answers:
            missing.append(qid)
            continue
        records.append((qa, answers[qid]))
    if missing:
        log(f"judge: {len(missing)}/{total_questions} dataset questions have no answer entry, skipping them")
    if not records:
        raise RuntimeError("no answers matched dataset question ids")

    by_category: dict[int, list[int]] = {}
    for qa, response in records:
        cat = qa["category"]
        if cat == ADVERSARIAL_CATEGORY:
            prompt = adversarial_tpl.format(question=qa["question"], response=response)
        else:
            prompt = standard_tpl.format(
                question=qa["question"], answer=qa.get("answer", ""), response=response
            )
        completion = client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": prompt}],
            n=1,
            temperature=0,
            max_tokens=10,
        )
        verdict = (completion.choices[0].message.content or "").strip().lower()
        by_category.setdefault(cat, []).append(1 if "yes" in verdict else 0)

    metrics = {}
    all_labels: list[int] = []
    for cat, name in CATEGORY_NAMES.items():
        labels = by_category.get(cat, [])
        if labels:
            metrics[f"judge.accuracy_{name}"] = sum(labels) / len(labels)
            all_labels.extend(labels)
    metrics["judge.accuracy_overall"] = sum(all_labels) / len(all_labels)
    metrics["judge.coverage"] = len(records) / total_questions
    return metrics


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--answers", required=True, type=Path, help="JSON: question id -> answer string")
    parser.add_argument("--mode", choices=["lexical", "judge", "both"], default="both")
    args = parser.parse_args()

    config = load_benchmark_toml()
    dataset = load_dataset()
    answers = load_answers(args.answers)
    total_questions = sum(1 for _ in iter_questions(dataset))

    metrics: dict[str, float] = {}
    judge_info = None

    if args.mode in ("lexical", "both"):
        log("scoring lexical F1 via official task_eval.evaluation.eval_question_answering")
        metrics.update(run_lexical(dataset, answers, total_questions))

    if args.mode in ("judge", "both"):
        judge_cfg = config["judge"]
        log(f"scoring with judge {judge_cfg['model']} (prompt {judge_cfg['prompt_version']})")
        metrics.update(run_judge(dataset, answers, total_questions, judge_cfg["model"]))
        judge_info = {"model": judge_cfg["model"], "prompt_version": judge_cfg["prompt_version"]}

    result = {
        "metrics": metrics,
        "dataset": {
            "name": DATASET_NAME,
            "revision": config["dataset"]["revision"],
            "sha256": sha256_of(DATASET_PATH),
        },
        "judge": judge_info,
    }
    print(json.dumps(result))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 - protocol requires a clean nonzero exit
        log(f"score failed: {exc}")
        sys.exit(1)
