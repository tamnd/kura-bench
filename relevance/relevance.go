// Package relevance scores what an engine returned against human judgments.
//
// A latency number says how fast an engine answered. It does not say whether it
// answered. An engine that returns the wrong ten documents quickly is not a
// faster search engine, it is a slower way of getting nothing, and until
// something here computes a relevance score there is no way to tell those two
// apart from the outside.
//
// The judgments come from the corpus that ships with them. The passage
// collection carries a real query log and a qrels file next to it, so the
// queries are ones people typed and the judgments are ones people made. Nothing
// here invents either.
//
// The metrics are the standard ones and they are computed here rather than
// taken from a library so that the definitions are in front of a reader.
// Everyone who has written their own nDCG has got it wrong at least once, so
// there is a test that checks these against numbers worked out by hand, and
// another that checks them against trec_eval, which is the program every
// published retrieval score is produced by. The definitions here are the ones
// trec_eval uses, not the ones that are merely defensible, because a score that
// cannot be lined up against a published one is a score that only compares
// against itself. The scorer also writes a run file in the format every
// evaluation tool reads, so anybody who doubts the arithmetic can check it with
// a different program.
package relevance

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Qrels is the judgments: for each query, which documents were judged and how
// relevant each one was.
//
// A document that is not in the map for a query was not judged, which is not
// the same as being judged irrelevant. Most judged sets are pooled from the
// systems that existed when they were made, so an engine that finds something
// new is marked wrong for finding it. That is a known flaw in every benchmark
// of this kind and the reason [Score] reports judged coverage next to the
// scores rather than only the scores.
type Qrels map[string]map[string]int

// ReadQrels reads the TREC format that every judged set here either ships in or
// converts to: a query id, a field nobody uses, a document id and a grade,
// separated by whitespace or by tabs.
func ReadQrels(r io.Reader) (Qrels, error) {
	out := Qrels{}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for line := 1; s.Scan(); line++ {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			continue
		}
		f := strings.Fields(text)
		if len(f) < 4 {
			return nil, fmt.Errorf("line %d has %d fields, want a query, an iteration, a document and a grade", line, len(f))
		}
		grade, err := strconv.Atoi(f[3])
		if err != nil {
			return nil, fmt.Errorf("line %d: the grade %q is not a number", line, f[3])
		}
		// A zero grade is a judgment that the document is not relevant, and it
		// is kept, because judged coverage needs to know it was looked at.
		if out[f[0]] == nil {
			out[f[0]] = map[string]int{}
		}
		out[f[0]][f[2]] = grade
	}
	return out, s.Err()
}

// ReadQrelsFile is [ReadQrels] on a path.
func ReadQrelsFile(path string) (Qrels, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadQrels(f)
}

// Relevant is how many documents were judged relevant for a query, which is the
// denominator of recall.
func (q Qrels) Relevant(query string) int {
	n := 0
	for _, grade := range q[query] {
		if grade > 0 {
			n++
		}
	}
	return n
}

// ReadQueries reads a query log of the form used by the passage collection: a
// query id, a tab, and the query as somebody typed it. It returns the map from
// the text back to the id, because a result file records what was searched for
// and not which line of the log it came from.
//
// Two identical queries with different ids collapse to the first, which is the
// only choice that keeps the mapping a function. It happens rarely and it is
// worth knowing about, so the count of collisions comes back too.
func ReadQueries(r io.Reader) (map[string]string, int, error) {
	out := map[string]string{}
	collisions := 0
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for line := 1; s.Scan(); line++ {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			continue
		}
		id, query, ok := strings.Cut(text, "\t")
		if !ok {
			return nil, 0, fmt.Errorf("line %d has no tab in it, so there is no query id to attach a judgment to", line)
		}
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		if _, seen := out[query]; seen {
			collisions++
			continue
		}
		out[query] = id
	}
	return out, collisions, s.Err()
}

// ReadQueriesFile is [ReadQueries] on a path.
func ReadQueriesFile(path string) (map[string]string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	return ReadQueries(f)
}

