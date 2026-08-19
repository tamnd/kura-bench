package bench

import (
	"strings"
	"testing"
)

func TestTheHeaderSaysWhatWasRunAndHowMuchOfIt(t *testing.T) {
	got := GraphHeader("box", "The Google web graph", GraphPlan{
		Neighbour: 1000, TwoHop: 100, Path: 100, BFS: 10, Iterations: 20, Damping: 0.85,
	})
	for _, want := range []string{
		"# Graph results on box",
		"The Google web graph.",
		"1000 neighbour lookups",
		"pagerank over 20 iterations at damping 0.85",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to mention %q", got, want)
		}
	}
}

// A report rebuilt for a graph this build has never heard of still has to be a
// report, and it must not describe a plan nobody told it about.
func TestAHeaderWithNothingToSayIsJustTheTitle(t *testing.T) {
	got := GraphHeader("box", "", GraphPlan{})
	if got != "# Graph results on box\n\n" {
		t.Errorf("got %q, want the title on its own", got)
	}
}

// The defect this was written for: an engine that ran out of time was left out
// of the report, so a reader had no reason to think it had ever been asked.
func TestAnEngineThatRanOutOfTimeIsStillInTheReport(t *testing.T) {
	got := GraphReport([]GraphResult{
		finished("csr"),
		{
			Engine:     "sqlite",
			Dataset:    GraphStats{Name: "web-google"},
			Machine:    Machine{Host: "box"},
			Build:      GraphBuildPhase{Usage: Usage{WallSeconds: 30}, Bytes: 1 << 20, Files: 1},
			Incomplete: "the query phase did not finish within 45m0s",
		},
	}, []string{"neighbours"})

	for _, want := range []string{
		"| sqlite | ran out of time |",
		"- sqlite: the query phase did not finish within 45m0s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not contain %q:\n%s", want, got)
		}
	}
	// The load phase did finish, so it is a measurement and belongs in the
	// table it was measured for.
	if !strings.Contains(got, "| sqlite | 30.0 s |") {
		t.Errorf("the report dropped the load figures it did have:\n%s", got)
	}
}

// A store that never got as far as opening has no cold start, and a row of
// zeros would read as the fastest open in the table.
func TestAPhaseThatNeverRanIsNotARowOfZeros(t *testing.T) {
	got := GraphReport([]GraphResult{
		finished("csr"),
		{
			Engine:     "sqlite",
			Machine:    Machine{Host: "box"},
			Incomplete: "the build phase did not finish within 45m0s",
		},
	}, []string{"neighbours"})

	for _, table := range []string{"## Loading the graph", "## Storage", "## Cold start"} {
		if body := section(t, got, table); strings.Contains(body, "sqlite") {
			t.Errorf("%s has a row for a phase that never ran:\n%s", table, body)
		}
	}
}

// section is one part of a report, from its heading to the next one.
func section(t *testing.T, report, heading string) string {
	t.Helper()
	start := strings.Index(report, heading)
	if start < 0 {
		t.Fatalf("the report has no %s section:\n%s", heading, report)
	}
	body := report[start+len(heading):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}

// The engine that ran out of time may sort first, and the sections describing
// the run have to come from an engine that got far enough to describe it.
func TestTheGraphIsDescribedByAnEngineThatSawIt(t *testing.T) {
	got := GraphReport([]GraphResult{
		{
			Engine:     "aaa",
			Dataset:    GraphStats{Name: "web-google"},
			Machine:    Machine{Host: "box"},
			Incomplete: "the build phase did not finish within 45m0s",
		},
		finished("csr"),
	}, []string{"neighbours"})

	if !strings.Contains(got, "875,713 nodes") {
		t.Errorf("the report does not say what graph was run:\n%s", got)
	}
}

// finished is an engine that did everything it was asked.
func finished(name string) GraphResult {
	return GraphResult{
		Engine:  name,
		Dataset: GraphStats{Name: "web-google", Nodes: 875_713, Edges: 5_105_039, Seeds: 1000},
		Machine: Machine{Host: "box"},
		Build:   GraphBuildPhase{Usage: Usage{WallSeconds: 6}, Bytes: 29 << 20, Files: 1},
		Open:    OpenPhase{Usage: Usage{WallSeconds: 0.27}},
		Query: GraphQueryPhase{Ops: []OpStat{
			{Op: "neighbours", Runs: 1000, Correct: 1, MedianMS: 0.0013},
		}},
	}
}

// The two processes usually say the same thing, and the merged result should
// say it once.
func TestTheSameNoteTwiceIsOneNote(t *testing.T) {
	const note = "loaded with COPY from a CSV"
	got := MergeGraph(GraphResult{Notes: note}, GraphResult{Notes: note}).Notes
	if got != note {
		t.Errorf("got %q, want it said once", got)
	}
}

// The query process is the one that finds out whether a traversal was cut off,
// so its note starts with the build process's note and goes on. Joining them
// would repeat the first half.
func TestANoteThatGrewInTheQueryPhaseReplacesTheShorterOne(t *testing.T) {
	const build = "queried in Cypher"
	const query = build + ", and the traversals were cut off at the depth bound"
	if got := MergeGraph(GraphResult{Notes: build}, GraphResult{Notes: query}).Notes; got != query {
		t.Errorf("got %q, want the longer note", got)
	}
	// The other way round as well, because which process learned more is not
	// something this function should be assuming.
	if got := MergeGraph(GraphResult{Notes: query}, GraphResult{Notes: build}).Notes; got != query {
		t.Errorf("got %q, want the longer note", got)
	}
}

func TestTwoDifferentNotesAreBothKept(t *testing.T) {
	got := MergeGraph(GraphResult{Notes: "one"}, GraphResult{Notes: "two"}).Notes
	if got != "one; two" {
		t.Errorf("got %q, want both of them", got)
	}
}

func TestAnEmptyNoteOnEitherSideIsNotJoinedToAnything(t *testing.T) {
	if got := MergeGraph(GraphResult{Notes: "one"}, GraphResult{}).Notes; got != "one" {
		t.Errorf("got %q, want the one note that exists", got)
	}
	if got := MergeGraph(GraphResult{}, GraphResult{Notes: "two"}).Notes; got != "two" {
		t.Errorf("got %q, want the one note that exists", got)
	}
	if got := MergeGraph(GraphResult{}, GraphResult{}).Notes; got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}
