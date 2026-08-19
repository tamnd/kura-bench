// Command kura-corpus builds the corpus file every engine is measured on.
//
// Point it at a directory holding checkouts and it writes one JSON lines file.
// Build it once per machine and keep it, because rebuilding it is the slowest
// thing in this repository and the file is what makes two runs comparable.
//
//	kura-corpus -src ~/corpus-src -out corpus.jsonl
//	kura-corpus -root ~/corpus -out corpus.jsonl
//	kura-corpus -repo linux=/src/linux -repo llvm=/src/llvm-project -out corpus.jsonl
//
// The first form is the one to use for a result anybody else is going to read.
// It fetches six released projects at the commits pinned in corpus/sources.go
// and builds the corpus from those, so the same command on four machines
// produces the same file. The other two forms are for trying something out on
// checkouts that are already on the machine.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tamnd/kura-bench/corpus"
)

func main() {
	root := flag.String("root", "", "directory holding one subdirectory per checkout")
	src := flag.String("src", "", "fetch the standard projects into this directory and build the corpus from them")
	out := flag.String("out", "corpus.jsonl", "corpus file to write")
	var repos repoList
	flag.Var(&repos, "repo", "a checkout to index, as name=path, repeatable")
	flag.Parse()

	if err := run(*root, repos, *src, *out); err != nil {
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

func run(root string, repos repoList, src, out string) error {
	if src != "" {
		if root != "" || len(repos) > 0 {
			return errors.New("give either -src or one of -root and -repo, so that what went into the corpus is unambiguous")
		}
		var err error
		if repos, err = fetchAll(src); err != nil {
			return err
		}
	}
	if root == "" && len(repos) == 0 {
		return errors.New("one of -src, -root or -repo is required")
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

// fetchAll puts every standard project under src and returns them in the order
// they are listed, which is the order the corpus is written in.
func fetchAll(src string) (repoList, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()

	log := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
	var repos repoList
	for _, s := range corpus.Sources() {
		dir, err := corpus.Fetch(ctx, s, src, log)
		if err != nil {
			return nil, err
		}
		repos = append(repos, corpus.Repo{Name: s.Name, Dir: dir})
	}
	return repos, nil
}
