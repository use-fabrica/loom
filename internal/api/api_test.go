package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	loomv1 "github.com/use-fabrica/loom/gen/loom/v1"
	"github.com/use-fabrica/loom/gen/loom/v1/loomv1connect"
	"github.com/use-fabrica/loom/internal/embed"
	"github.com/use-fabrica/loom/internal/engine"
)

// baseTime anchors every Turn's event_time to a fixed instant instead of
// time.Now(): whole-second resolution round-trips through protobuf and
// Postgres' timestamptz without rounding, so provenance equality checks
// never have to account for sub-second truncation.
var baseTime = time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)

// ingest Ingests sessions into namespace over the Connect seam and returns
// the wire Cursor.
func ingest(ctx context.Context, t *testing.T, h *harness, namespace string, sessions ...*loomv1.Session) string {
	t.Helper()
	resp, err := h.client.Ingest(ctx, connect.NewRequest(&loomv1.IngestRequest{
		Namespace: namespace,
		Sessions:  sessions,
	}))
	if err != nil {
		t.Fatalf("Ingest(%s): %v", namespace, err)
	}
	return resp.Msg.GetCursor()
}

// settle calls Settle over the Connect seam and returns its error, letting
// callers assert on Connect error codes without settle itself failing the
// test.
func settle(ctx context.Context, t *testing.T, h *harness, namespace, cursor string) error {
	t.Helper()
	_, err := h.client.Settle(ctx, connect.NewRequest(&loomv1.SettleRequest{
		Namespace: namespace,
		Cursor:    cursor,
	}))
	return err
}

// mustSettle calls settle and fails the test on any error.
func mustSettle(ctx context.Context, t *testing.T, h *harness, namespace, cursor string) {
	t.Helper()
	if err := settle(ctx, t, h, namespace, cursor); err != nil {
		t.Fatalf("Settle(%s, %s): %v", namespace, cursor, err)
	}
}

// retrieve calls Retrieve over the Connect seam and returns the resulting
// Context Bundle.
func retrieve(ctx context.Context, t *testing.T, h *harness, namespace, query string, limit uint32) *loomv1.ContextBundle {
	t.Helper()
	resp, err := h.client.Retrieve(ctx, connect.NewRequest(&loomv1.RetrieveRequest{
		Namespace: namespace,
		Query:     query,
		Limit:     limit,
	}))
	if err != nil {
		t.Fatalf("Retrieve(%s, %q): %v", namespace, query, err)
	}
	return resp.Msg.GetBundle()
}

// segmentText mirrors TurnSegmenter's Content formula so tests can key
// fake.Set on exactly the text the Engine will ask the Embedder to embed.
func segmentText(speaker, content string) string {
	return speaker + ": " + content
}

// passageIDSet collects Passage ids, for identity comparisons that don't
// care about ranking order.
func passageIDSet(passages []*loomv1.Passage) map[string]bool {
	set := make(map[string]bool, len(passages))
	for _, p := range passages {
		set[p.GetId()] = true
	}
	return set
}

// turnIDSet collects the ids of every provenance Turn across passages.
func turnIDSet(passages []*loomv1.Passage) map[string]bool {
	set := make(map[string]bool)
	for _, p := range passages {
		for _, turn := range p.GetTurns() {
			set[turn.GetId()] = true
		}
	}
	return set
}

