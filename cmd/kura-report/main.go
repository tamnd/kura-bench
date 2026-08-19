// Command kura-report rebuilds the reports from the results already on disk.
//
// Each orchestrator writes a report at the end of its run, from the engines it
// just ran. That is right while a run is happening and wrong afterwards: a
// rerun of one engine, which is a normal thing to do when a pin moves or a bug
// is fixed, overwrites the report for that machine with a table of one row and
// throws away the rivals it was supposed to be compared against. The result
// files themselves survive, so nothing is lost, but the document a person reads
// is the one that got clobbered.
//
// This reads every result file in a directory, groups them by suite and by
// machine, and writes each report from everything that machine has ever
// produced. It runs no engines and touches no network, so it is safe to run on
// a laptop against results collected on a server.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamnd/kura-bench/bench"
	"github.com/tamnd/kura-bench/graphs"
)

func main() {
	dir := flag.String("results", "results", "directory holding the result files")
	flag.Parse()

	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "kura-report:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no result files in %s", dir)
	}

	// One bucket per report. The key is the file name the report will get, so
	// two runs that would write the same file land in the same bucket and a
	// graph run on two different graphs does not.
	text := map[string][]bench.Result{}
	vector := map[string][]bench.VectorResult{}
	graph := map[string][]bench.GraphResult{}

	for _, path := range files {
		name := filepath.Base(path)
		switch {
		case strings.HasPrefix(name, "vector-"):
			var r bench.VectorResult
			if err := read(path, &r); err != nil {
				return err
			}
			key := "vector-report-" + slug(r.Dataset.Name) + "-" + slug(r.Machine.Host)
			vector[key] = append(vector[key], r)

		case strings.HasPrefix(name, "graph-"):
			var r bench.GraphResult
			if err := read(path, &r); err != nil {
				return err
			}
			key := "graph-report-" + slug(r.Dataset.Name) + "-" + slug(r.Machine.Host)
			graph[key] = append(graph[key], r)

		default:
			// A text result has no prefix, so anything left is one, and a file
			// that is not a result at all fails to decode into the shape below
			// and says so.
			var r bench.Result
			if err := read(path, &r); err != nil {
				return err
			}
			if r.Engine == "" {
				return fmt.Errorf("%s: no engine in it, is this a result file", path)
			}
			key := "report-" + slug(r.Machine.Host)
			text[key] = append(text[key], r)
		}
	}

	written := 0
	for key, rs := range text {
		sortBy(rs, func(r bench.Result) string { return r.Engine })
		body := textHeader(rs[0]) + bench.Report(rs)
		if err := write(dir, key, body, len(rs)); err != nil {
			return err
		}
		written++
	}
	for key, rs := range vector {
		sortBy(rs, func(r bench.VectorResult) string { return r.Engine })
		body := vectorHeader(rs[0]) + bench.VectorReport(rs)
		if err := write(dir, key, body, len(rs)); err != nil {
			return err
		}
		written++
	}
	for key, rs := range graph {
		sortBy(rs, func(r bench.GraphResult) string { return r.Engine })
		body := graphHeader(rs[0]) + bench.GraphReport(rs, graphs.Operations())
		if err := write(dir, key, body, len(rs)); err != nil {
			return err
		}
		written++
	}
	if written == 0 {
		return errors.New("nothing to report on")
	}
	return nil
}

// textHeader says what the run was over.
//
// It is rebuilt from the results rather than from a command line, because the
// command line that produced them is long gone and the corpus size, the machine
// and the run count are all in the files.
func textHeader(first bench.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Results on %s\n\n", first.Machine.Host)
	runs := 0
	if len(first.Search.Queries) > 0 {
		runs = first.Search.Queries[0].Runs
	}
	fmt.Fprintf(&b, "%d timed runs per query.\n\n", runs)
	return b.String()
}

func vectorHeader(first bench.VectorResult) string {
	return fmt.Sprintf("# Vector results on %s\n\n", first.Machine.Host)
}

// graphHeader says what graph the run was over.
//
// The sentence describing the graph and the counts the run worked to are not in
// a result file, so they are looked up by the graph's name. A run over a graph
// this build has never heard of, which is what the orchestrator's own -graph
// flag produces, gets the title and nothing it cannot stand behind.
func graphHeader(first bench.GraphResult) string {
	d, ok := graphs.Datasets[first.Dataset.Name]
	if !ok {
		return bench.GraphHeader(first.Machine.Host, "", bench.GraphPlan{})
	}
	return bench.GraphHeader(first.Machine.Host, d.About, graphs.DefaultPlan().Fit(d.Nodes).Report())
}

func read(path string, into any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func write(dir, key, body string, engines int) error {
	path := filepath.Join(dir, key+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s from %d engines\n", path, engines)
	return nil
}

// sortBy puts the engines in a stable order, so that rerunning this on the same
// files twice produces the same bytes and a report shows up in a diff only when
// a number changed.
func sortBy[T any](rs []T, key func(T) string) {
	sort.SliceStable(rs, func(i, j int) bool { return key(rs[i]) < key(rs[j]) })
}

// slug is the same mapping the orchestrators use for a file name, since this
// has to land on the file they would have written.
func slug(s string) string {
	if s == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return '-'
	}, s)
}
