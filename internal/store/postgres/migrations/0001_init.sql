-- 0001_init.sql: the storage port's own schema (ADR-0007). River's tables
-- are migrated separately by rivermigrate; this file only owns turns,
-- passages, and engine_state.
CREATE EXTENSION IF NOT EXISTS vector;

-- turns is the Engine's source of truth: one row per Turn, immutable once
-- Ingested. indexed_at is set by WritePassages once the Turn's Session has
-- been consolidated into Passages, and is what the settle barrier polls.
CREATE TABLE turns (
  seq         bigserial PRIMARY KEY,
  namespace   text NOT NULL,
  session_id  text NOT NULL,
  id          text NOT NULL,
  speaker     text NOT NULL,
  content     text NOT NULL,
  event_time  timestamptz NOT NULL,
  ingested_at timestamptz NOT NULL DEFAULT now(),
  indexed_at  timestamptz,
  UNIQUE (namespace, session_id, id)
);
CREATE INDEX turns_pending ON turns (namespace, seq) WHERE indexed_at IS NULL;
CREATE INDEX turns_session ON turns (namespace, session_id, seq);

-- passages is derived data (ADR-0008): rebuilt by consolidation and Reindex,
-- never written to directly by a client. embedding has no typmod until
-- EnsureEmbedder or BeginReindex sizes it to the configured Embedder's
-- Dimensions.
CREATE TABLE passages (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  namespace   text NOT NULL,
  session_id  text NOT NULL,
  turn_ids    text[] NOT NULL,
  content     text NOT NULL,
  tsv         tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
  embedding   vector,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX passages_session ON passages (namespace, session_id);
CREATE INDEX passages_turn_ids ON passages USING gin (turn_ids);
CREATE INDEX passages_tsv ON passages USING gin (tsv);

-- engine_state records the single Embedder the store is currently indexed
-- with (ADR-0008: swapping it is a Reindex, never a migration). One row,
-- key='embedder'.
CREATE TABLE engine_state (
  key         text PRIMARY KEY,
  embedder_id text NOT NULL,
  dimensions  int NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
