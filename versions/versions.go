// Package versions answers one question: is every engine measured here still
// the version people are actually running.
//
// A benchmark goes stale quietly. The numbers stay in the README, the engines
// keep releasing, and a year later the table is describing software nobody has
// installed. So the pinned version of every engine is compared against its
// registry on a schedule, and something that has fallen behind turns into an
// issue instead of into a footnote nobody writes.
package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Engine is one entry of engines.json.
type Engine struct {
	Name     string `json:"name"`
	Suite    string `json:"suite"`
	Language string `json:"language"`

	// Registry is where the latest version comes from: gomod, crate, or ours
	// for an engine of our own that is pinned to a commit and has nothing to
	// be behind.
	Registry string `json:"registry"`

	// Package is the module path or the crate name.
	Package string `json:"package"`

	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}

// Manifest is engines.json.
type Manifest struct {
	Engines []Engine `json:"engines"`
}

// Load reads a manifest.
func Load(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(m.Engines) == 0 {
		return Manifest{}, fmt.Errorf("%s: no engines in it", path)
	}
	return m, nil
}

// Status is one engine's answer.
type Status struct {
	Engine Engine
	Pinned string
	Latest string

	// Err is set when the registry could not be reached or the pin could not be
	// found. A network failure is reported rather than treated as up to date,
	// because a check that passes when it did not run is worse than no check.
	Err error
}

// Behind reports whether the pin is not the latest release. It is deliberately
// a string comparison rather than a semantic version ordering: the only thing
// worth acting on is "the registry has something else", and deciding whether
// 0.26.1 is newer than 0.24.2 is not this program's job to get subtly wrong.
func (s Status) Behind() bool {
	return s.Err == nil && s.Pinned != "" && s.Latest != "" && s.Pinned != s.Latest
}

// Repo is the checkout being inspected.
type Repo struct {
	// GoMod is the path to go.mod.
	GoMod string

	// CargoLocks are the Cargo.lock files of the Rust runners. A crate is
	// looked up in each of them, since a runner that does not depend on it
	// simply will not have it.
	CargoLocks []string
}

// Check looks up every engine in the manifest.
func Check(ctx context.Context, client *http.Client, m Manifest, repo Repo) []Status {
	out := make([]Status, 0, len(m.Engines))
	for _, e := range m.Engines {
		out = append(out, check(ctx, client, e, repo))
	}
	return out
}

func check(ctx context.Context, client *http.Client, e Engine, repo Repo) Status {
	s := Status{Engine: e}

	switch e.Registry {
	case "gomod":
		v, err := PinnedGoMod(repo.GoMod, e.Package)
		if err != nil {
			s.Err = err
			return s
		}
		s.Pinned = v
		s.Latest, s.Err = LatestGoMod(ctx, client, e.Package)

	case "crate":
		v, err := pinnedCrate(repo.CargoLocks, e.Package)
		if err != nil {
			s.Err = err
			return s
		}
		s.Pinned = v
		s.Latest, s.Err = LatestCrate(ctx, client, e.Package)

	case "ours":
		// Ours, pinned to a commit. The module proxy's idea of the latest
		// version of a repository we push to several times a day is not a
		// freshness signal, it is noise.
		s.Pinned, s.Err = PinnedGoMod(repo.GoMod, e.Package)
		s.Latest = s.Pinned

	default:
		s.Err = fmt.Errorf("%s: unknown registry %q", e.Name, e.Registry)
	}
	return s
}

// requireLine matches a single require entry in a go.mod, in either the block
// form or the one line form, and ignores an // indirect marker.
var requireLine = regexp.MustCompile(`(?m)^\s*(?:require\s+)?([^\s/]\S*)\s+(v\S+)`)

// PinnedGoMod finds what a module is pinned to in a go.mod.
func PinnedGoMod(path, module string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, m := range requireLine.FindAllStringSubmatch(string(b), -1) {
		if m[1] == module {
			return m[2], nil
		}
	}
	return "", fmt.Errorf("%s does not require %s", path, module)
}

