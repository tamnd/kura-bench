package relevance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

// Report is what one scoring run produced, in the form that gets written to
// disk and committed as a baseline.
//
// A table printed to a terminal is read once by the person who ran it. The
// point of writing the same numbers to a file is that the next run can be
// compared against them by a machine, which is the only way a ranking change
// gets reviewed on evidence rather than on the author's description of it.
type Report struct {
	// K is the depth the scores were computed at. Comparing a report at one
	// depth against a baseline at another is meaningless, so [Compare] refuses.
	K int `json:"k"`

	// Qrels and Queries are the judgment and query files these numbers came
	// from, recorded because the same engine scored against a different judged
	// set is a different number and nothing else in the file would say so.
	Qrels   string `json:"qrels"`
	Queries string `json:"queries"`

	// Tolerance is how far a score may fall below the baseline before it counts
	// as a regression.
	//
	// It belongs to the baseline rather than to the run being checked, because
	// it describes how much these particular numbers move on their own, and the
	// only way to know that is to have run the same benchmark several times and
	// looked at the spread. A tolerance picked because it sounds about right
	// either passes real regressions or fails clean runs, and both of those
	// teach people to ignore the check.
	//
	// It is a pointer so that a baseline missing the field is an error rather
	// than a silent demand for an exact match.
	Tolerance *float64 `json:"tolerance,omitempty"`

	Engines []EngineScores `json:"engines"`
}

// EngineScores is one engine's row in a [Report].
type EngineScores struct {
	Engine   string  `json:"engine"`
	NDCG     float64 `json:"ndcg"`
	MRR      float64 `json:"mrr"`
	Recall   float64 `json:"recall"`
	Success  float64 `json:"success_at_1"`
	Coverage float64 `json:"judged_coverage"`
	Queries  int     `json:"queries"`
	Unjudged int     `json:"unjudged"`
}

// Add appends an engine's scores to the report.
func (r *Report) Add(engine string, s Scores) {
	r.K = s.K
	r.Engines = append(r.Engines, EngineScores{
		Engine:   engine,
		NDCG:     s.NDCG,
		MRR:      s.MRR,
		Recall:   s.Recall,
		Success:  s.Success,
		Coverage: s.Coverage,
		Queries:  s.Queries,
		Unjudged: s.Unjudged,
	})
}

// Write writes the report as indented JSON with a trailing newline, so that a
// committed baseline reviews as a readable diff rather than as one long line.
func (r Report) Write(w io.Writer) error {
	sort.Slice(r.Engines, func(i, j int) bool { return r.Engines[i].Engine < r.Engines[j].Engine })
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(r)
}

// WriteFile is [Report.Write] to a path.
func (r Report) WriteFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := r.Write(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ReadReport reads a report written by [Report.Write].
func ReadReport(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = f.Close() }()
	var out Report
	if err := json.NewDecoder(f).Decode(&out); err != nil {
		return Report{}, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// Change is one engine's movement against the baseline.
type Change struct {
	Engine string

	// Baseline and Now are nDCG, which is the score the gate is on. The others
	// are reported in the table but a pull request is not failed on them,
	// because moving recall at a page of ten without moving nDCG is usually the
	// judged set being shallow rather than the engine being worse.
	Baseline float64
	Now      float64

	// Regressed is whether the drop is larger than the baseline's tolerance.
	Regressed bool

	// Missing is set when the baseline has this engine and the new report does
	// not. It is not a regression, because the usual cause is a machine without
	// the toolchain that engine needs, but it has to be said out loud or an
	// engine can quietly stop being measured.
	Missing bool
}

// Compare checks a fresh report against a committed baseline.
//
// It returns a change per engine in the baseline, in name order, and an error
// only when the two reports cannot be compared at all. A regression is not an
// error here: the caller decides what to do about it, and the caller is the
// thing that knows whether it is running in CI.
func Compare(baseline, now Report) ([]Change, error) {
	if baseline.Tolerance == nil {
		return nil, fmt.Errorf("the baseline has no tolerance in it, and a tolerance has to be measured from repeated runs of the same benchmark rather than assumed here")
	}
	if baseline.K != now.K {
		return nil, fmt.Errorf("the baseline was scored at a depth of %d and this run at %d, which are different measurements", baseline.K, now.K)
	}
	if baseline.Qrels != now.Qrels {
		return nil, fmt.Errorf("the baseline was scored against %s and this run against %s, so the numbers are not about the same thing", baseline.Qrels, now.Qrels)
	}

	current := map[string]EngineScores{}
	for _, e := range now.Engines {
		current[e.Engine] = e
	}

	out := make([]Change, 0, len(baseline.Engines))
	for _, was := range baseline.Engines {
		is, ok := current[was.Engine]
		if !ok {
			out = append(out, Change{Engine: was.Engine, Baseline: was.NDCG, Missing: true})
			continue
		}
		out = append(out, Change{
			Engine:    was.Engine,
			Baseline:  was.NDCG,
			Now:       is.NDCG,
			Regressed: was.NDCG-is.NDCG > *baseline.Tolerance,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Engine < out[j].Engine })
	return out, nil
}
