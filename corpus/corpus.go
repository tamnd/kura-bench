// Package corpus builds and reads the document set every engine is measured on.
//
// The corpus is one JSON lines file. That is the whole point of it. An earlier
// attempt measured each engine by pointing it at a directory tree, and the
// numbers that came back were mostly a measurement of the filesystem: a first
// pass over a checkout on Windows with a virus scanner in the way ran two orders
// of magnitude slower than a second pass over the same files, and no amount of
// staring at the engine explained the difference. Reading one sequential file
// that fits in the page cache removes that variable. What is left is the engine.
//
// It also makes the comparison fair in a way a directory does not. Engines
// disagree about which files are worth reading, how big a file has to be before
// it is skipped, and what counts as text. Deciding all of that once, here, means
// every engine is handed exactly the same documents in exactly the same order.
package corpus

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxDocumentSize is the largest file taken into the corpus.
//
// Past a megabyte a checked in file is almost always generated, and a handful of
// them are large enough to dominate a total that is meant to describe a hundred
// thousand documents.
const MaxDocumentSize = 1 << 20

// Document is one record of the corpus.
//
// The fields are the ones every engine here can represent. Anything an engine
// cannot store is left out rather than approximated, so that no engine is
// charged for work another one is not doing.
type Document struct {
	// ID is unique across the corpus and stable between builds, so that two
	// runs on two machines are comparable.
	ID string `json:"id"`

	// Repo is the checkout the document came from, which is what makes a corpus
	// built from several projects still describable.
	Repo string `json:"repo"`

	// Path is the file path inside that checkout, using forward slashes.
	Path string `json:"path"`

	// Title is what a result list would show.
	Title string `json:"title"`

	// Body is the file contents.
	Body string `json:"body"`

	// Extension is the file extension without the dot, which is the one field
	// worth having as a filter because every engine can index it cheaply.
	Extension string `json:"ext"`
}

// Stats describes a corpus.
type Stats struct {
	Documents int   `json:"documents"`
	Bytes     int64 `json:"bytes"`
}

// Repo is one checkout to take into the corpus.
type Repo struct {
	// Name is what the repo field carries, and it is what a result refers to.
	// It is given rather than derived so that a corpus built from checkouts
	// scattered around a machine still names them the way people do.
	Name string

	// Dir is the checkout on disk.
	Dir string
}

// Write walks the checkouts under root and writes a corpus file.
//
// Each immediate subdirectory of root is treated as one project, and its name
// becomes the repo field. Nothing else about the layout matters.
func Write(root string, w io.Writer) (Stats, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Stats{}, err
	}

	var repos []Repo
	for _, e := range entries {
		if !e.IsDir() || skipDir(e.Name()) {
			continue
		}
		repos = append(repos, Repo{Name: e.Name(), Dir: filepath.Join(root, e.Name())})
	}
	if len(repos) == 0 {
		return Stats{}, fmt.Errorf("corpus: nothing to index under %s, is there a checkout in it", root)
	}
	return WriteRepos(repos, w)
}

// WriteRepos writes a corpus from checkouts that are named explicitly.
//
// This is the form worth using for a published result. A corpus that means the
// same thing on four machines has to be built from the same named projects at
// the same revisions, and that is easier to state as a list than as a directory
// that happens to contain the right things.
func WriteRepos(repos []Repo, w io.Writer) (Stats, error) {
	bw := bufio.NewWriterSize(w, 1<<20)
	enc := json.NewEncoder(bw)
	var st Stats

	for _, r := range repos {
		got, err := writeRepo(r.Dir, r.Name, enc)
		if err != nil {
			return st, err
		}
		st.Documents += got.Documents
		st.Bytes += got.Bytes
	}

	if st.Documents == 0 {
		return st, fmt.Errorf("corpus: no documents found in any of the %d checkouts given", len(repos))
	}
	return st, bw.Flush()
}

func writeRepo(dir, repo string, enc *json.Encoder) (Stats, error) {
	var st Stats
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A path that cannot be read is a fact about the checkout. Failing
			// the whole build over one of them would make the corpus depend on
			// which machine unpacked it.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != dir && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !include(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > MaxDocumentSize {
			return nil
		}

		body, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		// A file that is not valid UTF-8 is a binary that happens to have a text
		// extension. Engines differ wildly in what they do with those, and none
		// of it is interesting.
		if !utf8.Valid(body) {
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		doc := Document{
			ID:        repo + ":" + rel,
			Repo:      repo,
			Path:      rel,
			Title:     rel,
			Body:      string(body),
			Extension: strings.TrimPrefix(path.Ext(rel), "."),
		}
		if err := enc.Encode(doc); err != nil {
			return err
		}
		st.Documents++
		st.Bytes += int64(len(body))
		return nil
	})
	return st, err
}

// ErrStop is returned by the callback of [Read] to stop early without making
// it an error. It exists so that a runner asked for a fixed number of documents
// does not have to read the rest of a multi gigabyte file to find out it is
// done.
var ErrStop = errors.New("corpus: stop")

// Read calls fn for every document in a corpus file.
//
// It streams rather than returning a slice, because the corpus is larger than
// the heap most of these engines want to be measured with, and a runner that
// held the whole thing in memory before indexing it would be measuring
// something nobody deploys.
func Read(r io.Reader, fn func(Document) error) (Stats, error) {
	var st Stats
	sc := bufio.NewScanner(r)
	// Buffer for the largest line a corpus can hold: a document at the size
	// limit, plus the escaping JSON adds to it in the worst case.
	sc.Buffer(make([]byte, 0, 1<<20), 8*MaxDocumentSize)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var d Document
		if err := json.Unmarshal(line, &d); err != nil {
			return st, fmt.Errorf("corpus: line %d: %w", st.Documents+1, err)
		}
		if err := fn(d); err != nil {
			if errors.Is(err, ErrStop) {
				return st, nil
			}
			return st, err
		}
		st.Documents++
		st.Bytes += int64(len(d.Body))
	}
	if err := sc.Err(); err != nil {
		return st, err
	}
	if st.Documents == 0 {
		return st, errors.New("corpus: file is empty")
	}
	return st, nil
}

// ReadFile is Read on a path.
func ReadFile(name string, fn func(Document) error) (Stats, error) {
	f, err := os.Open(name)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = f.Close() }()
	return Read(f, fn)
}

// skipDir drops version control, dependency and build directories. They hold
// far more files than the content does and none of the prose.
func skipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", "build",
		".venv", "__pycache__", ".idea", ".vscode":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// include is the list of extensions taken into the corpus. It is deliberately
// the same list the platform's directory connector uses, so that a number
// measured here describes the same work.
func include(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	switch name {
	case "OWNERS", "README", "LICENSE", "Makefile":
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".rst", ".adoc", ".txt", ".html",
		".go", ".rs", ".py", ".js", ".ts", ".tsx", ".jsx", ".c", ".h", ".cc", ".cpp",
		".java", ".rb", ".sh", ".sql", ".yaml", ".yml", ".toml", ".json", ".proto":
		return true
	}
	return false
}
