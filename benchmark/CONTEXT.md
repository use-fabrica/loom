# Benchmarking

Measures the Engine against published long-term-memory benchmarks (LoCoMo, LongMemEval, LMEB, BEAM) so that improvements are attributable and regressions are caught.

## Language

**Benchmark**:
A named dataset plus evaluation protocol plus metric set (e.g. LoCoMo).
_Avoid_: eval, test suite

**End-to-end Benchmark**:
A Benchmark that exercises the Engine solely through its public Ingest/Retrieve contract (LoCoMo, LongMemEval, BEAM).

**Component Benchmark**:
A Benchmark that evaluates one internal layer of the Engine in isolation (LMEB, which targets the embedding layer).

**Harness**:
The runner that executes any Benchmark against the Engine.
_Avoid_: eval framework, test runner

**Adapter**:
Per-Benchmark glue that translates a Benchmark's dataset and protocol into Engine contract calls.
_Avoid_: plugin, driver

**Subject**:
What a Run measures — an Engine build for an End-to-end Benchmark, a candidate component (e.g. an embedding model) for a Component Benchmark.

**Run**:
One execution of one Benchmark against one Subject.

**Report**:
The output of a Run — scores plus the provenance that reproduces it: Subject identity, dataset version, and (for End-to-end Benchmarks) Judge and Answerer identity.
_Avoid_: results, scores (bare)

**Judge**:
The pinned LLM (model plus prompt version) used to score answers where a Benchmark's metrics require one.
_Avoid_: evaluator, grader

**Answerer**:
The pinned LLM (model plus prompt version) the Harness uses to turn a Context Bundle into an answer for an End-to-end Benchmark; a controlled variable of every Run, never part of the Engine.
_Avoid_: reader, generator, answer model

**Baseline**:
The reference Report that later Runs of the same Benchmark are compared against.
