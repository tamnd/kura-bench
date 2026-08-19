package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// VectorResult is what a vector runner writes to standard output.
//
// It is a separate shape from [Result] rather than a field on it because the
// two suites do not share a single number. A text engine is compared on hits
// and an approximate vector index is compared on recall, and an approximate
// index has no fixed speed at all: it has a curve, and quoting one point off it
// without saying which point is how vector benchmarks end up disagreeing by an
// order of magnitude while all being technically correct.
type VectorResult struct {
	// Engine is the short name shown in the table, for example "turbovec".
	Engine string `json:"engine"`

	// Version is the library's version, not the runner's.
	Version string `json:"version"`

	// Language is what the engine is written in.
	Language string `json:"language"`

	// Dataset is what was indexed and searched, reported by the runner from the
	// files it actually read rather than taken on trust from the flags.
	Dataset DatasetStats `json:"dataset"`

	// Build is constructing the index from the base vectors.
	Build BuildPhase `json:"build"`

	// Open is the cold start, in a process that did not build the index.
	Open OpenPhase `json:"open"`

	// Search is the query set, at every operating point the runner tried.
	Search VectorSearchPhase `json:"search"`

	// Machine is where the numbers were taken.
	Machine Machine `json:"machine"`

	// Notes is for anything that would otherwise make a number misleading,
	// such as an engine that had to be given a smaller base set to fit.
	Notes string `json:"notes,omitempty"`

	// Incomplete says a phase was still running when the run gave up on it, and
	// is empty for a run that finished. See [GraphResult.Incomplete] for why an
	// engine that ran out of time stays in the report.
	Incomplete string `json:"incomplete,omitempty"`
}

// built says the build phase produced a measurement, which is not the same as
// the engine having been asked to build.
func (r VectorResult) built() bool { return r.Build.Usage.WallSeconds > 0 }

// searched says the query phase got as far as timing an operating point.
func (r VectorResult) searched() bool { return len(r.Search.Points) > 0 }

// DatasetStats is the input as the runner read it off disk.
type DatasetStats struct {
	Name string `json:"name"`

	// Dim is the number of components per vector.
	Dim int `json:"dim"`

	// Vectors is how many were indexed, which is not always the whole base set
	// when a run was limited to fit a machine.
	Vectors int `json:"vectors"`

	// Queries is how many query vectors were used.
	Queries int `json:"queries"`

	// K is how many neighbours were asked for, and the depth recall is scored
	// at.
	K int `json:"k"`

	// Metric is what nearest was told to mean, because an index built for
	// cosine and scored against Euclidean ground truth produces a recall figure
	// that is wrong in a way no timing will reveal.
	Metric string `json:"metric"`
}

// MetricPhrase says a metric the way somebody would say it out loud.
//
// Only one of the three is a distance. Cosine is a similarity and inner product
// is a score, and calling either of them a distance in a report is the sort of
// wrong that makes a reader stop trusting the rest of the table. Anything this
// does not know is passed through, since a runner is free to measure something
// this repository has not heard of and the report should print what it said.
func MetricPhrase(metric string) string {
	switch metric {
	case "euclidean":
		return "Euclidean distance"
	case "cosine":
		return "cosine similarity"
	case "inner-product":
		return "maximum inner product"
	case "":
		return "an unrecorded metric"
	default:
		return metric
	}
}

// RawBytes is what the vectors weigh as plain float32, which is the number an
// index size is only meaningful against. A quantizing index that is a quarter
// of this has told you something. A graph index that is twice it has also told
// you something.
func (d DatasetStats) RawBytes() int64 {
	return int64(d.Vectors) * int64(d.Dim) * 4
}

// BuildPhase is constructing the index.
type BuildPhase struct {
	// Usage is the whole build, including whatever the engine has to do before
	// the index can be searched or written out.
	Usage Usage `json:"usage"`

	// Bytes is the index on disk, or zero for an engine that keeps nothing.
	Bytes int64 `json:"bytes"`

	// Files is how many files it is spread over.
	Files int `json:"files"`
}

// VectorSearchPhase is the query set run at one or more operating points.
type VectorSearchPhase struct {
	// Usage covers every point together, which is why per point CPU is not
	// broken out. What the phase as a whole cost is still worth having for the
	// peak resident figure.
	Usage Usage `json:"usage"`

	// Points are the operating points, in the order the runner tried them,
	// which is normally cheapest and least accurate first.
	Points []VectorPoint `json:"points"`
}