// waitForEmbedCall blocks until fake has recorded at least min Embed
// calls, bounded by ctx. Tests use it to know a background job has
// actually reached a (deliberately blocked) Embedder before acting on the
// runner around it, instead of sleeping for a guessed duration.
func waitForEmbedCall(ctx context.Context, t *testing.T, fake *embed.Fake, min int) {
	t.Helper()
	for fake.Calls() < min {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %d Embed call(s); got %d", min, fake.Calls())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestIngestSettleRetrieve is the baseline Ingest/Settle/Retrieve loop: once
// a Cursor Settles, Retrieve must return one Passage per Turn with full
// provenance (Session id and the originating Turn's speaker, content, and
// event_time) round-tripped through the store and back over the wire.
func TestIngestSettleRetrieve(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "round-trip"
	turns := []*loomv1.Turn{
		{Id: "turn-1", Speaker: "alice", Content: "the archived zebra crossed the frozen savanna", EventTime: timestamppb.New(baseTime)},
		{Id: "turn-2", Speaker: "bob", Content: "a lone cartographer charted the coral reef at dawn", EventTime: timestamppb.New(baseTime.Add(time.Minute))},
	}
	session := &loomv1.Session{Id: "session-1", Turns: turns}

	cursor := ingest(ctx, t, h, namespace, session)
	mustSettle(ctx, t, h, namespace, cursor)

	bundle := retrieve(ctx, t, h, namespace, "zebra savanna coral reef", 10)
	if len(bundle.GetPassages()) != len(turns) {
		t.Fatalf("got %d passages, want %d", len(bundle.GetPassages()), len(turns))
	}

	byTurnID := make(map[string]*loomv1.Turn, len(turns))
	for _, turn := range turns {
		byTurnID[turn.GetId()] = turn
	}

	for _, p := range bundle.GetPassages() {
		if p.GetSessionId() != session.GetId() {
			t.Errorf("passage %q session id = %q, want %q", p.GetId(), p.GetSessionId(), session.GetId())
		}
		if len(p.GetTurns()) != 1 {
			t.Fatalf("passage %q has %d turns, want 1", p.GetId(), len(p.GetTurns()))
		}
		got := p.GetTurns()[0]
		want, ok := byTurnID[got.GetId()]
		if !ok {
			t.Fatalf("passage provenance turn id %q not among ingested turns", got.GetId())
		}
		if got.GetSpeaker() != want.GetSpeaker() || got.GetContent() != want.GetContent() {
			t.Errorf("provenance turn %q = %+v, want speaker/content matching %+v", got.GetId(), got, want)
		}
		if !got.GetEventTime().AsTime().Equal(want.GetEventTime().AsTime()) {
			t.Errorf("provenance turn %q event_time = %v, want %v", got.GetId(), got.GetEventTime().AsTime(), want.GetEventTime().AsTime())
		}
	}
}

// TestNamespaceIsolation proves the Namespace is a real isolation boundary:
// identical content Ingested into two Namespaces never crosses over at
// Retrieve, and a Cursor is only ever meaningful within the Namespace it
// was returned for.
func TestNamespaceIsolation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const shared = "identical content shared across namespaces"
	turnA := &loomv1.Turn{Id: "t-a", Speaker: "user", Content: shared, EventTime: timestamppb.New(baseTime)}
	turnB := &loomv1.Turn{Id: "t-b", Speaker: "user", Content: shared, EventTime: timestamppb.New(baseTime)}

	cursorA := ingest(ctx, t, h, "ns-a", &loomv1.Session{Id: "sess-a", Turns: []*loomv1.Turn{turnA}})
	cursorB := ingest(ctx, t, h, "ns-b", &loomv1.Session{Id: "sess-b", Turns: []*loomv1.Turn{turnB}})

	mustSettle(ctx, t, h, "ns-a", cursorA)
	mustSettle(ctx, t, h, "ns-b", cursorB)

	bundleA := retrieve(ctx, t, h, "ns-a", shared, 10)
	if len(bundleA.GetPassages()) != 1 {
		t.Fatalf("ns-a: got %d passages, want 1", len(bundleA.GetPassages()))
	}
	for _, p := range bundleA.GetPassages() {
		if p.GetSessionId() != "sess-a" {
			t.Errorf("ns-a Retrieve returned a passage from session %q", p.GetSessionId())
		}
	}

	bundleB := retrieve(ctx, t, h, "ns-b", shared, 10)
	if len(bundleB.GetPassages()) != 1 {
		t.Fatalf("ns-b: got %d passages, want 1", len(bundleB.GetPassages()))
	}
	for _, p := range bundleB.GetPassages() {
		if p.GetSessionId() != "sess-b" {
			t.Errorf("ns-b Retrieve returned a passage from session %q", p.GetSessionId())
		}
	}

	// cursorA is scoped to ns-a (the max ingest sequence number among ns-a's
	// own Turns): re-Settling ns-a with it is a no-op success, and Settling
	// ns-b with it also succeeds immediately, because ns-b's Turn was
	// ingested after cursorA's position and the barrier for ns-b only ever
	// waits on ns-b's own Turns at or below the given cursor.
	if err := settle(ctx, t, h, "ns-a", cursorA); err != nil {
		t.Errorf("re-Settle(ns-a, cursorA): %v", err)
	}
	shortCtx, shortCancel := context.WithTimeout(ctx, 2*time.Second)
	defer shortCancel()
	if err := settle(shortCtx, t, h, "ns-b", cursorA); err != nil {
		t.Errorf("Settle(ns-b, cursorA): %v", err)
	}
}

// TestRetrieveLimitBounds checks the limit clamp described on
// engine.Service.Retrieve: zero/negative selects DefaultLimit, an explicit
// limit under MaxLimit is honored exactly, and anything above MaxLimit is
// clamped rather than returning unbounded results.
func TestRetrieveLimitBounds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "limit-bounds"
	const total = engine.MaxLimit + 10 // more Passages than MaxLimit, so only the clamp explains a 100-Passage response

	turns := make([]*loomv1.Turn, total)
	for i := range total {
		turns[i] = &loomv1.Turn{
			Id:        fmt.Sprintf("turn-%03d", i),
			Speaker:   "archivist",
			Content:   fmt.Sprintf("expedition log entry number %d about the frostbound archive", i),
			EventTime: timestamppb.New(baseTime.Add(time.Duration(i) * time.Second)),
		}
	}
	cursor := ingest(ctx, t, h, namespace, &loomv1.Session{Id: "log", Turns: turns})
	mustSettle(ctx, t, h, namespace, cursor)

	assertDistinct := func(t *testing.T, passages []*loomv1.Passage) {
		t.Helper()
		seen := make(map[string]bool, len(passages))
		for _, p := range passages {
			if seen[p.GetId()] {
				t.Errorf("duplicate passage id %q in response", p.GetId())
			}
			seen[p.GetId()] = true
		}
	}

	def := retrieve(ctx, t, h, namespace, "expedition archive", 0)
	assertDistinct(t, def.GetPassages())
	if len(def.GetPassages()) != engine.DefaultLimit {
		t.Errorf("limit=0: got %d passages, want default limit %d", len(def.GetPassages()), engine.DefaultLimit)
	}

	small := retrieve(ctx, t, h, namespace, "expedition archive", 3)
	assertDistinct(t, small.GetPassages())
	if len(small.GetPassages()) != 3 {
		t.Errorf("limit=3: got %d passages, want 3", len(small.GetPassages()))
	}

	big := retrieve(ctx, t, h, namespace, "expedition archive", 500)
	assertDistinct(t, big.GetPassages())
	if len(big.GetPassages()) != engine.MaxLimit {
		t.Errorf("limit=500: got %d passages, want clamped max %d", len(big.GetPassages()), engine.MaxLimit)
	}
}

