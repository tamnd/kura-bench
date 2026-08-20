package bench

import (
	"os"
	"path/filepath"
	"testing"
)

// The empty digest of an empty file, which is the one value in this file worth
// hard coding because it is the same everywhere and proves the hash is SHA-256
// and not something else that also produces sixty four hex characters.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// A result nobody can reproduce is a recollection, and reproducing one starts
// with proving you have the same corpus.
func TestARunRecordsWhatItWasGiven(t *testing.T) {
	dir := t.TempDir()
	corpus := filepath.Join(dir, "corpus.jsonl")
	queries := filepath.Join(dir, "queries.txt")
	if err := os.WriteFile(corpus, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queries, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	r := Run{Corpus: corpus, Queries: queries, Repeat: 20, Depth: 10}.Describe()

	// sha256 of "hello", which is a value anybody can check against any other
	// implementation, and that is the whole point of writing it down.
	const hello = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if r.CorpusSHA256 != hello {
		t.Errorf("corpus digest is %q, want %q", r.CorpusSHA256, hello)
	}
	if r.CorpusBytes != 5 {
		t.Errorf("corpus is %d bytes, want 5", r.CorpusBytes)
	}
	if r.QueriesSHA256 != emptySHA256 {
		t.Errorf("queries digest is %q, want the empty one", r.QueriesSHA256)
	}
	if r.Started.IsZero() {
		t.Error("the run has no start time")
	}
	if r.Repeat != 20 || r.Depth != 10 {
		t.Errorf("the parameters were not kept: %+v", r)
	}
}

// The corpus is about to be opened by an engine, which is a better place to
// find out it is missing than in the bookkeeping.
func TestACorpusThatIsNotThereLeavesTheDigestEmptyRatherThanFailing(t *testing.T) {
	r := Run{Corpus: filepath.Join(t.TempDir(), "nothing.jsonl")}.Describe()

	if r.CorpusSHA256 != "" {
		t.Errorf("a missing file produced a digest: %q", r.CorpusSHA256)
	}
	if r.Started.IsZero() {
		t.Error("the rest of the run was abandoned too")
	}
}

// A result written before any of this existed still has to parse, since the
// committed ones were.
func TestAResultWithoutARunIsStillAResult(t *testing.T) {
	var res Result
	if res.Run != nil {
		t.Fatal("a zero result claims to know how it was produced")
	}
}
