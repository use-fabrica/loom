// Package postgres implements the store.Store port (internal/store/store.go)
// on a single Postgres database: pgvector for vector search, native
// tsvector for lexical search, and River for durable job state, all behind
// one pgxpool.Pool (ADR-0007).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/domain"
	"github.com/use-fabrica/loom/internal/store"
)

// Store implements store.Store on Postgres.
type Store struct {
	pool         *pgxpool.Pool
	insertClient *river.Client[pgx.Tx]
	log          *zap.Logger
}

var _ store.Store = (*Store)(nil)

// Open connects to databaseURL, applies every pending migration (this
// package's own schema, then River's — see migrate.go), and returns a Store
// ready to Ingest.
//
// Migrations run first on a throwaway pool with no AfterConnect hook: the
// pgvector codec registration below fails until the `vector` extension
// exists, and the first migration is what creates it, so no connection can
// need the codec registered before migrations have run.
func Open(ctx context.Context, databaseURL string, log *zap.Logger) (*Store, error) {
	bootstrap, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer bootstrap.Close()

	if err := runMigrations(ctx, bootstrap); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	insertClient, err := newInsertClient(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}

	log.Info("postgres store ready")

	return &Store{pool: pool, insertClient: insertClient, log: log}, nil
}

// Close releases the underlying connection pool. It does not stop any
// Runner built from this Store; stop those first.
func (s *Store) Close() {
	s.pool.Close()
}

// consolidateMetadata is the River job Metadata payload Ingest attaches to
// every ConsolidateArgs insert, so Barrier can attribute a discarded job it
// finds back to the Namespace it was working.
type consolidateMetadata struct {
	Namespace string `json:"namespace"`
}

// seqRange tracks the [min, max] ingest sequence numbers newly inserted for
// one Session within a single Ingest call.
type seqRange struct {
	min, max int64
}

// extend widens the range to include seq, initializing it on first use.
func (r *seqRange) extend(seq int64) {
	if r.min == 0 || seq < r.min {
		r.min = seq
	}
	if seq > r.max {
		r.max = seq
	}
}

