package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"time"
)

// Run is what the orchestrator knows about a run and a runner cannot.
//
// A number is only worth arguing with if somebody who is not us can produce it
// again, and that takes more than the engine and the machine. It takes the
// exact input, which is what the checksums are for, and the exact code, which
// is what the commit is for. Without them a table is a recollection: the corpus
// builder changed, or a branch was half merged, and a year later there is no
// way to tell which.
//
// It is filled in by the orchestrator rather than by the runners because it is
// the same for all of them. Asking three languages to each compute a checksum
// of the same file would be three chances to compute it differently, and a
// checksum that two implementations disagree about is worse than none.
type Run struct {
	// Corpus is the path every engine was given and CorpusSHA256 its digest.
	// The path is a convenience for a human reading the file and the digest is
	// the part that means anything, since two machines with different paths can
	// still prove they ran the same bytes.
	Corpus       string `json:"corpus"`
	CorpusSHA256 string `json:"corpus_sha256,omitempty"`
	CorpusBytes  int64  `json:"corpus_bytes,omitempty"`

	// Queries is the query file, digested for the same reason. A table that
	// moved because somebody regenerated the query set is not a regression, and
	// this is what tells the two apart.
	Queries       string `json:"queries"`
	QueriesSHA256 string `json:"queries_sha256,omitempty"`

	// Commit is the revision the orchestrator was built from, and Modified says
	// the tree had uncommitted changes at the time.
	//
	// It comes from the build information the toolchain embeds rather than from
	// asking git at run time, because the binary that produced the numbers is
	// the fact worth recording and the checkout it is running next to may have
	// moved on since. A number taken from a modified tree is not reproducible
	// and the field says so rather than implying a commit that does not
	// describe the code that ran.
	Commit   string `json:"commit,omitempty"`
	Modified bool   `json:"modified,omitempty"`

	// Started is when the run began, in UTC. Dates matter here more than they
	// look like they should, because the machines these run on are shared and
	// the answer to why a table moved is often what else was happening that
	// afternoon.
	Started time.Time `json:"started"`

	// The parameters, all of them, because a latency measured at a page of a
	// hundred against one measured at a page of ten is not a comparison and a
	// reader needs to be able to see that without being told.
	Repeat  int `json:"repeat"`
	Workers int `json:"workers,omitempty"`
	Depth   int `json:"depth"`
	Limit   int `json:"limit,omitempty"`
}

// Describe fills in the parts of a run that come off the disk and out of the
// binary. The parameters are the caller's to set.
//
// A file it cannot read leaves the digest empty rather than failing the run,
// because the corpus is about to be opened by an engine anyway and that is a
// better place to find out it is missing than here.
func (r Run) Describe() Run {
	r.Started = time.Now().UTC()
	r.CorpusSHA256, r.CorpusBytes = digest(r.Corpus)
	r.QueriesSHA256, _ = digest(r.Queries)
	r.Commit, r.Modified = commit()
	return r
}

// digest is the SHA-256 of a file and its size.
func digest(path string) (string, int64) {
	if path == "" {
		return "", 0
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0
	}
	return hex.EncodeToString(h.Sum(nil)), n
}

// commit reads the revision out of the build information.
//
// A binary built with `go build` in a checkout carries it. One built from a
// tarball or with the stamping turned off does not, and gets an empty string,
// which is honest about the run not being traceable rather than pretending.
func commit() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var rev string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return rev, modified
}