// pinnedCrate finds a crate's resolved version in the first lock file that has
// it.
func pinnedCrate(locks []string, crate string) (string, error) {
	for _, path := range locks {
		v, err := PinnedCargoLock(path, crate)
		if err == nil {
			return v, nil
		}
		if !os.IsNotExist(err) && !strings.Contains(err.Error(), "does not contain") {
			return "", err
		}
	}
	return "", fmt.Errorf("no lock file contains %s, has cargo build been run", crate)
}

// PinnedCargoLock finds a crate's resolved version in a Cargo.lock.
//
// The lock file is the right place to read rather than Cargo.toml, because the
// manifest says 0.26 and the thing that actually got compiled into the binary
// is 0.26.1.
func PinnedCargoLock(path, crate string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var name, version string
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "[[package]]":
			name, version = "", ""
		case strings.HasPrefix(line, "name = "):
			name = unquote(strings.TrimPrefix(line, "name = "))
		case strings.HasPrefix(line, "version = "):
			version = unquote(strings.TrimPrefix(line, "version = "))
		}
		if name == crate && version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("%s does not contain %s", path, crate)
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"`)
}

// GoProxy is where module versions are looked up. It is a variable so a test
// can point it somewhere it controls.
var GoProxy = "https://proxy.golang.org"

// CrateIndex is the crates.io sparse index. The registry's JSON API asks
// callers not to poll it, and the sparse index is the endpoint meant for
// machines.
var CrateIndex = "https://index.crates.io"

// UserAgent identifies this check to the registries, which both ask for one.
const UserAgent = "kura-bench version check (+https://github.com/tamnd/kura-bench)"

// LatestGoMod asks the module proxy for the newest release of a module.
func LatestGoMod(ctx context.Context, client *http.Client, module string) (string, error) {
	url := GoProxy + "/" + escapeModule(module) + "/@latest"
	body, err := get(ctx, client, url)
	if err != nil {
		return "", err
	}
	var v struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", fmt.Errorf("%s: %w", url, err)
	}
	if v.Version == "" {
		return "", fmt.Errorf("%s: no version in the answer", url)
	}
	return v.Version, nil
}

// LatestCrate asks the crates.io sparse index for the newest release of a
// crate.
//
// Yanked versions are skipped, and so is anything with a pre-release suffix,
// since pinning a benchmark to a release candidate would measure something
// nobody is running.
func LatestCrate(ctx context.Context, client *http.Client, crate string) (string, error) {
	url := CrateIndex + "/" + cratePath(crate)
	body, err := get(ctx, client, url)
	if err != nil {
		return "", err
	}

	latest := ""
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v struct {
			Version string `json:"vers"`
			Yanked  bool   `json:"yanked"`
		}
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return "", fmt.Errorf("%s: %w", url, err)
		}
		if v.Yanked || strings.Contains(v.Version, "-") {
			continue
		}
		// The index is in publication order, so the last one standing is the
		// newest.
		latest = v.Version
	}
	if latest == "" {
		return "", fmt.Errorf("%s: no released version", url)
	}
	return latest, nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// cratePath is the sparse index layout: crates are filed by the length and the
// first characters of their name.
func cratePath(crate string) string {
	c := strings.ToLower(crate)
	switch {
	case len(c) <= 2:
		return fmt.Sprintf("%d/%s", len(c), c)
	case len(c) == 3:
		return fmt.Sprintf("3/%s/%s", c[:1], c)
	default:
		return fmt.Sprintf("%s/%s/%s", c[:2], c[2:4], c)
	}
}

// escapeModule applies the module proxy's case encoding, where an upper case
// letter becomes an exclamation mark and the lower case letter. Without it a
// module with a capital in its path is a 404.
func escapeModule(module string) string {
	var b strings.Builder
	for _, r := range module {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + 32)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
