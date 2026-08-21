// Command kura-corpus builds the corpus files every engine is measured on.
//
// It writes one JSON lines file. Build it once per machine and keep it, because
// rebuilding it is the slowest thing in this repository and the file is what
// makes two runs comparable.
//
//	kura-corpus -src ~/corpus-src -out corpus.jsonl
//	kura-corpus -dataset enron -cache ~/bench/cache -out enron.jsonl
//	kura-corpus -datasets
//	kura-corpus -root ~/corpus -out corpus.jsonl
//	kura-corpus -repo linux=/src/linux -repo llvm=/src/llvm-project -out corpus.jsonl
//
// The first two forms are the ones to use for a result anybody else is going to
// read. The -src form fetches six released projects at the commits pinned in
// corpus/sources.go, and the -dataset form downloads a published corpus and
// verifies its checksum, so the same command on four machines produces the same
// file either way. The other two forms are for trying something out on
// checkouts that are already on the machine.
//
// One corpus is not enough. Source code, email and encyclopaedia articles have
// document lengths, vocabularies and duplication rates that differ by more than
// an order of magnitude, and an engine that is quick on one of them and slow on
// another is telling you something about itself that a single number hides.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamnd/kura-bench/corpus"
)

func main() {
	root := flag.String("root", "", "directory holding one subdirectory per checkout")
	src := flag.String("src", "", "fetch the standard projects into this directory and build the corpus from them")
	dataset := flag.String("dataset", "", "download a published corpus and build from it, see -datasets")
	cache := flag.String("cache", "", "where downloaded archives are kept, defaults to next to -out")
	list := flag.Bool("datasets", false, "print the published corpora and what each one is for")
	limit := flag.Int("limit", 0, "stop after this many documents of a -dataset, 0 for all of them")
	out := flag.String("out", "corpus.jsonl", "corpus file to write")
	var repos repoList
	flag.Var(&repos, "repo", "a checkout to index, as name=path, repeatable")
	flag.Parse()

	if *list {
		printDatasets()
		return
	}
	if err := run(*root, repos, *src, *dataset, *cache, *out, *limit); err != nil {
		fmt.Fprintln(os.Stderr, "kura-corpus:", err)
		os.Exit(1)
	}
}

// printDatasets is the answer to "which corpora are there and why".
//
// It prints the licence line next to each one because two of these carry real
// obligations, and a person choosing a corpus should see that at the moment
// they choose rather than in a file they will not open.
func printDatasets() {
	for _, d := range corpus.Datasets() {
		fmt.Printf("%-12s %6.0f MB  %s\n", d.Name, float64(d.Bytes)/(1<<20), d.About)
		fmt.Printf("%-12s %9s  licence: %s\n", "", "", d.Licence)
		if !d.Public {
			fmt.Printf("%-12s %9s  nothing from this corpus leaves the machine it was built on\n", "", "")
		}
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

func run(root string, repos repoList, src, dataset, cache, out string, limit int) error {
	if dataset != "" && (src != "" || root != "" || len(repos) > 0) {
		return errors.New("give -dataset on its own, so that what went into the corpus is unambiguous")
	}
	if src != "" {
		if root != "" || len(repos) > 0 {
			return errors.New("give either -src or one of -root and -repo, so that what went into the corpus is unambiguous")
		}
		var err error
		if repos, err = fetchAll(src); err != nil {
			return err
		}
	}
	if dataset == "" && root == "" && len(repos) == 0 {
		return errors.New("one of -dataset, -src, -root or -repo is required")
	}
	if root != "" && len(repos) > 0 {
		return errors.New("give either -root or -repo, not both, so that what went into the corpus is unambiguous")
	}

	var chosen corpus.Dataset
	var archive string
	if dataset != "" {
		var err error
		if chosen, archive, err = fetchDataset(dataset, cache, out); err != nil {
			return err
		}
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)

	var stats corpus.Stats
	switch {
	case dataset != "":
		stats, err = corpus.WriteDataset(chosen, archive, w, limit, out)
	case root != "":
		stats, err = corpus.Write(root, w)
	default:
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

	// Written every time, including for a corpus built out of source checkouts,
	// because the benchmark treats a corpus it knows nothing about as one it may
	// not quote from. A label saying "public" is how an ordinary corpus keeps
	// its document identifiers.
	label := corpus.Label{Public: true, Documents: stats.Documents, Bytes: stats.Bytes}
	if dataset != "" {
		label.Dataset = chosen.Name
		label.Licence = chosen.Licence
		label.Public = chosen.Public
	}
	if err := corpus.WriteLabel(out, label); err != nil {
		return err
	}

	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d documents, %.1f MB of text, %.1f MB on disk\n",
		out, stats.Documents,
		float64(stats.Bytes)/(1<<20), float64(info.Size())/(1<<20))
	if limit > 0 && stats.Documents >= limit {
		fmt.Printf("%s: stopped at the -limit, so this file is a latency corpus and not a relevance corpus\n", out)
	}
	if dataset != "" && !chosen.Public {
		fmt.Printf("%s: %s\n", out, chosen.Licence)
	}
	return nil
}

// fetchDataset downloads one published corpus and says where it landed.
//
// The cache defaults to sitting next to the corpus file rather than to a
// directory under the home directory, because the machines this runs on are
// shared and a gigabyte that appears in somebody's home directory without them
// asking is a gigabyte they will find in six months and wonder about.
func fetchDataset(name, cache, out string) (corpus.Dataset, string, error) {
	d, err := corpus.LookupDataset(name)
	if err != nil {
		return d, "", err
	}
	if cache == "" {
		cache = filepath.Join(filepath.Dir(out), "cache")
	}

	ctx, cancel := context.WithTimeout(context.Background(), corpus.DatasetTimeout)
	defer cancel()

	log := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
	archive, err := corpus.FetchDataset(ctx, d, cache, log)
	return d, archive, err
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
