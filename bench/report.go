package bench

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Merge combines the two runs of one engine into the result that gets
// published.
//
// The phases are run as separate processes so that the open phase is a real
// cold start, which means the numbers arrive in two objects. Everything about
// the corpus and the build comes from the first, everything about querying from
// the second, and the notes from both because either process can have something
// to say.
func Merge(index, query Result) Result {
	out := index
	out.Open = query.Open
	out.Search = query.Search
	out.Update = query.Update
	out.Notes = joinNotes(index.Notes, query.Notes)
	return out
}

// joinNotes puts the two processes' notes together, for whichever suite is
// merging them.
//
// Usually they are the same sentence, written by the same runner twice, because
// a runner that has something to say about its numbers says it in both of its
// processes. When they are not the same, it is because the second process found
// out something the first could not know yet, and what it says then starts with
// what it said before and goes on. So the longer of the two wins when one
// contains the other, and only genuinely different notes are joined, with
// something between them that keeps two sentences apart on the page.
func joinNotes(first, second string) string {
	switch {
	case second == "":
		return first
	case first == "":
		return second
	case strings.Contains(second, first):
		return second
	case strings.Contains(first, second):
		return first
	}
	return strings.TrimSpace(first + "; " + second)
}

// Report renders a set of results as markdown.
//
// Everything derived is derived here rather than by the runners, so that a
// number in the table can always be traced back to a field in the JSON next to
// it. The tables are wide because narrowing them would mean choosing which of
// these costs does not matter, and that choice depends on the deployment.
func Report(results []Result) string {
	if len(results) == 0 {
		return "No results.\n"
	}
	sorted := make([]Result, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Engine < sorted[j].Engine })

	// The machine and the corpus are read off the first result that measured
	// them rather than off the first result. An engine that ran out of time
	// while indexing is in the list with nothing but its name, and taking the
	// corpus from it would blank the section that says what was measured.
	head := sorted[0]
	for _, r := range sorted {
		if r.Corpus.Documents > 0 {
			head = r
			break
		}
	}

	var b strings.Builder
	writeMachine(&b, head.Machine)
	writeCorpus(&b, head.Corpus)
	writeIndexing(&b, sorted)
	writeStorage(&b, sorted)
	writeColdStart(&b, sorted)
	writeSearch(&b, sorted)
	writeConcurrency(&b, sorted)
	writeUpdates(&b, sorted)
	writePerQuery(&b, sorted)
	writeNotes(&b, sorted)
	return b.String()
}

func writeMachine(b *strings.Builder, m Machine) {
	fmt.Fprintf(b, "## Machine\n\n")
	fmt.Fprintf(b, "%s, %s/%s, %s, %d cores, %s of memory.\n",
		m.Host, m.OS, m.Arch, m.CPU, m.Cores, mb(m.MemoryBytes))
	// macOS has no single counter for available memory that is worth the
	// arithmetic to assemble, so it is left at zero there. Printing that as
	// "none was free" would be a claim about a machine with twenty four
	// gigabytes in it, so the clause is dropped when the figure is not known.
	free := ""
	if m.MemoryFreeBytes > 0 {
		free = " and " + mb(m.MemoryFreeBytes) + " was free"
	}
	if m.Dedicated {
		fmt.Fprintf(b, "Load before the run was %.2f%s, so the machine was idle.\n", m.LoadBefore, free)
	} else {
		fmt.Fprintf(b, "Load before the run was %.2f%s, so the machine was doing other work and these numbers are a floor rather than a measurement.\n",
			m.LoadBefore, free)
	}
	b.WriteString("\n")
}

func writeCorpus(b *strings.Builder, c CorpusStats) {
	fmt.Fprintf(b, "## Corpus\n\n")
	fmt.Fprintf(b, "%s documents, %s of text.\n\n", count(c.Documents), mb(c.Bytes))
}

