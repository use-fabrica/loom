# Embeddings are derived data; Reindex is a first-class operation

Turns are the Engine's only source of truth; Passages and their embeddings are derived and rebuildable. The Embedder is a swappable provider (any OpenAI-compatible endpoint or hosted API), selected via the Component Benchmark (LMEB), so swapping it — including a vector-dimension change — is a Reindex: a background job that re-derives Passages and re-embeds them from Turns, with completion observable through the same settle barrier as Ingest (ADR-0006). Segmentation strategy (how Turns become Passages) is thereby a per-Run experiment variable, never a contract change.

Rejected: treating stored vectors as part of the primary record (turns every embedder swap into a destructive migration and freezes Passage granularity into the contract), and in-process embedding inference (lost its rationale when single-binary deployment was dropped in favor of compose + Helm).