// Ingest implements store.Store.
func (s *Store) Ingest(ctx context.Context, namespace string, sessions []domain.Session) (domain.Cursor, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("ingest: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	// Queue every Turn across every Session as one batch (one round trip),
	// tracking which Session each queued statement belongs to so the
	// per-Session [min,max] inserted-seq range can be recovered from the
	// results, which arrive in the same order they were queued.
	batch := &pgx.Batch{}
	owningSession := make([]string, 0)
	for _, session := range sessions {
		for _, turn := range session.Turns {
			batch.Queue(`
				INSERT INTO turns (namespace, session_id, id, speaker, content, event_time)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (namespace, session_id, id) DO NOTHING
				RETURNING seq`,
				namespace, session.ID, turn.ID, turn.Speaker, turn.Content, turn.EventTime)
			owningSession = append(owningSession, session.ID)
		}
	}

	ranges := make(map[string]*seqRange)
	if batch.Len() > 0 {
		results := tx.SendBatch(ctx, batch)
		for _, sessionID := range owningSession {
			var seq int64
			switch err := results.QueryRow().Scan(&seq); {
			case errors.Is(err, pgx.ErrNoRows):
				// Redelivered Turn: (namespace, session_id, id) already
				// present, ON CONFLICT DO NOTHING skipped it.
			case err != nil:
				results.Close() //nolint:errcheck // returning the query error below
				return 0, fmt.Errorf("ingest: insert turn: %w", err)
			default:
				r, ok := ranges[sessionID]
				if !ok {
					r = &seqRange{}
					ranges[sessionID] = r
				}
				r.extend(seq)
			}
		}
		if err := results.Close(); err != nil {
			return 0, fmt.Errorf("ingest: insert turns: %w", err)
		}
	}

	// Enqueue consolidation only for Sessions that actually gained a Turn:
	// a fully redelivered request enqueues nothing, so a repeated Ingest
	// never re-derives Passages that already exist.
	for sessionID, r := range ranges {
		meta, err := json.Marshal(consolidateMetadata{Namespace: namespace})
		if err != nil {
			return 0, fmt.Errorf("ingest: encode consolidate metadata: %w", err)
		}
		args := store.ConsolidateArgs{
			Namespace: namespace,
			SessionID: sessionID,
			FromSeq:   r.min,
			ToSeq:     r.max,
		}
		if _, err := s.insertClient.InsertTx(ctx, tx, args, &river.InsertOpts{Metadata: meta}); err != nil {
			return 0, fmt.Errorf("ingest: enqueue consolidate: %w", err)
		}
	}

	var cursor int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM turns WHERE namespace = $1`, namespace).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("ingest: read cursor: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("ingest: commit: %w", err)
	}
	return domain.Cursor(cursor), nil
}

// embedderLockKey is a pg_advisory_xact_lock key serializing concurrent
// EnsureEmbedder calls — distinct from migrate.go's schemaLockKey (that
// one guards schema DDL; this one guards the first-write race below).
// `SELECT ... FOR UPDATE` locks nothing when the row is absent, so
// without this, two replicas starting on a fresh store can both take the
// no-rows branch and race two plain INSERTs on the same primary key.
const embedderLockKey int64 = 472_819_004

// EnsureEmbedder implements store.Store.
func (s *Store) EnsureEmbedder(ctx context.Context, emb store.Embedder) error {
	if emb.Dimensions <= 0 {
		return fmt.Errorf("ensure embedder: dimensions must be positive, got %d", emb.Dimensions)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ensure embedder: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, embedderLockKey); err != nil {
		return fmt.Errorf("ensure embedder: acquire lock: %w", err)
	}

	var existingID string
	var existingDims int
	err = tx.QueryRow(ctx, `SELECT embedder_id, dimensions FROM engine_state WHERE key = 'embedder' FOR UPDATE`).Scan(&existingID, &existingDims)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `INSERT INTO engine_state (key, embedder_id, dimensions) VALUES ('embedder', $1, $2)`, emb.ID, emb.Dimensions); err != nil {
			return fmt.Errorf("ensure embedder: record state: %w", err)
		}
		if err := setEmbeddingDimensions(ctx, tx, emb.Dimensions); err != nil {
			return fmt.Errorf("ensure embedder: %w", err)
		}
	case err != nil:
		return fmt.Errorf("ensure embedder: read state: %w", err)
	case existingID != emb.ID || existingDims != emb.Dimensions:
		return store.ErrEmbedderMismatch
	default:
		// Same Embedder already recorded; no-op.
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ensure embedder: commit: %w", err)
	}
	return nil
}

// setEmbeddingDimensions sizes the passages.embedding column and its HNSW
// index to dims. dims is validated by every caller (> 0) before this runs:
// it is interpolated directly into DDL because Postgres type modifiers
// don't accept bind parameters.
func setEmbeddingDimensions(ctx context.Context, tx pgx.Tx, dims int) error {
	if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE passages ALTER COLUMN embedding TYPE vector(%d)`, dims)); err != nil {
		return fmt.Errorf("resize embedding column: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE INDEX IF NOT EXISTS passages_embedding_hnsw ON passages USING hnsw (embedding vector_cosine_ops)`); err != nil {
		return fmt.Errorf("create embedding index: %w", err)
	}
	return nil
}

// reindexUniqueStates is passed as UniqueOpts.ByState on the ReindexArgs
// insert so that concurrent BeginReindex calls coalesce onto one job instead
// of queuing duplicates: any reindex job not yet finalized (still available,
// scheduled, running, retryable, or pending) blocks a second insert.
var reindexUniqueStates = []rivertype.JobState{
	rivertype.JobStateAvailable,
	rivertype.JobStateScheduled,
	rivertype.JobStateRunning,
	rivertype.JobStateRetryable,
	rivertype.JobStatePending,
}