// Scores is what one engine achieved over a query set.
type Scores struct {
	// Queries is how many of the queries in the result had judgments to be
	// scored against. Queries without any are left out entirely rather than
	// counted as zero, because a query nobody judged says nothing about the
	// engine.
	Queries int

	// Unjudged is how many were left out for that reason.
	Unjudged int

	// K is the depth every metric here was computed at, which is the size of
	// the page the runners return. Recall at a depth this shallow is a
	// different and much harder measurement than the recall at a hundred that
	// papers quote, and the two should never be compared.
	K int

	NDCG   float64
	MRR    float64
	Recall float64

	// Coverage is the share of returned documents that were judged either way.
	// A low number means the score is mostly measuring whether this engine
	// happens to return what the systems in the judging pool returned, and it
	// should be quoted next to the scores rather than left out of them.
	Coverage float64
}

// Query is one query as an engine answered it.
type Query struct {
	// ID is the query id in the judgments, and Text is what was searched for.
	ID   string
	Text string

	// IDs is the page the engine returned, in the order it returned it.
	IDs []string
}

// Score computes the metrics over a set of answered queries.
//
// The depth is taken from the shortest page rather than fixed, because scoring
// at a depth deeper than an engine was asked for silently penalises it for
// documents it was never given room to return.
func Score(queries []Query, qrels Qrels, k int) Scores {
	out := Scores{K: k}
	var judged, returned int
	for _, q := range queries {
		grades := qrels[q.ID]
		if len(grades) == 0 {
			out.Unjudged++
			continue
		}
		out.Queries++

		page := q.IDs
		if len(page) > k {
			page = page[:k]
		}
		returned += len(page)

		var dcg float64
		hit := 0
		for i, id := range page {
			grade, ok := grades[id]
			if ok {
				judged++
			}
			if grade <= 0 {
				continue
			}
			hit++
			// The gain is the grade itself and the discount is the logarithm of
			// the rank, which reduces to the binary case when every grade is
			// one, so the same code scores both kinds of judgment.
			//
			// The other definition in circulation raises two to the grade and
			// subtracts one, which weighs a grade of three more than three times
			// as heavily as a grade of one. It is not wrong, it is a different
			// question, and it is not the one anybody publishes a number for.
			// trec_eval uses this one, every score quoted for BEIR and the deep
			// learning tracks comes out of trec_eval, and a number of ours that
			// cannot be lined up against those is a number that only compares
			// against itself.
			dcg += float64(grade) / math.Log2(float64(i+2))
		}
		if ideal := idealDCG(grades, k); ideal > 0 {
			out.NDCG += dcg / ideal
		}
		out.MRR += reciprocalRank(page, grades)
		if total := countRelevant(grades); total > 0 {
			out.Recall += float64(hit) / float64(total)
		}
	}

	if out.Queries > 0 {
		out.NDCG /= float64(out.Queries)
		out.MRR /= float64(out.Queries)
		out.Recall /= float64(out.Queries)
	}
	if returned > 0 {
		out.Coverage = float64(judged) / float64(returned)
	}
	return out
}

// reciprocalRank is one over the position of the first relevant document, and
// zero when there is none in the page. For a query where the user already knows
// which document they want, this is the whole of their experience.
func reciprocalRank(page []string, grades map[string]int) float64 {
	for i, id := range page {
		if grades[id] > 0 {
			return 1 / float64(i+1)
		}
	}
	return 0
}

func countRelevant(grades map[string]int) int {
	n := 0
	for _, g := range grades {
		if g > 0 {
			n++
		}
	}
	return n
}

// idealDCG is the discounted gain of the best ranking the judgments allow,
// which is the denominator that turns a gain into a score between zero and one.
func idealDCG(grades map[string]int, k int) float64 {
	best := make([]int, 0, len(grades))
	for _, g := range grades {
		if g > 0 {
			best = append(best, g)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(best)))
	if len(best) > k {
		best = best[:k]
	}
	var out float64
	for i, g := range best {
		out += float64(g) / math.Log2(float64(i+2))
	}
	return out
}

// WriteRun writes the run in the TREC format, which is what makes these numbers
// checkable by somebody who does not trust this file. Every evaluation tool in
// the field reads it.
func WriteRun(w io.Writer, engine string, queries []Query) error {
	b := bufio.NewWriter(w)
	for _, q := range queries {
		for i, id := range q.IDs {
			// The score column is synthetic. The runners report the order they
			// returned documents in and not the scores behind it, and every
			// metric here depends on the order alone. A descending sequence
			// preserves that order in any tool that re-sorts by score.
			if _, err := fmt.Fprintf(b, "%s Q0 %s %d %d %s\n",
				q.ID, id, i+1, len(q.IDs)-i, engine); err != nil {
				return err
			}
		}
	}
	return b.Flush()
}
