# BEAM (Beyond a Million Tokens)

End-to-end Benchmark adapter for **BEAM**: Tavakoli, Salemi, Ye, Abdalla, Zamani & Mitchell,
["Beyond a Million Tokens: Benchmarking and Enhancing Long-Term Memory in
LLMs"](https://arxiv.org/abs/2510.27246), published as a conference paper at
ICLR 2026 (arXiv:2510.27246).

BEAM probes ten long-term-memory abilities with 2,000 human-validated
"probing questions" over 100 long, synthetically generated but
human-quality-checked conversations (128K–10M tokens each), scored by
LLM-judged "nugget" rubrics. Per this repo's Benchmark/Harness/Adapter/Run
vocabulary (`benchmark/CONTEXT.md`) and the engine/harness split (ADR-0001,
ADR-0002, ADR-0003), this directory holds only the fetch + score halves: it
downloads the dataset and shells into the paper's own evaluation code, but
never talks to the Engine and never generates answers itself (see "Answerer"
below and `answerer.model = ""` in `benchmark.toml`).

## Source URLs

| What | URL |
|---|---|
| Paper (arXiv) | <https://arxiv.org/abs/2510.27246> |
| Paper (PDF) | <https://arxiv.org/pdf/2510.27246> |
| Official code | <https://github.com/mohammadtavakoli78/BEAM> |
| Project page | <https://mohammadtavakoli78.github.io/beam-light/> |
| Dataset: 128K/500K/1M | <https://huggingface.co/datasets/Mohammadta/BEAM> |
| Dataset: 10M | <https://huggingface.co/datasets/Mohammadta/BEAM-10M> |

## Files

| File | Role |
|---|---|
| `pyproject.toml` | Own uv project; pinned deps needed to run `fetch.py`/`score.py` and to import the official evaluator. |
| `benchmark.toml` | Harness manifest (dataset pin, judge pin, entrypoints). Single source of truth for the HF dataset revision. |
| `_common.py` | Constants shared by `fetch.py`/`score.py` (paths, tier↔split mapping, the ten ability keys, the pinned official-repo SHA). |
| `fetch.py` | Downloads the 128K + 500K parquet shards and clones the official evaluator at a pinned commit into `data/official/`. |
| `score.py` | Scores an answers file against a tier using the official nugget evaluator. |
| `data/` | Gitignored. Populated by `fetch.py`; never committed (ADR-0002). |

## The ten memory abilities

BEAM's probing questions target ten memory abilities, two questions per
ability per conversation (20 probing questions per conversation). These are
the exact ability keys used throughout this adapter (`benchmark.toml`
metrics, `score.py`, `_common.ABILITY_KEYS`) and throughout the official
code (`src/evaluation/report_results.py`'s `column_names`, and every
`probing_questions.json` / `evaluate_*` dispatch):

1. `abstention` — **Abstention**: evaluates whether a model withholds answers when evidence is missing.
2. `contradiction_resolution` — **Contradiction Resolution**: tests the capacity to detect and reconcile inconsistent statements across widely separated turns, maintaining global coherence.
3. `event_ordering` — **Event Ordering**: assesses whether a model can recognize and reconstruct the sequence of evolving information in the dialogue.
4. `information_extraction` — **Information Extraction**: measures recall of entities and factual details in long histories.
5. `instruction_following` — **Instruction Following**: examines sustained adherence to user-specified constraints over long contexts.
6. `knowledge_update` — **Knowledge Update**: evaluates revising stored facts as new ones appear.
7. `multi_session_reasoning` — **Multi-Session Reasoning**: probes inference that integrates evidence across multiple, non-adjacent dialogue segments.
8. `preference_following` — **Preference Following**: captures personalized responses that adapt to evolving preferences.
9. `summarization` — **Summarization**: assesses the ability to abstract and compress dialogue content.
10. `temporal_reasoning` — **Temporal Reasoning**: tests reasoning about explicit and implicit time relations.

(Descriptions quoted from the official repo README. Seven of the ten are
drawn from prior benchmarks; *Instruction Following*, *Event Ordering*, and
*Contradiction Resolution* are introduced by this paper — arXiv:2510.27246
§2.2.)

Naming note: the paper's Table 1 column header calls ability 7
**"Multi-Hop Reasoning"**; the repo README's numbered list and every actual
code path (`multi_session_reasoning` key, `evaluate_multi_session_reasoning`
function) call it **"Multi-Session Reasoning"**. We use the code's name
throughout since `score.py` calls the code, not the paper's prose.

## Tier adoption plan

BEAM ships four conversation-length tiers. Per-tier conversation counts
(arXiv:2510.27246 §4.2 / repo README "Dataset Statistics", cross-checked
against the HF dataset_info `num_examples`):

| Tier | Conversations | HF dataset | Fetched by this adapter |
|---|---|---|---|
| 128K | 20 | `Mohammadta/BEAM`, split `100K` | **Yes** (now) |
| 500K | 35 | `Mohammadta/BEAM`, split `500K` | **Yes** (now) |
| 1M | 35 | `Mohammadta/BEAM`, split `1M` | Next |
| 10M | 10 | `Mohammadta/BEAM-10M` | Milestone (later) |

This is a session decision, not a paper or repo limitation: `fetch.py`
deliberately downloads only the `100K` and `500K` parquet shards (`--tier`
accordingly accepts `128k`, `500k`, or `all` = both of those). 1M is the
next tier to wire up; 10M — a separate, much larger HF dataset with a
materially different per-conversation schema (ten interlocking `plan-N`
sub-plans instead of one flat `chat`, see its HF dataset card) — is treated
as a later milestone requiring its own `fetch.py`/`score.py` work, not a
drop-in extension of `TIER_TO_SPLIT`.

### Tier naming

The dataset's own infrastructure names the 128K tier **"100K"**: the HF
`dataset_info.splits` entry is named `100K` (`num_examples: 20`), its parquet
shard is `data/100K-00000-of-00001.parquet`, and the official repo's
in-repo copy lives under `chats/100K/`. The paper's prose ("For conversations
of 128K, 500K, and 1M tokens…", arXiv:2510.27246 §2.2.1) and the repo
README's own "Dataset Statistics" table both call the very same tier
**"128K"**. Both names denote the same 20 conversations at the same target
length; `_common.TIER_TO_SPLIT = {"128k": "100K", "500k": "500K"}` documents
the mapping so this isn't rediscovered by trial and error. This benchmark's
own tier names (`128k`, `500k`, in `--tier` and in metric namespacing) follow
the paper-prose/README convention since that's the more legible one; the HF
split identifier is an internal implementation detail confined to
`fetch.py`/`score.py`.

## Dataset & licensing

Dual-licensed, confirmed from the official repo's `LICENSE` file and
`README.md` "License" section, and independently from the HF dataset card's
`license:` tag:

- **Code** (`https://github.com/mohammadtavakoli78/BEAM`): MIT License,
  Copyright (c) 2025 Mohammad Tavakoli.
- **Data** (both `Mohammadta/BEAM` and `Mohammadta/BEAM-10M`): Creative
  Commons Attribution-ShareAlike 4.0 International (CC BY-SA 4.0) —
  <https://creativecommons.org/licenses/by-sa/4.0/>.

Per ADR-0002, the dataset itself is never committed to this repository;
`fetch.py` downloads it into the gitignored `data/` on demand.

## Judge provenance

**Pin: `judge.model = "gpt-4.1-mini"`, temperature 0. This is a
documented fallback, not a paper-verified pin** — the paper never names a
judge model; we pin the official evaluation code's own hardcoded default.

What we checked in the paper (arXiv:2510.27246), specifically looking for the
model behind the nugget-scoring "LLM judge":

- §2.4 "Evaluation" states that "*System responses are scored against these
  nuggets by an LLM judge (Listing 20, Appendix H), which assigns 0
  (unsatisfied), 0.5 (partially satisfied), or 1 (fully satisfied)*" and that
  event ordering additionally uses "*an LLM equivalence detector (using the
  prompt in Listing 21 in Appendix H)*" — both describe the **procedure** and
  give the **prompt template**, but neither names a **model**.
