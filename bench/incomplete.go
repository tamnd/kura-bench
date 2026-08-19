package bench

import (
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

// TooSlow is a phase that was still running when the deadline passed.
//
// It is a type rather than a message because it is the one failure that is a
// result about the engine rather than a failure of the run, and the difference
// has to survive being passed back up. It lives here rather than in one of the
// three orchestrators so that the sentence a report prints is written once,
// which is the same reason [GraphHeader] is here.
type TooSlow struct {
	// Phase is what was running, in the words the runner's own -phase flag
	// uses, so that the sentence points at something somebody can rerun.
	Phase string

	// After is the deadline that passed, not how long the phase actually ran,
	// which nothing here can know once the process has been killed.
	After time.Duration
}

func (e TooSlow) Error() string {
	return fmt.Sprintf("the %s phase did not finish within %s", e.Phase, e.After)
}

// noteFor is one engine's line in the Notes section.
//
// An engine that ran out of time has the reason put in front of whatever it had
// to say about itself, because that is the first thing somebody looking for the
// missing numbers needs to read.
func noteFor(note, incomplete string) string {
	if incomplete == "" {
		return note
	}
	out := incomplete + ", which is why its rows are empty rather than the engine being absent from the run."
	if note != "" {
		out += " " + upperFirst(note)
	}
	return out
}

// upperFirst starts a sentence.
//
// An engine's note is written as a clause that reads on from its own name, and
// following the one sentence in this report that is not about the engine it
// has to begin like a sentence.
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// missing is what a table about work the run asked for says in place of a
// number, for an engine that has none.
//
// Not run and ran out of time look the same in a table and are not the same
// thing, and the difference is the whole reason the deadline is worth setting.
func missing(incomplete string) string {
	if incomplete != "" {
		return "ran out of time"
	}
	return "not run"
}

// missingBecause is missing for a suite where an engine can also refuse the run
// outright rather than fail at it.
//
// An engine that will not rank by the metric a run picked has not run out of
// time and has not been left out, and a table that said either of those about
// it would be wrong in a way that reflects on the engine.
func missingBecause(declined, incomplete string) string {
	if declined != "" {
		return "declined"
	}
	return missing(incomplete)
}

// noteBecause is noteFor where the reason can also be a refusal.
func noteBecause(note, declined, incomplete string) string {
	if declined == "" {
		return noteFor(note, incomplete)
	}
	out := declined + ", so it has no numbers here rather than having been left out of the run."
	if note != "" {
		out += " " + upperFirst(note)
	}
	return out
}
