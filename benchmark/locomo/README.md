# LoCoMo

End-to-end Benchmark (`kind = "e2e"`). Adapts [LoCoMo](https://github.com/snap-research/locomo)
(Maharana et al., *Evaluating Very Long-Term Conversational Memory of LLM
Agents*, [arXiv:2402.17753](https://arxiv.org/abs/2402.17753), ACL 2024) to
Loom's Harness contract. This directory owns the QA task only (LoCoMo also
defines event-summarization and multimodal-dialogue-generation tasks; those
are out of scope here).

The Engine's Ingest/Retrieve contract is blocked on issue #1, so nothing
here talks to an Engine. `fetch.py` and `score.py` implement fetch +
scoring-from-an-answers-file; `[answerer].model` in `benchmark.toml` is
deliberately empty until an Answerer is pinned.

## Protocol

```
uv run --project benchmark/locomo python fetch.py
uv run --project benchmark/locomo python score.py --answers <path> [--mode lexical|judge|both]
```

- `fetch.py`: idempotent. Downloads `data/locomo10.json` at the pinned
  commit, and clones the official repo at the same commit into
  `data/official/` (source for the lexical scorer; see "Dual scoring"
  below). Last stdout line: `{"name": "locomo10", "revision": "<sha>", "sha256": "<hex>"}`.
- `score.py --answers <path>`: `<path>` is a JSON object mapping **question
  id** to **answer string**. The dataset has no native question id; this
  adapter mints one as `"<sample_id>::<index-within-conversation>"`
  (0-based, in `qa` array order — stable because JSON preserves list
  order). Only question ids present in the answers file are scored; a
  `lexical.coverage` / `judge.coverage` metric reports what fraction of the
  1986 QA pairs were covered. Last stdout line:
  `{"metrics": {...}, "dataset": {...}, "judge": {...} | null}`.
  All logging goes to stderr; exit is non-zero on failure.

## Dual scoring

Per [ADR-0003](../../docs/adr/0003-official-scorers-and-locomo-dual-scoring.md),
scoring shells out to (here: imports, in-process) the official Python
scorer so numbers are comparable to the paper, and LoCoMo specifically is
*dual-scored*: the official lexical metric for paper parity, plus a pinned
Judge for paraphrase-tolerant tracking. Both are recorded in every score
output (`lexical.*` / `judge.*`), and `judge` is `null` only when
`--mode lexical`.

**The official LoCoMo QA metric is F1 only.** `score.py` imports
`eval_question_answering` unmodified from
`data/official/task_eval/evaluation.py` (cloned by `fetch.py`). Reading
that file and the paper (§4.1, Table 2: "Results are based on F1-score for
answer prediction") confirms the QA task is scored with a single
word-overlap F1 metric per category; `evaluation.py` also defines
`bert_score` and `rougel_score`/`rl` functions, but a repo-wide grep shows
`bert_score(` has no call site at all and `rougel_score`/`rl` are only
called from `eval_dialogue_system`, which backs LoCoMo's separate
multimodal-dialogue-generation task (BERTScore appears to be dead code in
the official repo; ROUGE-L, alongside FactScore, is used for LoCoMo's
event-summarization task instead — see §4.2). So despite `lexical.*` being
a generic namespace in Loom's harness, this adapter reports F1 per category
plus overall, not BERTScore/ROUGE-L, because that is what the official
LoCoMo QA scorer actually computes. `evaluation.py` still imports
`bert_score` unconditionally at module load time, so `bert-score` stays a
declared dependency in `pyproject.toml` even though the function is never
invoked on this path.

## Mem0-vs-Zep judge divergence

Per ADR-0003: "judge choice is why public Mem0-vs-Zep numbers contradict
each other." Vendor-published LoCoMo numbers must never be cited as
comparable to a Loom Report without re-running them under Loom's own pinned
Judge (`benchmark.toml [judge]`) — different judges (and different judge
prompts) produce materially different pass rates on the same answers, which
is exactly the divergence ADR-0003 flags between the Mem0 and Zep papers'
self-reported LoCoMo results.

## CC BY-NC handling

`LICENSE.txt` at the root of `snap-research/locomo` is [CC BY-NC 4.0
International](https://creativecommons.org/licenses/by-nc/4.0/deed.en) and
covers the entire repository — there is no separate license for the code
under `task_eval/`. Consequences enforced by this adapter, per
[ADR-0002](../../docs/adr/0002-benchmarks-in-repo-datasets-fetched.md):

- `data/locomo10.json` (the dataset) is downloaded by `fetch.py`, never
  committed.
- `data/official/` (the official repo, code included) is *cloned*, not
  vendored: `fetch.py` `git clone`s it into `data/` at run time and
  `score.py` imports from it in-process; none of that code is copied into
  this repo, and `data/` stays untracked.
- Because the license is NonCommercial, nothing under `data/` may be
  redistributed or used commercially by anything consuming this benchmark's
  output. This is a licensing constraint on the dataset/official code, not
  legal advice.

## Category mapping

The dataset labels each `qa` entry with an integer `category` (1-5) but
`data/locomo10.json` and `task_eval/evaluation.py` never name them. The
paper (§4.1) names five categories — single-hop, multi-hop, temporal
reasoning, open-domain knowledge, adversarial — but doesn't give their
integer ids either. We inferred the mapping from two independent primary
sources:

1. `eval_question_answering`'s own branches: category `1` splits the
   answer on commas and averages partial F1 across sub-answers (matches
   "multi-hop": combining several session-scoped facts); category `5` is
   graded by checking the response for "no information available" /
   "not mentioned" (matches "adversarial": the paper's unanswerable-by-design
   questions); categories `2`, `3`, `4` are graded as single free-text
   spans.
2. Evidence-count statistics over `data/locomo10.json` (1986 questions
   checked directly): category `1` averages 3.13 evidence turns per
   question (consistent with multi-hop synthesis); category `2` averages
   1.17 and its example questions ask "when" (temporal reasoning); category
   `3` is the smallest category (n=96) with some zero-evidence questions
   (consistent with open-domain/commonsense questions answerable partly
   from outside the conversation); category `4` is the largest (n=841) with
   ~1 evidence turn per question (consistent with single-hop extraction).

| category | name | n | avg evidence |
|---|---|---|---|
| 1 | multi_hop | 282 | 3.13 |
| 2 | temporal | 321 | 1.17 |
| 3 | open_domain | 96 | 2.08 |
| 4 | single_hop | 841 | 1.07 |
| 5 | adversarial | 446 | 1.03 |

`score.py`'s `CATEGORY_NAMES` encodes this mapping and namespaces metrics
accordingly (e.g. `lexical.f1_multi_hop`, `judge.accuracy_adversarial`).

Separately, category-5 (adversarial) entries in this dataset release carry
an `adversarial_answer` field instead of `answer`
(`eval_question_answering` reads `line['answer']` unconditionally before
branching on category, so `score.py` backfills it from `adversarial_answer`
when `answer` is absent — the adversarial branch never actually reads that
value, it only inspects the prediction text).

## Judge prompt

`judge_prompt.md` (v1) adapts LongMemEval's binary autoeval prompt
(`xiaowu0162/LongMemEval`, `src/evaluation/evaluate_qa.py`,
`get_anscheck_prompt`, pinned at `9e0b455f4ef0e2ab8f2e582289761153549043fc`):
a standard yes/no correctness prompt for categories 1-4, and — mirroring
LongMemEval's dedicated abstention-question prompt — a separate yes/no
prompt for the adversarial category (5) that asks whether the response
correctly declines to answer, worded to line up with the official lexical
scorer's own "no information available"/"not mentioned" check. The judge
model, `gpt-4o-2024-08-06`, is the exact string LongMemEval's
`evaluate_qa.py` `model_zoo` maps its `"gpt-4o"` alias to; it is called via
the OpenAI API (`OPENAI_API_KEY` env var), `temperature=0`, matching
LongMemEval's own call.

## Paper answerer setup

For later parity claims: the LoCoMo paper's QA experiments (§5,
"Experimental Setup") did not use a single fixed answerer. They evaluated
three regimes, all zero-shot (no fine-tuning), `temperature=0`:

1. **Base** — LLMs given only what fits in their context window, earliest
   dialogue truncated: Mistral-Instruct-7B, Llama-2-Chat-70B,
   gpt-3.5-turbo, gpt-4-turbo.
2. **Long-context** — gpt-3.5-turbo-16k, given as much of the conversation
   as fits in its larger window.
3. **Retrieval-augmented generation (RAG)** — [DRAGON](https://arxiv.org/abs/2302.07452)
   (Lin et al., 2023) as retriever over dialog history / observations /
   session summaries, top-k passed to gpt-3.5-turbo-16k as reader.

Table 2 (F1, higher is better; "Overall" column) reports gpt-4-turbo as the
best base model at 32.4 F1, still well below the paper's human baseline of
87.9 F1; all models specifically struggle on the adversarial and
open-domain-knowledge categories. Loom's own Answerer (once pinned in
`benchmark.toml [answerer]`) is a different, Engine-agnostic model choice —
this section exists only so a future comparison against the paper's
reported numbers knows precisely what those numbers assume.

## Sources

- Repo: <https://github.com/snap-research/locomo> (commit
  `3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376`)
- Dataset file: `data/locomo10.json` in that repo
- License: `LICENSE.txt` in that repo (CC BY-NC 4.0 International)
- Official scorer: `task_eval/evaluation.py`, `task_eval/evaluate_qa.py` in
  that repo
- Paper: Maharana et al. 2024, <https://arxiv.org/abs/2402.17753>
  ("Evaluating Very Long-Term Conversational Memory of LLM Agents")
- Judge prompt/model source: <https://github.com/xiaowu0162/LongMemEval>
  (commit `9e0b455f4ef0e2ab8f2e582289761153549043fc`),
  `src/evaluation/evaluate_qa.py`
- [ADR-0002](../../docs/adr/0002-benchmarks-in-repo-datasets-fetched.md),
  [ADR-0003](../../docs/adr/0003-official-scorers-and-locomo-dual-scoring.md)