// BeginReindex implements store.Store.
func (s *Store) BeginReindex(ctx context.Context, emb store.Embedder) (domain.Cursor, error) {
	if emb.Dimensions <= 0 {
		return 0, fmt.Errorf("begin reindex: dimensions must be positive, got %d", emb.Dimensions)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin reindex: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	if _, err := tx.Exec(ctx, `TRUNCATE passages`); err != nil {
		return 0, fmt.Errorf("begin reindex: truncate passages: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE turns SET indexed_at = NULL`); err != nil {
		return 0, fmt.Errorf("begin reindex: reset indexed_at: %w", err)
	}
	if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS passages_embedding_hnsw`); err != nil {
		return 0, fmt.Errorf("begin reindex: drop embedding index: %w", err)
	}
	if err := setEmbeddingDimensions(ctx, tx, emb.Dimensions); err != nil {
		return 0, fmt.Errorf("begin reindex: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO engine_state (key, embedder_id, dimensions, updated_at)
		VALUES ('embedder', $1, $2, now())
		ON CONFLICT (key) DO UPDATE SET
			embedder_id = EXCLUDED.embedder_id,
			dimensions  = EXCLUDED.dimensions,
			updated_at  = EXCLUDED.updated_at`,
		emb.ID, emb.Dimensions); err != nil {
		return 0, fmt.Errorf("begin reindex: record state: %w", err)
	}

	// A Reindex re-derives every Passage from every Turn (TRUNCATE above,
	// then the reindex job below re-embeds them all), which subsumes
	// whatever any previously discarded consolidate or reindex job failed
	// to produce. Their failure record is now obsolete: left in river_job
	// it would keep tripping Barrier's discarded-job check for the whole
	// rebuilt range even after this Reindex succeeds, so it is cleared
	// here rather than left for Barrier to reason its way around.
	if _, err := tx.Exec(ctx, `DELETE FROM river_job WHERE state = 'discarded' AND kind IN ('consolidate', 'reindex')`); err != nil {
		return 0, fmt.Errorf("begin reindex: clear discarded jobs: %w", err)
	}

	args := store.ReindexArgs{EmbedderID: emb.ID, Dimensions: emb.Dimensions}
	if _, err := s.insertClient.InsertTx(ctx, tx, args, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: false, ByState: reindexUniqueStates},
	}); err != nil {
		return 0, fmt.Errorf("begin reindex: enqueue reindex: %w", err)
	}

	var cursor int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM turns`).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("begin reindex: read cursor: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("begin reindex: commit: %w", err)
	}
	return domain.Cursor(cursor), nil
}

// SessionTurns implements store.Store.
func (s *Store) SessionTurns(ctx context.Context, ref store.SessionRef, fromSeq, toSeq int64) ([]store.StoredTurn, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT seq, id, speaker, content, event_time
		FROM turns
		WHERE namespace = $1 AND session_id = $2 AND seq BETWEEN $3 AND $4
		ORDER BY seq`,
		ref.Namespace, ref.SessionID, fromSeq, toSeq)
	if err != nil {
		return nil, fmt.Errorf("session turns: query: %w", err)
	}
	defer rows.Close()

	var turns []store.StoredTurn
	for rows.Next() {
		t := store.StoredTurn{Namespace: ref.Namespace, SessionID: ref.SessionID}
		if err := rows.Scan(&t.Seq, &t.Turn.ID, &t.Turn.Speaker, &t.Turn.Content, &t.Turn.EventTime); err != nil {
			return nil, fmt.Errorf("session turns: scan: %w", err)
		}
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session turns: %w", err)
	}
	return turns, nil
}