// VectorPoint is the query set at one setting of whatever knob the engine
// exposes.
//
// An exact engine has one point, its parameters are empty and its recall is one
// by construction. Anything approximate has several, and the pair of recall and
// throughput is the result. Either number on its own is not.
type VectorPoint struct {
	// Params is the setting, written the way the engine's own documentation
	// writes it, for example "ef=64" or "bits=4". Empty for an exact search.
	Params string `json:"params,omitempty"`

	// Recall is the fraction of true nearest neighbours found, at k.
	Recall float64 `json:"recall"`

	// Queries is how many were run and timed one at a time.
	Queries  int     `json:"queries"`
	MinMS    float64 `json:"min_ms"`
	MedianMS float64 `json:"median_ms"`
	P90MS    float64 `json:"p90_ms"`
	P99MS    float64 `json:"p99_ms"`
	MaxMS    float64 `json:"max_ms"`

	// Concurrent is the same point with several queries in flight, which is the
	// throughput a server would see and is not one over the serial latency.
	Concurrent *ConcurrentStat `json:"concurrent,omitempty"`

	// Bytes is the index this point searched, and BuildSeconds is what building
	// it cost. Both are zero for the usual engine, where every point is the same
	// index searched harder or less hard, and the size belongs in the storage
	// table instead.
	//
	// They are filled in by an engine whose setting is fixed when the index is
	// built, a quantizer's bit width being the obvious case. For those the
	// operating points are three different indexes rather than three ways of
	// searching one, and a table that showed a single size next to three recalls
	// would be hiding the trade being made.
	Bytes        int64   `json:"bytes,omitempty"`
	BuildSeconds float64 `json:"build_seconds,omitempty"`
}

// QPS is the throughput at this point, from the concurrent run if there was one
// and from the serial median otherwise. Which of the two it is comes out in the
// report, since they are not the same measurement and should not be compared
// against each other.
func (p VectorPoint) QPS() float64 {
	if p.Concurrent != nil {
		return p.Concurrent.QueriesPerSecond()
	}
	if p.MedianMS <= 0 {
		return 0
	}
	return 1000 / p.MedianMS
}

// BuildsPerPoint says whether this engine chose its setting when the index was
// built, so that each operating point is its own index with its own size and
// its own build cost.
func (r VectorResult) BuildsPerPoint() bool {
	for _, p := range r.Search.Points {
		if p.Bytes > 0 {
			return true
		}
	}
	return false
}

// VectorsPerSecond is the build rate.
func (r VectorResult) VectorsPerSecond() float64 {
	if r.Build.Usage.WallSeconds <= 0 {
		return 0
	}
	return float64(r.Dataset.Vectors) / r.Build.Usage.WallSeconds
}

// BytesPerVector is the index on disk divided by the vectors in it, which is
// the storage figure that compares across datasets of different width.
func (r VectorResult) BytesPerVector() float64 {
	if r.Dataset.Vectors == 0 {
		return 0
	}
	return float64(r.Build.Bytes) / float64(r.Dataset.Vectors)
}

// IndexRatio is the index on disk over the raw float32 vectors.
func (r VectorResult) IndexRatio() float64 {
	raw := r.Dataset.RawBytes()
	if raw == 0 {
		return 0
	}
	return float64(r.Build.Bytes) / float64(raw)
}

// At is the fastest point that reached a recall, which is the only fair way to
// put approximate engines in one column. Comparing two engines at their own
// favourite settings compares the settings.
func (r VectorResult) At(recall float64) (VectorPoint, bool) {
	var best VectorPoint
	found := false
	for _, p := range r.Search.Points {
		if p.Recall < recall {
			continue
		}
		if !found || p.QPS() > best.QPS() {
			best, found = p, true
		}
	}
	return best, found
}

// Write emits the result as the single line of JSON the orchestrator reads.
func (r VectorResult) Write(w io.Writer) error {
	return json.NewEncoder(w).Encode(r)
}

// SummarisePoint turns the timings of one operating point into a point.
func SummarisePoint(params string, recall float64, runs []time.Duration) VectorPoint {
	if len(runs) == 0 {
		return VectorPoint{Params: params, Recall: recall}
	}
	ms := make([]float64, len(runs))
	for i, d := range runs {
		ms[i] = float64(d.Nanoseconds()) / 1e6
	}
	sort.Float64s(ms)
	return VectorPoint{
		Params:   params,
		Recall:   recall,
		Queries:  len(ms),
		MinMS:    ms[0],
		MedianMS: Percentile(ms, 0.50),
		P90MS:    Percentile(ms, 0.90),
		P99MS:    Percentile(ms, 0.99),
		MaxMS:    ms[len(ms)-1],
	}
}

