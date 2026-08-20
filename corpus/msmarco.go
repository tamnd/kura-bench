package corpus

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// maxPassageLine is the longest line the passage collection is allowed to have.
//
// The passages are a few dozen words each. A line orders of magnitude longer
// than that means the file is not what we think it is, and finding that out
// here is better than finding it out as an out of memory a gigabyte later.
const maxPassageLine = 1 << 20

// buildMSMARCO turns the passage collection into documents and writes the
// queries and the judgments out beside them.
//
// This is the dataset that turns a ranking change from an opinion into a
// number. The other corpora here can say an engine got faster. Only this one
// can say whether it still returns the right thing, because it is the only one
// that arrives with a million queries somebody actually typed and a set of
// judgments saying which passage answered them.
//
// The passages have no titles. That is not an omission in this code, it is what
// the collection is: an identifier and a paragraph. Leaving the title empty
// rather than synthesising one from the first line keeps every engine reading
// the same document, which is the only thing that makes the comparison mean
// anything.
func buildMSMARCO(archive string, enc *json.Encoder, limit int, _ string) (Stats, error) {
	var st Stats

	f, err := os.Open(archive)
	if err != nil {
		return st, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return st, err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return st, fmt.Errorf("corpus: %s holds no collection.tsv", archive)
		}
		if err != nil {
			return st, err
		}
		if path.Base(h.Name) != "collection.tsv" {
			continue
		}

		sc := bufio.NewScanner(tr)
		sc.Buffer(make([]byte, 0, 64<<10), maxPassageLine)
		for sc.Scan() {
			id, body, ok := strings.Cut(sc.Text(), "\t")
			if !ok || body == "" {
				continue
			}
			doc := Document{
				ID:        id,
				Repo:      "msmarco",
				Path:      id,
				Body:      body,
				Extension: "txt",
			}
			if err := enc.Encode(doc); err != nil {
				return st, err
			}
			st.Documents++
			st.Bytes += int64(len(body))
			if limit > 0 && st.Documents >= limit {
				// A limited passage collection is still a fine latency corpus
				// and is no longer a relevance corpus, because a judged passage
				// past the cut cannot be retrieved and the run scores zero on
				// that query for a reason that has nothing to do with ranking.
				// kura-corpus says so on the way out.
				return st, nil
			}
		}
		return st, sc.Err()
	}
}

// extract copies named files out of a gzipped tar into a directory.
//
// The names are matched on the base name, because the two archives this is
// used on disagree about whether their contents sit at the root or under a
// directory, and the caller should not have to know which.
func extract(archive string, names []string, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	want := slices.Clone(names)
	tr := tar.NewReader(gz)
	for len(want) > 0 {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("corpus: %s holds none of %s", archive, strings.Join(want, " "))
		}
		if err != nil {
			return err
		}
		base := path.Base(h.Name)
		at := slices.Index(want, base)
		if h.Typeflag != tar.TypeReg || at < 0 {
			continue
		}
		want = slices.Delete(want, at, at+1)

		dst, err := os.Create(filepath.Join(dir, base))
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, tr); err != nil {
			_ = dst.Close()
			return err
		}
		if err := dst.Close(); err != nil {
			return err
		}
	}
	return nil
}
