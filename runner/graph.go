package runner

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamnd/kura-bench/bench"
	"github.com/tamnd/kura-bench/graphs"
)

// ConcurrentMinimum is how many runs an operation needs before it is also
// measured with several in flight.
//
// Below this the concurrent figure is mostly the cost of starting the workers.
// It leaves the traversals and PageRank serial, which is right: nobody runs ten
// breadth first searches at once to find out how fast one is. The Rust harness
// uses the same number for the same reason.
const ConcurrentMinimum = 100

// GraphEngine is one graph store under test.
//
// The lifecycle is Create, then Load, then Flush, then Close. A second process
// calls Open and then the operations.
//
// Every method takes and returns the publisher's own node identifiers, not
// whatever internal numbering the store settled on. A store with its own
// mapping does the translation inside the call, which means it is timed, which
// is correct: an answer the caller cannot read has not been produced.
//
// The operations must be safe to call from several goroutines at once, because
// the throughput pass does. A store that cannot do that says so in its notes
// and gets the serial numbers only.
type GraphEngine interface {
	Describe() Info

	// Create makes an empty store in dir. The directory exists and is empty.
	Create(dir string) error

	// Load takes the whole edge list, from and to alternating. It is one slice
	// rather than a stream because that is what the Rust runners are handed,
	// and a store that wants batches makes its own.
	Load(edges []uint32) error

	// Flush makes everything loaded queryable and durable. The build phase is
	// timed to the end of this call, because a store that returns early from
	// writes and does the work afterwards has not done less work.
	Flush() error

	// Open attaches to a store already on disk.
	Open(dir string) error

	// Close releases everything.
	Close() error

	// Cannot says why this store cannot do an operation, or empty when it can.
	// It is a sentence for the report rather than a boolean, because a row that
	// says why it is empty is worth more than a row that is missing.
	Cannot(op string) string

	// Neighbours is the out degree of one node.
	Neighbours(node uint32) int64

	// TwoHop is the distinct nodes within two hops, not counting the node.
	TwoHop(node uint32) int64

	// ShortestPath is the hop count between two nodes, or -1.
	ShortestPath(from, to uint32) int64

	// BFS is the size of the reachable set from one node and how deep it goes.
	BFS(node uint32) (int64, int64)

	// PageRank is the highest ranked node identifiers, best first. The
	// iterations and the damping come from the plan rather than from the store,
	// because a PageRank figure without them is not a measurement of anything.
	PageRank(iterations int, damping float64, top int) []int64
}

// GraphNoter is a store with something to say about its own numbers.
//
// It is an optional interface rather than a field on Info because most stores
// have nothing to add, and the ones that do are usually explaining a figure
// that would otherwise look like a mistake.
type GraphNoter interface {
	Note() string
}

// GraphConfig is the parsed command line.
//
// The flag names are the ones kura-graphbench sends, spelled exactly the way
// the Rust harness spells them, because the orchestrator invokes every runner
// the same way and does not know which language any of them is written in.
type GraphConfig struct {
	Edges   string
	Seeds   string
	Answers string
	Dataset string
	Work    string
	Phase   string
	Ops     []string
	Workers int
}

// Wants says whether an operation was asked for, which it was if nothing was
// asked for.
func (c GraphConfig) Wants(op string) bool {
	if len(c.Ops) == 0 {
		return true
	}
	for _, o := range c.Ops {
		if o == op {
			return true
		}
	}
	return false
}

// WorkerCount is how many operations the throughput pass runs at once.
func (c GraphConfig) WorkerCount() int {
	if c.Workers > 0 {
		return c.Workers
	}
	// One per core, which is what the Rust harness defaults to. The two have to
	// agree, because a throughput figure is a figure per level of concurrency
	// and two engines measured at different levels are not comparable.
	return runtime.NumCPU()
}

// GraphMain is the whole of a graph runner's main function.
//
// It parses the standard flags, runs the phase it was asked for, and writes one
// JSON object to standard output. Anything it wants to say to a person goes to
// standard error, because standard output is the result and nothing else.
func GraphMain(newEngine func(GraphConfig) (GraphEngine, error)) {
	cfg := GraphConfig{}
	ops := ""
	flag.StringVar(&cfg.Edges, "edges", "", "path to the edge file")
	flag.StringVar(&cfg.Seeds, "seeds", "", "path to the seed file")
	flag.StringVar(&cfg.Answers, "answers", "", "path to the answers file")
	flag.StringVar(&cfg.Dataset, "dataset", "", "the graph's name, for the result")
	flag.StringVar(&cfg.Work, "work", "", "directory the store is built in")
	flag.StringVar(&cfg.Phase, "phase", "all", "build, query or all")
	flag.StringVar(&ops, "ops", "", "comma separated operations, empty for all of them")
	flag.IntVar(&cfg.Workers, "workers", 0, "operations in flight for the throughput pass, zero for one per core")
	flag.Parse()

	for _, name := range strings.Split(ops, ",") {
		if name = strings.TrimSpace(name); name != "" {
			cfg.Ops = append(cfg.Ops, name)
		}
	}
	if err := runGraph(cfg, newEngine); err != nil {
		fmt.Fprintln(os.Stderr, "graphrunner:", err)
		os.Exit(1)
	}
}

