package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// GraphResult is what a graph runner writes to standard output.
//
// It is a separate shape from [Result] and [VectorResult] because a graph store
// is not compared on one number either. A neighbour lookup and a breadth first
// search over the same graph differ by six orders of magnitude, and a store
// that is quick at one is often the one that is slow at the other, so the
// operations are kept apart all the way to the table.
type GraphResult struct {
	// Engine is the short name shown in the table, for example "csr".
	Engine string `json:"engine"`

	// Version is the library's version, not the runner's.
	Version string `json:"version"`

	// Language is what the engine is written in.
	Language string `json:"language"`

	// Dataset is the graph as the runner read it off disk, rather than as the
	// flags claimed.
	Dataset GraphStats `json:"dataset"`

	// Build is loading the edges and getting them into whatever form the store
	// answers questions from.
	Build GraphBuildPhase `json:"build"`

	// Open is the cold start, in a process that did not build the store.
	Open OpenPhase `json:"open"`

	// Query is every operation the run asked for.
	Query GraphQueryPhase `json:"query"`

	// Machine is where the numbers were taken.
	Machine Machine `json:"machine"`

	// Notes is for anything that would otherwise make a number misleading,
	// such as a library that has no on disk form and rebuilds at every start.
	Notes string `json:"notes,omitempty"`
}

// GraphStats is the graph as the runner read it.
type GraphStats struct {
	Name  string `json:"name"`
	Nodes int    `json:"nodes"`
	Edges int    `json:"edges"`

	// Undirected says the publisher stored both directions of every edge. It
	// changes what a traversal means: a reachable set on an undirected graph is
	// a connected component and on a directed one it is not.
	Undirected bool `json:"undirected"`

	// Seeds is how many nodes the run asked about.
	Seeds int `json:"seeds"`
}

// GraphBuildPhase is getting the graph into the store.
type GraphBuildPhase struct {
	Usage Usage `json:"usage"`

	// Bytes is the store on disk, or zero for an engine that keeps nothing.
	Bytes int64 `json:"bytes"`

	// Files is how many files it is spread over.
	Files int `json:"files"`
}

// GraphQueryPhase is every operation the run asked for.
type GraphQueryPhase struct {
	// Usage covers every operation together, which is where the peak resident
	// figure comes from. Per operation CPU is not broken out because a
	// traversal and a point lookup share a process and there is no honest way
	// to split it.
	Usage Usage `json:"usage"`

	// Ops are the operations, cheapest first.
	Ops []OpStat `json:"ops"`
}

// OpStat is one operation, run as many times as the plan said.
type OpStat struct {
	// Op is the operation's name, one of the five the graphs package defines.
	Op string `json:"op"`

	// Runs is how many times it ran, which is one for pagerank and a thousand
	// for a neighbour lookup.
	Runs int `json:"runs"`

	// Correct is the fraction of answers that matched the ground truth. One
	// means every answer agreed with an implementation written in another
	// language, which is the only reason any of the timings are worth reading.
	Correct float64 `json:"correct"`

	// Mismatches is how many disagreed, which is the number somebody debugging
	// wants and is not recoverable from the fraction once it has been rounded.
	Mismatches int `json:"mismatches"`

	MinMS    float64 `json:"min_ms"`
	MedianMS float64 `json:"median_ms"`
	P90MS    float64 `json:"p90_ms"`
	P99MS    float64 `json:"p99_ms"`
	MaxMS    float64 `json:"max_ms"`

	// Concurrent is the same operation with several in flight, which is the
	// throughput a server would see and is not one over the serial latency. It
	// is only filled in for the operations that run often enough for the
	// figure to be about the store rather than about starting threads.
	Concurrent *ConcurrentStat `json:"concurrent,omitempty"`

	// Unsupported says why this operation has no timings, for a store that
	// cannot do it. An empty row with a reason in it is worth more than a
	// missing row, which reads as an oversight.
	Unsupported string `json:"unsupported,omitempty"`
}

