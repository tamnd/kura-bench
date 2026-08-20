package corpus

import (
	"encoding/json"
	"os"
)

// Label is what a built corpus says about itself, written in a small file
// beside it.
//
// It exists because of one property of one corpus. The mail corpus is the
// private correspondence of real people, its document identifiers are mailbox
// paths carrying their surnames, and those identifiers end up in every result
// file because that is how a relevance score knows what came back. A result
// file is a thing somebody commits. Nothing in the path from one to the other
// noticed, and the only reason it did not happen is that somebody looked.
//
// A rule that depends on somebody looking is not a rule. So the corpus carries
// the answer with it, the benchmark reads it, and the decision is made by the
// code every time rather than by a person once.
type Label struct {
	// Dataset is the manifest name, or empty for a corpus built out of source
	// checkouts or a directory.
	Dataset string `json:"dataset,omitempty"`

	// Licence is the one line position on redistribution, copied from the
	// manifest so that the corpus answers the question without the manifest.
	Licence string `json:"licence,omitempty"`

	// Public says content from this corpus may appear outside the machine it
	// was built on. Aggregate numbers are always fine. A document identifier, a
	// snippet or a matched document is not, for anything marked false.
	Public bool `json:"public"`

	Documents int   `json:"documents"`
	Bytes     int64 `json:"bytes"`
}

// LabelPath is where the label for a corpus file lives.
//
// Beside the corpus rather than inside it, because the corpus is a stream of
// documents that several programs in three languages read, and adding a header
// line to it would mean changing all of them to skip a line they do not care
// about.
func LabelPath(corpus string) string { return corpus + ".dataset.json" }

// WriteLabel records what a corpus is, next to it.
func WriteLabel(corpus string, l Label) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(LabelPath(corpus), append(b, '\n'), 0o644)
}

// ReadLabel reads the label beside a corpus, and says whether there was one.
//
// A corpus with no label is not treated as public. See [Publishable].
func ReadLabel(corpus string) (Label, bool) {
	b, err := os.ReadFile(LabelPath(corpus))
	if err != nil {
		return Label{}, false
	}
	var l Label
	if err := json.Unmarshal(b, &l); err != nil {
		return Label{}, false
	}
	return l, true
}

// Publishable says whether content from this corpus may be written into a file
// somebody might commit, and gives the reason when it may not.
//
// A corpus with no label is not publishable, and the reason says so. That is
// the wrong answer for most corpora and it is the right default anyway, because
// the two ways of being wrong here do not cost the same. Being wrong in this
// direction costs a rerun with the corpus rebuilt. Being wrong in the other
// direction puts a real person's name in a public git history, which is not
// something a later commit undoes.
//
// It is not silent about it either. Every caller prints the reason, so an
// unlabelled corpus produces a line telling you to rebuild it rather than a
// table that quietly lost a column.
func Publishable(corpus string) (bool, string) {
	l, ok := ReadLabel(corpus)
	if !ok {
		return false, "there is no " + LabelPath(corpus) + " saying what this corpus is, so it is treated as restricted, rebuild it with kura-corpus to get one"
	}
	if !l.Public {
		who := l.Dataset
		if who == "" {
			who = "this corpus"
		}
		reason := who + " is not publishable"
		if l.Licence != "" {
			reason += ": " + l.Licence
		}
		return false, reason
	}
	return true, ""
}
