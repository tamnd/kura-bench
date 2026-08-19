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
