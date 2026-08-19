// Command kura-graphbench runs every graph engine over the same graph and
// writes the report.
//
// It works the way the other two orchestrators do and for the same reasons: two
// processes per engine so the cold start is a real one, one engine at a time so
// the disk is not shared, and every number taken by the operating system rather
// than by the engine's own stopwatch.
//
// What is different is the correctness. A vector index is scored on recall
// against published ground truth, and a graph store either gives the same
// answer as a separate implementation of the same walk or it does not. Every
// operation is checked against the answers kura-graphs worked out, and an
// engine that disagrees says so in the first table of the report.
//
//	kura-graphbench -dataset web-google -data graphdata -out results
//
// A subgraph prepared by kura-graphs -nodes is a graph in its own right, with
// its own answers, and it is run by pointing at the directory it was written
// to.
//
//	kura-graphbench -graph graphdata/soc-livejournal-n500000 -out results
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/kura-bench/bench"
	"github.com/tamnd/kura-bench/graphs"
)

func main() {
	var (
		name     = flag.String("dataset", "ca-grqc", "graph to run, one of "+strings.Join(graphs.Names(), " "))
		data     = flag.String("data", "graphdata", "directory the graphs live in, as written by kura-graphs")
		out      = flag.String("out", "results", "directory the results and the report are written to")
		work     = flag.String("work", "", "directory the stores are built in, defaults to a temporary one")
		binDir   = flag.String("bin", "bin", "directory holding the runner binaries")
		engines  = flag.String("engines", "", "comma separated engines to run, empty for every runner found")
		ops      = flag.String("ops", "", "comma separated operations to run, empty for all of "+strings.Join(graphs.Operations(), " "))
		graph    = flag.String("graph", "", "run the graph in this directory instead of the one -data and -dataset name")
		workers  = flag.Int("workers", 0, "operations in flight for the throughput run, zero for one per core")
		deadline = flag.Duration("deadline", 0, "give up on a phase that runs longer than this, zero for no limit")
		keep     = flag.Bool("keep", false, "leave the stores in place after the run")
	)
	flag.Parse()

	cfg := config{
		out:      *out,
		work:     *work,
		binDir:   *binDir,
		workers:  *workers,
		keep:     *keep,
		deadline: *deadline,
	}
	// A named dataset is the usual case and a directory is the case where
	// somebody prepared a subgraph. Either way what the rest of this reads is a
	// directory and a label, because a result taken on half of LiveJournal that
	// was filed under the name of the whole thing would be worse than no result.
	if *graph != "" {
		cfg.dir = filepath.Clean(*graph)
		cfg.label = filepath.Base(cfg.dir)
		cfg.about = "prepared by kura-graphs"
	} else {
		d, err := graphs.Lookup(*name)
		if err != nil {
			fail(err)
		}
		cfg.dir = d.Dir(*data)
		cfg.label = d.Name
		cfg.about = d.About
	}

	var err error
	if *engines != "" {
		cfg.engines = strings.Split(*engines, ",")
	}
	if cfg.ops, err = chooseOps(*ops); err != nil {
		fail(err)
	}
	if err := run(cfg); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kura-graphbench:", err)
	os.Exit(1)
}

// chooseOps keeps the operations in the report's order whatever order they were
// asked for in, and refuses a name that is not one of the five rather than
// running four of them and leaving somebody to notice.
func chooseOps(list string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		return graphs.Operations(), nil
	}
	want := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		want[strings.TrimSpace(name)] = true
	}
	var out []string
	for _, name := range graphs.Operations() {
		if want[name] {
			out = append(out, name)
			delete(want, name)
		}
	}
	for name := range want {
		return nil, fmt.Errorf("no operation called %q, there is %v", name, graphs.Operations())
	}
	return out, nil
}

type config struct {
	// dir holds edges.bin, seeds.bin and answers.json, and label is what the
	// run is filed under.
	dir      string
	label    string
	about    string
	out      string
	work     string
	binDir   string
	engines  []string
	ops      []string
	workers  int
	keep     bool
	deadline time.Duration
}

