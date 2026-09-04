# Engine

Loom: a self-hostable context engine. Clients (agents, harnesses, applications) feed it conversational history and later retrieve the context they need; answer generation always belongs to the client, never to Loom.

## Language

**Context Engine**:
Loom itself — the system that accepts conversational history and produces Context Bundles on demand.
_Avoid_: memory system, RAG service

**Ingest**:
The act of handing conversational history to the Engine for it to remember.
_Avoid_: index, upload, sync

**Retrieve**:
The act of asking the Engine for the context relevant to a query; returns a Context Bundle.
_Avoid_: search, query (as a verb), recall

**Context Bundle**:
What Retrieve returns — the material a client places in front of its own LLM.
_Avoid_: search results, memories, chunks

**Turn**:
One utterance — speaker, content, time. The unit a client Ingests; immutable once Ingested; the Engine's source of truth.
_Avoid_: message, event

**Session**:
A client-delimited sequence of Turns forming one conversation.
_Avoid_: thread, chat

**Passage**:
A unit of memory the Engine derives from Turns and ranks at Retrieve; what a Context Bundle lists, always with provenance back to its Turns. Derived data — rebuilt by Reindex, never a source of truth.
_Avoid_: chunk, snippet, memory (as a noun)

**Namespace**:
The isolation boundary within one Engine deployment; every Ingest and Retrieve targets exactly one Namespace.
_Avoid_: tenant, user, workspace

**Embedder**:
The swappable component that turns text into embeddings; candidate Embedders are selected via the Component Benchmark, and swapping one triggers a Reindex.
_Avoid_: embedding service, vectorizer

**Reindex**:
Rebuilding everything the Engine derives from Ingested history (embeddings, indexes) after a component swap. Ingested history is the source of truth; a Reindex never loses it. Completion is observable through the same settle barrier as Ingest.
_Avoid_: migration, re-sync
