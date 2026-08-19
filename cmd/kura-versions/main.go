// Command kura-versions reports whether every engine measured here is still
// pinned to its latest release.
//
// It runs on a schedule in CI. With -check it exits non zero when something has
// fallen behind, which is what turns a stale pin into an issue somebody sees.
//
//	kura-versions
//	kura-versions -check
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/tamnd/kura-bench/versions"
)

func main() {
	manifest := flag.String("manifest", "engines.json", "the engine manifest")
	root := flag.String("root", ".", "the checkout to read the pins from")
	check := flag.Bool("check", false, "exit non zero if an engine is behind")
	markdown := flag.Bool("markdown", false, "write a markdown table, for pasting into an issue")
	flag.Parse()

	if err := run(*manifest, *root, *check, *markdown); err != nil {
		fmt.Fprintln(os.Stderr, "kura-versions:", err)
		os.Exit(1)
	}
}

func run(manifest, root string, check, markdown bool) error {
	m, err := versions.Load(manifest)
	if err != nil {
		return err
	}

	locks, err := cargoLocks(filepath.Join(root, "runners"))
	if err != nil {
		return err
	}
	repo := versions.Repo{
		GoMod:      filepath.Join(root, "go.mod"),
		CargoLocks: locks,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := &http.Client{Timeout: 30 * time.Second}
	statuses := versions.Check(ctx, client, m, repo)

	if markdown {
		writeMarkdown(os.Stdout, statuses)
	} else {
		writeTable(os.Stdout, statuses)
	}

	behind, failed := 0, 0
	for _, s := range statuses {
		if s.Err != nil {
			failed++
		}
		if s.Behind() {
			behind++
		}
	}
	if check && (behind > 0 || failed > 0) {
		return fmt.Errorf("%d engine(s) behind, %d could not be checked", behind, failed)
	}
	return nil
}

// cargoLocks finds every lock file under the runners directory.
//
// It walks rather than globbing because the Rust runners sit in a cargo
// workspace and a workspace has one lock file a level down from the runner
// crates, while a standalone crate would have its own. A pattern that assumed
// either layout would silently check nothing the day the other one appeared.
func cargoLocks(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// A build directory holds the lock files of vendored dependencies,
		// and those are not pins anybody chose.
		if d.IsDir() && d.Name() == "target" {
			return fs.SkipDir
		}
		if !d.IsDir() && d.Name() == "Cargo.lock" {
			out = append(out, path)
		}
		return nil
	})
	// A checkout without any Rust runner is not an error, it just has no
	// crates to check.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

func writeTable(w *os.File, statuses []versions.Status) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ENGINE\tPACKAGE\tPINNED\tLATEST\tSTATE")
	for _, s := range statuses {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			s.Engine.Name, s.Engine.Package, or(s.Pinned, "-"), or(s.Latest, "-"), state(s))
	}
	_ = tw.Flush()
}

func writeMarkdown(w *os.File, statuses []versions.Status) {
	fmt.Fprintln(w, "| engine | package | pinned | latest | state |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
	for _, s := range statuses {
		pkg := "-"
		if s.Engine.Package != "" {
			pkg = "`" + s.Engine.Package + "`"
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
			s.Engine.Name, pkg, or(s.Pinned, "-"), or(s.Latest, "-"), state(s))
	}
}

func state(s versions.Status) string {
	switch {
	case s.Err != nil:
		return "could not check: " + s.Err.Error()
	case s.Behind():
		return "behind"
	case s.Engine.Registry == "none":
		return "nothing to track"
	default:
		return "current"
	}
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
