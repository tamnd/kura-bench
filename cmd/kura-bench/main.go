// Command kura-bench runs every engine over the same corpus and writes the
// report.
//
// It runs each engine twice, once to build the index and once to query it,
// because a cold start measured in the process that just wrote the index is not
// a cold start. It also runs the engines one after another rather than at the
// same time, since two of them competing for the same disk would produce a
// table about the disk.
//
//	kura-bench -corpus corpus.jsonl -queries queries.txt -out results
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
)

func main() {
	var (
		corpusPath = flag.String("corpus", "corpus.jsonl", "corpus file every engine is given")
		queries    = flag.String("queries", "queries.txt", "query file")
		out        = flag.String("out", "results", "directory the results and the report are written to")
		work       = flag.String("work", "", "directory the indexes are built in, defaults to a temporary one")
		binDir     = flag.String("bin", "bin", "directory holding the runner binaries")
		engines    = flag.String("engines", "", "comma separated engines to run, empty for every runner found")
		limit      = flag.Int("limit", 0, "stop after this many documents, zero for the whole corpus")
		repeat     = flag.Int("repeat", 20, "how many times each query is timed")
		workers    = flag.Int("workers", 0, "queries in flight, zero for one per core")
		depth      = flag.Int("depth", bench.DefaultDepth, "how many results each search asks for, ten for a page and a hundred for what a reranker needs behind it")
		keep       = flag.Bool("keep", false, "leave the indexes in place after the run")
		deadline   = flag.Duration("deadline", 0, "give up on a phase that runs longer than this, zero for no limit")
	)
	flag.Parse()

	cfg := config{
		corpus:   *corpusPath,
		queries:  *queries,
		out:      *out,
		work:     *work,
		binDir:   *binDir,
		limit:    *limit,
		repeat:   *repeat,
		workers:  *workers,
		depth:    *depth,
		keep:     *keep,
		deadline: *deadline,
	}
	if *engines != "" {
		cfg.engines = strings.Split(*engines, ",")
	}

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "kura-bench:", err)
		os.Exit(1)
	}
}

type config struct {
	corpus   string
	queries  string
	out      string
	work     string
	binDir   string
	engines  []string
	limit    int
	repeat   int
	workers  int
	depth    int
	keep     bool
	deadline time.Duration
}

func run(cfg config) error {
	if _, err := os.Stat(cfg.corpus); err != nil {
		return fmt.Errorf("%w, build one with kura-corpus", err)
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
		root, err = os.MkdirTemp("", "kura-bench-")
		if err != nil {
			return err
		}
		if !cfg.keep {
			defer func() { _ = os.RemoveAll(root) }()
		}
	}

	var results []bench.Result
	for _, r := range runners {
		fmt.Fprintf(os.Stderr, "\n== %s\n", r.name)
		res, err := measure(cfg, r, filepath.Join(root, r.name))
		if err != nil {
			// One engine failing is a fact about that engine. Stopping here
			// would throw away the results of the ones that already ran, and
			// those took hours.
			fmt.Fprintf(os.Stderr, "%s failed, leaving it out of the report: %v\n", r.name, err)
			continue
		}
		if res.Incomplete != "" {
			fmt.Fprintf(os.Stderr, "%s: %s, keeping what it did measure\n", r.name, res.Incomplete)
		}
		results = append(results, res)

		name := filepath.Join(cfg.out, r.name+"-"+hostSlug(res.Machine.Host)+".json")
		if err := writeJSON(name, res); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", name)
	}
	if len(results) == 0 {
		return errors.New("every engine failed")
	}

	report := filepath.Join(cfg.out, "report-"+hostSlug(results[0].Machine.Host)+".md")
	body := header(cfg, results[0]) + bench.Report(results)
	if err := os.WriteFile(report, []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", report)
	return nil
}

// measure runs one engine's two phases and merges them.
func measure(cfg config, r runnerBin, work string) (bench.Result, error) {
	// A leftover index from a previous run would be measured as an engine that
	// indexes instantly, so the directory is emptied rather than reused.
	if err := os.RemoveAll(work); err != nil {
		return bench.Result{}, err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return bench.Result{}, err
	}
	if !cfg.keep {
		defer func() { _ = os.RemoveAll(work) }()
	}

	index, err := invoke(cfg, r, work, "index")
	if err != nil {
		var late bench.TooSlow
		if errors.As(err, &late) {
			return incomplete(r, bench.Result{}, late), nil
		}
		return bench.Result{}, fmt.Errorf("index phase: %w", err)
	}
	query, err := invoke(cfg, r, work, "query")
	if err != nil {
		var late bench.TooSlow
		if errors.As(err, &late) {
			// The index finished, so its size and its throughput are
			// measurements and are kept. What is lost is the query timings, and
			// the report says which engine lost them.
			return incomplete(r, index, late), nil
		}
		return bench.Result{}, fmt.Errorf("query phase: %w", err)
	}
	return bench.Merge(index, query), nil
}

// incomplete is what an engine that ran out of time gets instead of being
// dropped from the run.
//
// The machine is filled in from what the orchestrator knows, because it is what
// a report is filed under and an engine that never wrote a result cannot say it
// itself.
func incomplete(r runnerBin, so bench.Result, late bench.TooSlow) bench.Result {
	so.Engine = r.name
	so.Incomplete = late.Error()
	if so.Machine.Host == "" {
		so.Machine = bench.Describe()
	}
	return so
}

// invoke runs a runner once and parses the one JSON object it writes.
func invoke(cfg config, r runnerBin, work, phase string) (bench.Result, error) {
	args := []string{
		"-corpus", cfg.corpus,
		"-queries", cfg.queries,
		"-work", work,
		"-phase", phase,
		"-repeat", strconv.Itoa(cfg.repeat),
	}
	if cfg.limit > 0 {
		args = append(args, "-limit", strconv.Itoa(cfg.limit))
	}
	if cfg.workers > 0 {
		args = append(args, "-workers", strconv.Itoa(cfg.workers))
	}
	// Passed only when it is not the default, so that a runner built before this
	// flag existed still runs rather than failing on an argument it has never
	// heard of.
	if cfg.depth > 0 && cfg.depth != bench.DefaultDepth {
		args = append(args, "-depth", strconv.Itoa(cfg.depth))
	}

	var stdout bytes.Buffer
	// No deadline by default. An index phase over a real corpus takes as long
	// as it takes, and a timeout would turn a slow engine into a missing row
	// instead of a slow number, which is the opposite of what this is for.
	//
	// It is a flag because the other failure looks identical from outside. An
	// engine whose query cost is proportional to the match set does not stop,
	// it just keeps going, and one of those will hold a whole run for as long
	// as you let it. Setting -deadline says how long that is worth waiting,
	// and the engine then keeps whatever the phases that did finish measured
	// rather than being quietly left out.
	ctx := context.Background()
	if cfg.deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.deadline)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, r.path, args...)
	cmd.Stdout = &stdout
	// The runner's progress goes straight through, because these phases take
	// minutes and a run that prints nothing for that long looks hung.
	cmd.Stderr = os.Stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return bench.Result{}, bench.TooSlow{Phase: phase, After: cfg.deadline}
		}
		return bench.Result{}, err
	}
	fmt.Fprintf(os.Stderr, "%s %s took %s\n", r.name, phase, time.Since(start).Round(time.Second))

	// The result is the last line of stdout, not all of it. Some engines log
	// while they index and there is no flag to stop them, and losing a whole
	// engine because a library printed a line would be a silly reason for a
	// blank row.
	var res bench.Result
	if err := json.Unmarshal(lastLine(stdout.Bytes()), &res); err != nil {
		return bench.Result{}, fmt.Errorf("the runner did not write a result: %w", err)
	}
	return res, nil
}

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

