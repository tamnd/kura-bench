package runner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tamnd/kura-bench/bench"
	"github.com/tamnd/kura-bench/corpus"
)

// fake is an engine that does nothing but remember what it was asked to do. It
// is here so that the harness itself can be tested, which matters more than it
// sounds: every number in this repository is produced by this code, and a bug
// in it would show up as a finding about an engine.
type fake struct {
	mu       sync.Mutex
	created  string
	opened   string
	added    int
	batches  []int
	flushes  int
	searches int
	closed   int
}

func (f *fake) Describe() Info {
	return Info{Name: "fake", Version: "1.0", Language: "go"}
}

func (f *fake) Create(dir string) error {
	f.created = dir
	// A real engine leaves something behind, and the size of it is one of the
	// things being measured, so the fake does too.
	return os.WriteFile(filepath.Join(dir, "index"), []byte("x"), 0o644)
}

func (f *fake) Open(dir string) error { f.opened = dir; return nil }

func (f *fake) AddBatch(docs []corpus.Document) error {
	f.added += len(docs)
	f.batches = append(f.batches, len(docs))
	return nil
}

func (f *fake) Flush() error { f.flushes++; return nil }

func (f *fake) Search(ctx context.Context, query string, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches++
	return len(query), nil
}

func (f *fake) Close() error { f.closed++; return nil }

// talkative is an engine with a caveat, which is the case the [Noter] interface
// exists for.
type talkative struct{ fake }

func (t *talkative) Note() string {
	return "built against something other than the version it reports"
}

func TestTheIndexPhaseFeedsEveryDocumentOnceAndFlushesLast(t *testing.T) {
	cfg, _ := fixture(t, 1201)
	cfg.Phase = "index"

	f := &fake{}
	out := capture(t, cfg, f)

	if f.added != 1201 {
		t.Fatalf("indexed %d documents, want all 1201", f.added)
	}
	if want := []int{BatchSize, BatchSize, 201}; !equal(f.batches, want) {
		t.Fatalf("batches were %v, want %v", f.batches, want)
	}
	if f.flushes != 1 {
		t.Fatalf("flushed %d times, want exactly one and after the last batch", f.flushes)
	}
	if out.Corpus.Documents != 1201 {
		t.Fatalf("reported %d documents", out.Corpus.Documents)
	}
	if out.Index.Bytes == 0 || out.Index.Files == 0 {
		t.Fatalf("the index on disk was reported as %d bytes in %d files", out.Index.Bytes, out.Index.Files)
	}
	if out.Index.Usage.WallSeconds <= 0 {
		t.Fatal("the phase took no time at all, which means it was not timed")
	}
	if f.created != cfg.Work {
		t.Fatalf("the engine was told to build in %q, want %q", f.created, cfg.Work)
	}
	if f.closed != 1 {
		t.Fatalf("closed %d times, want exactly one", f.closed)
	}
}

func TestTheLimitStopsTheRunEarly(t *testing.T) {
	cfg, _ := fixture(t, 5000)
	cfg.Phase = "index"
	cfg.Limit = 300

	f := &fake{}
	out := capture(t, cfg, f)

	if f.added != 300 {
		t.Fatalf("indexed %d documents, want the 300 that were asked for", f.added)
	}
	if out.Corpus.Documents != 300 {
		t.Fatalf("reported %d documents, want the 300 that were actually indexed", out.Corpus.Documents)
	}
}

func TestTheQueryPhaseTimesEveryQueryAndTheColdStart(t *testing.T) {
	cfg, _ := fixture(t, 200)
	cfg.Phase = "query"
	cfg.Repeat = 3

	f := &fake{}
	out := capture(t, cfg, f)

	if f.opened == "" {
		t.Fatal("the index was never opened")
	}
	if len(out.Search.Queries) != 3 {
		t.Fatalf("summarised %d queries, want the three in the file", len(out.Search.Queries))
	}
	for _, q := range out.Search.Queries {
		if q.Runs != cfg.Repeat {
			t.Errorf("%q was timed %d times, want %d", q.Query, q.Runs, cfg.Repeat)
		}
		if q.MedianMS < q.MinMS || q.MaxMS < q.MedianMS {
			t.Errorf("%q has a median outside its own range: %+v", q.Query, q)
		}
	}
	if out.Open.Usage.WallSeconds <= 0 {
		t.Fatal("the cold start was not timed")
	}
	if out.Search.Concurrent == nil {
		t.Fatal("the concurrent phase was left out, and this engine has no reason to fail it")
	}
	if got := out.Search.Concurrent.Queries; got != 3*cfg.Repeat {
		t.Fatalf("ran %d queries concurrently, want %d", got, 3*cfg.Repeat)
	}
	if out.Update == nil {
		t.Fatal("the update phase was left out, and this engine accepts writes")
	}
	if f.searches == 0 {
		t.Fatal("no queries reached the engine")
	}
	if out.Update.Documents != 200 {
		t.Fatalf("updated %d documents, want the whole corpus since it is smaller than the update size", out.Update.Documents)
	}
}

