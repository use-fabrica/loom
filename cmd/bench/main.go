// Command bench is Loom's benchmark Harness: it discovers benchmark
// manifests under benchmark/ and runs their fetch/score scripts via uv.
// See benchmark/CONTEXT.md for the domain vocabulary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/use-fabrica/loom/internal/bench"
)

const (
	benchmarkRoot = "benchmark"
	reportsRoot   = "benchmark/reports"
	baselinesRoot = "benchmark/baselines"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		printUsage()
		return errors.New("missing command")
	}
	switch args[0] {
	case "list":
		return cmdList(args[1:])
	case "fetch":
		return cmdFetch(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "baseline":
		return cmdBaseline(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `usage: bench <command> [arguments]

commands:
  list                                            list discovered benchmarks
  fetch <name>                                    download a benchmark's dataset
  run <name> --subject-kind K --subject-id ID [--dry-run] [-- extra args]
                                                   run a benchmark and write a Report
  baseline set <name> <report>                    record <report> as the Baseline for <name>
  baseline compare <name> <report>                compare <report> against the Baseline for <name>
`)
}

// cmdList prints a table of every discovered manifest: name, kind, judge,
// and answerer pin state.
func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	manifests, err := bench.Discover(benchmarkRoot)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tJUDGE\tANSWERER")
	for _, m := range manifests {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Name, m.Kind, judgeColumn(m), answererColumn(m))
	}
	return tw.Flush()
}

func judgeColumn(m *bench.Manifest) string {
	if m.Judge == nil {
		return "none"
	}
	if strings.TrimSpace(m.Judge.Model) == "" {
		return "unpinned"
	}
	return m.Judge.Model
}

func answererColumn(m *bench.Manifest) string {
	if m.Kind != bench.KindE2E {
		return "n/a"
	}
	if m.AnswererPinned() {
		return "pinned"
	}
	return "unpinned"
}

func findManifest(name string) (*bench.Manifest, error) {
	manifests, err := bench.Discover(benchmarkRoot)
	if err != nil {
		return nil, err
	}
	for _, m := range manifests {
		if m.Name == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("no benchmark named %q (see 'bench list')", name)
}

// fetchDataset runs a manifest's fetch entrypoint. fetch.py is documented
// idempotent, so this doubles as the "fetch-if-needed" step before run.
func fetchDataset(ctx context.Context, m *bench.Manifest) (bench.Dataset, error) {
	fmt.Fprintf(os.Stderr, "bench: fetching %s...\n", m.Name)
	raw, err := bench.RunScript(ctx, m.Dir, m.Entrypoints.Fetch, nil, os.Stderr)
	if err != nil {
		return bench.Dataset{}, fmt.Errorf("fetch %s: %w", m.Name, err)
	}
	var result bench.Dataset
	if err := json.Unmarshal(raw, &result); err != nil {
		return bench.Dataset{}, fmt.Errorf("fetch %s: parse output: %w", m.Name, err)
	}
	return result, nil
}

func cmdFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: bench fetch <name>")
	}
	m, err := findManifest(fs.Arg(0))
	if err != nil {
		return err
	}
	result, err := fetchDataset(context.Background(), m)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "bench: fetched %s dataset %q @ %s (sha256 %s)\n", m.Name, result.Name, result.Revision, result.SHA256)
	return nil
}

