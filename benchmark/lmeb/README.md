# LMEB

Component Benchmark (ADR-0002 / ADR-0004). Drives the standard [MTEB](https://github.com/embeddings-benchmark/mteb)
harness against a candidate embedding model using [LMEB](https://github.com/KaLM-Embedding/LMEB)
(Zhao et al., *LMEB: Long-horizon Memory Embedding Benchmark*, arXiv:2603.12572),
which evaluates the Engine's embedding layer in isolation rather than the
Engine as a whole (CONTEXT.md: "Component Benchmark... evaluates one internal
layer of the Engine in isolation"). There is no ingestion, answering, or
judging here — this Benchmark shares no machinery with the three End-to-end
Benchmarks (ADR-0002) and does not talk to an Engine at all.

## Contamination rule (ADR-0004)

**Embedding-model selection may only use the `[selection]` tier in
`subsets.toml`.** LMEB repackages LoCoMo and LongMemEval as two of its own
retrieval tasks; scoring a candidate embedding model on those and then
reporting LoCoMo/LongMemEval End-to-end Benchmark numbers for the same model
would be tuning on our own test set. The `[diagnostic]` tier (`LoCoMo`,
`LongMemEval`) is computed and reported by `run.py` for visibility, but
**must never be used as a selection criterion** — see
[`docs/adr/0004`](../../docs/adr/0004-lmeb-selection-only-on-nonoverlapping-subsets.md)
for the full rule and its verified source list.

## Sources (read 2026-09-01)

- Paper: Zhao et al., *LMEB: Long-horizon Memory Embedding Benchmark*,
  arXiv:2603.12572. <https://arxiv.org/abs/2603.12572>
- Official repo: <https://github.com/KaLM-Embedding/LMEB>, pinned at commit
  `a02ae842598183ed162fc90c58ffaae5eec89f12` (tip of `main`, "Update README
  with MTEB support for LMEB", verified via the GitHub commits API — no later
  commit exists as of 2026-09-01). Apache License 2.0 covers the evaluation
  code; per-dataset content licenses vary (see table below).
- Dataset: <https://huggingface.co/datasets/KaLM-Embedding/LMEB>, pinned at
  revision `f9d4294be4a24b8c16d6bfb59d56a6fec4bd95c0` (the dataset repo's HF
  API `sha`).
- Harness: [mteb](https://github.com/embeddings-benchmark/mteb) `2.3.0` — the
  exact version LMEB's own `requirements.txt` pins.

## Subset classification

All 22 datasets are registered as MTEB tasks by
`data/official/src/__init__.py` (cloned by `fetch.py`). "Tier" is this
Benchmark's ADR-0004 classification; "Sub-tasks" is the number of MTEB
`hf_subset`s (LMEB's own eval_langs) LMEB defines for that task — the four
totals below (2+4+8+6 datasets, 69+31+15+67=182 sub-tasks + 11 diagnostic)
sum to the paper's claimed 22 datasets / 193 tasks exactly.

### Episodic memory

| MTEB task | Tier | Sub-tasks | Source | License |
|---|---|---|---|---|
| `EPBench` | selection | 54 | Huet, Ben-Houidi & Rossi, ICLR 2025 — [dataset](https://doi.org/10.6084/m9.figshare.28244480) | mit |
| `KnowMeBench` | selection | 15 | Wu et al. 2026, arXiv:2601.04745 — [dataset](https://github.com/QuantaAlpha/KnowMeBench/tree/main/KnowmeBench) | apache-2.0 |

### Dialogue memory

| MTEB task | Tier | Sub-tasks | Source | License |
|---|---|---|---|---|
| `LoCoMo` | **diagnostic** | 5 | Maharana et al., ACL 2024 — [dataset](https://github.com/snap-research/locomo/tree/main/data) | cc-by-nc-4.0 |
| `LongMemEval` | **diagnostic** | 6 | Wu et al., ICLR 2025 — [dataset](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned) | mit |
| `ConvoMem` | selection | 6 | Pakhomov, Nijkamp & Xiong 2025, arXiv:2511.10523 — [dataset](https://huggingface.co/datasets/Salesforce/ConvoMem) | cc-by-nc-4.0 |
| `MemBench` | selection | 10 | Tan et al., ACL Findings 2025 — [dataset](https://github.com/import-myself/Membench/tree/main/MemData) | mit |
| `REALTALK` | selection | 3 | Lee et al. 2025, arXiv:2502.13270 — [dataset](https://github.com/danny911kr/REALTALK/tree/main/data) | not specified |
| `TMD` | selection | 12 | Alonso et al. 2024, arXiv:2406.00057 — [dataset](https://github.com/Zyphra/TemporalMemoryDataset) | not specified |

### Semantic memory

| MTEB task | Tier | Sub-tasks | Source | License |
|---|---|---|---|---|
| `QASPER` | selection | 1 | Dasigi et al., NAACL 2021 — [dataset](https://huggingface.co/datasets/allenai/qasper) | cc-by-nc-4.0 |
| `NovelQA` | selection | 7 | Wang et al., ICLR 2025 — [dataset](https://huggingface.co/datasets/NovelQA/NovelQA) | not specified |
| `PeerQA` | selection | 1 | Baumgärtner, Briscoe & Gurevych, NAACL 2025 — [dataset](https://huggingface.co/datasets/UKPLab/PeerQA) | cc-by-nc-sa-4.0 |
| `CovidQA` | selection | 1 | Möller et al., ACL 2020 (NLP-COVID19 workshop) — [dataset](https://huggingface.co/datasets/illuin-conteb/covid-qa) | apache-2.0 |
| `ESGReports` | selection | 1 | Macé, Loison & Faysse 2025, arXiv:2505.17166 (ViDoRe V2) — [dataset](https://huggingface.co/datasets/illuin-conteb/esg-reports) | not specified |
| `MLDR` | selection | 1 | Chen et al. 2024, arXiv:2402.03216 (BGE M3-Embedding) — [dataset](https://huggingface.co/datasets/illuin-conteb/mldr-conteb-eval) | mit |
| `LooGLE` | selection | 2 | Li, Wang, Zheng & Zhang, ACL 2024 — [dataset](https://huggingface.co/datasets/bigai-nlco/LooGLE) | cc-by-sa-4.0 |
| `LMEB_SciFact` | selection | 1 | Wadden et al., EMNLP 2020 (SciFact) — [dataset](https://huggingface.co/datasets/allenai/scifact) | cc-by-nc-3.0 |

### Procedural memory

| MTEB task | Tier | Sub-tasks | Source | License |
|---|---|---|---|---|
| `Gorilla` | selection | 3 | Patil, Zhang, Wang & Gonzalez, NeurIPS 2024 — [dataset](https://huggingface.co/datasets/mangopy/ToolRet-Queries) | apache-2.0 |
| `ToolBench` | selection | 1 | Qin et al. (ToolLLM), ICLR 2024 — [dataset](https://huggingface.co/datasets/mangopy/ToolRet-Queries) | apache-2.0 |
| `ReMe` | selection | 9 | Cao et al. 2025, arXiv:2512.10696 — [dataset](https://github.com/agentscope-ai/ReMe/tree/main/docs/library/paper_data/task) | apache-2.0 |
| `Proced_mem_bench` | selection | 3 | Kohar & Krishnan 2025, arXiv:2511.21730 — [dataset](https://github.com/qpiai/Proced_mem_bench/tree/main/procedural_memory_benchmark) | apache-2.0 |
| `MemGovern` | selection | 48 | Wang et al. 2026, arXiv:2601.06789 — [dataset](https://github.com/QuantaAlpha/MemGovern/blob/main/data) | mit |
| `DeepPlanning` | selection | 3 | Zhang et al. 2026, arXiv:2601.18137 — [dataset](https://huggingface.co/datasets/Qwen/DeepPlanning) | apache-2.0 |

**20 selection tasks (182 sub-tasks) + 2 diagnostic tasks (11 sub-tasks) = 22
tasks / 193 sub-tasks**, matching the paper's abstract ("22 datasets and 193
zero-shot retrieval tasks") exactly. `LMEB_SciFact`, `Gorilla` and
`ToolBench` are themselves *independent* repackagings of pre-existing
retrieval/tool-use datasets (SciFact, and a shared ToolRet-Queries source for
Gorilla/ToolBench) — unrelated to LoCoMo/LongMemEval, hence `selection`.

## How LMEB invokes MTEB

The official repo's runner (`scripts/run_lmeb_wo_inst.sh` →
`run_lmeb.py`) does, per model:

```bash
export LOCAL_DATA_PREFIX="./eval_data"
python run_lmeb.py --model_path <model> --benchmark "LMEB" \
    --batch_size 256 --output_dir "lmeb_results/" --precision fp16 \
    --model_kwargs '{...}' --encode_kwargs '{"normalize_embeddings": true}'
```

which, stripped to the MTEB calls that matter, is:

```python
import mteb
tasks = mteb.get_tasks(tasks=[...])          # or mteb.get_benchmark("LMEB").tasks
evaluation = mteb.MTEB(tasks=tasks)
results = evaluation.run(model, output_folder=..., encode_kwargs=...)
```

`run.py` in this directory drives the same two calls, but:

- loads task names from `subsets.toml`'s `selection`/`diagnostic` tiers
  (ADR-0004) instead of the full `"LMEB"` benchmark or a hardcoded list;
- loads the model with `mteb.get_model(model_id)` (portable, CPU-or-GPU
  sentence-transformers loading) instead of the official repo's
  `STWrapper`, which hard-asserts `n_gpu > 0` and is unnecessary for scoring
  an arbitrary candidate model rather than reproducing the paper's exact
  15-model comparison.

**Default metric**: `ndcg_at_10` (nDCG@10) — every LMEB task sets
`main_score="ndcg_at_10"`, and the paper states it directly (§2.4): "we use
NDCG@10 as the primary metric and report NDCG@10 by default." `run.py`
reports per-task nDCG@10 (mean across a task's `hf_subset`s, matching the
paper's own "Mean (Dataset)" aggregation) plus the tier-level mean.

## Usage

```sh
# Fetch the dataset snapshot + official repo into data/ (once, or whenever
# the pinned revision in benchmark.toml changes)
uv run --project benchmark/lmeb python fetch.py

# Score a candidate embedding model on the selection tier (default)
uv run --project benchmark/lmeb python run.py \
    --model sentence-transformers/all-MiniLM-L6-v2

# Diagnostic tier (LoCoMo/LongMemEval-derived; never for selection)
uv run --project benchmark/lmeb python run.py \
    --model sentence-transformers/all-MiniLM-L6-v2 --tier diagnostic

# Both tiers in one run
uv run --project benchmark/lmeb python run.py \
    --model sentence-transformers/all-MiniLM-L6-v2 --tier all

# Explicit task subset, and a fast smoke run (caps queries per hf_subset)
uv run --project benchmark/lmeb python run.py \
    --model sentence-transformers/all-MiniLM-L6-v2 \
    --tasks ConvoMem,EPBench --limit 20
```

`run.py` prints its logs to stderr; the last stdout line is the protocol
JSON: `{"metrics": {...}, "dataset": {...}, "judge": null}`. `metrics` keys
are `"<MTEB task name>.ndcg@10"` for every task actually run, plus
`"selection.mean_ndcg@10"` and/or `"diagnostic.mean_ndcg@10"` — whichever
tier(s) contributed at least one of the tasks run. `judge` is always `null`:
LMEB has no Judge (nDCG is computed directly from qrels).

## Official scorer reuse (ADR-0003)

LMEB's task registrations (`src/tasks/*.py`, `src/abstasks/*.py`) are not
pip-installable — they must be imported as the `src` package to register
LMEB's 22 custom tasks into MTEB's task registry (`src/__init__.py` updates
`mteb.get_tasks._TASKS_REGISTRY` directly). `fetch.py` therefore clones the
official repo at the pinned SHA into `data/official/` (gitignored, never
committed) instead of vendoring it, and `run.py` adds that directory to
`sys.path` and imports `src` before calling into `mteb`.

## Layout

- `fetch.py` — downloads the `KaLM-Embedding/LMEB` dataset snapshot into
  `data/eval_data/` and clones the official repo at the pinned SHA into
  `data/official/` (gitignored).
- `run.py` — the scorer entrypoint (`benchmark.toml`'s `entrypoints.score`).
  Registers LMEB's MTEB tasks, resolves a tier or explicit `--tasks` list
  from `subsets.toml`, runs the MTEB evaluation, and reports nDCG@10.
- `subsets.toml` — the ADR-0004 `selection`/`diagnostic` task classification
  this Benchmark's contamination rule is built on.
- `benchmark.toml` — harness manifest (dataset pin, entrypoints). No
  `[judge]`/`[answerer]` table: this is a Component Benchmark with no Judge
  and no Answerer.