- Appendix H's own introduction ("Here we provide the prompts used in
  different stages of our framework") likewise gives prompt text only, with
  no listing-to-model mapping.
- We searched every named model in the paper (GPT-4.1, GPT-4.1-mini,
  GPT-4.1-nano, Gemini-2.0-flash, Qwen2.5-32B-AWQ, LLaMA-3.3-70B,
  Llama-4-Maverick-fp8) for a connection to §2.4 or Listing 20/21. Each is
  tied to a *different* pipeline stage (e.g. GPT-4.1-mini is named in §2.3
  for *probing-question generation*, a distinct step from §2.4's judging),
  never to the judge itself.
- We also checked Appendix B.1/B.2 (dataset statistics, human-evaluation
  protocol) and C.1–C.5 (ablations) — these cover annotator agreement and
  embedding/indexing/retriever ablations, not the judge model.

So the paper is genuinely silent on the judge model identity. The **official
evaluation code** is not: `src/llm.py` (in the official repo, pinned commit
`b2da22eac88bb0874c64665f13457eb99835774a`) hardcodes

```python
gpt_llm_obj = BuildLLm(model_url=None,
                       model_name="gpt-4.1-mini",
                       api_key=cfg["gpt"]["api_key"],
                       temperature=0)
...
gpt_llm = gpt_llm_obj.build_llm()
```

and `src/evaluation/run_evaluation.py`'s only entrypoint calls
`batch_run_evaluation(..., model=gpt_llm, ...)` — every nugget-rubric score
(Listing 20) and every event-ordering equivalence check (Listing 21) in every
published BEAM number is therefore judged by `gpt-4.1-mini` at temperature 0,
via `langchain_openai.ChatOpenAI`. `run_evaluation.py`'s argparse has no
`--model`/`--judge` flag, so this is not an incidental default a user was
expected to override — it is what the paper's own results were produced
with. We adopt it as our pin. `judge.prompt_version = "v1"` marks this as our
first pin of the official repo's judge prompts (`unified_llm_judge_base_prompt`
in `src/prompts.py`, matching Listing 20 verbatim; the inline system/user
prompt in `src/evaluation/compute_metrics.py`'s `llm_equivalence()`, matching
Listing 21) at `GIT_REVISION`; bump it if we ever re-pin against a different
official commit whose prompt text has changed.