// MergeVector combines the build process and the query process into the result
// that gets published, the same way [Merge] does for the text suite.
func MergeVector(build, query VectorResult) VectorResult {
	out := build
	out.Open = query.Open
	out.Search = query.Search
	// The build process never opens the query file and the query process never
	// counts the base vectors, so each half of the dataset line comes from the
	// process that actually read it.
	out.Dataset.Queries = query.Dataset.Queries
	out.Dataset.K = query.Dataset.K
	if query.Dataset.Metric != "" {
		out.Dataset.Metric = query.Dataset.Metric
	}
	// Two notes are two sentences that were never written to sit together, so
	// they are joined with something that keeps them apart on the page.
	if query.Notes != "" {
		out.Notes = strings.TrimSpace(strings.Trim(out.Notes+"; "+query.Notes, "; "))
	}
	return out
}

// VectorReport renders a set of vector results as markdown.
//
// The recall thresholds are fixed here rather than chosen per run. Ninety
// percent is where an approximate index stops being obviously worse than a
// scan for most uses, ninety nine is where people stop noticing it is
// approximate at all, and the distance between an engine's throughput at those
// two is the thing worth knowing about it.
func VectorReport(results []VectorResult) string {
	if len(results) == 0 {
		return "No results.\n"
	}
	sorted := make([]VectorResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Engine < sorted[j].Engine })

	// The machine and the dataset are read off the first result that measured
	// them rather than off the first result. An engine that ran out of time
	// while building is in the list with nothing but its name, and taking the
	// dataset from it would blank the section that says what was measured.
	head := sorted[0]
	for _, r := range sorted {
		if r.Dataset.Vectors > 0 {
			head = r
			break
		}
	}

	var b strings.Builder
	writeMachine(&b, head.Machine)
	writeDataset(&b, head.Dataset)
	writeHeadline(&b, sorted)
	writeBuild(&b, sorted)
	writeVectorStorage(&b, sorted)
	writeColdStart(&b, vectorOpens(sorted))
	writeCurve(&b, sorted)
	writeVectorNotes(&b, sorted)
	return b.String()
}

func writeDataset(b *strings.Builder, d DatasetStats) {
	fmt.Fprintf(b, "## Dataset\n\n")
	fmt.Fprintf(b, "%s, %s vectors of %d components, %s queries, recall at %d, ranked by %s.\n",
		d.Name, count(d.Vectors), d.Dim, count(d.Queries), d.K, MetricPhrase(d.Metric))
	fmt.Fprintf(b, "The vectors are %s as plain float32, which is what the index sizes below are measured against.\n\n",
		mb(d.RawBytes()))
}

// writeHeadline is the table to read first: what each engine can do while
// still being accurate enough to use.
func writeHeadline(b *strings.Builder, rs []VectorResult) {
	fmt.Fprintf(b, "## Throughput at a fixed accuracy\n\n")
	b.WriteString("| engine | version | at recall 0.90 | settings | at recall 0.99 | settings |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		// This is the table somebody comes to the report for, so the engine
		// that has no numbers for it says so here rather than being absent. Not
		// reached would be a claim about a curve that was never measured.
		if !r.searched() {
			fmt.Fprintf(b, "| %s | %s | %s | | | |\n", r.Engine, r.Version, missing(r.Incomplete))
			continue
		}
		lo, loOK := r.At(0.90)
		hi, hiOK := r.At(0.99)
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n",
			r.Engine, r.Version,
			qps(lo, loOK), params(lo, loOK), qps(hi, hiOK), params(hi, hiOK))
	}
	b.WriteString("\nEach cell is the fastest setting that reached that recall, so the engines are compared at the same accuracy rather than at their own favourite settings.\n")
	b.WriteString("An empty cell means no setting the runner tried got there.\n\n")
}

func qps(p VectorPoint, ok bool) string {
	if !ok {
		return "not reached"
	}
	return count(int(p.QPS())) + " q/s"
}

func params(p VectorPoint, ok bool) string {
	if !ok || p.Params == "" {
		return " "
	}
	return p.Params
}

