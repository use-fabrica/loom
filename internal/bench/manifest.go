// Package bench discovers benchmark manifests, runs their fetch/score
// scripts under uv, and manages the Reports and Baselines those Runs
// produce. See benchmark/CONTEXT.md for the domain vocabulary.
package bench

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Benchmark kinds, per benchmark/CONTEXT.md: an End-to-end Benchmark drives
// the Engine through its Ingest/Retrieve contract; a Component Benchmark
// evaluates one internal layer (currently only LMEB, the embedding layer)
// in isolation.
const (
	KindE2E       = "e2e"
	KindComponent = "component"
)

// DatasetSpec is a benchmark's [dataset] table: where to fetch it from and
// which pinned revision fetch.py must download.
type DatasetSpec struct {
	Source   string `toml:"source"`
	Revision string `toml:"revision"`
}

// Entrypoints is a benchmark's [entrypoints] table: the scripts the
// Harness invokes via uv. Score's underlying filename varies (LMEB uses
// run.py) but is always keyed "score".
type Entrypoints struct {
	Fetch string `toml:"fetch"`
	Score string `toml:"score"`
}

// Manifest is a parsed benchmark/<name>/benchmark.toml.
type Manifest struct {
	Name        string      `toml:"name"`
	Kind        string      `toml:"kind"`
	Dataset     DatasetSpec `toml:"dataset"`
	Judge       *Judge      `toml:"judge"`
	Answerer    *Answerer   `toml:"answerer"`
	Entrypoints Entrypoints `toml:"entrypoints"`

	// Dir is the benchmark's directory (e.g. "benchmark/locomo"). Set by
	// Load from the manifest's location on disk, never read from the file.
	Dir string `toml:"-"`
}

// AnswererPinned reports whether the manifest declares a non-empty
// Answerer model. An e2e benchmark with an unpinned Answerer cannot be run
// for real: the Harness has no Engine to call yet (blocked on issue #1).
func (m *Manifest) AnswererPinned() bool {
	return m.Answerer != nil && strings.TrimSpace(m.Answerer.Model) != ""
}

// Validate checks the structural rules of the frozen benchmark.toml shape:
// kind must be a known enum value, both entrypoints must be present, and
// e2e benchmarks must declare an [answerer] table (its model may still be
// the empty "not yet pinned" string).
func (m *Manifest) Validate() error {
	switch m.Kind {
	case KindE2E, KindComponent:
	default:
		return fmt.Errorf("kind %q must be %q or %q", m.Kind, KindE2E, KindComponent)
	}
	if strings.TrimSpace(m.Entrypoints.Fetch) == "" {
		return fmt.Errorf("entrypoints.fetch is required")
	}
	if strings.TrimSpace(m.Entrypoints.Score) == "" {
		return fmt.Errorf("entrypoints.score is required")
	}
	if m.Kind == KindE2E && m.Answerer == nil {
		return fmt.Errorf("e2e benchmarks require an [answerer] table (model may be empty until pinned)")
	}
	return nil
}

// Load parses a single benchmark.toml at path.
func Load(path string) (*Manifest, error) {
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.Dir = filepath.Dir(path)
	if m.Name == "" {
		m.Name = filepath.Base(m.Dir)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Discover finds and parses every benchmark/*/benchmark.toml manifest
// under root, sorted by name. No benchmark names are hardcoded: adding a
// benchmark means adding a directory with a manifest.
func Discover(root string) ([]*Manifest, error) {
	matches, err := filepath.Glob(filepath.Join(root, "*", "benchmark.toml"))
	if err != nil {
		return nil, fmt.Errorf("discover benchmarks under %s: %w", root, err)
	}
	manifests := make([]*Manifest, 0, len(matches))
	for _, path := range matches {
		m, err := Load(path)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, m)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Name < manifests[j].Name })
	return manifests, nil
}
