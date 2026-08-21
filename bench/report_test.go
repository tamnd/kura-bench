package bench

import (
	"strings"
	"testing"
)

// The defect this was written for: an engine that ran out of time was left out
// of the report, so a reader had no reason to think it had ever been asked.
func TestAnEngineThatRanOutOfTimeIsStillInTheTextReport(t *testing.T) {
	got := Report([]Result{
		indexedAndSearched("bleve"),
		{
			Engine:     "sqlitefts",
			Corpus:     CorpusStats{Documents: 500_000, Bytes: 550 << 20},
			Machine:    Machine{Host: "box"},
			Index:      IndexPhase{Usage: Usage{WallSeconds: 147}, Bytes: 648 << 20, Files: 3},
			Incomplete: "the query phase did not finish within 45m0s",
		},
	})

	for _, want := range []string{
		"| sqlitefts | ran out of time |",
		"- sqlitefts: the query phase did not finish within 45m0s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not contain %q:\n%s", want, got)
		}
	}
	// The index phase did finish, so it is a measurement and belongs in the
	// tables it was measured for.
	for _, table := range []string{"## Indexing", "## Storage"} {
		if body := section(t, got, table); !strings.Contains(body, "sqlitefts") {
			t.Errorf("%s dropped the figures the engine did have:\n%s", table, body)
		}
	}
}

// An engine that never got as far as indexing has no index, and a row of zeros
// would read as the fastest indexing in the table.
func TestATextPhaseThatNeverRanIsNotARowOfZeros(t *testing.T) {
	got := Report([]Result{
		indexedAndSearched("bleve"),
		{
			Engine:     "sqlitefts",
			Machine:    Machine{Host: "box"},
			Incomplete: "the index phase did not finish within 45m0s",
		},
	})

	for _, table := range []string{"## Indexing", "## Storage", "## Cold start"} {
		if body := section(t, got, table); strings.Contains(body, "sqlitefts") {
			t.Errorf("%s has a row for a phase that never ran:\n%s", table, body)
		}
	}
}

// Not measured and not supported are things an engine said about itself, and an
// engine whose query process was killed said neither.
func TestATimedOutEngineIsNotCalledUnsupported(t *testing.T) {
	got := Report([]Result{
		indexedAndSearched("bleve"),
		{
			Engine:     "sqlitefts",
			Machine:    Machine{Host: "box"},
			Index:      IndexPhase{Usage: Usage{WallSeconds: 147}},
			Incomplete: "the query phase did not finish within 45m0s",
		},
	})

	for _, table := range []string{"## Search, several in flight", "## Incremental update"} {
		if body := section(t, got, table); strings.Contains(body, "sqlitefts") {
			t.Errorf("%s speaks for an engine that never got there:\n%s", table, body)
		}
	}
}

// The engine that ran out of time may sort first, and the section saying what
// was indexed has to come from an engine that indexed it.
func TestTheCorpusIsDescribedByAnEngineThatIndexedIt(t *testing.T) {
	got := Report([]Result{
		{
			Engine:     "aaa",
			Machine:    Machine{Host: "box"},
			Incomplete: "the index phase did not finish within 45m0s",
		},
		indexedAndSearched("bleve"),
	})

	if !strings.Contains(got, "500,000 documents") {
		t.Errorf("the report does not say what corpus was indexed:\n%s", got)
	}
}

// A runner that has a caveat writes it in both of its processes, and a report
// that printed it twice would read like two separate warnings.
func TestTheSameNoteFromBothPhasesIsSaidOnce(t *testing.T) {
	note := "measured with its segment library held ahead of the release it names"
	got := Merge(
		Result{Engine: "bleve", Notes: note},
		Result{Engine: "bleve", Notes: note},
	)
	if got.Notes != note {
		t.Fatalf("the merged note is %q, want it said once", got.Notes)
	}
}

// The second process can find out something the first could not, and that has to
// survive the merge rather than being taken for a repeat.
func TestANoteOnlyTheQueryPhaseCouldMakeSurvives(t *testing.T) {
	got := Merge(
		Result{Engine: "sqlitefts", Notes: "one segment"},
		Result{Engine: "sqlitefts", Notes: "the update phase was left out"},
	)
	for _, want := range []string{"one segment", "the update phase was left out"} {
		if !strings.Contains(got.Notes, want) {
			t.Errorf("the merged note %q lost %q", got.Notes, want)
		}
	}
}