func writeBuild(b *strings.Builder, rs []VectorResult) {
	var rows []string
	for _, r := range rs {
		if !r.built() {
			continue
		}
		u := r.Build.Usage
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %.1f | %.1fx | %s |",
			r.Engine, secs(u.WallSeconds), count(int(r.VectorsPerSecond())),
			u.CPUSeconds(), u.Parallelism(), mb(u.PeakRSSBytes)))
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## Building the index\n\n")
	b.WriteString("| engine | wall | vectors/s | CPU s | parallelism | peak RSS |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "%s\n", strings.Join(rows, "\n"))
	b.WriteString("\nBuild time is the cost of changing your mind about an index, and on a graph index it is the largest number in this report.\n\n")
}

func writeVectorStorage(b *strings.Builder, rs []VectorResult) {
	var rows []string
	for _, r := range rs {
		if !r.built() {
			continue
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %.0f | %.2fx |",
			r.Engine, mb(r.Build.Bytes), count(r.Build.Files),
			r.BytesPerVector(), r.IndexRatio()))
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## Storage\n\n")
	b.WriteString("| engine | index on disk | files | bytes per vector | index over raw vectors |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "%s\n", strings.Join(rows, "\n"))
	b.WriteString("\nBelow one means the engine is not keeping the full precision vectors, which is the whole point of a quantizing index and is also where its recall ceiling comes from.\n")
	b.WriteString("Above one is the cost of a graph, which buys the search time back.\n\n")
}

// writeCurve is the full set of operating points, which is what the headline
// table is a summary of and what somebody arguing with the headline table
// needs.
func writeCurve(b *strings.Builder, rs []VectorResult) {
	// The two extra columns only appear when some engine in the run needs them,
	// so a report of nothing but graph indexes is not carrying two empty columns
	// to describe a case it does not contain.
	perPoint := false
	for _, r := range rs {
		perPoint = perPoint || r.BuildsPerPoint()
	}

	fmt.Fprintf(b, "## Recall against speed\n\n")
	head := "| engine | settings | recall | median | p99 | queries/s | measured |"
	rule := "| --- | --- | --- | --- | --- | --- | --- |"
	if perPoint {
		head = "| engine | settings | recall | median | p99 | queries/s | measured | index | build |"
		rule = "| --- | --- | --- | --- | --- | --- | --- | --- | --- |"
	}
	fmt.Fprintf(b, "%s\n%s\n", head, rule)

	for _, r := range rs {
		if !r.searched() {
			fmt.Fprintf(b, "| %s | | %s | | | | |", r.Engine, missing(r.Incomplete))
			if perPoint {
				b.WriteString(" | |")
			}
			b.WriteString("\n")
			continue
		}
		for _, p := range r.Search.Points {
			how := "serial median"
			if p.Concurrent != nil {
				how = fmt.Sprintf("%d in flight", p.Concurrent.Workers)
			}
			fmt.Fprintf(b, "| %s | %s | %.4f | %s | %s | %s | %s |",
				r.Engine, params(p, true), p.Recall, ms(p.MedianMS), ms(p.P99MS),
				count(int(p.QPS())), how)
			if perPoint {
				fmt.Fprintf(b, " %s | %s |", pointSize(p), pointBuild(p))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nRecall is against the exact ground truth for this metric, at the k in the dataset line above.\n")
	b.WriteString("Latency is one query at a time and throughput is with several in flight, because a server does one and a batch job does the other.\n")
	if perPoint {
		b.WriteString("The last two columns are filled in for an engine whose setting is fixed when the index is built, where each row is a different index rather than a different way of searching the same one.\n")
		b.WriteString("For those engines the build and storage tables above cover the whole sweep together, since that is what the run actually cost.\n")
	}
	b.WriteString("\n")
}

func pointSize(p VectorPoint) string {
	if p.Bytes <= 0 {
		return " "
	}
	return mb(p.Bytes)
}

func pointBuild(p VectorPoint) string {
	if p.BuildSeconds <= 0 {
		return " "
	}
	return secs(p.BuildSeconds)
}

func writeVectorNotes(b *strings.Builder, rs []VectorResult) {
	var lines []string
	for _, r := range rs {
		if note := noteFor(r.Notes, r.Incomplete); note != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", r.Engine, note))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "## Notes\n\n%s\n\n", strings.Join(lines, "\n"))
}

// vectorOpens borrows the text suite's cold start table, since opening an index
// off disk is the same measurement whatever is in it. The table drops the
// engines that never opened anything.
func vectorOpens(rs []VectorResult) []Result {
	out := make([]Result, len(rs))
	for i, r := range rs {
		out[i] = Result{Engine: r.Engine, Open: r.Open}
	}
	return out
}
