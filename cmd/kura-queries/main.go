// Command kura-queries writes the query set for a corpus.
//
//	kura-queries -log queries.dev.small.tsv -n 40 -out queries-msmarco.txt
//	kura-queries -corpus enron.jsonl -out queries-enron.txt
//
// The first form is the one to prefer and the second is the one that is usually
// available. Real queries look nothing like the queries an engineer writes when
// testing a search box: they are one to three words, they are full of jargon
// and names that appear in no dictionary, and the distribution has a very heavy
// head. A query set somebody invented measures whether the engine is good at
// the queries that person imagined.
//
// So when a corpus comes with a real query log, this samples it and nothing
// else happens. When it does not, which is every corpus here except the passage
// collection, the set is built from the corpus itself by measuring how many
// documents each term appears in and picking terms from fixed bands of that
// distribution. That is still a constructed query set and the file says so at
// the top. What it is not is a set of words somebody thought sounded plausible,
// because the property that decides what a query costs an engine is how many
// documents it matches, and that is measured here rather than guessed.
package main

import (
	"bufio"
	"cmp"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/tamnd/kura-bench/corpus"
)

func main() {
	corpusPath := flag.String("corpus", "", "corpus file to build a query set from")
	sample := flag.Int("sample", sampleDocuments, "how many documents the vocabulary is taken from, zero for all of them")
	logPath := flag.String("log", "", "a real query log, one query per line or an id and a query separated by a tab")
	n := flag.Int("n", 40, "how many queries to take from a -log")
	out := flag.String("out", "", "file to write, standard output if empty")
	flag.Parse()

	if err := run(*corpusPath, *logPath, *out, *n, *sample); err != nil {
		fmt.Fprintln(os.Stderr, "kura-queries:", err)
		os.Exit(1)
	}
}