func runGraph(cfg GraphConfig, newEngine func(GraphConfig) (GraphEngine, error)) error {
	if cfg.Edges == "" || cfg.Work == "" {
		return errors.New("both -edges and -work are required")
	}
	query := cfg.Phase == "query" || cfg.Phase == "all"
	if query && (cfg.Seeds == "" || cfg.Answers == "") {
		// A query phase without the answers would report a latency and nothing
		// else, and a latency on its own is not a result: a store that forgot
		// half the edges answers every question faster than one that did not.
		return errors.New("-seeds and -answers are required for the query phase")
	}

	eng, err := newEngine(cfg)
	if err != nil {
		return err
	}
	info := eng.Describe()

	res := bench.GraphResult{
		Engine:   info.Name,
		Version:  info.Version,
		Language: info.Language,
		Machine:  bench.Describe(),
	}
	if n, ok := eng.(GraphNoter); ok {
		res.Notes = n.Note()
	}

	switch cfg.Phase {
	case "build":
		if err := graphBuildPhase(cfg, eng, &res); err != nil {
			return err
		}
	case "query":
		if err := graphQueryPhase(cfg, eng, &res); err != nil {
			return err
		}
	case "all":
		if err := graphBuildPhase(cfg, eng, &res); err != nil {
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
		if err := graphQueryPhase(cfg, eng, &res); err != nil {
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

// graphBuildPhase loads the edges and times it to the end of the flush.
func graphBuildPhase(cfg GraphConfig, eng GraphEngine, res *bench.GraphResult) error {
	if err := os.MkdirAll(cfg.Work, 0o755); err != nil {
		return err
	}

	header, edges, err := graphs.ReadEdges(cfg.Edges)
	if err != nil {
		return err
	}
	nodes, count := header.Nodes, header.Edges
	fmt.Fprintf(os.Stderr, "read %d edges over %d nodes\n", count, nodes)

	start := bench.Take()
	if err := eng.Create(cfg.Work); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := eng.Load(edges); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if err := eng.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	usage := bench.Measure(start)

	size, files, err := bench.DirSize(cfg.Work)
	if err != nil {
		return err
	}
	res.Build = bench.GraphBuildPhase{Usage: usage, Bytes: size, Files: files}
	res.Dataset = bench.GraphStats{
		Name:       cfg.Dataset,
		Nodes:      nodes,
		Edges:      count,
		Undirected: header.Flags&graphs.Undirected != 0,
	}
	fmt.Fprintf(os.Stderr, "loaded %d edges in %.1fs, store %.1f MB\n",
		count, usage.WallSeconds, float64(size)/(1<<20))
	return nil
}

// graphQueryPhase opens a store already on disk and runs the operations.
func graphQueryPhase(cfg GraphConfig, eng GraphEngine, res *bench.GraphResult) error {
	seeds, err := graphs.ReadIDs(cfg.Seeds)
	if err != nil {
		return err
	}
	if len(seeds) == 0 {
		return errors.New("the seed file is empty")
	}

	// Opening and answering one lookup is the cold start, and the one lookup is
	// part of it on purpose. Several stores do almost nothing in their open
	// call and pay for it on the first read instead, and an open timed without
	// one would report that as free.
	openStart := bench.Take()
	if err := eng.Open(cfg.Work); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	eng.Neighbours(seeds[0])
	openUsage := bench.Measure(openStart)
	res.Open = bench.OpenPhase{Usage: openUsage, ResidentBytes: openUsage.RSSBytes}

	answers, err := graphs.ReadAnswers(cfg.Answers)
	if err != nil {
		return err
	}

	start := bench.Take()
	res.Query.Ops = runOps(cfg, eng, seeds, answers)
	res.Query.Usage = bench.Measure(start)
	for _, o := range res.Query.Ops {
		fmt.Fprintf(os.Stderr, "%s scored %.4f over %d runs\n", o.Op, o.Correct, o.Runs)
	}

	res.Dataset.Name = cfg.Dataset
	res.Dataset.Seeds = len(seeds)
	return nil
}

// runOps runs every operation the plan asks for and scores it against the
// answers.
//
// The seeds are taken from the front of the list in the order they were
// written, so a run with fewer of them is a subset of a run with more rather
// than a different sample, and two engines are always asked about the same
// nodes in the same order.
func runOps(cfg GraphConfig, eng GraphEngine, seeds []uint32, a graphs.Answers) []bench.OpStat {
	plan := a.Plan
	workers := cfg.WorkerCount()
	var out []bench.OpStat

	for _, op := range graphs.Operations() {
		if !cfg.Wants(op) {
			continue
		}
		if why := eng.Cannot(op); why != "" {
			out = append(out, bench.OpStat{Op: op, Unsupported: why})
			continue
		}
		want := a.Answers[op]

		switch op {
		case graphs.Neighbours:
			n := min(plan.Neighbour, len(seeds))
			stat := onePerSeed(op, want, n, func(i int) int64 { return eng.Neighbours(seeds[i]) })
			stat.Concurrent = alongside(n, workers, func(i int) { eng.Neighbours(seeds[i]) })
			out = append(out, stat)
		case graphs.TwoHop:
			n := min(plan.TwoHop, len(seeds))
			stat := onePerSeed(op, want, n, func(i int) int64 { return eng.TwoHop(seeds[i]) })
			stat.Concurrent = alongside(n, workers, func(i int) { eng.TwoHop(seeds[i]) })
			out = append(out, stat)
		case graphs.ShortestPath:
			n := min(plan.Path, len(seeds)/2)
			stat := onePerSeed(op, want, n, func(i int) int64 {
				return eng.ShortestPath(seeds[2*i], seeds[2*i+1])
			})
			stat.Concurrent = alongside(n, workers, func(i int) {
				eng.ShortestPath(seeds[2*i], seeds[2*i+1])
			})
			out = append(out, stat)
		case graphs.BFS:
			out = append(out, traversals(op, want, min(plan.BFS, len(seeds)), seeds, eng))
		case graphs.PageRank:
			t := time.Now()
			got := eng.PageRank(plan.Iterations, plan.Damping, plan.Top)
			runs := []time.Duration{time.Since(t)}
			correct, wrong := Score(got, want)
			out = append(out, bench.SummariseOp(op, correct, wrong, runs))
		}
	}
	return out
}

// onePerSeed times an operation that produces one number per seed.
func onePerSeed(op string, want []int64, count int, call func(int) int64) bench.OpStat {
	got := make([]int64, 0, count)
	runs := make([]time.Duration, 0, count)
	for i := range count {
		t := time.Now()
		answer := call(i)
		runs = append(runs, time.Since(t))
		got = append(got, answer)
	}
	correct, wrong := Score(got, want)
	return bench.SummariseOp(op, correct, wrong, runs)
}

// traversals times the breadth first searches, which produce two numbers each.
func traversals(op string, want []int64, count int, seeds []uint32, eng GraphEngine) bench.OpStat {
	got := make([]int64, 0, count*2)
	runs := make([]time.Duration, 0, count)
	for i := range count {
		t := time.Now()
		reached, depth := eng.BFS(seeds[i])
		runs = append(runs, time.Since(t))
		got = append(got, reached, depth)
	}
	correct, wrong := Score(got, want)
	return bench.SummariseOp(op, correct, wrong, runs)
}

// alongside runs the same operation again with several in flight.
//
// The workers take seeds off a shared counter rather than being handed a slice
// each, because a two hop lookup on a hub costs a thousand times what one on a
// leaf costs and a static split would end with one worker still going while the
// rest idle, which reports as lower throughput than the store has.
func alongside(count, workers int, call func(int)) *bench.ConcurrentStat {
	if count < ConcurrentMinimum || workers <= 0 {
		return nil
	}

	var (
		cursor atomic.Int64
		wg     sync.WaitGroup
		mu     sync.Mutex
	)
	times := make([]float64, 0, count)

	start := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mine := make([]float64, 0, count/workers+1)
			for {
				i := int(cursor.Add(1)) - 1
				if i >= count {
					break
				}
				t := time.Now()
				call(i)
				mine = append(mine, float64(time.Since(t).Nanoseconds())/1e6)
			}
			mu.Lock()
			times = append(times, mine...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	seconds := time.Since(start).Seconds()

	sort.Float64s(times)
	return &bench.ConcurrentStat{
		Workers:  workers,
		Queries:  len(times),
		Seconds:  seconds,
		MedianMS: bench.Percentile(times, 0.50),
		P99MS:    bench.Percentile(times, 0.99),
	}
}

// Score is the fraction of answers that matched, and how many did not.
//
// An answer the ground truth has nothing to say about counts as wrong rather
// than being skipped. That case means the answers file and the plan disagree
// about how many of something to run, and quietly scoring out of the shorter of
// the two would turn a broken run into a perfect score.
func Score(got, want []int64) (float64, int) {
	if len(got) == 0 {
		return 0, 0
	}
	wrong := 0
	for i, v := range got {
		if i >= len(want) || want[i] != v {
			wrong++
		}
	}
	return float64(len(got)-wrong) / float64(len(got)), wrong
}
