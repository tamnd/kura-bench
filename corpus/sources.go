package corpus

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source is one project the standard corpus is built from.
//
// The commit is here rather than the branch because a corpus is only a
// measuring instrument if it is the same instrument on every machine. A run on
// a four core server and a run on a laptop are only comparable if both of them
// indexed the same files, and "the main branch of these six projects" is a
// different set of files on Tuesday than it was on Monday.
type Source struct {
	// Name is the repo field on every document that came out of this project,
	// and it is what a result refers to.
	Name string

	// URL is the clone address.
	URL string

	// Tag is the release the commit belongs to. It is carried alongside the
	// commit because a released version is something a person can reason about
	// and a hex string is not.
	Tag string

	// Commit is what the checkout is verified against after fetching.
	Commit string

	// About says what the project is and why it is in the corpus.
	About string
}

// Sources is the standard corpus, six released projects.
//
// They were chosen for spread rather than for size. Go and Kubernetes are large
// Go trees with a lot of generated code in them, Rust is a very large number of
// very small test files, PostgreSQL and Redis are C with long comment blocks
// that read like prose, and Lucene is the source of a search engine, which
// makes the query set land differently on it than on anything else here.
//
// An engine that is quick on one of these and slow on another is telling you
// something about itself. An engine measured on only one of them is not.
func Sources() []Source {
	return []Source{
		{
			Name:   "go",
			URL:    "https://github.com/golang/go",
			Tag:    "go1.26.6",
			Commit: "1ea5a71ad8ceb7b9f16b4b6f8ea4739a4327dd6e",
			About:  "the Go toolchain and standard library",
		},
		{
			Name:   "kubernetes",
			URL:    "https://github.com/kubernetes/kubernetes",
			Tag:    "v1.36.3",
			Commit: "49c14f82ca9748897f0189be31cbf9c2f4085fc1",
			About:  "a large Go tree with a vendored dependency set",
		},
		{
			Name:   "rust",
			URL:    "https://github.com/rust-lang/rust",
			Tag:    "1.97.1",
			Commit: "bd3cd8fdf9945e13d317642df03363bfa1b4c30e",
			About:  "the Rust compiler and its very large test suite",
		},
		{
			Name:   "postgres",
			URL:    "https://github.com/postgres/postgres",
			Tag:    "REL_18_6",
			Commit: "724edf9bde9d356724ad384a2e196edc3c9f80f7",
			About:  "C with the longest comment blocks in the corpus",
		},
		{
			Name:   "redis",
			URL:    "https://github.com/redis/redis",
			Tag:    "8.10.1",
			Commit: "3399357e7c17b668289386b8a15a3037bc4527b1",
			About:  "a small C tree, which is what a rare term run looks like",
		},
		{
			Name:   "lucene",
			URL:    "https://github.com/apache/lucene",
			Tag:    "releases/lucene/10.5.1",
			Commit: "6bde4304bc737c28212cbae91400a62844834b73",
			About:  "the source of a search engine, indexed by search engines",
		},
	}
}

// LookupSource finds one project by name.
func LookupSource(name string) (Source, error) {
	for _, s := range Sources() {
		if s.Name == name {
			return s, nil
		}
	}
	names := make([]string, 0, len(Sources()))
	for _, s := range Sources() {
		names = append(names, s.Name)
	}
	return Source{}, fmt.Errorf("corpus: no project named %q, there is %s", name, strings.Join(names, " "))
}

// Fetch puts a source's checkout under root and returns where it landed.
//
// It is a shallow fetch of one commit rather than a clone, because the history
// is several gigabytes on some of these and nothing here reads it. A checkout
// that is already at the pinned commit is left alone, so this is cheap to run
// again and is how a machine confirms it is measuring what it thinks it is.
func Fetch(ctx context.Context, s Source, root string, log func(string, ...any)) (string, error) {
	dir := filepath.Join(root, s.Name)
	if at, err := head(ctx, dir); err == nil && at == s.Commit {
		log("%s is already at %s", s.Name, s.Tag)
		return dir, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := git(ctx, dir, "init", "-q"); err != nil {
		return "", err
	}
	// The remote is set rather than added, because adding it a second time is
	// an error and this has to be safe to run again.
	if err := git(ctx, dir, "remote", "remove", "origin"); err != nil {
		// There was no remote, which is the normal case on a fresh directory.
		_ = err
	}
	if err := git(ctx, dir, "remote", "add", "origin", s.URL); err != nil {
		return "", err
	}

	log("fetching %s at %s, %s", s.Name, s.Tag, s.About)
	if err := git(ctx, dir, "fetch", "--depth", "1", "--no-tags", "origin", s.Commit); err != nil {
		return "", err
	}
	if err := git(ctx, dir, "checkout", "-q", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}

	at, err := head(ctx, dir)
	if err != nil {
		return "", err
	}
	if at != s.Commit {
		// This should be impossible, and it is checked anyway. A corpus built
		// from the wrong revision produces numbers that look fine and are not
		// comparable with anybody else's, which is the worst kind of wrong.
		return "", fmt.Errorf("corpus: %s checked out %s, wanted %s", s.Name, at, s.Commit)
	}
	return dir, nil
}

// head is the commit a checkout is sitting on.
func head(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func git(ctx context.Context, dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