func run(corpusPath, logPath, out string, n, sample int) error {
	if (corpusPath == "") == (logPath == "") {
		return fmt.Errorf("give exactly one of -corpus and -log")
	}

	var text string
	var err error
	if logPath != "" {
		text, err = fromLog(logPath, n)
	} else {
		text, err = fromCorpus(corpusPath, sample)
	}
	if err != nil {
		return err
	}

	if out == "" {
		_, err := os.Stdout.WriteString(text)
		return err
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s written\n", out)
	return nil
}

// fromLog takes the first n queries off a real log.
//
// The first n rather than a sample, because a sample needs a seed and a seed is
// one more thing that has to be the same on two machines for two runs to be
// comparable. The log is not ordered by anything that would bias the head of
// it, so the first n is as good a slice as any and it is reproducible without
// anybody having to remember a number.
func fromLog(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	b.WriteString("# The query set, taken from a real query log.\n")
	b.WriteString("#\n")
	b.WriteString("# These are queries real people typed into a real search engine. Nothing was\n")
	b.WriteString("# chosen, cleaned or grouped: this is the first " + fmt.Sprint(n) + " lines of the log, which is\n")
	b.WriteString("# reproducible without anybody having to agree on a random seed.\n")
	b.WriteString("#\n")
	b.WriteString("# Read the spread rather than the average. A real log is mostly two and three\n")
	b.WriteString("# word queries with a heavy head, so an engine that is quick on the median and\n")
	b.WriteString("# slow on the worst line will look fine here and will not feel fine.\n")
	b.WriteString("# Lines starting with a hash are ignored.\n")

	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() && len(seen) < n {
		line := sc.Text()
		// The log is either one query per line or an identifier and a query
		// separated by a tab, and both forms turn up in the same collection.
		if _, after, ok := strings.Cut(line, "\t"); ok {
			line = after
		}
		q := strings.TrimSpace(line)
		if q == "" || strings.HasPrefix(q, "#") || seen[q] {
			continue
		}
		seen[q] = true
		b.WriteString(q)
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if len(seen) == 0 {
		return "", fmt.Errorf("%s holds no queries", path)
	}
	return b.String(), nil
}

// band is one group of the constructed query set.
//
// The share is the fraction of the corpus a term in this band matches, and it
// is the only thing that decides which terms are picked. It is the property
// that decides what a query costs: a term matching half the corpus is a walk
// down a long posting list, and a term matching a hundred documents is a
// dictionary lookup and a cold read with nothing to walk.
type band struct {
	title string
	why   string
	share float64
	take  int
}

func bands() []band {
	return []band{
		{
			title: "A single very common word",
			why: "the worst case for any engine, and the query that separates one\n" +
				"# that can skip ahead from one that cannot",
			share: 1.0,
			take:  1,
		},
		{
			title: "Very common terms",
			why: "every engine has a long posting list for these and the cost is\n" +
				"# dominated by walking it",
			share: 0.25,
			take:  3,
		},
		{
			title: "Ordinary terms",
			why:   "the middle of the distribution, which is where most traffic lands",
			share: 0.02,
			take:  3,
		},
		{
			title: "Rare terms",
			why: "the posting list is short, so what is measured is the dictionary\n" +
				"# lookup and the cold read rather than the scan",
			share: 0.0005,
			take:  3,
		},
	}
}

// fromCorpus builds a query set out of the corpus itself.
func fromCorpus(path string, sample int) (string, error) {
	documents, df, err := documentFrequency(path, sample)
	if err != nil {
		return "", err
	}
	if documents == 0 {
		return "", fmt.Errorf("%s holds no documents", path)
	}

	// Sorted once, descending by how many documents the term is in, and by the
	// term itself where two are tied, so that the same corpus produces the same
	// query set on every machine.
	terms := make([]string, 0, len(df))
	for term := range df {
		terms = append(terms, term)
	}
	slices.SortFunc(terms, func(a, b string) int {
		if c := cmp.Compare(df[b], df[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})

	pairs, err := commonPairs(path, df, documents)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# The query set for %s, built from the corpus.\n", path)
	b.WriteString("#\n")
	b.WriteString("# This corpus comes with no query log, so there are no real queries for it\n")
	b.WriteString("# and none is invented. Every line below is a term or a pair of adjacent\n")
	b.WriteString("# terms that occurs in the corpus, picked because of how many documents it\n")
	b.WriteString("# is in, which is the property that decides what a query costs an engine.\n")
	b.WriteString("#\n")
	b.WriteString("# That makes this a constructed query set and it is worth saying so. It is\n")
	b.WriteString("# not a set of words somebody thought sounded plausible, and it is not a\n")
	b.WriteString("# record of anything anybody searched for. Regenerate it with kura-queries\n")
	b.WriteString("# rather than editing it, so that it keeps describing the corpus it names.\n")
	b.WriteString("#\n")
	fmt.Fprintf(&b, "# %d documents, %d terms counted", documents, len(df))
	if sample > 0 && sample < documents {
		fmt.Fprintf(&b, ", taken from the first %d documents", sample)
	}
	b.WriteString(". Lines starting with a hash are ignored.\n")

	used := make(map[string]bool)
	for _, band := range bands() {
		fmt.Fprintf(&b, "\n# %s: %s.\n", band.title, band.why)
		want := int(band.share * float64(documents))
		for _, term := range pick(terms, df, want, band.take, used) {
			fmt.Fprintf(&b, "%s\n", term)
		}
	}

	if len(pairs) > 0 {
		b.WriteString("\n# Two word queries, which is what most searches actually look like. Both\n")
		b.WriteString("# halves are common and the pair is one that really does occur in the\n")
		b.WriteString("# corpus, so an engine that intersects and one that unions are both doing\n")
		b.WriteString("# work rather than one of them answering nothing.\n")
		for _, p := range pairs {
			fmt.Fprintf(&b, "%s\n", p)
		}
	}
	return b.String(), nil
}

// pick takes n terms whose document count sits nearest to want.
//
// Nearest rather than above or below, because the bands are a description of
// the distribution rather than a threshold, and a corpus where nothing matches
// a quarter of the documents should still produce a query set rather than a
// gap.
//
// The n are spread across the terms that tie at that count rather than taken
// from where the list happens to be cut. Thousands of terms share a document
// count in the middle of any real corpus, they are in alphabetical order
// because that is what breaks the tie, and taking three adjacent ones produces
// a query set that is three words beginning with the same letter.
func pick(terms []string, df map[string]int, want, n int, used map[string]bool) []string {
	at := slices.IndexFunc(terms, func(t string) bool { return df[t] <= want })
	if at < 0 {
		at = len(terms) - 1
	}

	lo, hi := at, at+1
	for lo > 0 && df[terms[lo-1]] == df[terms[at]] {
		lo--
	}
	for hi < len(terms) && df[terms[hi]] == df[terms[at]] {
		hi++
	}

	out := make([]string, 0, n)
	// Two passes. The first walks the tie group at even intervals, which is
	// where a real corpus finds all n. The second is the fallback for a corpus
	// small enough that the group holds fewer than n usable terms.
	stride := max((hi-lo)/max(n, 1), 1)
	for i := lo; i < hi && len(out) < n; i += stride {
		if term := terms[i]; !used[term] && len(term) >= 3 {
			used[term] = true
			out = append(out, term)
		}
	}
	for step := 0; len(out) < n && step < len(terms); step++ {
		for _, i := range [2]int{hi + step, lo - step - 1} {
			if i < 0 || i >= len(terms) || len(out) == n {
				continue
			}
			term := terms[i]
			if used[term] || len(term) < 3 {
				continue
			}
			used[term] = true
			out = append(out, term)
		}
	}
	slices.Sort(out)
	return out
}

// pairShare is how common both halves of a pair have to be before the pair is
// counted. It exists to keep the second pass from holding a map with an entry
// for every adjacent pair of words in the corpus, which on the passage
// collection would be most of a machine.
const pairShare = 0.01

// pairsWanted is how many two word queries the set gets.
const pairsWanted = 4

// commonPairs finds the adjacent term pairs worth asking for.
//
// Only pairs where both halves are common are counted, for two reasons. The
// cheap one is memory. The real one is that a two word query where one half is
// rare is answered by the rare half alone in every engine here, so it measures
// the same thing the rare band already measures.
func commonPairs(path string, df map[string]int, documents int) ([]string, error) {
	floor := int(pairShare * float64(documents))
	count := make(map[string]int)
	seen := make(map[string]bool)

	_, err := corpus.ReadFile(path, func(d corpus.Document) error {
		clear(seen)
		var previous string
		for _, term := range terms(d.Title + " " + d.Body) {
			if previous != "" && df[previous] >= floor && df[term] >= floor {
				pair := previous + " " + term
				if !seen[pair] {
					seen[pair] = true
					count[pair]++
				}
			}
			previous = term
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	pairs := make([]string, 0, len(count))
	for pair := range count {
		pairs = append(pairs, pair)
	}
	slices.SortFunc(pairs, func(a, b string) int {
		if c := cmp.Compare(count[b], count[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	// The most common pairs in any corpus are pairs of stop words, which is not
	// a query anybody types, so the top of the list is skipped. What is taken
	// is then spread across the rest of it rather than taken from one place,
	// because four pairs at four different selectivities say more than four
	// pairs that all match a third of the corpus.
	if len(pairs) <= pairsWanted {
		slices.Sort(pairs)
		return pairs, nil
	}
	skip := min(len(pairs)/20, 50)
	stride := max((len(pairs)-skip)/(pairsWanted*8), 1)

	out := make([]string, 0, pairsWanted)
	for i := skip; i < len(pairs) && len(out) < pairsWanted; i += stride {
		out = append(out, pairs[i])
	}
	slices.Sort(out)
	return out, nil
}

// sampleDocuments is how many documents the vocabulary is taken from.
//
// Counting every term in the corpus is the obvious implementation and it does
// not survive a real one. Half a million mail messages hold tens of millions of
// distinct terms, because every message identifier, every quoted-printable
// fragment and every mangled address is a term that occurs once, and a map with
// an entry for each of them is gigabytes before it has counted anything worth
// having.
//
// So the vocabulary comes from the first documents and the counting runs over
// all of them. Fifty thousand documents is far more than enough to contain
// every term a query set would want, since a term this misses is one that
// appears in none of the first fifty thousand documents and is therefore rarer
// than the rarest band asks for.
const sampleDocuments = 50000

// documentFrequency counts how many documents each term is in.
//
// It is documents rather than occurrences because that is the number that
// decides how long a posting list is, and the length of the posting list is
// what a query costs.
//
// Two passes. The first fixes the vocabulary from the head of the corpus and
// the second counts those terms over the whole of it, so the counts are exact
// for every term that is counted and the memory is bounded by the sample rather
// than by the corpus.
func documentFrequency(path string, sample int) (int, map[string]int, error) {
	df := make(map[string]int)
	// One map reused across documents. A corpus this size is half a million
	// calls, and allocating a map in each of them measures the allocator.
	seen := make(map[string]bool)

	read := 0
	if _, err := corpus.ReadFile(path, func(d corpus.Document) error {
		read++
		for _, term := range terms(d.Title + " " + d.Body) {
			df[term] = 0
		}
		if sample > 0 && read >= sample {
			return corpus.ErrStop
		}
		return nil
	}); err != nil {
		return 0, nil, err
	}

	documents := 0
	_, err := corpus.ReadFile(path, func(d corpus.Document) error {
		documents++
		clear(seen)
		for _, term := range terms(d.Title + " " + d.Body) {
			if _, want := df[term]; !want || seen[term] {
				continue
			}
			seen[term] = true
			df[term]++
		}
		return nil
	})
	return documents, df, err
}

// maxTermLength drops the things that are not words.
//
// A corpus of real documents contains base64 blobs, minified javascript and
// hashes, and every one of them is a distinct term that appears in exactly one
// document. None of them is a query anybody would type.
const maxTermLength = 24

// terms splits text the way every engine measured here does, near enough.
//
// The engines disagree in the details, and the details do not matter for this,
// because the output is a query set rather than an index. What matters is that
// a term written here is a term the engines will find, which splitting on
// anything that is not a letter or a digit guarantees.
func terms(text string) []string {
	out := make([]string, 0, 256)
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(field) <= maxTermLength {
			out = append(out, field)
		}
	}
	return out
}