// ListSessions implements store.Store.
func (s *Store) ListSessions(ctx context.Context, after store.SessionRef, limit int) ([]store.SessionRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT namespace, session_id
		FROM turns
		WHERE (namespace, session_id) > ($1, $2)
		ORDER BY 1, 2
		LIMIT $3`,
		after.Namespace, after.SessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: query: %w", err)
	}
	defer rows.Close()

	var refs []store.SessionRef
	for rows.Next() {
		var ref store.SessionRef
		if err := rows.Scan(&ref.Namespace, &ref.SessionID); err != nil {
			return nil, fmt.Errorf("list sessions: scan: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return refs, nil
}

// WritePassages implements store.Store.
func (s *Store) WritePassages(ctx context.Context, ref store.SessionRef, records []store.PassageRecord, fromSeq, toSeq int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("write passages: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	// Union of every new record's provenance: any existing Passage whose
	// provenance overlaps it is superseded by this consolidation and must
	// go, per the documented Store.WritePassages contract.
	turnIDSet := make(map[string]struct{})
	for _, r := range records {
		for _, id := range r.TurnIDs {
			turnIDSet[id] = struct{}{}
		}
	}
	if len(turnIDSet) > 0 {
		turnIDs := make([]string, 0, len(turnIDSet))
		for id := range turnIDSet {
			turnIDs = append(turnIDs, id)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM passages WHERE namespace = $1 AND session_id = $2 AND turn_ids && $3`, ref.Namespace, ref.SessionID, turnIDs); err != nil {
			return fmt.Errorf("write passages: delete superseded: %w", err)
		}
	}

	if len(records) > 0 {
		batch := &pgx.Batch{}
		for _, r := range records {
			batch.Queue(`
				INSERT INTO passages (namespace, session_id, turn_ids, content, embedding)
				VALUES ($1, $2, $3, $4, $5)`,
				ref.Namespace, ref.SessionID, r.TurnIDs, r.Content, pgvector.NewVector(r.Embedding))
		}
		results := tx.SendBatch(ctx, batch)
		for range records {
			if _, err := results.Exec(); err != nil {
				results.Close() //nolint:errcheck // returning the query error below
				return fmt.Errorf("write passages: insert: %w", err)
			}
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("write passages: insert: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE turns SET indexed_at = now()
		WHERE namespace = $1 AND session_id = $2 AND seq BETWEEN $3 AND $4 AND indexed_at IS NULL`,
		ref.Namespace, ref.SessionID, fromSeq, toSeq); err != nil {
		return fmt.Errorf("write passages: mark indexed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("write passages: commit: %w", err)
	}
	return nil
}

// querier is the subset of *pgxpool.Pool and pgx.Tx that hydratePassages
// needs, so it can run either as part of a larger transaction (SearchVector)
// or directly against the pool (SearchLexical).
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// passageRow is one row of the shared shape SearchVector and SearchLexical
// both select before provenance is hydrated onto it.
type passageRow struct {
	id        string
	sessionID string
	content   string
	turnIDs   []string
	score     float64
}

// scanPassageRows drains rows shaped (id, session_id, content, turn_ids,
// score), the common projection of SearchVector and SearchLexical.
func scanPassageRows(rows pgx.Rows) ([]passageRow, error) {
	defer rows.Close()
	var results []passageRow
	for rows.Next() {
		var r passageRow
		if err := rows.Scan(&r.id, &r.sessionID, &r.content, &r.turnIDs, &r.score); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return results, nil
}

// hydratePassages turns passageRows into Candidates, populating each
// Passage's Turns (its provenance, per domain.Passage) with one extra query
// that joins every matched Passage against turns and groups the results back
// by Passage ID — never one query per Passage.
func hydratePassages(ctx context.Context, q querier, rows []passageRow) ([]store.Candidate, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}

	turnRows, err := q.Query(ctx, `
		SELECT p.id, t.id, t.speaker, t.content, t.event_time
		FROM passages p
		JOIN turns t ON t.namespace = p.namespace AND t.session_id = p.session_id AND t.id = ANY(p.turn_ids)
		WHERE p.id = ANY($1)
		ORDER BY p.id, t.seq`, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate turns: query: %w", err)
	}
	defer turnRows.Close()

	turnsByPassage := make(map[string][]domain.Turn, len(rows))
	for turnRows.Next() {
		var passageID string
		var turn domain.Turn
		if err := turnRows.Scan(&passageID, &turn.ID, &turn.Speaker, &turn.Content, &turn.EventTime); err != nil {
			return nil, fmt.Errorf("hydrate turns: scan: %w", err)
		}
		turnsByPassage[passageID] = append(turnsByPassage[passageID], turn)
	}
	if err := turnRows.Err(); err != nil {
		return nil, fmt.Errorf("hydrate turns: %w", err)
	}

	candidates := make([]store.Candidate, len(rows))
	for i, r := range rows {
		candidates[i] = store.Candidate{
			Passage: domain.Passage{
				ID:        r.id,
				SessionID: r.sessionID,
				Content:   r.content,
				Turns:     turnsByPassage[r.id],
			},
			Score: r.score,
		}
	}
	return candidates, nil
}

// SearchVector implements store.Store.
func (s *Store) SearchVector(ctx context.Context, namespace string, embedding []float32, k int) ([]store.Candidate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("search vector: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once Commit succeeds

	// Both are pgvector session-local HNSW query knobs (ADR-0007 requires
	// iterative scans enabled): relaxed_order lets the index keep scanning
	// past its list size to satisfy the WHERE namespace filter and LIMIT
	// instead of silently returning fewer than k rows.
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = 'relaxed_order'`); err != nil {
		return nil, fmt.Errorf("search vector: set iterative_scan: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.ef_search = 100`); err != nil {
		return nil, fmt.Errorf("search vector: set ef_search: %w", err)
	}

	vec := pgvector.NewVector(embedding)
	rows, err := tx.Query(ctx, `
		SELECT id, session_id, content, turn_ids, 1 - (embedding <=> $2) AS score
		FROM passages
		WHERE namespace = $1
		ORDER BY embedding <=> $2
		LIMIT $3`,
		namespace, vec, k)
	if err != nil {
		return nil, fmt.Errorf("search vector: query: %w", err)
	}
	results, err := scanPassageRows(rows)
	if err != nil {
		return nil, fmt.Errorf("search vector: %w", err)
	}

	candidates, err := hydratePassages(ctx, tx, results)
	if err != nil {
		return nil, fmt.Errorf("search vector: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("search vector: commit: %w", err)
	}
	return candidates, nil
}

// SearchLexical implements store.Store.
func (s *Store) SearchLexical(ctx context.Context, namespace, query string, k int) ([]store.Candidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, content, turn_ids, ts_rank_cd(tsv, websearch_to_tsquery('english', $2)) AS score
		FROM passages
		WHERE namespace = $1 AND tsv @@ websearch_to_tsquery('english', $2)
		ORDER BY score DESC
		LIMIT $3`,
		namespace, query, k)
	if err != nil {
		return nil, fmt.Errorf("search lexical: query: %w", err)
	}
	results, err := scanPassageRows(rows)
	if err != nil {
		return nil, fmt.Errorf("search lexical: %w", err)
	}

	candidates, err := hydratePassages(ctx, s.pool, results)
	if err != nil {
		return nil, fmt.Errorf("search lexical: %w", err)
	}
	return candidates, nil
}

// Barrier implements store.Store.
func (s *Store) Barrier(ctx context.Context, namespace string, cursor domain.Cursor) (store.BarrierState, error) {
	var pending bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM turns WHERE namespace = $1 AND seq <= $2 AND indexed_at IS NULL)`,
		namespace, int64(cursor),
	).Scan(&pending); err != nil {
		return store.BarrierPending, fmt.Errorf("barrier: check pending: %w", err)
	}
	if !pending {
		return store.BarrierSettled, nil
	}

	// Something at or below cursor isn't queryable yet. That's expected
	// while consolidation is still running; it's only BarrierFailed if a
	// discarded job still covers unindexed work at or below cursor.
	// River retains discarded jobs indefinitely, and this queries
	// river_job directly rather than through insertClient.JobList:
	// JobList(First(100)) only returns the 100 oldest discarded jobs
	// (River sorts by id ascending), so a job discarded after 100 have
	// accumulated would fall outside that window and Barrier would
	// report Pending forever. Scoping each discarded job to whether it
	// still covers unindexed work — rather than counting any discarded
	// job at all — is what keeps a job that failed once from poisoning
	// every later Settle after the work it was responsible for has
	// actually been redone:
	//   - a discarded consolidate only counts while its own
	//     [from_seq, to_seq] Session range still has an unindexed Turn
	//     at or below cursor. A retried attempt, a later redelivery of
	//     the same Turns, or a Reindex all clear indexed_at through
	//     WritePassages, which scopes the discarded row back out even
	//     though the river_job row itself is still there.
	//   - a discarded reindex only counts while it is the most recent
	//     reindex job. Once a later reindex job exists in any
	//     non-discarded state, that Reindex has already re-derived
	//     everything the discarded one failed to produce — BeginReindex
	//     also deletes discarded rows outright, but Barrier does not
	//     lean on that: this ordering check is what keeps a
	//     concurrently-committing BeginReindex from leaving a window
	//     where the stale discarded row still poisons every Namespace.
	var failed bool
	if err := s.pool.QueryRow(ctx,
		`SELECT
			EXISTS (
				SELECT 1 FROM river_job j
				WHERE j.state = 'discarded' AND j.kind = 'consolidate' AND j.args->>'namespace' = $1
				AND EXISTS (
					SELECT 1 FROM turns t
					WHERE t.namespace = $1 AND t.session_id = j.args->>'session_id'
					AND t.seq BETWEEN (j.args->>'from_seq')::bigint AND (j.args->>'to_seq')::bigint
					AND t.seq <= $2 AND t.indexed_at IS NULL
				)
			)
			OR EXISTS (
				SELECT 1 FROM river_job j
				WHERE j.state = 'discarded' AND j.kind = 'reindex'
				AND NOT EXISTS (
					SELECT 1 FROM river_job n
					WHERE n.kind = 'reindex' AND n.id > j.id AND n.state <> 'discarded'
				)
			)`,
		namespace, int64(cursor),
	).Scan(&failed); err != nil {
		return store.BarrierPending, fmt.Errorf("barrier: check discarded jobs: %w", err)
	}
	if failed {
		return store.BarrierFailed, nil
	}
	return store.BarrierPending, nil
}
