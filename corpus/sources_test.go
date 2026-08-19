package corpus

import (
	"regexp"
	"testing"
)

// A pin that is not a full commit is not a pin. An abbreviated hash can become
// ambiguous as a repository grows, and a branch name is not a revision at all.
var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestSourcesArePinned(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Sources() {
		if seen[s.Name] {
			t.Errorf("two projects are called %q, so their documents would share an id prefix", s.Name)
		}
		seen[s.Name] = true

		if !fullCommit.MatchString(s.Commit) {
			t.Errorf("%s is pinned to %q, which is not a full commit", s.Name, s.Commit)
		}
		if s.Tag == "" {
			t.Errorf("%s has no tag, so nobody can say which release the corpus holds", s.Name)
		}
		if s.URL == "" || s.About == "" {
			t.Errorf("%s is missing its address or its description", s.Name)
		}
	}
	if len(seen) == 0 {
		t.Fatal("the standard corpus is empty")
	}
}

func TestLookupSource(t *testing.T) {
	s, err := LookupSource("redis")
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://github.com/redis/redis" {
		t.Errorf("URL = %q", s.URL)
	}
	if _, err := LookupSource("nothing"); err == nil {
		t.Error("a project that is not in the list should not resolve")
	}
}
