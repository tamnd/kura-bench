// Command kura-relevance scores the results in a directory against judgments.
//
// The rest of this suite measures how fast an engine answered. This measures
// whether it answered. Both matter and neither substitutes for the other: an
// engine that returns the wrong ten documents in a tenth of the time has not
// won anything, and until the runners started reporting the page they returned
// there was no way to see that from the outside.
//
// It only works on a corpus that came with judgments, which today means the
// passage collection. Running it against results from the source checkouts
// prints nothing, because nobody has judged those queries and inventing
// judgments to have a number would be worse than having none.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamnd/kura-bench/bench"
	"github.com/tamnd/kura-bench/relevance"
)

func main() {
	results := flag.String("results", "results", "directory of result files")
	qrels := flag.String("qrels", "qrels.dev.small.tsv", "judgments in the standard format")
	queries := flag.String("queries", "queries.dev.small.tsv", "the query log the judgments refer to, so query text maps back to a query id")
	runs := flag.String("runs", "", "directory to write a run file per engine, for checking these numbers with another tool")
	depth := flag.Int("k", 10, "the depth every metric is computed at")
	out := flag.String("out", "", "file to write the scores to as JSON, which is what a baseline is made of")
	baseline := flag.String("baseline", "", "a scores file from an earlier run to check this one against, failing if nDCG dropped by more than the tolerance recorded in it")
	flag.Parse()

	cfg := config{
		results:  *results,
		qrels:    *qrels,
		queries:  *queries,
		runsDir:  *runs,
		depth:    *depth,
		out:      *out,
		baseline: *baseline,
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "kura-relevance:", err)
		os.Exit(1)
	}
}

type config struct {
	results  string
	qrels    string
	queries  string
	runsDir  string
	depth    int
	out      string
	baseline string
}

