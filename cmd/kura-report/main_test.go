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
	writeResult(t, dir, "vector-exact-sift-srv.json", bench.VectorResult{
		Engine:  "exact",
		Machine: bench.Machine{Host: "srv"},
		Dataset: bench.DatasetStats{Name: "sift"},
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
		"vector-report-sift-srv.md",
		"graph-report-ca-grqc-srv.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
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
