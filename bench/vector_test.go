package bench

import (
	"strings"
	"testing"
)

// The defect this was written for: an engine that ran out of time was left out
// of the report, so a reader had no reason to think it had ever been asked.
func TestAVectorEngineThatRanOutOfTimeIsStillInTheReport(t *testing.T) {
	got := VectorReport([]VectorResult{
		builtAndSearched("exact"),
		{
			Engine:     "hnsw",
			Version:    "0.11.0",
			Dataset:    DatasetStats{Name: "sift", Dim: 128, Vectors: 1_000_000},
			Machine:    Machine{Host: "box"},
			Build:      BuildPhase{Usage: Usage{WallSeconds: 900}, Bytes: 700 << 20, Files: 1},
			Incomplete: "the query phase did not finish within 45m0s",
		},
	})

	for _, want := range []string{
		"| hnsw | 0.11.0 | ran out of time |",
		"- hnsw: the query phase did not finish within 45m0s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not contain %q:\n%s", want, got)
		}
	}
	// The build finished, so it is a measurement and belongs in the tables it
	// was measured for.
	for _, table := range []string{"## Building the index", "## Storage"} {
		if body := section(t, got, table); !strings.Contains(body, "hnsw") {
			t.Errorf("%s dropped the figures the engine did have:\n%s", table, body)
		}
	}
}

// An engine that never finished building has no index, and a row of zeros would
// read as the smallest index in the table.
func TestAVectorPhaseThatNeverRanIsNotARowOfZeros(t *testing.T) {
	got := VectorReport([]VectorResult{
		builtAndSearched("exact"),
		{
			Engine:     "hnsw",
			Machine:    Machine{Host: "box"},
			Incomplete: "the build phase did not finish within 45m0s",
		},
	})

	for _, table := range []string{"## Building the index", "## Storage", "## Cold start"} {
		if body := section(t, got, table); strings.Contains(body, "hnsw") {
			t.Errorf("%s has a row for a phase that never ran:\n%s", table, body)
		}
	}
	// Not reached is a claim about a curve, and this engine never measured one.
	if body := section(t, got, "## Throughput at a fixed accuracy"); strings.Contains(body, "not reached") {
		t.Errorf("an engine that never searched is described as having missed a recall:\n%s", body)
	}
}

// An engine that refuses the metric a run picked has not failed and has not run
// out of time, and the report has to be able to tell the three apart. The defect
// this was written for: turbovec ranks by inner product only, and a Euclidean
// run dropped it, so the report read as though nobody had thought to measure it.
func TestAVectorEngineThatDeclinedTheMetricKeepsItsRow(t *testing.T) {
	got := VectorReport([]VectorResult{
		builtAndSearched("exact"),
		{
			Engine:   "turbovec",
			Version:  "1.0.0",
			Dataset:  DatasetStats{Name: "sift", Metric: "euclidean"},
			Machine:  Machine{Host: "box"},
			Declined: "this engine ranks by inner-product, and the run asked for euclidean",
		},
	})

	for _, want := range []string{
		"| turbovec | 1.0.0 | declined |",
		"- turbovec: this engine ranks by inner-product, and the run asked for euclidean",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "turbovec | 1.0.0 | ran out of time") {
		t.Errorf("an engine that refused the run is described as having been too slow:\n%s", got)
	}
	// It built nothing and opened nothing, so a row in either table would be a
	// row of zeros.
	for _, table := range []string{"## Building the index", "## Storage", "## Cold start"} {
		if body := section(t, got, table); strings.Contains(body, "turbovec") {
			t.Errorf("%s has a row for an engine that never ran:\n%s", table, body)
		}
	}
}

// The engine that ran out of time may sort first, and the section saying what
// was searched has to come from an engine that read it.
func TestTheDatasetIsDescribedByAnEngineThatSawIt(t *testing.T) {
	got := VectorReport([]VectorResult{
		{
			Engine:     "aaa",
			Machine:    Machine{Host: "box"},
			Incomplete: "the build phase did not finish within 45m0s",
		},
		builtAndSearched("exact"),
	})

	if !strings.Contains(got, "1,000,000 vectors of 128 components") {
		t.Errorf("the report does not say what dataset was run:\n%s", got)
	}
}

// builtAndSearched is an engine that did everything it was asked.
func builtAndSearched(name string) VectorResult {
	return VectorResult{
		Engine:  name,
		Version: "1.0.0",
		Dataset: DatasetStats{Name: "sift", Dim: 128, Vectors: 1_000_000, Queries: 1000, K: 10, Metric: "euclidean"},
		Machine: Machine{Host: "box"},
		Build:   BuildPhase{Usage: Usage{WallSeconds: 30}, Bytes: 488 << 20, Files: 1},
		Open:    OpenPhase{Usage: Usage{WallSeconds: 0.2}},
		Search: VectorSearchPhase{
			Points: []VectorPoint{{Recall: 1, Queries: 1000, MedianMS: 40}},
		},
	}
}
