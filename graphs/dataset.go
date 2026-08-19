// Package graphs prepares real graphs for the graph suite.
//
// The datasets are from the Stanford Large Network Dataset Collection, which
// is where the graph papers get theirs. They are real networks with real degree
// distributions, and that matters more here than in any other suite: a graph
// with a uniform degree distribution makes every traversal look the same, and
// the whole reason one store beats another is what happens when a handful of
// nodes have a hundred thousand neighbours and most have three.
package graphs

import (
	"fmt"
	"path/filepath"
	"sort"
)

// A Dataset is one published graph, described down to the byte.
//
// The archive checksum is pinned for the same reason the vector datasets pin
// theirs. A graph that is missing a few thousand edges answers every query
// slightly faster and slightly wrongly, and nothing in a timing table would
// show it.
type Dataset struct {
	// Name is what the flag takes and what the directory is called.
	Name string

	// About is a sentence for the report, saying what the graph is.
	About string

	// URL is the gzipped edge list, as published.
	URL string

	// ArchiveBytes and SHA256 are the compressed download, which is the thing
	// that can be verified before anything has been parsed.
	ArchiveBytes int64
	SHA256       string

	// Nodes and Edges are what the file's own header claims, and what the
	// converter checks its parse against.
	Nodes int
	Edges int

	// Undirected says the publisher stored an undirected graph with both
	// directions of every edge written out. It changes nothing about how the
	// file is read and it is worth recording, because a reachable set on an
	// undirected graph is a connected component and on a directed one it is
	// not.
	Undirected bool
}

// Datasets are the ones that can be fetched.
//
// Three sizes on purpose. ca-GrQc is small enough to run on every pull request
// and is still a real collaboration network. web-Google is the size most people
// actually have and has the skewed degree distribution that separates the
// stores. soc-LiveJournal1 is large enough that an engine which was quietly
// holding the whole thing in memory has to admit it.
var Datasets = map[string]Dataset{
	"ca-grqc": {
		Name:         "ca-grqc",
		About:        "The Arxiv General Relativity collaboration network, 5,242 authors and 14,490 collaborations stored in both directions",
		URL:          "https://snap.stanford.edu/data/ca-GrQc.txt.gz",
		ArchiveBytes: 109_261,
		SHA256:       "a254442cdf5d684712578b630c2e0d7543518ab154ef2341cabb607572ce7230",
		Nodes:        5_242,
		Edges:        28_980,
		Undirected:   true,
	},
	"web-google": {
		Name:         "web-google",
		About:        "The Google web graph from the 2002 programming contest, 875,713 pages and 5,105,039 links",
		URL:          "https://snap.stanford.edu/data/web-Google.txt.gz",
		ArchiveBytes: 21_168_784,
		SHA256:       "bcac0af0471d749f4a8c010bca92b61cf2868a0570741de06892fc062f265ea6",
		Nodes:        875_713,
		Edges:        5_105_039,
	},
	"soc-livejournal": {
		Name:         "soc-livejournal",
		About:        "The LiveJournal friendship graph, 4,847,571 members and 68,993,773 declared friendships",
		URL:          "https://snap.stanford.edu/data/soc-LiveJournal1.txt.gz",
		ArchiveBytes: 259_619_239,
		SHA256:       "d7bcd5a87b88c896c35fdb9611e804c3f4033c39b58c4c9ea3ba53c680d516d8",
		Nodes:        4_847_571,
		Edges:        68_993_773,
	},
}

// Names lists the datasets in a stable order, for a flag's help text and for
// anything that prints them.
func Names() []string {
	out := make([]string, 0, len(Datasets))
	for name := range Datasets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Lookup finds a dataset by name and says what the choices were when it does
// not exist, since a typo in a flag should not turn into a download of nothing.
func Lookup(name string) (Dataset, error) {
	d, ok := Datasets[name]
	if !ok {
		return Dataset{}, fmt.Errorf("no graph called %q, there is %v", name, Names())
	}
	return d, nil
}

// The three files a prepared dataset consists of.
//
// Every runner is handed the same three paths and none of them ever sees the
// published text file. Parsing a gzipped tab separated list is the same work
// for every engine and it is not the work being measured, so it happens once.
const (
	// EdgeFile is the whole graph as fixed width binary, described in
	// edgefile.go.
	EdgeFile = "edges.bin"

	// SeedFile is the nodes every runner is asked about, in the order it has to
	// ask about them. It is a file rather than a rule so that there is nothing
	// for two implementations of the rule to disagree about.
	SeedFile = "seeds.bin"

	// AnswerFile is what the operations should come back with, worked out once
	// here so that an engine that is fast because it is wrong is caught.
	AnswerFile = "answers.json"
)

// Dir is where a dataset's files live under a root directory.
func (d Dataset) Dir(root string) string { return filepath.Join(root, d.Name) }

// Path is where one of a dataset's files lives, named by [EdgeFile], [SeedFile]
// or [AnswerFile].
func (d Dataset) Path(root, name string) string { return filepath.Join(d.Dir(root), name) }
