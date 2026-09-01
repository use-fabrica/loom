package bench

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Subject kinds, per benchmark/CONTEXT.md: an Engine build for an
// End-to-end Benchmark, or a candidate component (currently only an
// embedding model, for LMEB) for a Component Benchmark.
const (
	SubjectKindEngine         = "engine"
	SubjectKindEmbeddingModel = "embedding_model"
)

// Subject identifies what a Run measures.
type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Dataset is fetched-artifact provenance: which dataset, which pinned
// revision, and its content hash. It is the shape fetch.py emits directly,
// and the shape every scorer echoes back in its "dataset" field.
type Dataset struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}

// Judge is a pinned LLM (model plus prompt version) used to score answers.
// Benchmarks whose metrics need no Judge omit the manifest's [judge]
// table entirely, hence the pointer.
type Judge struct {
	Model         string `toml:"model" json:"model"`
	PromptVersion string `toml:"prompt_version" json:"prompt_version"`
}

// Answerer is the pinned LLM the Harness uses to turn a Context Bundle
// into an answer for an End-to-end Benchmark. Component Benchmarks omit
// it, hence the pointer.
type Answerer struct {
	Model string `toml:"model" json:"model"`
}

// Report is the output of a Run: scores plus the provenance that
// reproduces them. Field shape matches benchmark/report.schema.json.
type Report struct {
	Benchmark  string             `json:"benchmark"`
	RunID      string             `json:"run_id"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at"`
	Subject    Subject            `json:"subject"`
	Dataset    Dataset            `json:"dataset"`
	Judge      *Judge             `json:"judge"`
	Answerer   *Answerer          `json:"answerer"`
	Metrics    map[string]float64 `json:"metrics"`
	Notes      string             `json:"notes,omitempty"`
}

// ScoreResult is the shape a scorer entrypoint's last stdout line parses
// into, per the script protocol in the benchmark Contract.
type ScoreResult struct {
	Metrics map[string]float64 `json:"metrics"`
	Dataset Dataset            `json:"dataset"`
	Judge   *Judge             `json:"judge"`
}

// NewRunID returns a short random identifier for a Run.
func NewRunID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is effectively unreachable on supported
		// platforms; fall back to a timestamp so a Run can still proceed.
		return strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "")
	}
	return hex.EncodeToString(buf)
}

var slugPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// slugify makes s safe for use as a filename component.
func slugify(s string) string {
	s = slugPattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "unknown"
	}
	return s
}

// Path returns this Report's destination path:
// <reportsRoot>/<benchmark>/<RFC3339 finished_at>-<subject-slug>.json
func (r *Report) Path(reportsRoot string) string {
	filename := fmt.Sprintf("%s-%s.json", r.FinishedAt.UTC().Format(time.RFC3339), slugify(r.Subject.ID))
	return filepath.Join(reportsRoot, r.Benchmark, filename)
}

// Write marshals the Report as indented JSON and writes it to its Path
// under reportsRoot, creating directories as needed. It returns the path
// written.
func (r *Report) Write(reportsRoot string) (string, error) {
	path := r.Path(reportsRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	return path, nil
}

// LoadReport reads and parses a Report from path.
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load report %s: %w", path, err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("load report %s: %w", path, err)
	}
	return &r, nil
}