// indexedAndSearched is an engine that did everything it was asked.
func indexedAndSearched(name string) Result {
	return Result{
		Engine:  name,
		Version: "1.0.0",
		Corpus:  CorpusStats{Documents: 500_000, Bytes: 550 << 20},
		Machine: Machine{Host: "box"},
		Index:   IndexPhase{Usage: Usage{WallSeconds: 120}, Bytes: 400 << 20, Files: 40},
		Open:    OpenPhase{Usage: Usage{WallSeconds: 0.1}},
		Search: SearchPhase{
			Queries:    []QueryStat{{Query: "kernel", Hits: 900, Runs: 20, MedianMS: 12}},
			Concurrent: &ConcurrentStat{Workers: 8, Queries: 5000, Seconds: 20, MedianMS: 30},
		},
		Update: &UpdatePhase{Documents: 5000, IndexBytesAfter: 420 << 20},
	}
}

// The defect this was written for: a run on a shared machine recorded one load
// average, at the start, and every engine measured after it inherited a number
// that had stopped being true. An engine measured under load 30 beside engines
// measured under load 4 reads as a regression and is not one.
func TestTheReportSaysWhatTheMachineWasDoingDuringEachPhase(t *testing.T) {
	quiet := indexedAndSearched("tantivy")
	quiet.Index.Usage.LoadBefore, quiet.Index.Usage.LoadAfter = 0.8, 1.1
	quiet.Search.Usage.LoadBefore, quiet.Search.Usage.LoadAfter = 1.0, 1.2

	busy := indexedAndSearched("kura")
	busy.Index.Usage.LoadBefore, busy.Index.Usage.LoadAfter = 1.2, 22.5
	busy.Search.Usage.LoadBefore, busy.Search.Usage.LoadAfter = 24.0, 26.1

	got := Report([]Result{withCores(quiet, 8), withCores(busy, 8)})
	body := section(t, got, "## Machine load per phase")

	for _, want := range []string{
		"| tantivy | 0.80 to 1.10 | 1.00 to 1.20 |",
		"| kura | 1.20 to 22.50 | 24.00 to 26.10 |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the load table does not contain %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "kura were measured while the machine was busy") {
		t.Errorf("the report does not say which engine was measured under load:\n%s", body)
	}
	if strings.Contains(body, "tantivy were measured while the machine was busy") {
		t.Errorf("an engine measured on an idle machine was called contended:\n%s", body)
	}
	if !strings.Contains(body, "Load ranged from 0.80 to 26.10") {
		t.Errorf("the report does not say the engines saw different machines:\n%s", body)
	}
}

// Every result file written before the field existed has no load in it, and a
// table of zeros would be a claim that the machine was idle.
func TestAResultWithNoRecordedLoadHasNoLoadSection(t *testing.T) {
	got := Report([]Result{indexedAndSearched("tantivy"), indexedAndSearched("kura")})
	if strings.Contains(got, "## Machine load per phase") {
		t.Errorf("a run that recorded no load got a load section:\n%s", got)
	}
}

// A run where the load barely moved should not be told to go and do it again.
func TestASteadyMachineIsNotAccusedOfMovingUnderTheRun(t *testing.T) {
	first := indexedAndSearched("tantivy")
	first.Index.Usage.LoadBefore, first.Index.Usage.LoadAfter = 0.9, 1.0
	first.Search.Usage.LoadBefore, first.Search.Usage.LoadAfter = 1.0, 1.1

	second := indexedAndSearched("kura")
	second.Index.Usage.LoadBefore, second.Index.Usage.LoadAfter = 1.1, 1.2
	second.Search.Usage.LoadBefore, second.Search.Usage.LoadAfter = 1.2, 1.3

	body := section(t, Report([]Result{withCores(first, 8), withCores(second, 8)}), "## Machine load per phase")
	if strings.Contains(body, "Load ranged") {
		t.Errorf("a load that moved by a fifth was called a change of conditions:\n%s", body)
	}
	if strings.Contains(body, "measured while the machine was busy") {
		t.Errorf("an idle eight core machine was called busy:\n%s", body)
	}
}

// withCores puts a core count on a result, because contention is judged per
// core and the shared helper does not carry one.
func withCores(r Result, cores int) Result {
	r.Machine.Cores = cores
	return r
}