## Nugget-evaluation protocol

(arXiv:2510.27246 §2.4, cross-checked against
`src/evaluation/compute_metrics.py` and `src/evaluation/report_results.py`
at the pinned commit.)

1. Each of the 20 probing questions per conversation (2 per ability × 10
   abilities) has a **rubric**: an ordered list of atomic, self-contained
   "nuggets" — minimal semantic units a compliant answer must satisfy.
2. For nine of the ten abilities, each nugget is scored **0** (unsatisfied),
   **0.5** (partially satisfied), or **1** (fully satisfied) by the judge LLM
   via the rubric-scoring prompt (`unified_llm_judge_base_prompt`, paper
   Listing 20), and the per-question score (`llm_judge_score`) is the mean
   over that question's nuggets.
3. `event_ordering` is scored differently: the judge LLM aligns predicted
   event mentions to reference nuggets via a binary same-event/same-fact
   equivalence prompt (paper Listing 21), Kendall's tau-b is computed over
   the aligned rank sequences, and normalized to `[0, 1]` as
   `tau_norm = (tau_b + 1) / 2`. This — not the also-computed
   `llm_judge_score` — is the number the paper's Table 1 reports for this
   ability (confirmed from `report_results.py`'s own aggregation, see next).
4. Ability-level score for one conversation = mean of that ability's 2
   per-question scores (`llm_judge_score`, or `tau_norm` for
   `event_ordering`) — this exact selection is `report_results.py`'s
   aggregation, reproduced by `score.py`.
5. Tier-level per-ability score = mean across all conversations in the tier.
   `score.py` additionally reports `<tier>.overall` = the unweighted mean of
   the ten per-ability tier scores, matching the paper's Table 1 "Average"
   row convention.

`score.py` does not reimplement any of steps 1–3 (the actual LLM judging,
JSON-repair parsing, and Kendall tau-b computation): it imports and calls the
official `src.evaluation.run_evaluation.run_evaluation()` function from the
repo cloned by `fetch.py` into `data/official/`, per ADR-0003 ("scoring
reuses the official Python scorers … only trivial deterministic metrics may
be ported"). Steps 4–5 (plain arithmetic means) are re-implemented directly
in `score.py`, mirroring `report_results.py` line for line, so scoring can
run against one harness-supplied answers file instead of a pre-existing
`results/<tier>/<index>/<row_name>.json` tree with a separate
`report_results.py --row_names` pass. The one intentional behavior change:
`run_evaluation()`'s CLI hardcodes the judge to the module-level `gpt_llm`
(`gpt-4.1-mini`, unconditionally); `score.py` instead builds its own
`ChatOpenAI` from `benchmark.toml`'s `[judge].model` and passes it as the
`model=` argument every official `evaluate_*` function already accepts, so
the judge is config-driven rather than hardcoded — the actual scoring logic
those functions run is untouched.

## Answers-file format

`score.py --answers <dir>` expects `<dir>/<conversation_id>.json` — one file
per conversation, named by that conversation's `conversation_id` (the HF
dataset column of the same name).

The shape matches exactly what the official
`src/evaluation/run_evaluation.py` consumes as its `answers_directory`
argument, and what the official `src/answer_probing_questions/answer_generation.py`
itself produces (`object_temp = question; object_temp["llm_response"] = response`):
it is **the tier's `probing_questions` dict for that conversation, with an
`"llm_response"` string added to every question object**. Concretely: a
top-level JSON object keyed by the ten ability names above; each value is
the list of that ability's 2 question objects (as they appear verbatim in
the `probing_questions` column of the tier's parquet file, each carrying at
least `question` and `rubric`, plus other ability-specific fields such as
`ideal_answer`/`difficulty`); every question object additionally carries
`"llm_response"`, the Answerer's generated response to that `question`.

