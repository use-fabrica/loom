package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
)

// BaselinePath returns the path of the Baseline for a benchmark:
// <baselinesRoot>/<name>.json
func BaselinePath(baselinesRoot, name string) string {
	return filepath.Join(baselinesRoot, name+".json")
}

// SetBaseline records report as the Baseline for its benchmark, overwriting
// any existing Baseline. It returns the path written.
func SetBaseline(baselinesRoot string, report *Report) (string, error) {
	if err := os.MkdirAll(baselinesRoot, 0o755); err != nil {
		return "", fmt.Errorf("set baseline: %w", err)
	}
	path := BaselinePath(baselinesRoot, report.Benchmark)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("set baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("set baseline: %w", err)
	}
	return path, nil
}

// LoadBaseline reads the current Baseline Report for name.
func LoadBaseline(baselinesRoot, name string) (*Report, error) {
	report, err := LoadReport(BaselinePath(baselinesRoot, name))
	if err != nil {
		return nil, fmt.Errorf("load baseline for %s: %w", name, err)
	}
	return report, nil
}

// MetricDelta is one metric's Baseline-vs-current comparison.
type MetricDelta struct {
	Metric      string
	Baseline    float64
	Current     float64
	Delta       float64
	HasBaseline bool
	HasCurrent  bool
}

// Compare returns the per-metric delta between a Baseline and a current
// Report, over the union of metric names either Report has.
func Compare(baseline, current *Report) []MetricDelta {
	names := make(map[string]struct{}, len(baseline.Metrics)+len(current.Metrics))
	for name := range baseline.Metrics {
		names[name] = struct{}{}
	}
	for name := range current.Metrics {
		names[name] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	deltas := make([]MetricDelta, 0, len(sorted))
	for _, name := range sorted {
		b, hasB := baseline.Metrics[name]
		c, hasC := current.Metrics[name]
		d := MetricDelta{Metric: name, Baseline: b, Current: c, HasBaseline: hasB, HasCurrent: hasC}
		if hasB && hasC {
			d.Delta = c - b
		}
		deltas = append(deltas, d)
	}
	return deltas
}

// WriteTable renders deltas as an aligned METRIC/BASELINE/CURRENT/DELTA
// table to w.
func WriteTable(w io.Writer, deltas []MetricDelta) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "METRIC\tBASELINE\tCURRENT\tDELTA"); err != nil {
		return err
	}
	for _, d := range deltas {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Metric, formatMetric(d.Baseline, d.HasBaseline), formatMetric(d.Current, d.HasCurrent), formatDelta(d)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func formatMetric(v float64, present bool) string {
	if !present {
		return "-"
	}
	return fmt.Sprintf("%.4f", v)
}

func formatDelta(d MetricDelta) string {
	if !d.HasBaseline || !d.HasCurrent {
		return "-"
	}
	sign := ""
	if d.Delta > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%.4f", sign, d.Delta)
}
