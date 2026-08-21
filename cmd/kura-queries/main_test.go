package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/kura-bench/corpus"
)

// TestBandsComeOutInOrder is the only property of a constructed query set worth
// asserting: a term in a later band is in fewer documents than one in an
// earlier band. If that stops holding, the file still looks like a query set
// and stops describing the distribution it claims to.
func TestBandsComeOutInOrder(t *testing.T) {
	path := writeCorpus(t, 2000)

	text, err := fromCorpus(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, df, err := documentFrequency(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	var last = 1 << 30
	for _, q := range queries(text) {
		if strings.Contains(q, " ") {
			continue
		}
		got, ok := df[q]
		if !ok {
			t.Fatalf("%q is not a term in the corpus", q)
		}
		if got > last {
			t.Errorf("%q is in %d documents, which is more than the band before it at %d", q, got, last)
		}
		last = got
	}
}

func TestEveryQueryReallyOccurs(t *testing.T) {
	path := writeCorpus(t, 500)

	text, err := fromCorpus(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	bodies := make([]string, 0, 500)
	if _, err := corpus.ReadFile(path, func(d corpus.Document) error {
		bodies = append(bodies, strings.ToLower(d.Title+" "+d.Body))
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A query that matches nothing measures a dictionary miss and calls it a
	// search. Every line here has to be something the corpus really contains.
	for _, q := range queries(text) {
		found := false
		for _, body := range bodies {
			if strings.Contains(body, q) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q does not occur in the corpus", q)
		}
	}
}

// The vocabulary is taken from the head of the corpus so that a large one does
// not need a map with an entry for every term in it. A term the sample misses
// is one that appears in none of the first documents, which is rarer than the
// rarest band asks for, so the bands have to come out the same either way.
func TestSamplingTheVocabularyDoesNotChangeTheBands(t *testing.T) {
	path := writeCorpus(t, 2000)

	whole, err := fromCorpus(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	sampled, err := fromCorpus(path, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !equalQueries(queries(whole), queries(sampled)) {
		t.Errorf("counting every term gave %q and sampling gave %q", queries(whole), queries(sampled))
	}
}

// The counts have to be exact for the terms that are counted, even though the
// vocabulary they came from is a sample. A term picked from the head of the
// corpus and then counted over a tenth of it would describe a distribution
// nobody is searching.
func TestTheCountsCoverTheWholeCorpusEvenWhenTheVocabularyDoesNot(t *testing.T) {
	path := writeCorpus(t, 1000)

	documents, df, err := documentFrequency(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if documents != 1000 {
		t.Fatalf("counted %d documents, want all 1000", documents)
	}
	// Every document has this word in it.
	if got := df["quick"]; got != 1000 {
		t.Errorf("quick is in %d documents, want all 1000", got)
	}
	// One document in two has this one.
	if got := df["band1term"]; got != 500 {
		t.Errorf("band1term is in %d documents, want 500", got)
	}
}

func TestTheSameCorpusGivesTheSameQueries(t *testing.T) {
	path := writeCorpus(t, 300)

	// Two runs over the same corpus have to agree, or two machines running the
	// same benchmark are not running the same benchmark.
	first, err := fromCorpus(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fromCorpus(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("two runs over the same corpus produced different query sets")
	}
}

func TestARealLogIsTakenAsItIs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.tsv")
	// The tab separated form is what the passage collection ships, and the bare
	// form is what most other logs look like.
	body := "1048578\tcost of endless pools\n" +
		"1048579\twhat is a nas drive\n" +
		"1048578\tcost of endless pools\n" +
		"how far is the moon\n" +
		"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	text, err := fromLog(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := queries(text)
	want := []string{"cost of endless pools", "what is a nas drive", "how far is the moon"}
	if len(got) != len(want) {
		t.Fatalf("got %d queries %q, want %d", len(got), got, len(want))
	}
	// The order is the log's order, because a query set that reorders a real
	// log has already started editing it.
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("query %d is %q, want %q", i, got[i], want[i])
		}
	}
}

func TestALogStopsAtTheCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.tsv")
	if err := os.WriteFile(path, []byte("a query\nanother query\na third query\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := fromLog(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := queries(text); len(got) != 2 {
		t.Fatalf("got %d queries, want 2: %q", len(got), got)
	}
}

func TestAnEmptyLogIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.tsv")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fromLog(path, 10); err == nil {
		t.Fatal("an empty log should be an error rather than an empty query set")
	}
}

// writeCorpus builds a corpus whose term distribution is known, so that a test
// can say what the bands should land on.
//
// The shape is the one every real corpus has: a handful of terms in almost
// every document, a middle that thins out quickly, and a long tail of terms in
// exactly one document. It is generated rather than real because what is under
// test is the selection, and a selection is only checkable against a
// distribution somebody can state.
func writeCorpus(t *testing.T, documents int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	for i := range documents {
		var b strings.Builder
		b.WriteString("the quick alpha ")
		for band := 1; band <= 9; band++ {
			// A term in this band is in one document in 2^band, which spreads
			// the distribution over three orders of magnitude by the last one.
			if i%(1<<band) == 0 {
				fmt.Fprintf(&b, "band%dterm ", band)
			}
		}
		fmt.Fprintf(&b, "unique%dword", i)

		if err := enc.Encode(corpus.Document{
			ID:        fmt.Sprint(i),
			Repo:      "generated",
			Path:      fmt.Sprint(i),
			Body:      b.String(),
			Extension: "txt",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func equalQueries(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// queries is the lines of a query set that are queries.
func queries(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}
