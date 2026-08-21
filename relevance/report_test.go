package relevance

import (
	"path/filepath"
	"strings"
	"testing"
)

func tolerance(v float64) *float64 { return &v }

func TestAReportRoundTripsThroughItsOwnFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scores.json")
	want := Report{
		K:         10,
		Qrels:     "qrels.dev.small.tsv",
		Queries:   "queries.dev.small.tsv",
		Tolerance: tolerance(0.002),
		Engines: []EngineScores{
			{Engine: "kura", NDCG: 0.31, MRR: 0.29, Recall: 0.55, Success: 0.2, Coverage: 0.4, Queries: 6980},
		},
	}
	if err := want.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.K != want.K || got.Qrels != want.Qrels || len(got.Engines) != 1 {
		t.Fatalf("read back %+v", got)
	}
	if got.Tolerance == nil || *got.Tolerance != 0.002 {
		t.Fatalf("the tolerance came back as %v", got.Tolerance)
	}
	if got.Engines[0] != want.Engines[0] {
		t.Fatalf("the engine row came back as %+v", got.Engines[0])
	}
}

// A baseline with no tolerance in it is the case worth being strict about. The
// tempting thing is to treat a missing tolerance as zero, which turns the gate
// into a demand for bit identical scores and gets it switched off within a week.
func TestABaselineWithNoToleranceIsRefused(t *testing.T) {
	base := Report{K: 10, Qrels: "q", Engines: []EngineScores{{Engine: "kura", NDCG: 0.3}}}
	_, err := Compare(base, Report{K: 10, Qrels: "q"})
	if err == nil {
		t.Fatal("a baseline with no measured tolerance was accepted")
	}
	if !strings.Contains(err.Error(), "tolerance") {
		t.Fatalf("the error does not say what is missing: %v", err)
	}
}

func TestADropLargerThanTheToleranceIsARegression(t *testing.T) {
	base := Report{
		K: 10, Qrels: "q", Tolerance: tolerance(0.005),
		Engines: []EngineScores{
			{Engine: "kura", NDCG: 0.3000},
			{Engine: "tantivy", NDCG: 0.3000},
			{Engine: "lucene", NDCG: 0.3000},
		},
	}
	now := Report{
		K: 10, Qrels: "q",
		Engines: []EngineScores{
			{Engine: "kura", NDCG: 0.2949},    // down 0.0051, past the tolerance
			{Engine: "tantivy", NDCG: 0.2960}, // down 0.0040, within it
			{Engine: "lucene", NDCG: 0.3400},  // up, which is never a regression
		},
	}

	changes, err := Compare(base, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range changes {
		got[c.Engine] = c.Regressed
	}
	if !got["kura"] {
		t.Error("a drop past the tolerance was not called a regression")
	}
	if got["tantivy"] {
		t.Error("a drop inside the tolerance was called a regression, which is what makes a gate get ignored")
	}
	if got["lucene"] {
		t.Error("an improvement was called a regression")
	}
}

// An engine that stops being built stops being measured, and without this the
// only sign of it is a row quietly missing from a table nobody reads closely.
func TestAnEngineMissingFromTheRunIsReportedRatherThanIgnored(t *testing.T) {
	base := Report{
		K: 10, Qrels: "q", Tolerance: tolerance(0.005),
		Engines: []EngineScores{{Engine: "lucene", NDCG: 0.3}},
	}
	changes, err := Compare(base, Report{K: 10, Qrels: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].Missing {
		t.Fatalf("the missing engine came back as %+v", changes)
	}
	if changes[0].Regressed {
		t.Error("an engine that did not run was called a regression, and the usual cause is a machine without its toolchain")
	}
}

// Two reports that are not about the same thing produce a comparison that looks
// perfectly reasonable and means nothing, so the arithmetic never happens.
func TestReportsThatAreNotComparableAreRefused(t *testing.T) {
	base := Report{K: 10, Qrels: "trec-dl-2019", Tolerance: tolerance(0.005)}

	if _, err := Compare(base, Report{K: 100, Qrels: "trec-dl-2019"}); err == nil {
		t.Error("a baseline at depth 10 was compared against a run at depth 100")
	}
	if _, err := Compare(base, Report{K: 10, Qrels: "msmarco-dev"}); err == nil {
		t.Error("a baseline was compared against a run scored on a different judged set")
	}
}
