package relevance

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The numbers in this test are worked out by hand below each case rather than
// copied from a run of this code, which is the only way a test of an arithmetic
// definition means anything. A test that asserts the code agrees with itself
// passes just as happily when the definition is wrong.
func TestTheMetricsMatchTheArithmetic(t *testing.T) {
	// One query, three judged documents, all binary. The engine returns the
	// relevant ones at ranks 2 and 3 and a judged irrelevant one at rank 1.
	qrels := Qrels{"1": {"a": 1, "b": 1, "c": 0}}
	queries := []Query{{ID: "1", Text: "q", IDs: []string{"c", "a", "b"}}}

	got := Score(queries, qrels, 10)

	// DCG is the gain over the discount at each rank. With binary grades the
	// gain is one, and the discount is log2 of the rank plus one.
	//   rank 1 c, not relevant, nothing
	//   rank 2 a, 1 / log2(3) = 0.6309297535714574
	//   rank 3 b, 1 / log2(4) = 0.5
	// The ideal ranking puts both relevant documents first.
	//   rank 1 = 1 / log2(2) = 1
	//   rank 2 = 1 / log2(3) = 0.6309297535714574
	dcg := 1/math.Log2(3) + 1/math.Log2(4)
	ideal := 1.0 + 1/math.Log2(3)
	near(t, "nDCG", got.NDCG, dcg/ideal)

	// The first relevant document is at rank 2.
	near(t, "MRR", got.MRR, 0.5)

	// Both relevant documents came back.
	near(t, "recall", got.Recall, 1)

	// All three returned documents were judged, one way or the other.
	near(t, "coverage", got.Coverage, 1)

	if got.Queries != 1 || got.Unjudged != 0 {
		t.Fatalf("scored %d queries with %d unjudged, want one and none", got.Queries, got.Unjudged)
	}
}

func TestAGradedJudgmentWeighsMoreThanABinaryOne(t *testing.T) {
	// The gain is the grade, so a grade of three is worth three times a grade
	// of one. Putting the highly relevant document second rather than first has
	// to cost more than swapping two equally relevant ones would.
	qrels := Qrels{"1": {"a": 3, "b": 1}}

	right := Score([]Query{{ID: "1", IDs: []string{"a", "b"}}}, qrels, 10)
	wrong := Score([]Query{{ID: "1", IDs: []string{"b", "a"}}}, qrels, 10)

	near(t, "the ideal order", right.NDCG, 1)
	// DCG of the wrong order is 1/log2(2) + 3/log2(3), ideal is 3 + 1/log2(3).
	near(t, "the wrong order", wrong.NDCG, (1+3/math.Log2(3))/(3+1/math.Log2(3)))
	if wrong.NDCG >= right.NDCG {
		t.Fatal("putting the best document second scored no worse than putting it first")
	}

	// The same swap with two equally relevant documents costs nothing at all,
	// which is what makes the cost above attributable to the grades.
	flat := Qrels{"1": {"a": 1, "b": 1}}
	swapped := Score([]Query{{ID: "1", IDs: []string{"b", "a"}}}, flat, 10)
	near(t, "swapping equals", swapped.NDCG, 1)
}

func TestAPageWithNothingRelevantScoresZeroWithoutFailing(t *testing.T) {
	qrels := Qrels{"1": {"a": 1}}
	got := Score([]Query{{ID: "1", IDs: []string{"x", "y"}}}, qrels, 10)

	if got.NDCG != 0 || got.MRR != 0 || got.Recall != 0 {
		t.Fatalf("a page with nothing relevant in it scored %+v", got)
	}
	if got.Coverage != 0 {
		t.Fatalf("coverage was %v, and neither returned document was judged", got.Coverage)
	}
	if got.Queries != 1 {
		t.Fatal("the query was not counted, and a query an engine got wrong is still a query")
	}
}

// A query nobody judged says nothing about an engine, and counting it as a zero
// would punish an engine for a gap in the judgments.
func TestAnUnjudgedQueryIsLeftOutRatherThanScoredZero(t *testing.T) {
	qrels := Qrels{"1": {"a": 1}}
	got := Score([]Query{
		{ID: "1", IDs: []string{"a"}},
		{ID: "2", IDs: []string{"b"}},
	}, qrels, 10)

	if got.Queries != 1 || got.Unjudged != 1 {
		t.Fatalf("scored %d and skipped %d, want one of each", got.Queries, got.Unjudged)
	}
	near(t, "nDCG", got.NDCG, 1)
}

func TestTheDepthCutsThePage(t *testing.T) {
	qrels := Qrels{"1": {"a": 1}}
	// The one relevant document is at rank 3, so a depth of two cannot see it.
	shallow := Score([]Query{{ID: "1", IDs: []string{"x", "y", "a"}}}, qrels, 2)
	deep := Score([]Query{{ID: "1", IDs: []string{"x", "y", "a"}}}, qrels, 10)

	if shallow.Recall != 0 {
		t.Fatalf("recall at 2 was %v, and the relevant document is at rank 3", shallow.Recall)
	}
	near(t, "recall at 10", deep.Recall, 1)
	near(t, "MRR at 10", deep.MRR, 1.0/3)
}

func TestCoverageSaysHowMuchOfThePageWasJudged(t *testing.T) {
	qrels := Qrels{"1": {"a": 1, "b": 0}}
	got := Score([]Query{{ID: "1", IDs: []string{"a", "b", "c", "d"}}}, qrels, 10)

	// Two of the four returned documents appear in the judgments, one relevant
	// and one not. The other two nobody ever looked at.
	near(t, "coverage", got.Coverage, 0.5)
}

