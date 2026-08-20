// Package bench is the contract between a runner and the orchestrator.
//
// A runner is a program that puts the corpus through one engine and writes one
// JSON object to standard output. Everything else it prints goes to standard
// error and is passed through to the operator. That is the whole interface, and
// it is a pipe rather than a Go interface because half the engines worth
// measuring are not written in Go.
//
// A runner is invoked as:
//
//	runner -corpus corpus.jsonl -queries queries.txt -work <dir> [-repeat n]
//
// # What gets measured
//
// Four phases, because the engines here trade against each other differently in
// each one and a single number hides that. An engine that indexes twice as fast
// and then answers queries three times slower is not faster, and an engine that
// wins both while holding the whole corpus in memory has not won at all if the
// machine it has to run on does not have that memory.
//
//   - Index. Build the index from an empty directory.
//   - Open. Close everything, reopen the index from disk, answer one query.
//     This is the cold start an operator sees on every deploy and restart, and
//     it is where engines that keep the index in memory stop looking free.
//   - Search. Run the query set, at one query at a time and then at the
//     machine's worth of concurrency.
//   - Update. Reindex a slice of the corpus into the built index, which is what
//     a connector does on every incremental sync and is not the same operation
//     as building from empty.
//
// Each phase reports wall clock, CPU time split into user and system, peak
// resident memory, and what it left on disk.
package bench

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Result is what a runner writes to standard output.
type Result struct {
	// Engine is the short name shown in the table, for example "tantivy".
	Engine string `json:"engine"`

	// Version is the engine's own version, not the runner's. A number without
	// one is not reproducible.
	Version string `json:"version"`

	// Language is what the engine is written in, which explains more of the
	// spread between these numbers than anything else does.
	Language string `json:"language"`

	// Corpus describes what was actually indexed. It is reported rather than
	// assumed, so that a runner which silently dropped documents shows up as
	// having indexed fewer of them instead of looking fast.
	Corpus CorpusStats `json:"corpus"`

	// Index is the build from empty.
	Index IndexPhase `json:"index"`

	// Open is the cold start: reopen from disk and answer one query.
	Open OpenPhase `json:"open"`

	// Search is the query set.
	Search SearchPhase `json:"search"`

	// Update is an incremental reindex, and is omitted by an engine that has no
	// way to do one.
	Update *UpdatePhase `json:"update,omitempty"`

	// Machine is where the numbers were taken. A result without it is a number
	// nobody can compare against anything.
	Machine Machine `json:"machine"`

	// Notes is for anything that would otherwise make a number misleading, such
	// as an engine that could not express one of the filters, or a phase that
	// was skipped and why.
	Notes string `json:"notes,omitempty"`

	// Incomplete says a phase was still running when the run gave up on it, and
	// is empty for a run that finished. See [GraphResult.Incomplete] for why an
	// engine that ran out of time stays in the report.
	Incomplete string `json:"incomplete,omitempty"`
}

// indexed says the index phase produced a measurement, which is not the same as
// the engine having been asked to index.
func (r Result) indexed() bool { return r.Index.Usage.WallSeconds > 0 }

// searched says the query phase got as far as timing the query set.
func (r Result) searched() bool { return len(r.Search.Queries) > 0 }

// CorpusStats is the input as the runner saw it.
type CorpusStats struct {
	Documents int   `json:"documents"`
	Bytes     int64 `json:"bytes"`
}

// IndexPhase is building the index from nothing.
type IndexPhase struct {
	// Usage is the cost of the phase, including whatever commit or flush the
	// engine needs before the index is queryable. An engine that returns from
	// its last write with work still queued is measured on when the work is
	// done, not on when the call returned.
	Usage Usage `json:"usage"`

	// Bytes is the index on disk after that flush, or zero for an engine that
	// keeps nothing on disk.
	Bytes int64 `json:"bytes"`

	// Files is how many files the index is made of, which is worth knowing
	// because an engine that writes ten thousand small segments behaves very
	// differently on a network filesystem than one that writes a single file.
	Files int `json:"files"`
}

// OpenPhase is the cold start.
type OpenPhase struct {
	// Usage covers opening the index and answering one query against it, in a
	// process that has not read the index before.
	Usage Usage `json:"usage"`

	// ResidentBytes is resident memory once the index is open and one query has
	// been answered, which is the floor an idle replica sits at.
	ResidentBytes int64 `json:"resident_bytes"`
}

// SearchPhase is the query set.
type SearchPhase struct {
	// Usage is the whole phase, and is what the CPU cost per query is derived
	// from. Latency alone does not say how many cores a query burned, and an
	// engine that answers in ten milliseconds using eight threads is not
	// cheaper than one that answers in fifty using one.
	Usage Usage `json:"usage"`

	// Queries holds one entry per query in the set, measured one at a time.
	Queries []QueryStat `json:"queries"`

	// Concurrent is the same query set run with several in flight at once, and
	// is what a throughput figure has to come from. Serial latency divided into
	// one second is a number that no deployment has ever achieved.
	Concurrent *ConcurrentStat `json:"concurrent,omitempty"`
}

// UpdatePhase is an incremental reindex over an already built index.
type UpdatePhase struct {
	Usage Usage `json:"usage"`

	// Documents is how many were rewritten.
	Documents int   `json:"documents"`
	Bytes     int64 `json:"bytes"`

	// IndexBytesAfter is the index size once the update has been flushed.
	// Compared against the size before it, this is what shows an engine that
	// never reclaims the space a replaced document used.
	IndexBytesAfter int64 `json:"index_bytes_after"`
}