// OpsPerSecond is the rate from the concurrent run when there was one and from
// the serial median otherwise. Which of the two it is comes out in the report,
// since they are not the same measurement.
func (o OpStat) OpsPerSecond() float64 {
	if o.Concurrent != nil {
		return o.Concurrent.QueriesPerSecond()
	}
	if o.MedianMS <= 0 {
		return 0
	}
	return 1000 / o.MedianMS
}

// EdgesPerSecond is the build rate, which is the figure that compares across
// graphs of different size.
func (r GraphResult) EdgesPerSecond() float64 {
	if r.Build.Usage.WallSeconds <= 0 {
		return 0
	}
	return float64(r.Dataset.Edges) / r.Build.Usage.WallSeconds
}

// BytesPerEdge is the store on disk divided by the edges in it.
func (r GraphResult) BytesPerEdge() float64 {
	if r.Dataset.Edges == 0 {
		return 0
	}
	return float64(r.Build.Bytes) / float64(r.Dataset.Edges)
}

// RawBytes is what the graph weighs as the pairs of uint32 it arrived as, which
// is the only number a store's own size means anything against.
func (g GraphStats) RawBytes() int64 { return int64(g.Edges) * 8 }

// StorageRatio is the store on disk over the edge list it was built from.
func (r GraphResult) StorageRatio() float64 {
	raw := r.Dataset.RawBytes()
	if raw == 0 {
		return 0
	}
	return float64(r.Build.Bytes) / float64(raw)
}

// Op finds one operation's row, since not every engine ran every operation.
func (r GraphResult) Op(name string) (OpStat, bool) {
	for _, o := range r.Query.Ops {
		if o.Op == name {
			return o, true
		}
	}
	return OpStat{}, false
}

// Wrong says whether any operation disagreed with the ground truth, which is
// the one thing a reader has to know before reading any of the timings.
func (r GraphResult) Wrong() bool {
	for _, o := range r.Query.Ops {
		if o.Unsupported == "" && o.Mismatches > 0 {
			return true
		}
	}
	return false
}

// Write emits the result as the single line of JSON the orchestrator reads.
func (r GraphResult) Write(w io.Writer) error {
	return json.NewEncoder(w).Encode(r)
}

// SummariseOp turns the timings of one operation into a row.
func SummariseOp(op string, correct float64, mismatches int, runs []time.Duration) OpStat {
	if len(runs) == 0 {
		return OpStat{Op: op, Correct: correct, Mismatches: mismatches}
	}
	ms := make([]float64, len(runs))
	for i, d := range runs {
		ms[i] = float64(d.Nanoseconds()) / 1e6
	}
	sort.Float64s(ms)
	return OpStat{
		Op:         op,
		Runs:       len(ms),
		Correct:    correct,
		Mismatches: mismatches,
		MinMS:      ms[0],
		MedianMS:   Percentile(ms, 0.50),
		P90MS:      Percentile(ms, 0.90),
		P99MS:      Percentile(ms, 0.99),
		MaxMS:      ms[len(ms)-1],
	}
}

// MergeGraph combines the build process and the query process into the result
// that gets published, the same way [Merge] does for the text suite.
func MergeGraph(build, query GraphResult) GraphResult {
	out := build
	out.Open = query.Open
	out.Query = query.Query
	// The build process never reads the seed file, so that half of the dataset
	// line comes from the process that actually read it.
	out.Dataset.Seeds = query.Dataset.Seeds
	// Two notes are two sentences that were never written to sit together, so
	// they are joined with something that keeps them apart on the page.
	if query.Notes != "" && query.Notes != out.Notes {
		out.Notes = strings.TrimSpace(strings.Trim(out.Notes+"; "+query.Notes, "; "))
	}
	return out
}

