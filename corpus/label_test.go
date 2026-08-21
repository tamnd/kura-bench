package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestALabelSurvivesBeingWrittenAndReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enron.jsonl")
	want := Label{
		Dataset:   "enron",
		Licence:   "released in a federal investigation, real personal data, local use only",
		Public:    false,
		Documents: 517290,
		Bytes:     941693799,
	}
	if err := WriteLabel(path, want); err != nil {
		t.Fatal(err)
	}

	got, ok := ReadLabel(path)
	if !ok {
		t.Fatal("the label just written could not be read back")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The whole point. A corpus of real people's mail must not put their mailbox
// paths into a file somebody commits, and this is what decides that.
func TestACorpusOfPersonalDataIsNotPublishable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enron.jsonl")
	if err := WriteLabel(path, Label{Dataset: "enron", Licence: "real personal data", Public: false}); err != nil {
		t.Fatal(err)
	}

	ok, why := Publishable(path)
	if ok {
		t.Fatal("a corpus of personal data was cleared for publication")
	}
	if !strings.Contains(why, "enron") || !strings.Contains(why, "real personal data") {
		t.Errorf("the reason does not say what or why: %q", why)
	}
}

func TestAPublicCorpusKeepsItsDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "simplewiki.jsonl")
	if err := WriteLabel(path, Label{Dataset: "simplewiki", Public: true}); err != nil {
		t.Fatal(err)
	}

	if ok, why := Publishable(path); !ok {
		t.Errorf("a freely licensed corpus was held back: %q", why)
	}
}

// Being wrong in this direction costs a rerun. Being wrong in the other puts a
// real person's name in a public git history, which no later commit undoes.
func TestACorpusThatSaysNothingAboutItselfIsTreatedAsRestricted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mystery.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, why := Publishable(path)
	if ok {
		t.Fatal("a corpus nobody labelled was cleared for publication")
	}
	if !strings.Contains(why, "kura-corpus") {
		t.Errorf("the reason does not say how to fix it: %q", why)
	}
}

// A truncated or hand edited label is the same situation as no label, and the
// safe answer is the same one.
func TestALabelThatDoesNotParseIsTreatedAsNoLabel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	if err := os.WriteFile(LabelPath(path), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := ReadLabel(path); ok {
		t.Fatal("a broken label was read as a label")
	}
	if ok, _ := Publishable(path); ok {
		t.Fatal("a broken label cleared a corpus for publication")
	}
}
