package runner

import "testing"

func TestScore(t *testing.T) {
	cases := []struct {
		name    string
		got     []int64
		want    []int64
		correct float64
		wrong   int
	}{
		{"all right", []int64{1, 2, 3}, []int64{1, 2, 3}, 1, 0},
		{"one wrong", []int64{1, 9, 3}, []int64{1, 2, 3}, 2.0 / 3.0, 1},
		{"all wrong", []int64{9, 9}, []int64{1, 2}, 0, 2},
		// A store that answered more times than there are answers is not
		// scored out of the shorter of the two, because that would turn a run
		// against the wrong answers file into a perfect score.
		{"more answers than truth", []int64{1, 2, 3}, []int64{1, 2}, 2.0 / 3.0, 1},
		{"nothing answered", nil, []int64{1, 2}, 0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			correct, wrong := Score(c.got, c.want)
			if wrong != c.wrong {
				t.Errorf("wrong = %d, want %d", wrong, c.wrong)
			}
			if diff := correct - c.correct; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("correct = %v, want %v", correct, c.correct)
			}
		})
	}
}

func TestGraphConfigWants(t *testing.T) {
	empty := GraphConfig{}
	if !empty.Wants("bfs") {
		t.Error("an empty list should mean every operation")
	}

	some := GraphConfig{Ops: []string{"bfs", "pagerank"}}
	if !some.Wants("pagerank") {
		t.Error("pagerank was asked for")
	}
	if some.Wants("two-hop") {
		t.Error("two-hop was not asked for")
	}
}

func TestGraphConfigWorkerCount(t *testing.T) {
	if got := (GraphConfig{Workers: 4}).WorkerCount(); got != 4 {
		t.Errorf("WorkerCount = %d, want 4", got)
	}
	if got := (GraphConfig{}).WorkerCount(); got < 1 {
		t.Errorf("WorkerCount = %d, want at least one", got)
	}
}
