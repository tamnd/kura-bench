// Command kura-vecbench runs every vector engine over the same dataset and
// writes the report.
//
// It works the way kura-bench does and for the same reasons: two processes per
// engine so the cold start is a real one, one engine at a time so the disk is
// not shared, and every number taken by the operating system rather than by the
// engine's own stopwatch.
//
// What is different is the accuracy. A text engine either found the document or
// it did not, and an approximate vector index is only as fast as it is
// inaccurate. So each runner is asked to search at several settings and report
// the recall it achieved at each, and the report compares the engines at equal
// recall rather than at equal effort.
//
//	kura-vecbench -dataset sift -data ~/vectors -out results
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
	"github.com/tamnd/kura-bench/vectors"
)

func main() {
	var (
		name     = flag.String("dataset", "sift", "dataset to run, one of "+strings.Join(vectors.Names(), " "))
		metric   = flag.String("metric", "euclidean", "what nearest means, one of "+metricNames())
		data     = flag.String("data", "vecdata", "directory the datasets live in, as written by kura-vectors")
		out      = flag.String("out", "results", "directory the results and the report are written to")
		work     = flag.String("work", "", "directory the indexes are built in, defaults to a temporary one")
		binDir   = flag.String("bin", "bin", "directory holding the runner binaries")
		engines  = flag.String("engines", "", "comma separated engines to run, empty for every runner found")
		k        = flag.Int("k", 10, "neighbours per query, and the depth recall is scored at")
		limit    = flag.Int("limit", 0, "index only this many base vectors, zero for all of them")
		queries  = flag.Int("queries", 1000, "how many query vectors to use, zero for all of them")
		workers  = flag.Int("workers", 0, "queries in flight for the throughput run, zero for one per core")
		deadline = flag.Duration("deadline", 0, "give up on a phase that runs longer than this, zero for no limit")
		keep     = flag.Bool("keep", false, "leave the indexes in place after the run")
	)
	flag.Parse()

	d, err := vectors.Lookup(*name)
	if err != nil {
		fail(err)
	}
	m, err := vectors.ParseMetric(*metric)
	if err != nil {
		fail(err)
	}
	cfg := config{
		dataset:  d,
		metric:   m,
		data:     *data,
		out:      *out,
		work:     *work,
		binDir:   *binDir,
		k:        *k,
		limit:    *limit,
		queries:  *queries,
		workers:  *workers,
		keep:     *keep,
		deadline: *deadline,
	}
	if *engines != "" {
		cfg.engines = strings.Split(*engines, ",")
	}
	if err := run(cfg); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kura-vecbench:", err)
	os.Exit(1)
}

func metricNames() string {
	out := make([]string, 0, len(vectors.Metrics()))
	for _, m := range vectors.Metrics() {
		out = append(out, string(m))
	}
	return strings.Join(out, " ")
}

type config struct {
	dataset  vectors.Dataset
	metric   vectors.Metric
	data     string
	out      string
	work     string
	binDir   string
	engines  []string
	k        int
	limit    int
	queries  int
	workers  int
	keep     bool
	deadline time.Duration
}

