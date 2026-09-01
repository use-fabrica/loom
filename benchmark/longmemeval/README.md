# LongMemEval

End-to-end Benchmark (ADR-0002 / ADR-0003). Adapter around the official
[LongMemEval](https://github.com/xiaowu0162/LongMemEval) evaluation code
(ICLR 2025). Per ADR-0001 the Loom Engine's contract stops at Retrieve, so
this Benchmark implements only dataset fetch and scoring-from-an-answers-file;
nothing here talks to an engine, and the `[answerer]` model in
`benchmark.toml` is intentionally empty until an Answerer is pinned.

## Sources (read 2026-09-01)

- Paper: Wu et al., *LongMemEval: Benchmarking Chat Assistants on Long-Term
  Interactive Memory*, ICLR 2025. <https://arxiv.org/abs/2410.10813>
- Official repo: <https://github.com/xiaowu0162/LongMemEval>, pinned at commit
  `9e0b455f4ef0e2ab8f2e582289761153549043fc` (tip of `main`, verified via
  `git ls-remote`). MIT License.
- Dataset: <https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned>,
  pinned at revision `98d7416c24c778c2fee6e6f3006e7a073259d48f` (HF API `sha`
  for the dataset repo). MIT-licensed per the dataset card.
- LongMemEval-V2: <https://github.com/xiaowu0162/LongMemEval-V2> — see
  "Why not V2" below.

## Five abilities / six question types

The paper (§3.1) defines five core long-term memory abilities. Five of the
six `question_type` values in the data map one-to-one to an ability; the
sixth (**Abstention**) is not its own `question_type` — it is a cross-cutting
subset of ~30 questions drawn from the other types and rewritten as
false-premise questions, flagged by a `question_id` ending in `_abs` (paper
§3.2; HF dataset card field docs).

| Ability | `question_type` value(s) |
|---|---|
| Information Extraction | `single-session-user`, `single-session-assistant` |
| (personalization variant) | `single-session-preference` |
| Multi-Session Reasoning | `multi-session` |
| Temporal Reasoning | `temporal-reasoning` |
| Knowledge Updates | `knowledge-update` |
| Abstention | any of the above with `_abs` in `question_id` |

## S vs Oracle

- **S** (`longmemeval_s_cleaned.json`): the full `LongMemEval_S` haystack —
  roughly 40 history sessions / ~115k tokens per question (paper §3.1). This
  is the setting that exercises retrieval.
- **Oracle** (`longmemeval_oracle.json`): the same questions with the history
  reduced to only the evidence (`answer_session_ids`) sessions — i.e. a
  perfect-retrieval upper bound. It is diagnostic: comparing `S.*` against
  `Oracle.*` on the same question isolates whether score loss comes from
  retrieval (Engine Retrieve quality) or from reading/answering, without
  needing a separate retrieval-quality metric.

`LongMemEval_M` (~500 sessions / ~1.5M tokens) exists upstream but is out of
scope for this adapter (S + Oracle only, per assignment).

## Why not V2

[LongMemEval-V2](https://github.com/xiaowu0162/LongMemEval-V2) (announced by
the same authors, May 2026) evaluates memory "in agentic context" — a
different benchmark measuring agent-trajectory behavior, not the QA-over-chat-
history protocol this adapter implements. Per the project's benchmark
selection session decision, and consistent with ADR-0002 (which names
"LongMemEval", not "LongMemEval-V2", as one of the four in-repo Benchmarks),
this adapter targets the original QA benchmark only.

## Paper answerer setup (context, not a Loom pin)

Per ADR-0001 the Answerer is a Harness/client concern, never the Engine's.
The pinned `[answerer].model` here is empty on purpose. For context, the
paper's own experimental setup (§4.2, §5.1, Table 3) used:

- **Readers evaluated**: GPT-4o, Llama 3.1 70B Instruct, Llama 3.1 8B
  Instruct.
- **Default indexing model**: Llama 3.1 8B Instruct, used to extract session
  summaries / facts / keyphrases / timestamped events (§4.2).
- **Default retriever**: dense retrieval with Stella-en-1.5B-v5 (§4.2, "we
  choose dense retrieval with the 1.5B Stella V5 model").
- **Default reading strategy**: Chain-of-Note (Yu et al., 2023) with JSON
  history format, applied by default throughout §5.2–§5.4.
- **Judge**: `gpt-4o-2024-08-06` via the OpenAI API (paper §3.3: "we
  prompt-engineer the gpt-4o-2024-08-06 model via the OpenAI API"), matching
  the official `evaluate_qa.py` default described below.

## Judge pin

`[judge].model = "gpt-4o-2024-08-06"` is not a guess: it is the exact string
the official `evaluate_qa.py` resolves for its `'gpt-4o'` short key —

```python
# src/evaluation/evaluate_qa.py:11-15 (at the pinned SHA)
model_zoo = {
    'llama-3.1-70b-instruct': ('meta-llama/Meta-Llama-3.1-70B-Instruct', 'local'),
    'gpt-4o-mini': ('gpt-4o-mini-2024-07-18', 'openai'),
    'gpt-4o': ('gpt-4o-2024-08-06', 'openai'),
}
```

and it is the model the official `print_qa_metrics.py` asserts against —

```python
# src/evaluation/print_qa_metrics.py:20
assert entry['autoeval_label']['model'] == 'gpt-4o-2024-08-06'
```

— i.e. `gpt-4o-2024-08-06` is what the paper's own reported numbers used.
`score.py` invokes `evaluate_qa.py` with the short key `gpt-4o` and asserts
the resulting log's `autoeval_label.model` equals `benchmark.toml`'s pinned
`judge.model`, so any future drift between the two fails loudly instead of
silently.

## `evaluate_qa.py` CLI (verified from source)

```
python3 evaluate_qa.py <metric_model_short> <hyp_file> <ref_file>
```

`hyp_file` must be JSONL, one object per line: `{"question_id": "...",
"hypothesis": "..."}` (this is also documented in the upstream README's
"Testing Your System" section, and is what the script's loader expects).
Requires `OPENAI_API_KEY` (and optionally `OPENAI_ORGANIZATION`) in the
environment.

## Usage

```sh
# Fetch the S/Oracle dataset files + official repo into data/
uv run --project benchmark/longmemeval python fetch.py

# Score a hypothesis file
export OPENAI_API_KEY=...
uv run --project benchmark/longmemeval python score.py \
    --answers /path/to/hypotheses.jsonl --config S
```

`score.py` prints its own logs to stderr; the last stdout line is the
protocol JSON: `{"metrics": {...}, "dataset": {...}, "judge": {...}}`.
Metric keys are namespaced by config, e.g. `S.overall`, `S.task_averaged`,
`S.abstention`, `Oracle.multi_session`, `Oracle.temporal_reasoning`, etc.
(the six `question_type` values with hyphens replaced by underscores, plus
`task_averaged`/`overall`/`abstention` — mirroring the official
`print_qa_metrics.py` aggregation exactly).

### Optional retrieval metrics

`score.py --retrieval <path>` accepts a retrieval log in the exact shape the
official `src/retrieval/run_retrieval.py` produces (one JSON object per
line, each with a `retrieval_results.metrics.{session,turn}` block of
`recall_all@K` / `ndcg_any@K` values) and aggregates it the same way the
official `print_retrieval_metrics.py` does, emitting
`<config>.retrieval.session.<metric>` / `<config>.retrieval.turn.<metric>`
keys. This adapter does not run retrieval itself — it only parses an
already-produced log, which is cheap and requires no extra dependency. Since
no engine exists yet (issue #1), there is no producer of such a log today;
this flag exists for when a Retrieve-backed adapter can generate one.

## Official scorer reuse (ADR-0003)

The official evaluation code (`evaluate_qa.py`, MIT-licensed) is not
pip-installable, so `fetch.py` clones it at the pinned SHA into
`data/official/` (gitignored, never committed) and `score.py` invokes it
as a subprocess with `sys.executable`, so it runs inside this project's own
uv-managed virtualenv and dependency set.

## Layout

- `fetch.py` — downloads `longmemeval_s_cleaned.json` and
  `longmemeval_oracle.json` from the pinned HF revision, and clones the
  official repo at the pinned SHA, into `data/` (gitignored).
- `score.py` — invokes the official `evaluate_qa.py` with the pinned judge
  and reports namespaced accuracy metrics; optionally aggregates a retrieval
  log.
- `benchmark.toml` — harness manifest (dataset/judge pins, entrypoints).
