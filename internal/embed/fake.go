package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"unicode"
)

// Fake is a deterministic, in-process Embedder for tests and Runs without a
// model. Unset text hashes to a stable bag-of-words vector; Set installs an
// exact override. Safe for concurrent use.
type Fake struct {
	id   string
	dims int

	mu        sync.Mutex
	overrides map[string][]float32
	calls     int
	block     <-chan struct{}
	fail      error
}

// NewFake returns a Fake Embedder identified by id, producing dims-length
// vectors.
func NewFake(id string, dims int) *Fake {
	return &Fake{
		id:        id,
		dims:      dims,
		overrides: make(map[string][]float32),
	}
}

// ID implements embed.Embedder.
func (f *Fake) ID() string { return f.id }

// Dimensions implements embed.Embedder.
func (f *Fake) Dimensions() int { return f.dims }

// Set installs an exact override vector for text; vec must be len(dims).
// Later calls to Embed for text return this vector instead of the hashed
// default.
func (f *Fake) Set(text string, vec []float32) {
	if len(vec) != f.dims {
		panic("embed: Fake.Set vector length mismatch")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]float32, len(vec))
	copy(cp, vec)
	f.overrides[text] = cp
}

// Calls returns the number of Embed invocations (batches, not texts) — for
// assertions in restart/idempotency tests.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Block makes subsequent Embed calls wait for ch to close (or ctx.Done)
// before returning, simulating a stalled provider for restart tests. A nil
// channel disables blocking.
func (f *Fake) Block(ch <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = ch
}

// Fail makes subsequent Embed calls return err instead of a vector,
// simulating a permanently unhealthy provider for tests that need the
// settle barrier to observe a discarded job. A nil err clears it, letting
// Embed resume producing vectors.
func (f *Fake) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// Embed implements embed.Embedder.
func (f *Fake) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls++
	block := f.block
	fail := f.fail
	f.mu.Unlock()

	if fail != nil {
		return nil, fail
	}

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	out := make([][]float32, len(texts))
	for i, text := range texts {
		f.mu.Lock()
		override, ok := f.overrides[text]
		f.mu.Unlock()
		if ok {
			out[i] = override
			continue
		}
		out[i] = hashEmbed(text, f.dims)
	}
	return out, nil
}

// hashEmbed produces a deterministic bag-of-words vector: lowercase text,
// split on non-alphanumeric runs, bucket each word into fnv32(word)%dims and
// increment, then L2-normalize. Text with no words falls back to a vector
// with element 0 set to 1, so the zero vector never collides with "no
// signal" during cosine ranking.
func hashEmbed(text string, dims int) []float32 {
	vec := make([]float32, dims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) == 0 {
		vec[0] = 1
		return vec
	}
	for _, w := range words {
		h := fnv.New32()
		_, _ = h.Write([]byte(w))
		bucket := int(h.Sum32() % uint32(dims))
		vec[bucket]++
	}
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	norm := float32(math.Sqrt(sumSq))
	if norm == 0 {
		vec[0] = 1
		return vec
	}
	for i, v := range vec {
		vec[i] = v / norm
	}
	return vec
}
