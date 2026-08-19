package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/kura-bench/bench"
)

// The defect this command exists for: a rerun of one engine writes a report
// with one row in it and the rivals disappear from the document even though
// their results are still sitting next to it.
func TestARerunOfOneEngineDoesNotLoseTheOthers(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "tantivy-srv.json", bench.Result{
		Engine: "tantivy", Version: "0.26.1", Machine: bench.Machine{Host: "srv"},
	})
	writeResult(t, dir, "genba-srv.json", bench.Result{
		Engine: "genba", Version: "v0.0.1", Machine: bench.Machine{Host: "srv"},
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}

	body := readReport(t, filepath.Join(dir, "report-srv.md"))
	for _, want := range []string{"tantivy", "genba"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report does not mention %s", want)
		}
	}
}

// Two machines are two reports, because a table that mixed them would be
// comparing engines across different hardware and would say nothing.
func TestOneReportPerMachine(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "tantivy-one.json", bench.Result{
		Engine: "tantivy", Machine: bench.Machine{Host: "one"},
	})
	writeResult(t, dir, "tantivy-two.json", bench.Result{
		Engine: "tantivy", Machine: bench.Machine{Host: "two"},
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report-one.md", "report-two.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
}

// The three suites write three differently shaped files into the same
// directory, and each one has to land in its own report.
func TestEachSuiteGetsItsOwnReport(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "tantivy-srv.json", bench.Result{
		Engine: "tantivy", Machine: bench.Machine{Host: "srv"},
	})
	writeResult(t, dir, "vec-exact-sift-euclidean-srv.json", bench.VectorResult{
		Engine:  "exact",
		Machine: bench.Machine{Host: "srv"},
		Dataset: bench.DatasetStats{Name: "sift", Metric: "euclidean"},
	})
	writeResult(t, dir, "graph-csr-ca-grqc-srv.json", bench.GraphResult{
		Engine:  "csr",
		Machine: bench.Machine{Host: "srv"},
		Dataset: bench.GraphStats{Name: "ca-grqc"},
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"report-srv.md",
		"vector-report-sift-euclidean-srv.md",
		"graph-report-ca-grqc-srv.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
}

// The defect this was written for: the vector suite writes vec-<engine>-... and
// this command looked for vector-<engine>-..., so every vector result fell
// through to the text bucket, decoded into a text result with every number
// zero, and was published as an engine that indexed nothing.
func TestAVectorResultDoesNotEndUpInTheTextReport(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "tantivy-srv.json", bench.Result{
		Engine: "tantivy", Machine: bench.Machine{Host: "srv"},
	})
	writeResult(t, dir, "vec-turbovec-sift-inner-product-srv.json", bench.VectorResult{
		Engine:  "turbovec",
		Machine: bench.Machine{Host: "srv"},
		Dataset: bench.DatasetStats{Name: "sift", Metric: "inner-product"},
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if body := readReport(t, filepath.Join(dir, "report-srv.md")); strings.Contains(body, "turbovec") {
		t.Errorf("a vector engine is in the text report:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "vector-report-sift-inner-product-srv.md")); err != nil {
		t.Errorf("the vector report was not written: %v", err)
	}
}

// Two metrics over one dataset are two runs measured against two different
// answers, and a single report holding both would be comparing recalls that
// were never scored against the same neighbours.
func TestTwoMetricsAreTwoVectorReports(t *testing.T) {
	dir := t.TempDir()
	for _, m := range []string{"euclidean", "cosine"} {
		writeResult(t, dir, "vec-exact-sift-"+m+"-srv.json", bench.VectorResult{
			Engine:  "exact",
			Machine: bench.Machine{Host: "srv"},
			Dataset: bench.DatasetStats{Name: "sift", Metric: m},
		})
	}

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"euclidean", "cosine"} {
		if _, err := os.Stat(filepath.Join(dir, "vector-report-sift-"+m+"-srv.md")); err != nil {
			t.Errorf("the %s report was not written: %v", m, err)
		}
	}
}

// Rebuilding twice from the same files has to produce the same bytes, or every
// rerun shows up as a diff in a committed report and nobody can see which
// number actually moved.
func TestItIsStableAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	// Written in an order that is not the order the report puts them in, so a
	// report that followed the directory listing would fail here.
	writeResult(t, dir, "zzz-srv.json", bench.Result{Engine: "zzz", Machine: bench.Machine{Host: "srv"}})
	writeResult(t, dir, "aaa-srv.json", bench.Result{Engine: "aaa", Machine: bench.Machine{Host: "srv"}})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	first := readReport(t, filepath.Join(dir, "report-srv.md"))
	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if second := readReport(t, filepath.Join(dir, "report-srv.md")); second != first {
		t.Fatal("two rebuilds from the same results produced different reports")
	}
	if strings.Index(first, "aaa") > strings.Index(first, "zzz") {
		t.Fatal("the engines are not in a sorted order")
	}
}

// The description of the graph and the counts the run worked to are not in a
// result file, and a rebuilt report that dropped them would be a table with
// nothing above it saying what was measured.
func TestARebuiltGraphReportStillSaysWhatTheGraphIs(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "graph-csr-ca-grqc-srv.json", bench.GraphResult{
		Engine:  "csr",
		Machine: bench.Machine{Host: "srv"},
		Dataset: bench.GraphStats{Name: "ca-grqc"},
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	body := readReport(t, filepath.Join(dir, "graph-report-ca-grqc-srv.md"))
	for _, want := range []string{"collaboration network", "The plan is"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report does not mention %q", want)
		}
	}
}

// A graph run outside the published set is a real thing the orchestrator can
// do, and a rebuild of it must not attach another graph's description to it.
func TestAGraphNobodyPublishedGetsNoDescription(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "graph-csr-mine-srv.json", bench.GraphResult{
		Engine:  "csr",
		Machine: bench.Machine{Host: "srv"},
		Dataset: bench.GraphStats{Name: "mine"},
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	body := readReport(t, filepath.Join(dir, "graph-report-mine-srv.md"))
	if strings.Contains(body, "The plan is") {
		t.Error("the report states a plan it was never told")
	}
}

// A run where every engine was cut off before it timed a query has no run
// count, and a header that said zero would be stating a figure nobody measured.
func TestARebuiltReportDoesNotInventARunCount(t *testing.T) {
	dir := t.TempDir()
	writeResult(t, dir, "sqlitefts-srv.json", bench.Result{
		Engine:     "sqlitefts",
		Machine:    bench.Machine{Host: "srv"},
		Incomplete: "the query phase did not finish within 45m0s",
	})

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	body := readReport(t, filepath.Join(dir, "report-srv.md"))
	if strings.Contains(body, "timed runs per query") {
		t.Errorf("the report states a run count nobody measured:\n%s", body)
	}
	if !strings.Contains(body, "the query phase did not finish within 45m0s") {
		t.Errorf("the report does not say why the engine has no numbers:\n%s", body)
	}
}

func TestAnEmptyDirectorySaysSo(t *testing.T) {
	if err := run(t.TempDir()); err == nil {
		t.Fatal("an empty directory came back without an error")
	}
}

func writeResult(t *testing.T, dir, name string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readReport(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