// A run without a query file is a mistake worth catching before an engine
// spends an hour building an index nobody is going to search.
func TestAQueryPhaseWithoutQueriesIsRefused(t *testing.T) {
	cfg, _ := fixture(t, 10)
	cfg.Phase = "query"
	cfg.Queries = ""

	if err := run(cfg, func(Config) (Engine, error) { return &fake{}, nil }); err == nil {
		t.Fatal("a query phase with no query file was accepted")
	}
}

func TestAnUnknownPhaseIsRefused(t *testing.T) {
	cfg, _ := fixture(t, 10)
	cfg.Phase = "warmup"

	if err := run(cfg, func(Config) (Engine, error) { return &fake{}, nil }); err == nil {
		t.Fatal("an unknown phase was accepted")
	}
}

// An engine whose numbers need a sentence next to them has to get that sentence
// into both phases, because the orchestrator runs them as separate processes and
// either one can be the one that ends up in a report on its own.
func TestAnEngineThatHasSomethingToSayIsHeardInBothPhases(t *testing.T) {
	for _, phase := range []string{"index", "query"} {
		cfg, _ := fixture(t, 20)
		cfg.Phase = phase
		cfg.Repeat = 1

		out := capture(t, cfg, &talkative{})
		if !strings.Contains(out.Notes, "built against something other than the version it reports") {
			t.Errorf("the %s phase dropped the engine's note, notes were %q", phase, out.Notes)
		}
	}
}

func TestTheModuleVersionIsTheOneThatWasBuilt(t *testing.T) {
	// The test binary depends on this module, so the lookup has something real
	// to find and a miss is a bug in the lookup rather than a missing
	// dependency.
	if v := ModuleVersion("github.com/tamnd/kura-bench"); v == "" {
		t.Fatal("the version came back empty rather than unknown")
	}
	if v := ModuleVersion("example.com/not/a/dependency"); v != "unknown" {
		t.Fatalf("an absent dependency reported %q", v)
	}
}

// fixture writes a corpus and a query file, and returns a config pointing at
// them.
func fixture(t *testing.T, documents int) (Config, string) {
	t.Helper()
	dir := t.TempDir()

	corpusPath := filepath.Join(dir, "corpus.jsonl")
	f, err := os.Create(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for i := range documents {
		d := corpus.Document{
			ID:        "repo:file" + strconv.Itoa(i) + ".go",
			Repo:      "repo",
			Path:      "file" + strconv.Itoa(i) + ".go",
			Title:     "file" + strconv.Itoa(i) + ".go",
			Body:      "package main // document " + strconv.Itoa(i),
			Extension: "go",
		}
		if err := enc.Encode(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	queriesPath := filepath.Join(dir, "queries.txt")
	body := "# a comment\n\npackage main\ndocument\nmain\n"
	if err := os.WriteFile(queriesPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	return Config{
		Corpus:  corpusPath,
		Queries: queriesPath,
		Work:    filepath.Join(dir, "work"),
		Repeat:  2,
	}, dir
}

// capture runs a phase and parses the one JSON object the runner writes, which
// is the same thing the orchestrator does and is therefore worth testing rather
// than reaching into the result directly.
func capture(t *testing.T, cfg Config, eng Engine) bench.Result {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	runErr := run(cfg, func(Config) (Engine, error) { return eng, nil })
	_ = w.Close()
	os.Stdout = old
	output := <-done

	if runErr != nil {
		t.Fatal(runErr)
	}

	var res bench.Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("the runner wrote something that is not a result: %v\n%s", err, output)
	}
	return res
}

func equal(a, b []int) bool {
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
