// Package runner is the harness every engine written in Go is measured by.
//
// The point of putting it here rather than in each runner is that the timing,
// the batching and the phase boundaries are then provably the same for every
// engine. A benchmark where each subject brings its own stopwatch measures the
// stopwatches.
//
// An engine written in another language reimplements this, and the contract it
// has to match is written down in [github.com/tamnd/kura-bench/bench] rather
// than only expressed in this code.
package runner

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/kura-bench/bench"
	"github.com/tamnd/kura-bench/corpus"
)

// BatchSize is how many documents are handed to an engine at once.
//
// It is the same for every engine on purpose. Each of them has a batch size its
// own documentation recommends, and using each one's favourite would measure
// the recommendations rather than the engines. Five hundred is large enough
// that per call overhead has stopped mattering for all of them and small enough
// that none of them is holding the corpus in memory to satisfy it.
const BatchSize = 500

// Info is what an engine calls itself.
type Info struct {
	Name     string
	Version  string
	Language string
}

// Engine is one search engine under test.
//
// The lifecycle is Create, then AddBatch until the corpus is exhausted, then
// Flush, then Close. A second process calls Open and then Search.
//
// Search must be safe to call from several goroutines at once, because the
// throughput phase does. An engine that cannot do that should say so in its
// notes and the harness will report the serial numbers only.
type Engine interface {
	Describe() Info

	// Create makes an empty index in dir. The directory exists and is empty.
	Create(dir string) error

	// AddBatch indexes documents. It may return before they are queryable.
	AddBatch(docs []corpus.Document) error

	// Flush makes everything added queryable and durable. The indexing phase is
	// timed to the end of this call, not to the end of the last AddBatch,
	// because an engine that returns early from writes and does the work in the
	// background has not done less work.
	Flush() error

	// Open attaches to an index already on disk.
	Open(dir string) error

	// Search runs one query and returns how many documents matched in total,
	// which is not the same as how many were returned.
	Search(ctx context.Context, query string, limit int) (int, error)

	// Close releases everything.
	Close() error
}

// ModuleVersion returns the version of a dependency as it was actually built,
// which is the only version worth printing in a result. A constant in the
// source says what somebody meant to use.
func ModuleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, d := range info.Deps {
		if d.Path == path {
			if d.Replace != nil {
				return d.Replace.Version + " (replaced)"
			}
			return d.Version
		}
	}
	return "unknown"
}

// Config is the parsed command line.
type Config struct {
	Corpus  string
	Queries string
	Work    string
	Phase   string
	Repeat  int
	Limit   int
	Workers int
}

// Main is the whole of a runner's main function.
//
// It parses the standard flags, runs the phase it was asked for, and writes one
// JSON object to standard output. Anything it wants to say to a person goes to
// standard error, because standard output is the result and nothing else.
func Main(newEngine func(Config) (Engine, error)) {
	cfg := Config{}
	flag.StringVar(&cfg.Corpus, "corpus", "", "path to the corpus jsonl file")
	flag.StringVar(&cfg.Queries, "queries", "", "path to the query file, one per line")
	flag.StringVar(&cfg.Work, "work", "", "directory the index is built in")
	flag.StringVar(&cfg.Phase, "phase", "all", "index, query or all")
	flag.IntVar(&cfg.Repeat, "repeat", 20, "how many times each query is timed")
	flag.IntVar(&cfg.Limit, "limit", 0, "stop after this many documents, zero for all")
	flag.IntVar(&cfg.Workers, "workers", 0, "queries in flight for the throughput phase, zero for one per core")
	flag.Parse()

	if err := run(cfg, newEngine); err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(1)
	}
}

func run(cfg Config, newEngine func(Config) (Engine, error)) error {
	if cfg.Corpus == "" || cfg.Work == "" {
		return errors.New("both -corpus and -work are required")
	}
	eng, err := newEngine(cfg)
	if err != nil {
		return err
	}
	info := eng.Describe()

	res := bench.Result{
		Engine:   info.Name,
		Version:  info.Version,
		Language: info.Language,
		Machine:  bench.Describe(),
	}

	switch cfg.Phase {
	case "index":
		if err := indexPhase(cfg, eng, &res); err != nil {
			return err
		}
	case "query":
		if err := queryPhase(cfg, eng, &res); err != nil {
			return err
		}
	case "all":
		if err := indexPhase(cfg, eng, &res); err != nil {
			return err
		}
		if err := eng.Close(); err != nil {
			return err
		}
		// Reopening in the same process is not a cold start, and the result
		// says so. A real one needs a second process, which is what the
		// orchestrator does when it runs the two phases separately.
		res.Notes = strings.TrimSpace(res.Notes +
			" the open phase ran in the same process as the build, so it is warmer than a real cold start")
		fresh, err := newEngine(cfg)
		if err != nil {
			return err
		}
		eng = fresh
		if err := queryPhase(cfg, eng, &res); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown phase %q", cfg.Phase)
	}

	if err := eng.Close(); err != nil {
		return err
	}
	return res.Write(os.Stdout)
}