// discover finds the runner binaries.
//
// A runner is any file in the directory called <engine>-runner, which is how an
// engine written in another language joins the comparison without anything here
// knowing about it. The corpus builder and the orchestrator live in the same
// directory and are skipped by the same rule.
//
// When -engines names them, they run in the order it names them. On a real
// corpus one engine's index phase can take hours, and having that decided by
// where its name falls in the alphabet means a slow engine holds up every
// result behind it. Without the flag the order is the directory's, which is
// alphabetical and has to be something.
func discover(cfg config) ([]runnerBin, error) {
	entries, err := os.ReadDir(cfg.binDir)
	if err != nil {
		return nil, fmt.Errorf("%w, run make build first", err)
	}

	found := map[string]runnerBin{}
	var order []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".exe")
		name, ok := strings.CutSuffix(base, "-runner")
		if !ok || name == "" {
			continue
		}
		found[name] = runnerBin{name: name, path: filepath.Join(cfg.binDir, e.Name())}
		order = append(order, name)
	}

	if len(cfg.engines) > 0 {
		order = order[:0]
		for _, e := range cfg.engines {
			name := strings.TrimSpace(e)
			if name == "" {
				continue
			}
			if _, ok := found[name]; !ok {
				// Named and not there is a typo or a runner that was never
				// built, and either way a silent skip means waiting out a whole
				// run to find the engine missing from the table.
				return nil, fmt.Errorf("no %s-runner in %s", name, cfg.binDir)
			}
			order = append(order, name)
		}
	}

	out := make([]runnerBin, 0, len(order))
	for _, name := range order {
		out = append(out, found[name])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no runners in %s, run make build first", cfg.binDir)
	}
	return out, nil
}

func writeJSON(name string, res bench.Result) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(name, append(b, '\n'), 0o644)
}

// header records how the run was invoked, because a table without the command
// that produced it is not something anybody can repeat.
func header(cfg config, first bench.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Results on %s\n\n", first.Machine.Host)
	fmt.Fprintf(&b, "Corpus %s, queries %s, %d timed runs per query", cfg.corpus, cfg.queries, cfg.repeat)
	if cfg.limit > 0 {
		fmt.Fprintf(&b, ", limited to %d documents", cfg.limit)
	}
	b.WriteString(".\n\n")
	return b.String()
}

// hostSlug makes a host name safe to put in a file name, since these results
// are committed and a machine called something with a dot in it should not
// produce a file that looks like it has an extension.
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