func writeIndexing(b *strings.Builder, rs []Result) {
	var rows []string
	for _, r := range rs {
		if !r.indexed() {
			continue
		}
		u := r.Index.Usage
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s | %.1f | %.1f | %.1fx | %s |",
			r.Engine, r.Version, secs(u.WallSeconds), count(int(r.DocsPerSecond())),
			r.MBPerSecond(), u.CPUSeconds(), u.Parallelism(), mb(u.PeakRSSBytes)))
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## Indexing\n\n")
	b.WriteString("| engine | version | wall | docs/s | MB/s | CPU s | parallelism | peak RSS |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "%s\n\n", strings.Join(rows, "\n"))
}

func writeStorage(b *strings.Builder, rs []Result) {
	var rows []string
	for _, r := range rs {
		if !r.indexed() {
			continue
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %.2fx | %s |",
			r.Engine, mb(r.Index.Bytes), count(r.Index.Files),
			r.IndexRatio(), mb(r.Index.Usage.WriteBytes)))
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## Storage\n\n")
	b.WriteString("| engine | index on disk | files | index over corpus | bytes written |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "%s\n", strings.Join(rows, "\n"))
	b.WriteString("\nIndex over corpus below one means the engine does not keep the document text.\n")
	b.WriteString("Bytes written is what the process asked the kernel to write, which on some platforms counts every handle and not only files.\n\n")
}

// writeColdStart is shared by all three suites, since opening an index off disk
// is the same measurement whatever is in it.
//
// A heading with an empty table under it and a paragraph explaining numbers
// that are not there is worse than no section, so a table nobody has a row in
// is not written at all. The reason is in the notes, one line per engine.
func writeColdStart(b *strings.Builder, rs []Result) {
	var rows []string
	for _, r := range rs {
		// An engine whose query process never got as far as opening has no
		// cold start, and a row of zeros would read as the fastest open in the
		// table.
		if r.Open.Usage.WallSeconds <= 0 {
			continue
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %.2f | %s |",
			r.Engine, secs(r.Open.Usage.WallSeconds),
			r.Open.Usage.CPUSeconds(), mb(r.Open.ResidentBytes)))
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## Cold start\n\n")
	b.WriteString("| engine | open and first query | CPU s | resident after open |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "%s\n", strings.Join(rows, "\n"))
	b.WriteString("\nThis is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.\n\n")
}

func writeSearch(b *strings.Builder, rs []Result) {
	fmt.Fprintf(b, "## Search, one query at a time\n\n")
	b.WriteString("| engine | median | p90 | p99 | CPU ms per query | peak RSS |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		// This is the table somebody comes to the report for, so the engine
		// that has no numbers for it says so here rather than being absent.
		if !r.searched() {
			fmt.Fprintf(b, "| %s | %s | | | | |\n", r.Engine, missing(r.Incomplete))
			continue
		}
		p90, p99 := aggregate(r)
		fmt.Fprintf(b, "| %s | %s | %s | %s | %.1f | %s |\n",
			r.Engine, ms(r.MedianQueryMS()), ms(p90), ms(p99),
			r.CPUMillisPerQuery(), mb(r.Search.Usage.PeakRSSBytes))
	}
	b.WriteString("\n")
}

func writeConcurrency(b *strings.Builder, rs []Result) {
	measured := false
	for _, r := range rs {
		if r.Search.Concurrent != nil {
			measured = true
		}
	}
	if !measured {
		return
	}
	fmt.Fprintf(b, "## Search, several in flight\n\n")
	b.WriteString("| engine | workers | queries/s | median | p99 |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		// An engine that never got to the query set has not declined to measure
		// this, so it is left out rather than told apart from one that did.
		if !r.searched() {
			continue
		}
		c := r.Search.Concurrent
		if c == nil {
			fmt.Fprintf(b, "| %s | | not measured | | |\n", r.Engine)
			continue
		}
		fmt.Fprintf(b, "| %s | %d | %s | %s | %s |\n",
			r.Engine, c.Workers, count(int(c.QueriesPerSecond())), ms(c.MedianMS), ms(c.P99MS))
	}
	b.WriteString("\n")
}

func writeUpdates(b *strings.Builder, rs []Result) {
	measured := false
	for _, r := range rs {
		if r.Update != nil {
			measured = true
		}
	}
	if !measured {
		return
	}
	fmt.Fprintf(b, "## Incremental update\n\n")
	b.WriteString("| engine | documents | wall | docs/s | index after | growth |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		// Not supported and not reached are different things, and an engine
		// whose query process was killed has said nothing about either.
		if !r.searched() {
			continue
		}
		u := r.Update
		if u == nil {
			fmt.Fprintf(b, "| %s | | not supported | | | |\n", r.Engine)
			continue
		}
		rate := 0.0
		if u.Usage.WallSeconds > 0 {
			rate = float64(u.Documents) / u.Usage.WallSeconds
		}
		growth := 0.0
		if r.Index.Bytes > 0 {
			growth = float64(u.IndexBytesAfter-r.Index.Bytes) / float64(r.Index.Bytes) * 100
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %+.1f%% |\n",
			r.Engine, count(u.Documents), secs(u.Usage.WallSeconds),
			count(int(rate)), mb(u.IndexBytesAfter), growth)
	}
	b.WriteString("\nGrowth is what rewriting documents the index already had cost in space.\n")
	b.WriteString("An engine that never reclaims the old copies grows by roughly the size of the update.\n\n")
}

func writePerQuery(b *strings.Builder, rs []Result) {
	var rows []string
	for _, q := range queryOrder(rs) {
		for _, r := range rs {
			for _, s := range r.Search.Queries {
				if s.Query != q {
					continue
				}
				rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s | %s |",
					q, r.Engine, count(s.Hits), ms(s.MedianMS), ms(s.P99MS)))
			}
		}
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "## Per query\n\n")
	b.WriteString("| query | engine | hits | median | p99 |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "%s\n", strings.Join(rows, "\n"))
	b.WriteString("\nTwo engines that disagree about the hit count are not answering the same question, and their latencies are not comparable.\n\n")
}

func writeNotes(b *strings.Builder, rs []Result) {
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

// queryOrder is the query set in the order the first result reports it, which
// is the order of the query file.
func queryOrder(rs []Result) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rs {
		for _, q := range r.Search.Queries {
			if !seen[q.Query] {
				seen[q.Query] = true
				out = append(out, q.Query)
			}
		}
	}
	return out
}

// aggregate is the p90 and p99 across every query, taken over the per query
// figures rather than over the raw runs, because the raw runs are not carried
// in the result.
func aggregate(r Result) (p90, p99 float64) {
	if len(r.Search.Queries) == 0 {
		return 0, 0
	}
	a := make([]float64, 0, len(r.Search.Queries))
	c := make([]float64, 0, len(r.Search.Queries))
	for _, q := range r.Search.Queries {
		a = append(a, q.P90MS)
		c = append(c, q.P99MS)
	}
	sort.Float64s(a)
	sort.Float64s(c)
	return Percentile(a, 0.90), Percentile(c, 0.99)
}

func mb(n int64) string {
	switch {
	case n == 0:
		return "none"
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
}

func secs(s float64) string {
	switch {
	case s == 0:
		return "not measured"
	case s < 1:
		return fmt.Sprintf("%.0f ms", s*1000)
	case s < 60:
		return fmt.Sprintf("%.1f s", s)
	default:
		return fmt.Sprintf("%dm%02ds", int(s)/60, int(s)%60)
	}
}

func ms(v float64) string {
	switch {
	case v == 0:
		return "not measured"
	case v < 10:
		return fmt.Sprintf("%.2f ms", v)
	case v < 1000:
		return fmt.Sprintf("%.0f ms", v)
	default:
		return fmt.Sprintf("%.1f s", v/1000)
	}
}

// count groups thousands, because a six figure document count is unreadable
// without it and every table here has one.
func count(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