// TestIngestIdempotent redelivers the same Sessions and checks that Ingest
// is idempotent on (namespace, session id, turn id): the second Cursor must
// still Settle, and Retrieve must keep returning exactly the same Passages,
// never doubled.
func TestIngestIdempotent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "idempotent"
	session := &loomv1.Session{
		Id: "sess-repeat",
		Turns: []*loomv1.Turn{
			{Id: "t-1", Speaker: "user", Content: "the harbor lights flickered in the fog", EventTime: timestamppb.New(baseTime)},
			{Id: "t-2", Speaker: "guide", Content: "watch for the lighthouse beam every ten seconds", EventTime: timestamppb.New(baseTime.Add(time.Second))},
		},
	}

	cursor1 := ingest(ctx, t, h, namespace, session)
	mustSettle(ctx, t, h, namespace, cursor1)
	first := retrieve(ctx, t, h, namespace, "harbor lighthouse fog", 10)
	if len(first.GetPassages()) != 2 {
		t.Fatalf("after first ingest: got %d passages, want 2", len(first.GetPassages()))
	}

	cursor2 := ingest(ctx, t, h, namespace, session) // redelivery of the identical Session
	mustSettle(ctx, t, h, namespace, cursor2)

	second := retrieve(ctx, t, h, namespace, "harbor lighthouse fog", 10)
	if len(second.GetPassages()) != 2 {
		t.Fatalf("after redelivery: got %d passages, want 2 (no duplicates)", len(second.GetPassages()))
	}

	firstIDs, secondIDs := passageIDSet(first.GetPassages()), passageIDSet(second.GetPassages())
	if len(firstIDs) != len(secondIDs) {
		t.Fatalf("passage id set size changed after redelivery: %d vs %d", len(firstIDs), len(secondIDs))
	}
	for id := range firstIDs {
		if !secondIDs[id] {
			t.Errorf("passage %q present before redelivery is missing after redelivery", id)
		}
	}
}