func run(cfg config) error {
	results, qrelsPath, queriesPath, runsDir, depth := cfg.results, cfg.qrels, cfg.queries, cfg.runsDir, cfg.depth
	judgments, err := relevance.ReadQrelsFile(qrelsPath)
	if err != nil {
		return err
	}
	byText, collisions, err := relevance.ReadQueriesFile(queriesPath)
	if err != nil {
		return err
	}
	if collisions > 0 {
		fmt.Fprintf(os.Stderr, "%d queries in the log are repeats of an earlier one and were mapped to the first\n", collisions)
	}

	files, err := filepath.Glob(filepath.Join(results, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(files)

	type row struct {
		engine string
		scores relevance.Scores
	}
	var rows []row
	var shallow []string
	for _, path := range files {
		res, err := read(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		queries := answered(res, byText)
		if len(queries) == 0 {
			continue
		}
		// Scoring at a depth deeper than the engine was allowed to return is
		// the way to produce a recall figure that is really a measurement of
		// the benchmark's own page size. Every engine loses the same way, so the
		// table still ranks them correctly and the absolute number is nonsense,
		// which is the worst of both.
		if got := res.Search.Depth; got > 0 && got < depth {
			shallow = append(shallow, fmt.Sprintf("%s at %d", res.Engine, got))
		}
		if runsDir != "" {
			if err := writeRun(runsDir, res.Engine, queries); err != nil {
				return err
			}
		}
		rows = append(rows, row{res.Engine, relevance.Score(queries, judgments, depth)})
	}
	if len(shallow) > 0 {
		fmt.Fprintf(os.Stderr,
			"scoring at %d, but these engines were only asked for a shorter page: %s\n"+
				"rerun the query phase with -depth %d, because recall at %d cannot be computed from a page that never held that many documents\n\n",
			depth, strings.Join(shallow, ", "), depth, depth)
	}

	if len(rows) == 0 {
		return fmt.Errorf("nothing in %s carries the pages an engine returned, so there is nothing to score", results)
	}

	fmt.Printf("%-14s %8s %8s %8s %8s %8s %9s %9s\n",
		"engine", "nDCG@"+itoa(depth), "MRR@"+itoa(depth), "recall", "succ@1", "judged", "queries", "unjudged")
	for _, r := range rows {
		fmt.Printf("%-14s %8.4f %8.4f %8.4f %8.4f %7.1f%% %9d %9d\n",
			r.engine, r.scores.NDCG, r.scores.MRR, r.scores.Recall, r.scores.Success,
			r.scores.Coverage*100, r.scores.Queries, r.scores.Unjudged)
	}

	fmt.Println()
	if depth == bench.DefaultDepth {
		fmt.Printf("Recall is at %d and not at the hundred a paper would quote, because that is\n", depth)
		fmt.Println("the size of the page the runners return by default. The two are not")
		fmt.Println("comparable. Rerun the query phase with -depth 100 for the deeper number.")
	} else {
		fmt.Printf("Recall is at %d, which is the depth the runners were asked for.\n", depth)
	}
	fmt.Println("Success at 1 is the share of queries whose first result was relevant, which is")
	fmt.Println("what somebody looking for a document they already know about experiences.")
	fmt.Println("Judged is the share of returned documents anybody looked at. A low number")
	fmt.Println("means the scores are mostly measuring how much this engine agrees with the")
	fmt.Println("systems that were in the pool when the judgments were made.")

	report := relevance.Report{Qrels: qrelsPath, Queries: queriesPath}
	for _, r := range rows {
		report.Add(r.engine, r.scores)
	}
	if cfg.out != "" {
		if err := report.WriteFile(cfg.out); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\nwrote %s\n", cfg.out)
	}
	if cfg.baseline != "" {
		return compare(cfg.baseline, report)
	}
	return nil
}

// compare is the gate. It returns an error when a score fell further than the
// baseline says these numbers move on their own, which is what makes the exit
// status usable from CI.
func compare(path string, now relevance.Report) error {
	was, err := relevance.ReadReport(path)
	if err != nil {
		return err
	}
	changes, err := relevance.Compare(was, now)
	if err != nil {
		return err
	}

	fmt.Printf("\nagainst %s, tolerance %.4f\n", path, *was.Tolerance)
	bad := 0
	for _, c := range changes {
		switch {
		case c.Missing:
			fmt.Printf("%-14s %8.4f       -- not measured in this run\n", c.Engine, c.Baseline)
		case c.Regressed:
			bad++
			fmt.Printf("%-14s %8.4f -> %8.4f  %+.4f  regressed\n", c.Engine, c.Baseline, c.Now, c.Now-c.Baseline)
		default:
			fmt.Printf("%-14s %8.4f -> %8.4f  %+.4f\n", c.Engine, c.Baseline, c.Now, c.Now-c.Baseline)
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d engines scored worse than the baseline by more than %.4f", bad, *was.Tolerance)
	}
	return nil
}

// answered turns a result file into the queries the scorer wants, dropping the
// ones whose text is not in the log. A result run against a different query set
// contributes nothing rather than being scored against the wrong judgments.
func answered(res bench.Result, byText map[string]string) []relevance.Query {
	out := make([]relevance.Query, 0, len(res.Search.Queries))
	for _, q := range res.Search.Queries {
		if len(q.IDs) == 0 {
			continue
		}
		id, ok := byText[strings.TrimSpace(q.Query)]
		if !ok {
			continue
		}
		out = append(out, relevance.Query{ID: id, Text: q.Query, IDs: q.IDs})
	}
	return out
}

func writeRun(dir, engine string, queries []relevance.Query) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, engine+".run"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := relevance.WriteRun(f, engine, queries); err != nil {
		return err
	}
	return f.Close()
}

func read(path string) (bench.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return bench.Result{}, err
	}
	defer func() { _ = f.Close() }()
	var res bench.Result
	err = json.NewDecoder(f).Decode(&res)
	return res, err
}

func itoa(n int) string { return fmt.Sprint(n) }
