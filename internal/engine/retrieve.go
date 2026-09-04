package engine

import (
	"context"
	"fmt"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/use-fabrica/loom/internal/store"
)

// rrfK is Reciprocal Rank Fusion's rank-damping constant. Fixed at the
// value from the original RRF paper: large enough that no single
// top-ranked candidate dominates the fused score, small enough that rank
// order still matters.
const rrfK = 60

// Retrieve embeds the query once, searches the vector and lexical channels
// concurrently, and fuses the results with Reciprocal Rank Fusion so
// neither channel's native score scale (cosine similarity vs. ts_rank_cd)
// dominates the other.
func (e *Engine) Retrieve(ctx context.Context, namespace, query string, limit int) ([]Hit, error) {
	if namespace == "" {
		return nil, fmt.Errorf("%w: namespace must not be empty", ErrInvalidArgument)
	}
	if query == "" {
		return nil, fmt.Errorf("%w: query must not be empty", ErrInvalidArgument)
	}
	if e.reindexRequired.Load() {
		return nil, ErrReindexRequired
	}

	limit = clampLimit(limit)
	k := e.opts.candidatesPerChannel(limit)

	vectors, err := e.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("engine: retrieve: embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("engine: retrieve: embedder returned %d vectors for %d inputs", len(vectors), 1)
	}
	if dims := e.emb.Dimensions(); len(vectors[0]) != dims {
		return nil, fmt.Errorf("engine: retrieve: embedder returned a %d-dimension vector, want %d", len(vectors[0]), dims)
	}

	var vectorHits, lexicalHits []store.Candidate
	group, gctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		hits, err := e.store.SearchVector(gctx, namespace, vectors[0], k)
		if err != nil {
			return err
		}
		vectorHits = hits
		return nil
	})
	group.Go(func() error {
		hits, err := e.store.SearchLexical(gctx, namespace, query, k)
		if err != nil {
			return err
		}
		lexicalHits = hits
		return nil
	})
	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("engine: retrieve: %w", err)
	}

	return fuseRRF(limit, vectorHits, lexicalHits), nil
}

// clampLimit applies the Retrieve limit bounds: <= 0 selects DefaultLimit,
// values above MaxLimit are clamped down to it.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

// fuseRRF combines ranked Candidates from any number of channels with
// Reciprocal Rank Fusion: score(p) = Σ_channels 1/(rrfK + rank), rank
// 1-based within each channel's own results. A Passage present in more
// than one channel accumulates a term from each and is never duplicated in
// the result. Ties break deterministically by Passage.ID so repeated
// queries against unchanged data return a stable order. The result is
// sorted by score descending and capped at limit.
func fuseRRF(limit int, channels ...[]store.Candidate) []Hit {
	scores := make(map[string]float64)
	passages := make(map[string]Passage)
	for _, channel := range channels {
		for i, cand := range channel {
			rank := i + 1
			scores[cand.Passage.ID] += 1 / float64(rrfK+rank)
			if _, seen := passages[cand.Passage.ID]; !seen {
				passages[cand.Passage.ID] = cand.Passage
			}
		}
	}

	hits := make([]Hit, 0, len(scores))
	for id, score := range scores {
		hits = append(hits, Hit{Passage: passages[id], Score: score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Passage.ID < hits[j].Passage.ID
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