// GraphReport renders a set of graph results as markdown.
//
// The operations get a table each rather than a column each, because their
// costs are six orders of magnitude apart and a single table would round four
// of the five to zero.
func GraphReport(results []GraphResult, ops []string) string {
	if len(results) == 0 {
		return "No results.\n"
	}
	sorted := make([]GraphResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Engine < sorted[j].Engine })

	var b strings.Builder
	writeMachine(&b, sorted[0].Machine)
	writeGraph(&b, sorted[0].Dataset)
	writeCorrectness(&b, sorted, ops)
	writeGraphBuild(&b, sorted)
	writeGraphStorage(&b, sorted)
	writeColdStart(&b, graphOpens(sorted))
	writeOps(&b, sorted, ops)
	writeGraphNotes(&b, sorted)
	return b.String()
}

func writeGraph(b *strings.Builder, g GraphStats) {
	fmt.Fprintf(b, "## Graph\n\n")
	shape := "directed"
	if g.Undirected {
		shape = "undirected, stored with both directions of every edge"
	}
	fmt.Fprintf(b, "%s, %s nodes and %s edges, %s.\n", g.Name, count(g.Nodes), count(g.Edges), shape)
	fmt.Fprintf(b, "The run asked about %s nodes, the same ones in the same order for every engine.\n", count(g.Seeds))
	fmt.Fprintf(b, "The edge list is %s as pairs of uint32, which is what the store sizes below are measured against.\n\n",
		mb(g.RawBytes()))
}