```json
{
  "abstention": [
    {
      "question": "How did the user feedback influence the UI/UX improvements I made before the public launch?",
      "ideal_response": "Based on the provided chat, there is no information related to how user feedback influenced UI/UX improvements.",
      "difficulty": "medium",
      "rubric": ["Based on the provided chat, there is no information related to how user feedback influenced UI/UX improvements"],
      "llm_response": "<the Answerer's response to this question>"
    },
    { "...": "second abstention question, same shape" }
  ],
  "contradiction_resolution": [ "...2 questions..." ],
  "event_ordering": [ "...2 questions..." ],
  "information_extraction": [ "...2 questions..." ],
  "instruction_following": [ "...2 questions..." ],
  "knowledge_update": [ "...2 questions..." ],
  "multi_session_reasoning": [ "...2 questions..." ],
  "preference_following": [ "...2 questions..." ],
  "summarization": [ "...2 questions..." ],
  "temporal_reasoning": [ "...2 questions..." ]
}
```

`score.py` reads `rubric` from the tier's own `probing_questions` column (not
from the answers file) — exactly like the official `get_rubric()` does — and
reads `question`/`llm_response` from the answers file, matched to the
probing-questions list *by index*, not by text. Both lists must therefore
stay in the same order the parquet's `probing_questions` column defines,
with exactly 2 entries per ability; `score.py` validates this (all ten
ability keys present, matching lengths) before invoking the official
evaluator and fails with a clear error otherwise.

## Paper answerer setup

`answerer.model = ""` in `benchmark.toml`: this repo's Answerer is not yet
pinned (Constraints: nothing here talks to an Engine yet). The paper's own
"Answerer"-equivalent setup — its long-context and RAG baselines — is
recorded here as reference for whoever pins ours (arXiv:2510.27246 §4.1
"Experimental Setup", cross-checked against Appendix C.4):

