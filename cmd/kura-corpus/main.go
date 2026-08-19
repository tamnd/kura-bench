// Command kura-corpus builds the corpus file every engine is measured on.
//
// Point it at a directory holding checkouts and it writes one JSON lines file.
// Build it once per machine and keep it, because rebuilding it is the slowest
// thing in this repository and the file is what makes two runs comparable.
//
//	kura-corpus -root ~/corpus -out corpus.jsonl
//	kura-corpus -repo linux=/src/linux -repo llvm=/src/llvm-project -out corpus.jsonl
//
// The second form is the one to use for a result anybody else is going to read.
// A corpus that means the same thing on four machines has to be built from the
// same named projects, and naming them is how that gets checked.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tamnd/kura-bench/corpus"
)

func main() {
	root := flag.String("root", "", "directory holding one subdirectory per checkout")
	out := flag.String("out", "corpus.jsonl", "corpus file to write")
	var repos repoList
	flag.Var(&repos, "repo", "a checkout to index, as name=path, repeatable")
	flag.Parse()

	if err := run(*root, repos, *out); err != nil {
		fmt.Fprintln(os.Stderr, "kura-corpus:", err)
		os.Exit(1)
	}
}

// repoList collects the repeated -repo flag.
type repoList []corpus.Repo

func (r *repoList) String() string {
	names := make([]string, 0, len(*r))
	for _, repo := range *r {
		names = append(names, repo.Name)
	}
	return strings.Join(names, ",")
}

func (r *repoList) Set(v string) error {
	name, dir, ok := strings.Cut(v, "=")
	if !ok || name == "" || dir == "" {
		return fmt.Errorf("want name=path, got %q", v)
	}
	*r = append(*r, corpus.Repo{Name: name, Dir: dir})
	return nil
}

func run(root string, repos repoList, out string) error {
	if root == "" && len(repos) == 0 {
		return errors.New("one of -root or -repo is required")
	}
	if root != "" && len(repos) > 0 {
		return errors.New("give either -root or -repo, not both, so that what went into the corpus is unambiguous")
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	var stats corpus.Stats
	if root != "" {
		stats, err = corpus.Write(root, w)
	} else {
		stats, err = corpus.WriteRepos(repos, w)
	}
	if err != nil {
		_ = f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d documents, %.1f MB of text, %.1f MB on disk\n",
		out, stats.Documents,
		float64(stats.Bytes)/(1<<20), float64(info.Size())/(1<<20))
	return nil
}