// TestHybridRanking proves Retrieve fuses the vector and lexical channels
// rather than picking one: a Passage with an embedding identical to the
// query's but sharing no words with it, and a Passage that shares one rare
// token with the query but has an orthogonal embedding, must both surface
// ahead of unrelated distractors that score well on neither channel.
func TestHybridRanking(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "hybrid"
	const query = "zxqvtoken"
	queryVec := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	lexicalVec := []float32{0, 1, 0, 0, 0, 0, 0, 0} // orthogonal to queryVec: cosine similarity 0
	h.fake.Set(query, queryVec)

	semanticTurn := &loomv1.Turn{
		Id:        "turn-semantic",
		Speaker:   "note",
		Content:   "violet green pink ribbons shimmer above distant glaciers tonight",
		EventTime: timestamppb.New(baseTime),
	}
	h.fake.Set(segmentText(semanticTurn.GetSpeaker(), semanticTurn.GetContent()), queryVec)

	lexicalTurn := &loomv1.Turn{
		Id:        "turn-lexical",
		Speaker:   "sensor",
		Content:   "array zxqvtoken reading nominal",
		EventTime: timestamppb.New(baseTime.Add(time.Second)),
	}
	h.fake.Set(segmentText(lexicalTurn.GetSpeaker(), lexicalTurn.GetContent()), lexicalVec)

	// Distractors keep their default hash-based embedding: bag-of-words
	// hashing only ever adds non-negative weight, so nothing can beat the
	// semantic passage's perfect (1.0) similarity to the query, and none of
	// these mention the rare token, so none compete on the lexical channel
	// either.
	distractors := []*loomv1.Turn{
		{Id: "turn-d1", Speaker: "weather", Content: "skies were clear over the valley this morning", EventTime: timestamppb.New(baseTime.Add(2 * time.Second))},
		{Id: "turn-d2", Speaker: "chef", Content: "simmer the tomatoes with basil and garlic", EventTime: timestamppb.New(baseTime.Add(3 * time.Second))},
		{Id: "turn-d3", Speaker: "coach", Content: "the team ran drills before the scrimmage", EventTime: timestamppb.New(baseTime.Add(4 * time.Second))},
		{Id: "turn-d4", Speaker: "clerk", Content: "inventory counts matched the shipment manifest", EventTime: timestamppb.New(baseTime.Add(5 * time.Second))},
	}

	turns := append([]*loomv1.Turn{semanticTurn, lexicalTurn}, distractors...)
	cursor := ingest(ctx, t, h, namespace, &loomv1.Session{Id: "mix", Turns: turns})
	mustSettle(ctx, t, h, namespace, cursor)

	bundle := retrieve(ctx, t, h, namespace, query, 2)
	if len(bundle.GetPassages()) != 2 {
		t.Fatalf("got %d passages, want top 2", len(bundle.GetPassages()))
	}

	got := make(map[string]bool, 2)
	for _, p := range bundle.GetPassages() {
		if len(p.GetTurns()) != 1 {
			t.Fatalf("passage %q has %d turns, want 1", p.GetId(), len(p.GetTurns()))
		}
		got[p.GetTurns()[0].GetId()] = true
	}
	if !got["turn-semantic"] {
		t.Errorf("top 2 results %v missing the semantic match (identical embedding, no shared words)", got)
	}
	if !got["turn-lexical"] {
		t.Errorf("top 2 results %v missing the lexical match (shared rare token, orthogonal embedding)", got)
	}
}