// QueryStat is one query measured over several runs.
type QueryStat struct {
	Query string `json:"query"`

	// Hits is the total the engine reported, not the page it returned. Two
	// engines that disagree wildly here are not answering the same question,
	// and their latencies are not comparable no matter how careful the timing
	// was.
	Hits int `json:"hits"`

	Runs     int     `json:"runs"`
	MinMS    float64 `json:"min_ms"`
	MedianMS float64 `json:"median_ms"`
	P90MS    float64 `json:"p90_ms"`
	P99MS    float64 `json:"p99_ms"`
	MaxMS    float64 `json:"max_ms"`

	// IDs is the page the engine returned, in the order it returned it. It
	// comes from the warm up run rather than a timed one, so collecting it
	// costs nothing a timing sees.
	//
	// Two things need it. A relevance score needs to know what came back and
	// not just how many, and a latency comparison needs a way to check that two
	// engines answered the same question, which the total alone does not
	// establish. An engine can report the same total and return a different
	// page, and until this field existed there was no way to see that.
	IDs []string `json:"ids,omitempty"`
}

// ConcurrentStat is the query set run with several in flight.
type ConcurrentStat struct {
	// Workers is how many were in flight at once.
	Workers int `json:"workers"`

	// Queries is how many completed, and Seconds how long that took.
	Queries int     `json:"queries"`
	Seconds float64 `json:"seconds"`

	MedianMS float64 `json:"median_ms"`
	P99MS    float64 `json:"p99_ms"`
}

// QueriesPerSecond is the throughput of the concurrent phase.
func (c ConcurrentStat) QueriesPerSecond() float64 {
	if c.Seconds <= 0 {
		return 0
	}
	return float64(c.Queries) / c.Seconds
}

// DocsPerSecond is the indexing rate people quote.
//
// It is also the one that misleads, because a corpus of chat messages and a
// corpus of source files differ by three orders of magnitude in bytes per
// document. [Result.MBPerSecond] is the one that compares across corpora.
func (r Result) DocsPerSecond() float64 {
	if r.Index.Usage.WallSeconds <= 0 {
		return 0
	}
	return float64(r.Corpus.Documents) / r.Index.Usage.WallSeconds
}

// MBPerSecond is indexing throughput over the bytes of document text.
func (r Result) MBPerSecond() float64 {
	if r.Index.Usage.WallSeconds <= 0 {
		return 0
	}
	return float64(r.Corpus.Bytes) / (1 << 20) / r.Index.Usage.WallSeconds
}

// IndexRatio is index size over corpus size.
//
// Below one means the engine stores less than the text it was given, which for
// a full text index means it is not keeping the documents. Above one means it
// keeps them and adds structure. Neither is wrong, and comparing an engine that
// stores documents against one that does not without saying so is.
func (r Result) IndexRatio() float64 {
	if r.Corpus.Bytes == 0 {
		return 0
	}
	return float64(r.Index.Bytes) / float64(r.Corpus.Bytes)
}

// MedianQueryMS is the median across the medians of every query, which is the
// single number worth putting in a table. The per query rows are there for when
// it turns out one query carries the whole figure.
func (r Result) MedianQueryMS() float64 {
	if len(r.Search.Queries) == 0 {
		return 0
	}
	all := make([]float64, 0, len(r.Search.Queries))
	for _, q := range r.Search.Queries {
		all = append(all, q.MedianMS)
	}
	sort.Float64s(all)
	return all[len(all)/2]
}

// CPUMillisPerQuery is how much processor time one query costs, which is what
// decides how many a machine can serve. It is not the same as latency and on a
// parallel engine it is much larger.
func (r Result) CPUMillisPerQuery() float64 {
	n := 0
	for _, q := range r.Search.Queries {
		n += q.Runs
	}
	if n == 0 {
		return 0
	}
	return r.Search.Usage.CPUSeconds() * 1000 / float64(n)
}

// Write emits the result as the single line of JSON the orchestrator reads.
func (r Result) Write(w io.Writer) error {
	return json.NewEncoder(w).Encode(r)
}

// Summarise turns a set of timings into a stat.
//
// The caller passes every run it wants counted and this discards nothing.
// Deciding what to throw away is the runner's job and belongs where a reader
// can see it, not hidden in a helper that every engine shares.
func Summarise(query string, hits int, runs []time.Duration) QueryStat {
	if len(runs) == 0 {
		return QueryStat{Query: query, Hits: hits}
	}
	ms := make([]float64, len(runs))
	for i, d := range runs {
		ms[i] = float64(d.Nanoseconds()) / 1e6
	}
	sort.Float64s(ms)
	return QueryStat{
		Query:    query,
		Hits:     hits,
		Runs:     len(ms),
		MinMS:    ms[0],
		MedianMS: Percentile(ms, 0.50),
		P90MS:    Percentile(ms, 0.90),
		P99MS:    Percentile(ms, 0.99),
		MaxMS:    ms[len(ms)-1],
	}
}

// Percentile takes the nearest rank on an already sorted slice, which is the
// definition that does not invent a value nobody measured.
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

// DirSize adds up the files under a path and counts them, which is how an index
// that is a directory of segments gets compared with one that is a single file.
// A path that does not exist is not an error, because an engine that keeps
// nothing on disk is a legitimate answer.
func DirSize(dir string) (int64, int, error) {
	var (
		total int64
		files int
	)
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
			files++
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	return total, files, err
}
