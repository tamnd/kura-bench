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
	if query.Notes != "" {
		out.Notes = strings.TrimSpace(out.Notes + " " + query.Notes)
	}
	return out
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

	var b strings.Builder
	writeMachine(&b, sorted[0].Machine)
	writeCorpus(&b, sorted[0].Corpus)
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
	if m.Dedicated {
		fmt.Fprintf(b, "Load before the run was %.2f and %s was free, so the machine was idle.\n",
			m.LoadBefore, mb(m.MemoryFreeBytes))
	} else {
		fmt.Fprintf(b, "Load before the run was %.2f and %s was free, so the machine was doing other work and these numbers are a floor rather than a measurement.\n",
			m.LoadBefore, mb(m.MemoryFreeBytes))
	}
	if m.DedicatedComment != "" {
		fmt.Fprintf(b, "%s\n", m.DedicatedComment)
	}
	b.WriteString("\n")
}

func writeCorpus(b *strings.Builder, c CorpusStats) {
	fmt.Fprintf(b, "## Corpus\n\n")
	fmt.Fprintf(b, "%s documents, %s of text.\n\n", count(c.Documents), mb(c.Bytes))
}

func writeIndexing(b *strings.Builder, rs []Result) {
	fmt.Fprintf(b, "## Indexing\n\n")
	b.WriteString("| engine | version | wall | docs/s | MB/s | CPU s | parallelism | peak RSS |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		u := r.Index.Usage
		fmt.Fprintf(b, "| %s | %s | %s | %s | %.1f | %.1f | %.1fx | %s |\n",
			r.Engine, r.Version, secs(u.WallSeconds), count(int(r.DocsPerSecond())),
			r.MBPerSecond(), u.CPUSeconds(), u.Parallelism(), mb(u.PeakRSSBytes))
	}
	b.WriteString("\n")
}

func writeStorage(b *strings.Builder, rs []Result) {
	fmt.Fprintf(b, "## Storage\n\n")
	b.WriteString("| engine | index on disk | files | index over corpus | bytes written |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		fmt.Fprintf(b, "| %s | %s | %s | %.2fx | %s |\n",
			r.Engine, mb(r.Index.Bytes), count(r.Index.Files),
			r.IndexRatio(), mb(r.Index.Usage.WriteBytes))
	}
	b.WriteString("\nIndex over corpus below one means the engine does not keep the document text.\n")
	b.WriteString("Bytes written is what the process asked the kernel to write, which on some platforms counts every handle and not only files.\n\n")
}

func writeColdStart(b *strings.Builder, rs []Result) {
	fmt.Fprintf(b, "## Cold start\n\n")
	b.WriteString("| engine | open and first query | CPU s | resident after open |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, r := range rs {
		fmt.Fprintf(b, "| %s | %s | %.2f | %s |\n",
			r.Engine, secs(r.Open.Usage.WallSeconds),
			r.Open.Usage.CPUSeconds(), mb(r.Open.ResidentBytes))
	}
	b.WriteString("\nThis is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.\n\n")
}

func writeSearch(b *strings.Builder, rs []Result) {
	fmt.Fprintf(b, "## Search, one query at a time\n\n")
	b.WriteString("| engine | median | p90 | p99 | CPU ms per query | peak RSS |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
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
	fmt.Fprintf(b, "## Per query\n\n")
	b.WriteString("| query | engine | hits | median | p99 |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, q := range queryOrder(rs) {
		for _, r := range rs {
			for _, s := range r.Search.Queries {
				if s.Query != q {
					continue
				}
				fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n",
					q, r.Engine, count(s.Hits), ms(s.MedianMS), ms(s.P99MS))
			}
		}
	}
	b.WriteString("\nTwo engines that disagree about the hit count are not answering the same question, and their latencies are not comparable.\n\n")
}

func writeNotes(b *strings.Builder, rs []Result) {
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