// TestReindexDimensionChange swaps in an Embedder with a different vector
// width on the same store — the scenario ADR-0008 calls a Reindex — and
// checks that Retrieve refuses stale-dimension results until Reindex runs,
// after which every original Passage is derivable again.
func TestReindexDimensionChange(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "reindex-dims"
	turns := []*loomv1.Turn{
		{Id: "t-1", Speaker: "guide", Content: "the expedition reached base camp before the storm", EventTime: timestamppb.New(baseTime)},
		{Id: "t-2", Speaker: "guide", Content: "supplies were rationed for the final ascent", EventTime: timestamppb.New(baseTime.Add(time.Second))},
		{Id: "t-3", Speaker: "medic", Content: "altitude sickness delayed two climbers overnight", EventTime: timestamppb.New(baseTime.Add(2 * time.Second))},
	}
	cursor := ingest(ctx, t, h, namespace, &loomv1.Session{Id: "expedition", Turns: turns})
	mustSettle(ctx, t, h, namespace, cursor)

	before := retrieve(ctx, t, h, namespace, "expedition ascent altitude", 10)
	beforeIDs := turnIDSet(before.GetPassages())
	if len(beforeIDs) != len(turns) {
		t.Fatalf("before reindex: got %d distinct provenance turns, want %d", len(beforeIDs), len(turns))
	}

	// Swap to an Engine configured with a 16-dimension Embedder on the same
	// store, as a redeployed process choosing a new Embedder would. The old
	// Runner must stop first: left running, its ReindexWorker is still
	// configured for the 8-dimension Embedder and would see the new reindex
	// job as stale, canceling it before the new Runner claims it.
	h.engineServer.stop()
	fake16 := embed.NewFake("fake", 16)
	h.engineServer = newEngineServer(t, ctx, h.store, h.log, fake16)

	if _, err := h.client.Retrieve(ctx, connect.NewRequest(&loomv1.RetrieveRequest{Namespace: namespace, Query: "expedition", Limit: 10})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Retrieve before Reindex: code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
	}

	reindexResp, err := h.client.Reindex(ctx, connect.NewRequest(&loomv1.ReindexRequest{}))
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	mustSettle(ctx, t, h, namespace, reindexResp.Msg.GetCursor())

	after := retrieve(ctx, t, h, namespace, "expedition ascent altitude", 10)
	afterIDs := turnIDSet(after.GetPassages())
	if len(afterIDs) != len(turns) {
		t.Fatalf("after reindex: got %d distinct provenance turns, want %d", len(afterIDs), len(turns))
	}
	for id := range beforeIDs {
		if !afterIDs[id] {
			t.Errorf("turn %q present before Reindex is missing after Reindex", id)
		}
	}
}

// TestSettleSemantics covers Settle's edge behavior: a Cursor that fails to
// parse or is negative is rejected outright, Cursor 0 settles immediately
// because no Turn ever has a sequence number below it, and Settle honors
// the caller's deadline instead of blocking forever on stalled work.
func TestSettleSemantics(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	t.Run("garbage cursor", func(t *testing.T) {
		for _, cursor := range []string{"", "not-a-number", "-1"} {
			err := settle(ctx, t, h, "settle-garbage", cursor)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("Settle(cursor=%q): code = %v, want InvalidArgument (err=%v)", cursor, connect.CodeOf(err), err)
			}
		}
	})

	t.Run("cursor zero settles immediately", func(t *testing.T) {
		shortCtx, shortCancel := context.WithTimeout(ctx, 2*time.Second)
		defer shortCancel()
		if err := settle(shortCtx, t, h, "settle-zero", "0"); err != nil {
			t.Errorf("Settle(cursor=0): %v", err)
		}
	})

	t.Run("deadline exceeded while blocked, then succeeds", func(t *testing.T) {
		const namespace = "settle-timeout"
		block := make(chan struct{})
		h.fake.Block(block)

		cursor := ingest(ctx, t, h, namespace, &loomv1.Session{
			Id: "sess",
			Turns: []*loomv1.Turn{
				{Id: "t-1", Speaker: "user", Content: "waiting on a slow embedder", EventTime: timestamppb.New(baseTime)},
			},
		})

		tightCtx, tightCancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer tightCancel()
		if err := settle(tightCtx, t, h, namespace, cursor); connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			t.Fatalf("Settle while blocked: code = %v, want DeadlineExceeded (err=%v)", connect.CodeOf(err), err)
		}

		close(block)
		if err := settle(ctx, t, h, namespace, cursor); err != nil {
			t.Fatalf("Settle after unblocking: %v", err)
		}
	})
}

