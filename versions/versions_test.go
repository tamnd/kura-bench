package versions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestItReadsThePinOutOfAGoMod(t *testing.T) {
	path := write(t, "go.mod", `module github.com/tamnd/kura-bench

go 1.26.6

require (
	github.com/blevesearch/bleve/v2 v2.6.0
	modernc.org/sqlite v1.56.0
)

require github.com/one/liner v1.2.3

require golang.org/x/sys v0.38.0 // indirect
`)

	for module, want := range map[string]string{
		"github.com/blevesearch/bleve/v2": "v2.6.0",
		"modernc.org/sqlite":              "v1.56.0",
		"github.com/one/liner":            "v1.2.3",
		"golang.org/x/sys":                "v0.38.0",
	} {
		got, err := PinnedGoMod(path, module)
		if err != nil {
			t.Fatalf("%s: %v", module, err)
		}
		if got != want {
			t.Errorf("%s is pinned to %s, want %s", module, got, want)
		}
	}

	if _, err := PinnedGoMod(path, "example.com/absent"); err == nil {
		t.Fatal("a module that is not required came back with a version")
	}
}

func TestItReadsTheResolvedVersionOutOfACargoLock(t *testing.T) {
	// The resolved version is the point. The manifest says 0.26 and the thing
	// that got compiled is 0.26.1, and it is the second one a report has to
	// name.
	path := write(t, "Cargo.lock", `version = 4

[[package]]
name = "serde"
version = "1.0.228"

[[package]]
name = "tantivy"
version = "0.26.1"
dependencies = [
 "serde",
]
`)

	got, err := PinnedCargoLock(path, "tantivy")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.26.1" {
		t.Fatalf("tantivy is pinned to %s, want 0.26.1", got)
	}
	if _, err := PinnedCargoLock(path, "not-a-crate"); err == nil {
		t.Fatal("a crate that is not in the lock file came back with a version")
	}
}

func TestItAsksTheRegistriesAndNoticesWhatIsBehind(t *testing.T) {
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case "/github.com/blevesearch/bleve/v2/@latest":
			_, _ = w.Write([]byte(`{"Version":"v2.7.0","Time":"2026-08-01T00:00:00Z"}`))
		case "/ta/nt/tantivy":
			// Publication order, with a yank and a pre-release in the middle
			// that both have to be ignored.
			_, _ = w.Write([]byte(`{"name":"tantivy","vers":"0.24.2","yanked":false}
{"name":"tantivy","vers":"0.25.0","yanked":true}
{"name":"tantivy","vers":"0.26.1","yanked":false}
{"name":"tantivy","vers":"0.27.0-rc.1","yanked":false}
`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	GoProxy, CrateIndex = srv.URL, srv.URL
	defer func() {
		GoProxy = "https://proxy.golang.org"
		CrateIndex = "https://index.crates.io"
	}()

	dir := t.TempDir()
	writeIn(t, dir, "go.mod", "module m\n\nrequire github.com/blevesearch/bleve/v2 v2.6.0\n")
	writeIn(t, dir, "Cargo.lock", "[[package]]\nname = \"tantivy\"\nversion = \"0.26.1\"\n")

	m := Manifest{Engines: []Engine{
		{Name: "bleve", Registry: "gomod", Package: "github.com/blevesearch/bleve/v2"},
		{Name: "tantivy", Registry: "crate", Package: "tantivy"},
	}}
	repo := Repo{
		GoMod:      filepath.Join(dir, "go.mod"),
		CargoLocks: []string{filepath.Join(dir, "Cargo.lock")},
	}

	got := Check(context.Background(), srv.Client(), m, repo)
	if len(got) != 2 {
		t.Fatalf("checked %d engines, want 2", len(got))
	}

	if got[0].Err != nil {
		t.Fatal(got[0].Err)
	}
	if got[0].Pinned != "v2.6.0" || got[0].Latest != "v2.7.0" || !got[0].Behind() {
		t.Errorf("bleve came back %+v, want v2.6.0 pinned, v2.7.0 latest and behind", got[0])
	}

	if got[1].Err != nil {
		t.Fatal(got[1].Err)
	}
	if got[1].Latest != "0.26.1" {
		t.Errorf("tantivy's latest is %s, want the newest released version 0.26.1", got[1].Latest)
	}
	if got[1].Behind() {
		t.Error("tantivy is pinned to the latest release and was reported as behind")
	}

	for _, a := range agents {
		if a != UserAgent {
			t.Fatalf("a request went out as %q, and both registries ask for an agent that names the caller", a)
		}
	}
}

// A registry that cannot be reached must not read as up to date. A check that
// passes when it did not run is worse than no check.
func TestAnUnreachableRegistryIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	GoProxy = srv.URL
	defer func() { GoProxy = "https://proxy.golang.org" }()

	dir := t.TempDir()
	writeIn(t, dir, "go.mod", "module m\n\nrequire example.com/thing v1.0.0\n")

	got := Check(context.Background(), srv.Client(),
		Manifest{Engines: []Engine{{Name: "thing", Registry: "gomod", Package: "example.com/thing"}}},
		Repo{GoMod: filepath.Join(dir, "go.mod")})

	if got[0].Err == nil {
		t.Fatal("a registry that answered 500 was treated as a successful check")
	}
	if got[0].Behind() {
		t.Fatal("a failed check reported as behind, which would send somebody looking for a release that may not exist")
	}
}

func TestTheSparseIndexPathIsTheOneCratesIoUses(t *testing.T) {
	for crate, want := range map[string]string{
		"a":         "1/a",
		"ab":        "2/ab",
		"abc":       "3/a/abc",
		"tantivy":   "ta/nt/tantivy",
		"turbovec":  "tu/rb/turbovec",
		"hnsw_rs":   "hn/sw/hnsw_rs",
		"SeekStorm": "se/ek/seekstorm",
	} {
		if got := cratePath(crate); got != want {
			t.Errorf("%s is filed at %s, want %s", crate, got, want)
		}
	}
}

func TestAModulePathWithACapitalIsEscapedForTheProxy(t *testing.T) {
	got := escapeModule("github.com/RoaringBitmap/roaring/v2")
	want := "github.com/!roaring!bitmap/roaring/v2"
	if got != want {
		t.Fatalf("escaped to %s, want %s", got, want)
	}
}

// The manifest in the repository is the one CI reads, so a typo in it should
// fail here rather than in a scheduled run a week later.
func TestTheManifestInTheRepositoryParses(t *testing.T) {
	m, err := Load(filepath.Join("..", "engines.json"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range m.Engines {
		if e.Name == "" || e.Suite == "" || e.Source == "" {
			t.Errorf("%+v is missing a field", e)
		}
		switch e.Registry {
		case "gomod", "crate", "ours":
			// Everything with a registry behind it has to say what to look up
			// there, since an empty package is a lookup of nothing that would
			// report as an error every week until somebody noticed.
			if e.Package == "" {
				t.Errorf("%s is in a registry but names no package", e.Name)
			}
		case "none":
		default:
			t.Errorf("%s has registry %q, want gomod, crate, ours or none", e.Name, e.Registry)
		}
		if seen[e.Name] {
			t.Errorf("%s is in the manifest twice", e.Name)
		}
		seen[e.Name] = true
	}
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	return writeIn(t, t.TempDir(), name, body)
}

func writeIn(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