// cmdRun implements: run <name> --subject-kind K --subject-id ID
// [--dry-run] [-- extra args passed to scorer]. <name> is positional and
// must come first so the flag package's own "--" terminator is free to
// separate extra scorer args.
func cmdRun(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: bench run <name> --subject-kind K --subject-id ID [--dry-run] [-- extra args]")
	}
	name := args[0]

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	subjectKind := fs.String("subject-kind", "", fmt.Sprintf("subject kind: %q or %q", bench.SubjectKindEngine, bench.SubjectKindEmbeddingModel))
	subjectID := fs.String("subject-id", "", "subject identifier")
	dryRun := fs.Bool("dry-run", false, "print the resolved uv command without executing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	extra := fs.Args()

	m, err := findManifest(name)
	if err != nil {
		return err
	}

	// e2e benchmarks with no Answerer pinned have nothing to run for real:
	// the Harness has no Engine to call yet (blocked on issue #1). --dry-run
	// still previews the resolved scorer command either way.
	if m.Kind == bench.KindE2E && !m.AnswererPinned() {
		if *dryRun {
			fmt.Println(bench.CommandString(m.Dir, m.Entrypoints.Score, extra))
			return nil
		}
		return fmt.Errorf("%s is an end-to-end benchmark with no answerer pinned yet: the Harness has no Engine to call for answers (blocked on issue #1, the Ingest/Retrieve contract). Pass --dry-run to see the resolved command", m.Name)
	}

	if !*dryRun {
		if *subjectKind != bench.SubjectKindEngine && *subjectKind != bench.SubjectKindEmbeddingModel {
			return fmt.Errorf("--subject-kind must be %q or %q, got %q", bench.SubjectKindEngine, bench.SubjectKindEmbeddingModel, *subjectKind)
		}
		if strings.TrimSpace(*subjectID) == "" {
			return errors.New("--subject-id is required")
		}
	}

	if *dryRun {
		fmt.Println(bench.CommandString(m.Dir, m.Entrypoints.Score, extra))
		return nil
	}

	ctx := context.Background()
	startedAt := time.Now().UTC()
	if _, err := fetchDataset(ctx, m); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "bench: scoring %s...\n", m.Name)
	raw, err := bench.RunScript(ctx, m.Dir, m.Entrypoints.Score, extra, os.Stderr)
	if err != nil {
		return fmt.Errorf("run %s: %w", m.Name, err)
	}
	var result bench.ScoreResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("run %s: parse scorer output: %w", m.Name, err)
	}

	report := &bench.Report{
		Benchmark:  m.Name,
		RunID:      bench.NewRunID(),
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		Subject:    bench.Subject{Kind: *subjectKind, ID: *subjectID},
		Dataset:    result.Dataset,
		Judge:      m.Judge,
		Answerer:   m.Answerer,
		Metrics:    result.Metrics,
	}
	path, err := report.Write(reportsRoot)
	if err != nil {
		return fmt.Errorf("run %s: %w", m.Name, err)
	}
	fmt.Fprintf(os.Stderr, "bench: wrote %s\n", path)
	fmt.Println(path)
	return nil
}

func cmdBaseline(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bench baseline <set|compare> <name> <report>")
	}
	switch args[0] {
	case "set":
		return cmdBaselineSet(args[1:])
	case "compare":
		return cmdBaselineCompare(args[1:])
	default:
		return fmt.Errorf("unknown baseline command %q", args[0])
	}
}

func cmdBaselineSet(args []string) error {
	fs := flag.NewFlagSet("baseline set", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: bench baseline set <name> <report>")
	}
	name, reportPath := fs.Arg(0), fs.Arg(1)
	report, err := bench.LoadReport(reportPath)
	if err != nil {
		return err
	}
	if report.Benchmark != name {
		return fmt.Errorf("report %s is for benchmark %q, not %q", reportPath, report.Benchmark, name)
	}
	path, err := bench.SetBaseline(baselinesRoot, report)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "bench: baseline for %s set from %s\n", name, reportPath)
	fmt.Println(path)
	return nil
}

func cmdBaselineCompare(args []string) error {
	fs := flag.NewFlagSet("baseline compare", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: bench baseline compare <name> <report>")
	}
	name, reportPath := fs.Arg(0), fs.Arg(1)
	baseline, err := bench.LoadBaseline(baselinesRoot, name)
	if err != nil {
		return err
	}
	current, err := bench.LoadReport(reportPath)
	if err != nil {
		return err
	}
	bench.WriteTable(os.Stdout, bench.Compare(baseline, current))
	return nil
}