// TestRestartMidConsolidation interrupts a ConsolidateWorker job mid-flight
// (StopAndCancel) and proves the Turn survives the restart: a fresh
// Engine+Runner on the same store must still consolidate it, because Turns
// — not the Runner's in-memory job state — are the Engine's source of
// truth.
func TestRestartMidConsolidation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "restart"
	block := make(chan struct{})
	h.fake.Block(block)

	cursor := ingest(ctx, t, h, namespace, &loomv1.Session{
		Id: "sess",
		Turns: []*loomv1.Turn{
			{Id: "t-1", Speaker: "user", Content: "this consolidation will be interrupted", EventTime: timestamppb.New(baseTime)},
		},
	})
	waitForEmbedCall(ctx, t, h.fake, 1) // the ConsolidateWorker is now blocked inside Embed

	h.engineServer.stop() // StopAndCancel: the blocked job's context is canceled mid-attempt

	fresh := embed.NewFake("fake", fakeDimensions) // a restarted process starts with an unblocked Embedder
	h.engineServer = newEngineServer(t, ctx, h.store, h.log, fresh)

	mustSettle(ctx, t, h, namespace, cursor)

	bundle := retrieve(ctx, t, h, namespace, "consolidation interrupted", 10)
	if len(bundle.GetPassages()) != 1 {
		t.Fatalf("got %d passages after restart, want 1", len(bundle.GetPassages()))
	}
	if got := bundle.GetPassages()[0].GetTurns()[0].GetId(); got != "t-1" {
		t.Errorf("passage turn id = %q, want t-1", got)
	}
}

// TestValidation checks that malformed requests are rejected before they
// touch the store: an empty Namespace, a Turn missing event_time, and an
// empty Retrieve query must all fail with InvalidArgument.
func TestValidation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	t.Run("missing namespace", func(t *testing.T) {
		_, err := h.client.Ingest(ctx, connect.NewRequest(&loomv1.IngestRequest{
			Namespace: "",
			Sessions: []*loomv1.Session{{
				Id:    "sess",
				Turns: []*loomv1.Turn{{Id: "t-1", Speaker: "user", Content: "hello", EventTime: timestamppb.New(baseTime)}},
			}},
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("Ingest with empty namespace: code = %v, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
		}
	})

	t.Run("turn without event_time", func(t *testing.T) {
		_, err := h.client.Ingest(ctx, connect.NewRequest(&loomv1.IngestRequest{
			Namespace: "validation",
			Sessions: []*loomv1.Session{{
				Id:    "sess",
				Turns: []*loomv1.Turn{{Id: "t-1", Speaker: "user", Content: "hello"}}, // EventTime left unset
			}},
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("Ingest with no event_time: code = %v, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
		}
	})

	t.Run("empty query", func(t *testing.T) {
		_, err := h.client.Retrieve(ctx, connect.NewRequest(&loomv1.RetrieveRequest{
			Namespace: "validation",
			Query:     "",
			Limit:     10,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("Retrieve with empty query: code = %v, want InvalidArgument (err=%v)", connect.CodeOf(err), err)
		}
	})
}

// TestConnectJSON checks the wire contract ADR-0005 promises: a plain
// net/http client speaking Connect's unary JSON protocol, with no
// generated client, can call Retrieve and get back a 200 with a JSON body.
func TestConnectJSON(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := newHarness(t, ctx)

	const namespace = "connect-json"
	cursor := ingest(ctx, t, h, namespace, &loomv1.Session{
		Id: "sess",
		Turns: []*loomv1.Turn{
			{Id: "t-1", Speaker: "user", Content: "served over plain http json", EventTime: timestamppb.New(baseTime)},
		},
	})
	mustSettle(ctx, t, h, namespace, cursor)

	body, err := json.Marshal(map[string]any{
		"namespace": namespace,
		"query":     "plain http json",
		"limit":     5,
	})
	if err != nil {
		t.Fatalf("json.Marshal request: %v", err)
	}

	resp, err := http.Post(h.srv.URL+"/"+loomv1connect.LoomServiceName+"/Retrieve", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("raw Connect JSON POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var decoded struct {
		Bundle struct {
			Passages []struct {
				Id        string `json:"id"`
				SessionId string `json:"sessionId"`
				Content   string `json:"content"`
			} `json:"passages"`
		} `json:"bundle"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode JSON body: %v", err)
	}
	if len(decoded.Bundle.Passages) != 1 {
		t.Fatalf("got %d passages in raw JSON body, want 1", len(decoded.Bundle.Passages))
	}
	if decoded.Bundle.Passages[0].SessionId != "sess" {
		t.Errorf("passage sessionId = %q, want %q", decoded.Bundle.Passages[0].SessionId, "sess")
	}
}