func TestJudgmentsAreReadInTheStandardFormat(t *testing.T) {
	body := "1 0 doc-a 1\n1 0 doc-b 0\n2 0 doc-c 2\n\n"
	q, err := ReadQrels(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := q["1"]["doc-a"]; got != 1 {
		t.Errorf("doc-a was read as grade %d", got)
	}
	if _, ok := q["1"]["doc-b"]; !ok {
		t.Error("a grade of zero was dropped, and judged coverage needs to know it was looked at")
	}
	if got := q.Relevant("1"); got != 1 {
		t.Errorf("query 1 has %d relevant documents, want the one graded above zero", got)
	}
	if got := q["2"]["doc-c"]; got != 2 {
		t.Errorf("a graded judgment came back as %d", got)
	}
}

func TestAShortJudgmentLineIsAnError(t *testing.T) {
	if _, err := ReadQrels(strings.NewReader("1 0 doc-a\n")); err == nil {
		t.Fatal("a line with three fields was accepted as a judgment")
	}
}

func TestTheQueryLogMapsTextBackToItsIdentifier(t *testing.T) {
	body := "1048578\tcost of endless pools\n1048579\twhat is a nas drive\n"
	byText, collisions, err := ReadQueries(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if collisions != 0 {
		t.Errorf("reported %d collisions in a log with no repeats", collisions)
	}
	if got := byText["cost of endless pools"]; got != "1048578" {
		t.Errorf("the query mapped to %q", got)
	}
}

func TestARepeatedQueryKeepsTheFirstIdentifier(t *testing.T) {
	body := "1\tsame words\n2\tsame words\n"
	byText, collisions, err := ReadQueries(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if collisions != 1 {
		t.Errorf("reported %d collisions, want the one repeat", collisions)
	}
	if got := byText["same words"]; got != "1" {
		t.Errorf("kept %q, want the first identifier", got)
	}
}

func TestTheRunFileIsInTheFormatOtherToolsRead(t *testing.T) {
	var b strings.Builder
	err := WriteRun(&b, "kura", []Query{{ID: "7", IDs: []string{"a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "7 Q0 a 1 2 kura\n7 Q0 b 2 1 kura\n"
	if b.String() != want {
		t.Fatalf("wrote\n%q\nwant\n%q", b.String(), want)
	}
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s is %v, want %v", what, got, want)
	}
}

// The one test here that does not check our arithmetic against our own
// arithmetic.
//
// Everybody who has written their own nDCG has got it wrong at least once, and
// the ways to get it wrong all produce plausible numbers: the wrong gain, the
// wrong logarithm, an ideal ranking taken from the page instead of from the
// judgments, a cutoff applied to one of those and not the other. None of that
// shows up in a test whose expected value came from the same code.
//
// So the expected values below came out of trec_eval, which is the reference
// every retrieval paper is checked against. The fixture next to this file is
// the input it was given:
//
//	trec_eval -m ndcg_cut.10 -m recip_rank -m recall.10 \
//	        relevance/testdata/reference.qrels relevance/testdata/reference.run
//
// It was version 10.0-rc3. The fixture is deliberately awkward: a graded query
// with an unjudged document at the top of the page, a judgment of zero, a
// relevant document that was never retrieved, a query whose only hit is third,
// and a query where nothing relevant came back at all.
func TestOurArithmeticAgreesWithTrecEval(t *testing.T) {
	qrels, err := ReadQrelsFile(filepath.Join("testdata", "reference.qrels"))
	if err != nil {
		t.Fatal(err)
	}

	// The same three pages the run file holds, in the same order.
	queries := []Query{
		{ID: "q1", IDs: []string{"d5", "d1", "d3", "d2"}},
		{ID: "q2", IDs: []string{"d8", "d6", "d4"}},
		{ID: "q3", IDs: []string{"d11", "d12", "d13"}},
	}

	got := Score(queries, qrels, 10)
	if got.Queries != 3 || got.Unjudged != 0 {
		t.Fatalf("scored %d queries with %d unjudged, want 3 and 0", got.Queries, got.Unjudged)
	}
	// trec_eval prints four decimal places, so that is the agreement asked for.
	for _, c := range []struct {
		name string
		got  float64
		want float64
	}{
		{"nDCG@10", got.NDCG, 0.2260},
		{"MRR@10", got.MRR, 0.2778},
		{"recall@10", got.Recall, 0.3889},
	} {
		if math.Abs(c.got-c.want) > 0.00005 {
			t.Errorf("%s is %.4f, trec_eval says %.4f", c.name, c.got, c.want)
		}
	}
}

// The run file this package writes is the one trec_eval was given above, so a
// change to its format would make the agreement above meaningless without
// failing anything. This is what fails instead.
func TestTheRunFileMatchesTheOneTrecEvalWasGiven(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "reference.run"))
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	err = WriteRun(&b, "kura-bench", []Query{
		{ID: "q1", IDs: []string{"d5", "d1", "d3", "d2"}},
		{ID: "q2", IDs: []string{"d8", "d6", "d4"}},
		{ID: "q3", IDs: []string{"d11", "d12", "d13"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("the run file this package writes is no longer the fixture trec_eval read:\ngot:\n%swant:\n%s", b.String(), want)
	}
}