// indexPhase builds the index from empty and times it to the end of the flush.
func indexPhase(cfg Config, eng Engine, res *bench.Result) error {
	if err := os.MkdirAll(cfg.Work, 0o755); err != nil {
		return err
	}
	if err := eng.Create(cfg.Work); err != nil {
		return fmt.Errorf("create: %w", err)
	}

	batch := make([]corpus.Document, 0, BatchSize)
	var stats bench.CorpusStats

	start := bench.Take()
	read, err := corpus.ReadFile(cfg.Corpus, func(d corpus.Document) error {
		if cfg.Limit > 0 && stats.Documents >= cfg.Limit {
			return corpus.ErrStop
		}
		stats.Documents++
		stats.Bytes += int64(len(d.Body))
		batch = append(batch, d)
		if len(batch) < BatchSize {
			return nil
		}
		if err := eng.AddBatch(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	})
	if err != nil && !errors.Is(err, corpus.ErrStop) {
		return fmt.Errorf("indexing: %w", err)
	}
	if len(batch) > 0 {
		if err := eng.AddBatch(batch); err != nil {
			return fmt.Errorf("indexing the last batch: %w", err)
		}
	}
	if err := eng.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	usage := bench.Measure(start)

	if cfg.Limit == 0 {
		stats = bench.CorpusStats{Documents: read.Documents, Bytes: read.Bytes}
	}
	size, files, err := bench.DirSize(cfg.Work)
	if err != nil {
		return err
	}

	res.Corpus = stats
	res.Index = bench.IndexPhase{Usage: usage, Bytes: size, Files: files}
	fmt.Fprintf(os.Stderr, "indexed %d documents in %.1fs, %.1f MB/s, index %.1f MB\n",
		stats.Documents, usage.WallSeconds, res.MBPerSecond(), float64(size)/(1<<20))
	return nil
}

// queryPhase opens an index already on disk and runs the query set.
func queryPhase(cfg Config, eng Engine, res *bench.Result) error {
	queries, err := readQueries(cfg.Queries)
	if err != nil {
		return err
	}
	ctx := context.Background()

	// Opening and answering one query is the cold start, and the one query is
	// part of it on purpose. Several of these engines do almost nothing in
	// their open call and pay for it on the first search instead, and an open
	// timed without a query would report that as free.
	openStart := bench.Take()
	if err := eng.Open(cfg.Work); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if _, err := eng.Search(ctx, queries[0], 10); err != nil {
		return fmt.Errorf("the first query after open: %w", err)
	}
	openUsage := bench.Measure(openStart)
	res.Open = bench.OpenPhase{Usage: openUsage, ResidentBytes: openUsage.RSSBytes}

	searchStart := bench.Take()
	stats := make([]bench.QueryStat, 0, len(queries))
	for _, q := range queries {
		// One warm up that is not counted, because the first run of a query
		// pays for whatever the engine caches per term and no deployment sees
		// that cost on every request.
		hits, err := eng.Search(ctx, q, 10)
		if err != nil {
			return fmt.Errorf("query %q: %w", q, err)
		}
		runs := make([]time.Duration, 0, cfg.Repeat)
		for range cfg.Repeat {
			t := time.Now()
			hits, err = eng.Search(ctx, q, 10)
			if err != nil {
				return fmt.Errorf("query %q: %w", q, err)
			}
			runs = append(runs, time.Since(t))
		}
		stat := bench.Summarise(q, hits, runs)
		stats = append(stats, stat)
		// One line per query rather than per run, so a slow engine says where
		// it has got to instead of looking hung for an hour. It is printed
		// after the timed runs and never between them.
		fmt.Fprintf(os.Stderr, "  %-40s %8d hits  %s median\n",
			q, stat.Hits, time.Duration(stat.MedianMS*float64(time.Millisecond)).Round(time.Microsecond))
	}
	searchUsage := bench.Measure(searchStart)

	res.Search = bench.SearchPhase{Usage: searchUsage, Queries: stats}
	res.Search.Concurrent = concurrent(ctx, eng, queries, cfg)
	res.Update = updatePhase(cfg, eng, res)
	return nil
}

// UpdateDocuments is how many documents the update phase rewrites.
//
// It is a fixed count rather than a fraction of the corpus so that the figure
// means the same thing on a machine that ran the whole corpus and on one that
// was given a tenth of it. Five thousand is roughly a busy day of edits on a
// repository this size, and it is enough work that an engine which handles
// updates by rebuilding is obvious rather than merely slow.
const UpdateDocuments = 5000

// updatePhase reindexes a slice of the corpus into the index that is already
// open, which is what an incremental sync does and is not the same operation as
// building from empty.
//
// An engine that cannot write to an index it opened for reading returns an
// error here, and the phase is left out with the reason in the notes instead of
// being reported as a zero.
func updatePhase(cfg Config, eng Engine, res *bench.Result) *bench.UpdatePhase {
	want := UpdateDocuments
	if cfg.Limit > 0 && cfg.Limit < want {
		// Rewriting documents the index does not have would be an insert
		// measured as an update, which is a different and easier operation.
		want = cfg.Limit
	}

	batch := make([]corpus.Document, 0, BatchSize)
	var (
		done  int
		bytes int64
		bad   error
	)

	start := bench.Take()
	_, err := corpus.ReadFile(cfg.Corpus, func(d corpus.Document) error {
		if done >= want {
			return corpus.ErrStop
		}
		done++
		bytes += int64(len(d.Body))
		batch = append(batch, d)
		if len(batch) < BatchSize {
			return nil
		}
		if err := eng.AddBatch(batch); err != nil {
			bad = err
			return corpus.ErrStop
		}
		batch = batch[:0]
		return nil
	})
	if err != nil && !errors.Is(err, corpus.ErrStop) {
		bad = err
	}
	if bad == nil && len(batch) > 0 {
		bad = eng.AddBatch(batch)
	}
	if bad == nil {
		bad = eng.Flush()
	}
	usage := bench.Measure(start)

	if bad != nil {
		res.Notes = strings.TrimSpace(res.Notes +
			" the update phase was left out because this engine cannot reindex into an index it opened: " + bad.Error())
		return nil
	}

	size, _, err := bench.DirSize(cfg.Work)
	if err != nil {
		size = 0
	}
	return &bench.UpdatePhase{
		Usage:           usage,
		Documents:       done,
		Bytes:           bytes,
		IndexBytesAfter: size,
	}
}

// concurrent runs the query set with several in flight, which is the only way
// to get a throughput number that means anything. Dividing one second by the
// serial latency gives a figure no deployment has ever reached.
func concurrent(ctx context.Context, eng Engine, queries []string, cfg Config) *bench.ConcurrentStat {
	workers := cfg.Workers
	if workers <= 0 {
		workers = len(queries)
	}
	if workers > 64 {
		workers = 64
	}
	total := len(queries) * cfg.Repeat

	jobs := make(chan string, workers)
	times := make([]time.Duration, total)
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		bad error
		i   int
	)

	start := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range jobs {
				t := time.Now()
				_, err := eng.Search(ctx, q, 10)
				d := time.Since(t)
				mu.Lock()
				if err != nil && bad == nil {
					bad = err
				}
				if i < len(times) {
					times[i] = d
					i++
				}
				mu.Unlock()
			}
		}()
	}
	for range cfg.Repeat {
		for _, q := range queries {
			jobs <- q
		}
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)

	if bad != nil {
		// An engine whose Search is not safe for concurrent use fails here, and
		// the throughput figure is left out rather than reported as whatever
		// the race happened to produce.
		fmt.Fprintf(os.Stderr, "concurrent phase failed, leaving it out: %v\n", bad)
		return nil
	}

	summary := bench.Summarise("", 0, times[:i])
	return &bench.ConcurrentStat{
		Workers:  workers,
		Queries:  i,
		Seconds:  elapsed.Seconds(),
		MedianMS: summary.MedianMS,
		P99MS:    summary.P99MS,
	}
}

// readQueries reads one query per line, ignoring blanks and comments.
func readQueries(name string) ([]string, error) {
	if name == "" {
		return nil, errors.New("-queries is required for the query phase")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s has no queries in it", name)
	}
	return out, nil
}