// writeCorrectness is the table to read first, because a store that is fast and
// wrong is not fast.
func writeCorrectness(b *strings.Builder, rs []GraphResult, ops []string) {
	fmt.Fprintf(b, "## Correctness\n\n")
	b.WriteString("| engine | version |")
	for _, op := range ops {
		fmt.Fprintf(b, " %s |", op)
	}
	b.WriteString("\n| --- | --- |")
	for range ops {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")

	for _, r := range rs {
		fmt.Fprintf(b, "| %s | %s |", r.Engine, r.Version)
		for _, name := range ops {
			o, ok := r.Op(name)
			switch {
			case !ok:
				b.WriteString(" not run |")
			case o.Unsupported != "":
				b.WriteString(" cannot |")
			case o.Mismatches == 0 && o.Runs > 0:
				b.WriteString(" agrees |")
			default:
				// A fraction rather than a count, because bfs answers two
				// numbers per run and a count of wrong numbers against a count
				// of runs is two different units in one cell.
				fmt.Fprintf(b, " %.1f%% agree |", o.Correct*100)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\nThe answers were worked out separately, in Go, by walking the same edge list the plainest way there is.\n")
	b.WriteString("Agreement between that and an engine is two independent implementations landing on the same numbers, which is the only reason the timings below are worth reading.\n\n")
}

func writeGraphBuild(b *strings.Builder, rs []GraphResult) {
	fmt.Fprintf(b, "## Loading the graph\n\n")
	b.WriteString("| engine | wall | edges/s | CPU s | parallelism | peak RSS |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		u := r.Build.Usage
		fmt.Fprintf(b, "| %s | %s | %s | %.1f | %.1fx | %s |\n",
			r.Engine, secs(u.WallSeconds), count(int(r.EdgesPerSecond())),
			u.CPUSeconds(), u.Parallelism(), mb(u.PeakRSSBytes))
	}
	b.WriteString("\nThis is the cost of getting a graph into the store in the first place, which on a large one is the largest number in this report.\n\n")
}

func writeGraphStorage(b *strings.Builder, rs []GraphResult) {
	fmt.Fprintf(b, "## Storage\n\n")
	b.WriteString("| engine | store on disk | files | bytes per edge | store over edge list |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		fmt.Fprintf(b, "| %s | %s | %s | %.1f | %.2fx |\n",
			r.Engine, mb(r.Build.Bytes), count(r.Build.Files),
			r.BytesPerEdge(), r.StorageRatio())
	}
	b.WriteString("\nBelow one means the store is keeping less than the eight bytes an edge arrived as, which is what a dense adjacency buys.\n")
	b.WriteString("Above one is the cost of an index, a property store or a page layout, and it should be buying something back in the tables below.\n\n")
}

// writeOps is a table per operation, because their costs are far enough apart
// that one table would round most of them away.
func writeOps(b *strings.Builder, rs []GraphResult, ops []string) {
	fmt.Fprintf(b, "## Operations\n\n")
	for _, name := range ops {
		fmt.Fprintf(b, "### %s\n\n", name)
		fmt.Fprintf(b, "%s\n\n", opAbout(name))
		b.WriteString("| engine | runs | median | p90 | p99 | max | per second | measured |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, r := range rs {
			o, ok := r.Op(name)
			if !ok {
				fmt.Fprintf(b, "| %s | not run | | | | | | |\n", r.Engine)
				continue
			}
			if o.Unsupported != "" {
				fmt.Fprintf(b, "| %s | | | | | | | %s |\n", r.Engine, o.Unsupported)
				continue
			}
			how := "one at a time"
			if o.Concurrent != nil {
				how = fmt.Sprintf("%d in flight", o.Concurrent.Workers)
			}
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				r.Engine, count(o.Runs), opTime(o.MedianMS), opTime(o.P90MS), opTime(o.P99MS), opTime(o.MaxMS),
				count(int(o.OpsPerSecond())), how)
		}
		b.WriteString("\n")
	}
	b.WriteString("The maximum matters more here than in the other suites.\n")
	b.WriteString("Most nodes in a real graph have a handful of neighbours and a few have a hundred thousand, so the median says what the common case costs and the maximum says what a hub costs.\n")
	b.WriteString("A cell that says below the clock is a lookup that finished inside one tick of the monotonic timer, which is a real result on a small graph and not a missing one.\n\n")
}

// opTime writes one operation's time in a unit somebody can read.
//
// The other two suites measure in milliseconds because that is the scale a
// query runs at. A neighbour lookup is three orders of magnitude below that, so
// printing it the same way fills a column with 0.00 and throws away the
// comparison the table exists for.
func opTime(v float64) string {
	switch {
	case v <= 0:
		return "below the clock"
	case v < 0.001:
		return fmt.Sprintf("%.0f ns", v*1e6)
	case v < 1:
		return fmt.Sprintf("%.1f us", v*1000)
	case v < 1000:
		return fmt.Sprintf("%.2f ms", v)
	default:
		return fmt.Sprintf("%.2f s", v/1000)
	}
}

// opAbout is the sentence under each operation's heading, so that somebody
// reading the table knows what was asked before they read what it cost.
func opAbout(op string) string {
	switch op {
	case "neighbours":
		return "One hop out of one node, the cheapest thing a graph store does and the one it does most."
	case "two-hop":
		return "The distinct nodes within two hops, which is a friend of a friend, and the operation where the cost of a hub shows up."
	case "shortest-path":
		return "The hop count between two nodes, or nothing when they are not connected, which costs a full traversal of everything the start can reach."
	case "bfs":
		return "The whole reachable set from one node, which touches everything and cannot be helped by any index."
	case "pagerank":
		return "The whole graph, several times over, which is the analytics workload rather than the serving one."
	default:
		return "Measured by the runners, not described here."
	}
}

func writeGraphNotes(b *strings.Builder, rs []GraphResult) {
	var lines []string
	for _, r := range rs {
		if r.Notes != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", r.Engine, r.Notes))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "## Notes\n\n%s\n\n", strings.Join(lines, "\n"))
}

// graphOpens borrows the text suite's cold start table, since opening a store
// off disk is the same measurement whatever is in it.
func graphOpens(rs []GraphResult) []Result {
	out := make([]Result, len(rs))
	for i, r := range rs {
		out[i] = Result{Engine: r.Engine, Open: r.Open}
	}
	return out
}