- **Backbone models evaluated**: two proprietary long-context LLMs
  (`GPT-4.1-nano`, `Gemini-2.0-flash`, both advertised 1M context) and two
  open-source models (`Qwen2.5-32B-AWQ`, `Llama-4-Maverick-fp8`).
- **Long-context baseline**: the entire conversation history is placed in
  context, followed by the probing question. `Qwen2.5-32B-AWQ` is evaluated
  at a 128K context length for this baseline (its practical limit in their
  setup); the others at their full advertised window. At the 10M tier — a
  tier this adapter does not yet fetch — no model's context fits the whole
  conversation, so the largest recent segment that does fit is used instead.
- **RAG baseline**: each user–assistant turn pair is embedded as one
  document (`BAAI/bge-small-en-v1.5`) and stored in FAISS
  (`IndexFlatIP` in the primary experiments, per Appendix C.4); at inference
  the top-5 most similar documents are retrieved and passed to the LLM
  alongside a 32K context budget.
- **Sampling**: nucleus sampling at **temperature 0** for
  answering/inference generally; **temperature 0.1** is used only for the
  conversation-plan / user-turn / assistant-turn *generation* stages (i.e.
  dataset construction, not answering), to encourage diversity there.
  Maximum output length is each model's own default, except `Llama-3.3-70B`
  during user-turn generation, capped at 6K tokens.

This is descriptive context for the paper's own baselines, not a
prescription for this repo's future Answerer — no Engine/Answerer wiring
exists in this directory (ADR-0001: the Engine's contract stops at
Retrieve; answering is the Harness's/client's job).

## Running

```console
$ export OPENAI_API_KEY=sk-...           # judge model + official module import both need it
$ uv run --project benchmark/beam python fetch.py
$ uv run --project benchmark/beam python score.py --answers /path/to/answers-dir --tier 128k
```

`fetch.py` downloads the 128K + 500K parquet shards (idempotent — reruns are
near-instant no-ops once cached) and clones the pinned official-repo commit
into `data/official/`; its last stdout line is
`{"name": "beam", "revision": "<hf sha>", "sha256": "<manifest sha256>"}`.

`score.py --tier {128k,500k,all}` scores an already-produced answers
directory (see "Answers-file format") against that tier and prints
`{"metrics": {...}, "dataset": {...}, "judge": {...}}` as its last stdout
line, with `metrics` namespaced per tier, e.g. `128k.temporal_reasoning`,
`128k.overall`, `500k.abstention`, `500k.overall`. All progress/diagnostic
output — including the official evaluator's own `print()` calls and any
`nltk`/`sentence-transformers` model-download noise triggered by importing
it — is redirected to stderr, never stdout.

Note: the official evaluation code imports `sentence-transformers` (and,
through it, `torch`/`transformers`) and downloads NLTK's `punkt`/`punkt_tab`
data at import time; the first `score.py` run needs network access for
those regardless of tier, in addition to `OPENAI_API_KEY` for the judge.

## Pinned revisions

| What | Pin | Source |
|---|---|---|
| HF dataset `Mohammadta/BEAM` | `3205395e897e7318c7b094ef4e6047b9b82dbb03` | `benchmark.toml` `[dataset].revision`; read via `GET https://huggingface.co/api/datasets/Mohammadta/BEAM/refs` |
| HF dataset `Mohammadta/BEAM-10M` (not yet fetched) | `9b2096193fe74e2837e4713e483351e19817773c` | `GET https://huggingface.co/api/datasets/Mohammadta/BEAM-10M/refs`, recorded here for the 10M milestone |
| GitHub `mohammadtavakoli78/BEAM` | `b2da22eac88bb0874c64665f13457eb99835774a` | `_common.GIT_REVISION`; read via `gh api repos/mohammadtavakoli78/BEAM/commits/main` |
| Judge model | `gpt-4.1-mini`, temperature 0 | `benchmark.toml` `[judge].model`; see "Judge provenance" above |