func run(cfg config) error {
	// The graph is checked once, here, rather than by every runner. A store
	// that was handed a truncated edge file answers everything slightly faster
	// and completely wrongly, and finding that out from the fifth engine's
	// numbers rather than before the first one started is hours wasted.
	header, err := graphs.ReadHeader(filepath.Join(cfg.dir, graphs.EdgeFile))
	if err != nil {
		return fmt.Errorf("%w, prepare it with kura-graphs", err)
	}
	answers, err := graphs.ReadAnswers(filepath.Join(cfg.dir, graphs.AnswerFile))
	if err != nil {
		return fmt.Errorf("%w, prepare it with kura-graphs", err)
	}
	if answers.Nodes != header.Nodes || answers.Edges != header.Edges {
		return fmt.Errorf("the answers describe a graph of %d nodes and %d edges and the edge file holds %d and %d, so rerun kura-graphs -force",
			answers.Nodes, answers.Edges, header.Nodes, header.Edges)
	}

	runners, err := discover(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.out, 0o755); err != nil {
		return err
	}

	root := cfg.work
	if root == "" {
		root, err = os.MkdirTemp("", "kura-graphbench-")
		if err != nil {
			return err
		}
		if !cfg.keep {
			defer func() { _ = os.RemoveAll(root) }()
		}
	}

	var results []bench.GraphResult
	for _, r := range runners {
		fmt.Fprintf(os.Stderr, "\n== %s\n", r.name)
		res, err := measure(cfg, r, filepath.Join(root, r.name))
		if err != nil {
			// One engine failing is a fact about that engine. Stopping here
			// would throw away the results of the ones that already ran.
			fmt.Fprintf(os.Stderr, "%s failed, leaving it out of the report: %v\n", r.name, err)
			continue
		}
		results = append(results, res)

		name := filepath.Join(cfg.out, "graph-"+r.name+"-"+cfg.slug(res.Machine.Host)+".json")
		if err := writeJSON(name, res); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", name)
	}
	if len(results) == 0 {
		return errors.New("every engine failed")
	}

	report := filepath.Join(cfg.out, "graph-report-"+cfg.slug(results[0].Machine.Host)+".md")
	body := reportHeader(cfg, results[0], answers) + bench.GraphReport(results, cfg.ops)
	if err := os.WriteFile(report, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", report)
	return nil
}

// measure runs one engine's two phases and merges them.
func measure(cfg config, r runnerBin, work string) (bench.GraphResult, error) {
	if err := os.RemoveAll(work); err != nil {
		return bench.GraphResult{}, err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return bench.GraphResult{}, err
	}
	if !cfg.keep {
		defer func() { _ = os.RemoveAll(work) }()
	}

	build, err := invoke(cfg, r, work, "build")
	if err != nil {
		return bench.GraphResult{}, fmt.Errorf("build phase: %w", err)
	}
	query, err := invoke(cfg, r, work, "query")
	if err != nil {
		return bench.GraphResult{}, fmt.Errorf("query phase: %w", err)
	}
	return bench.MergeGraph(build, query), nil
}

// invoke runs a runner once and parses the one JSON object it writes.
func invoke(cfg config, r runnerBin, work, phase string) (bench.GraphResult, error) {
	args := []string{
		"-edges", filepath.Join(cfg.dir, graphs.EdgeFile),
		"-seeds", filepath.Join(cfg.dir, graphs.SeedFile),
		"-answers", filepath.Join(cfg.dir, graphs.AnswerFile),
		"-dataset", cfg.label,
		"-work", work,
		"-phase", phase,
		"-ops", strings.Join(cfg.ops, ","),
	}
	if cfg.workers > 0 {
		args = append(args, "-workers", strconv.Itoa(cfg.workers))
	}

	var stdout bytes.Buffer
	// No deadline by default. A breadth first search over sixty nine million
	// edges takes as long as it takes, and a timeout would turn a slow engine
	// into a missing row instead of a slow number, which is the opposite of
	// what this is for.
	//
	// It is a flag because an engine that is merely slow and one that will
	// never finish look identical from outside. Setting -deadline says how
	// long the difference is worth waiting for, and the engine is then
	// reported as having failed to finish rather than quietly left out.
	ctx := context.Background()
	if cfg.deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.deadline)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, r.path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return bench.GraphResult{}, fmt.Errorf("did not finish within %s", cfg.deadline)
		}
		return bench.GraphResult{}, err
	}
	fmt.Fprintf(os.Stderr, "%s %s took %s\n", r.name, phase, time.Since(start).Round(time.Second))

	var res bench.GraphResult
	if err := json.Unmarshal(lastLine(stdout.Bytes()), &res); err != nil {
		return bench.GraphResult{}, fmt.Errorf("the runner did not write a result: %w", err)
	}
	return res, nil
}

// lastLine takes the result off the end of stdout, since a library that logs
// while it loads should not cost the engine its row.
func lastLine(b []byte) []byte {
	b = bytes.TrimRight(b, "\r\n \t")
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[i+1:]
	}
	return bytes.TrimSpace(b)
}

type runnerBin struct {
	name string
	path string
}

// discover finds the graph runner binaries.
//
// They are named <engine>-graphrunner, which keeps them out of the way of the
// text suite's <engine>-runner and the vector suite's <engine>-vecrunner in the
// same directory. All three suites are built into bin/ and none of them should
// ever try to run another's binaries.
func discover(cfg config) ([]runnerBin, error) {
	entries, err := os.ReadDir(cfg.binDir)
	if err != nil {
		return nil, fmt.Errorf("%w, run make build first", err)
	}

	want := map[string]bool{}
	for _, e := range cfg.engines {
		want[strings.TrimSpace(e)] = true
	}

	var out []runnerBin
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".exe")
		name, ok := strings.CutSuffix(base, "-graphrunner")
		if !ok || name == "" {
			continue
		}
		if len(want) > 0 && !want[name] {
			continue
		}
		out = append(out, runnerBin{name: name, path: filepath.Join(cfg.binDir, e.Name())})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no graph runners in %s, run make build first", cfg.binDir)
	}
	return out, nil
}

func writeJSON(name string, res bench.GraphResult) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(name, append(b, '\n'), 0o644)
}

// reportHeader records how the run was invoked, because a table without the
// command that produced it is not something anybody can repeat.
func reportHeader(cfg config, first bench.GraphResult, a graphs.Answers) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Graph results on %s\n\n", first.Machine.Host)
	fmt.Fprintf(&b, "Graph %s from %s, %s.\n\n", cfg.label, cfg.dir, cfg.about)

	p := a.Plan
	fmt.Fprintf(&b, "The plan is %d neighbour lookups, %d two hop lookups, %d shortest paths, %d full traversals, and pagerank over %d iterations at damping %v.\n",
		p.Neighbour, p.TwoHop, p.Path, p.BFS, p.Iterations, p.Damping)
	b.WriteString("The nodes are a fixed sample, so every engine is asked about the same ones in the same order, and a run with fewer of them is a subset of a run with more.\n\n")

	return b.String()
}

// slug is what tells one run's files from another's.
//
// The graph is in it as well as the host because several graphs get run on one
// machine, and a name that held only the host would have the LiveJournal run
// quietly overwrite the ca-GrQc one.
func (cfg config) slug(host string) string {
	return cfg.label + "-" + hostSlug(host)
}

// hostSlug makes a host name safe to put in a file name.
func hostSlug(host string) string {
	if host == "" {
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
	}, host)
}