func run(cfg config) error {
	// The dataset is checked once, here, rather than by every runner. A base
	// file that is short by a few thousand vectors produces a recall figure
	// that looks plausible, and finding that out from the fifth engine's
	// numbers rather than before the first one started is hours wasted.
	if err := cfg.dataset.Verify(cfg.data); err != nil {
		return fmt.Errorf("%w, fetch it with kura-vectors", err)
	}
	if err := cfg.dataset.VerifyGroundTruth(cfg.data, cfg.metric); err != nil {
		return fmt.Errorf("%w, build it with kura-vectors -metric %s", err, cfg.metric)
	}
	if cfg.k > cfg.dataset.Depth {
		return fmt.Errorf("k of %d cannot be scored against ground truth that is only %d deep", cfg.k, cfg.dataset.Depth)
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
		root, err = os.MkdirTemp("", "kura-vecbench-")
		if err != nil {
			return err
		}
		if !cfg.keep {
			defer func() { _ = os.RemoveAll(root) }()
		}
	}

	var results []bench.VectorResult
	for _, r := range runners {
		fmt.Fprintf(os.Stderr, "\n== %s\n", r.name)
		res, err := measure(cfg, r, filepath.Join(root, r.name))
		if err != nil {
			// One engine failing is a fact about that engine. Stopping here
			// would throw away the results of the ones that already ran.
			fmt.Fprintf(os.Stderr, "%s failed, leaving it out of the report: %v\n", r.name, err)
			continue
		}
		if res.Incomplete != "" {
			fmt.Fprintf(os.Stderr, "%s: %s, keeping what it did measure\n", r.name, res.Incomplete)
		}
		if res.Declined != "" {
			fmt.Fprintf(os.Stderr, "%s: %s, so it is in the report without numbers\n", r.name, res.Declined)
		}
		results = append(results, res)

		name := filepath.Join(cfg.out, "vec-"+r.name+"-"+cfg.slug(res.Machine.Host)+".json")
		if err := writeJSON(name, res); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", name)
	}
	if len(results) == 0 {
		return errors.New("every engine failed")
	}

	report := filepath.Join(cfg.out, "vector-report-"+cfg.slug(results[0].Machine.Host)+".md")
	body := header(cfg, results[0]) + bench.VectorReport(results)
	if err := os.WriteFile(report, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", report)
	return nil
}

// measure runs one engine's two phases and merges them.
func measure(cfg config, r runnerBin, work string) (bench.VectorResult, error) {
	if err := os.RemoveAll(work); err != nil {
		return bench.VectorResult{}, err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return bench.VectorResult{}, err
	}
	if !cfg.keep {
		defer func() { _ = os.RemoveAll(work) }()
	}

	build, err := invoke(cfg, r, work, "build")
	if err != nil {
		var late bench.TooSlow
		if errors.As(err, &late) {
			return incomplete(cfg, r, bench.VectorResult{}, late), nil
		}
		return bench.VectorResult{}, fmt.Errorf("build phase: %w", err)
	}
	// An engine that will not answer this metric said so instead of building,
	// and there is nothing for the query phase to open. It keeps its row.
	if build.Declined != "" {
		return build, nil
	}
	query, err := invoke(cfg, r, work, "query")
	if err != nil {
		var late bench.TooSlow
		if errors.As(err, &late) {
			// The build finished, so the index size and the build rate are
			// measurements and are kept. What is lost is the curve, and the
			// report says which engine lost it.
			return incomplete(cfg, r, build, late), nil
		}
		return bench.VectorResult{}, fmt.Errorf("query phase: %w", err)
	}
	return bench.MergeVector(build, query), nil
}

// incomplete is what an engine that ran out of time gets instead of being
// dropped from the run.
//
// The dataset's name, the metric and the machine are filled in from what the
// orchestrator knows, because they are what a report is filed under and an
// engine that never wrote a result cannot say any of them itself.
func incomplete(cfg config, r runnerBin, so bench.VectorResult, late bench.TooSlow) bench.VectorResult {
	so.Engine = r.name
	so.Incomplete = late.Error()
	if so.Dataset.Name == "" {
		so.Dataset.Name = cfg.dataset.Name
	}
	if so.Dataset.Metric == "" {
		so.Dataset.Metric = string(cfg.metric)
	}
	if so.Machine.Host == "" {
		so.Machine = bench.Describe()
	}
	return so
}

// invoke runs a runner once and parses the one JSON object it writes.
func invoke(cfg config, r runnerBin, work, phase string) (bench.VectorResult, error) {
	d := cfg.dataset
	args := []string{
		"-base", d.Path(cfg.data, vectors.Base),
		"-query", d.Path(cfg.data, vectors.Query),
		"-groundtruth", d.GroundTruthPath(cfg.data, cfg.metric),
		"-dataset", d.Name,
		"-metric", string(cfg.metric),
		"-work", work,
		"-phase", phase,
		"-k", strconv.Itoa(cfg.k),
	}
	if cfg.limit > 0 {
		args = append(args, "-limit", strconv.Itoa(cfg.limit))
	}
	if cfg.queries > 0 {
		args = append(args, "-queries", strconv.Itoa(cfg.queries))
	}
	if cfg.workers > 0 {
		args = append(args, "-workers", strconv.Itoa(cfg.workers))
	}

	var stdout bytes.Buffer
	// No deadline by default. Building a graph index over a million vectors
	// takes as long as it takes, and a timeout would turn a slow engine into a
	// missing row instead of a slow number, which is the opposite of what this
	// is for.
	//
	// It is a flag because an engine that is merely slow and one that will
	// never finish look identical from outside. Setting -deadline says how
	// long the difference is worth waiting for, and the engine then keeps
	// whatever the phases that did finish measured rather than being quietly
	// left out.
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
			return bench.VectorResult{}, bench.TooSlow{Phase: phase, After: cfg.deadline}
		}
		return bench.VectorResult{}, err
	}
	elapsed := time.Since(start).Round(time.Second)

	var res bench.VectorResult
	if err := json.Unmarshal(lastLine(stdout.Bytes()), &res); err != nil {
		return bench.VectorResult{}, fmt.Errorf("the runner did not write a result: %w", err)
	}
	// An engine that refused the run did no work, and timing the refusal reads
	// as a build that finished in no time at all.
	if res.Declined == "" {
		fmt.Fprintf(os.Stderr, "%s %s took %s\n", r.name, phase, elapsed)
	}
	return res, nil
}

// lastLine takes the result off the end of stdout, since a library that logs
// while it builds should not cost the engine its row.
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

// discover finds the vector runner binaries.
//
// They are named <engine>-vecrunner, which keeps them out of the way of the
// text suite's <engine>-runner in the same directory. Both suites are built
// into bin/ and neither should ever try to run the other's binaries.
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
		name, ok := strings.CutSuffix(base, "-vecrunner")
		if !ok || name == "" {
			continue
		}
		if len(want) > 0 && !want[name] {
			continue
		}
		out = append(out, runnerBin{name: name, path: filepath.Join(cfg.binDir, e.Name())})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no vector runners in %s, run make build first", cfg.binDir)
	}
	return out, nil
}

func writeJSON(name string, res bench.VectorResult) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(name, append(b, '\n'), 0o644)
}

// header records how the run was invoked, because a table without the command
// that produced it is not something anybody can repeat.
// header is what the report says about the run above its tables. The sentences
// live in the bench package because the command that rebuilds reports from the
// files on disk writes the same paragraph and should not write it differently.
func header(cfg config, first bench.VectorResult) string {
	d := first.Dataset
	if d.Name == "" {
		d.Name = cfg.dataset.Name
	}
	if d.Metric == "" {
		d.Metric = string(cfg.metric)
	}
	if d.K == 0 {
		d.K = cfg.k
	}
	return bench.VectorHeader(bench.VectorRun{
		Host:        first.Machine.Host,
		Dataset:     d,
		From:        cfg.data,
		Published:   cfg.metric.Published(),
		BaseVectors: cfg.dataset.Count,
	})
}

// slug is what tells one run's files from another's.
//
// The dataset and the metric are in it as well as the host because they are
// three separate runs of the same engines on one machine, and a name that held
// only the host would have the inner product run quietly overwrite the
// Euclidean one.
func (cfg config) slug(host string) string {
	return cfg.dataset.Name + "-" + string(cfg.metric) + "-" + hostSlug(host)
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
