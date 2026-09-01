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
