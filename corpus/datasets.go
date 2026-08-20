package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Dataset is a corpus that is downloaded rather than cloned.
//
// The six projects in sources.go are all source code, and a corpus of source
// code teaches one lesson: long documents, a small vocabulary, and a term
// distribution shaped by a language grammar rather than by a person writing.
// Every other content type a search engine is asked to hold looks different.
// Email is short, repetitive and quoted. Encyclopaedia articles are long, dense
// and full of markup. A passage collection is a few dozen words per document
// across millions of documents, which is the shape that makes a posting list
// long and a document short, and that is where a scorer's per document cost
// stops hiding behind its per posting cost.
//
// An engine measured on one shape is not measured.
type Dataset struct {
	// Name is what -dataset takes and what the corpus file is named after.
	Name string

	// URL is the archive to download. It is a direct link to a file rather
	// than a page about the file, because a fetch that needs a person in front
	// of it is a fetch that does not happen in CI.
	URL string

	// SHA256 is the checksum of the downloaded archive, lower case hex.
	//
	// It is here for the same reason sources.go pins a commit. A corpus is a
	// measuring instrument, and two runs are only comparable if both of them
	// read the same bytes. It also catches the case where a publisher replaces
	// a file in place, which happens more often than anybody expects.
	//
	// An empty checksum means the download is not verified, which is only ever
	// right while a new dataset is being added.
	SHA256 string

	// Bytes is the download size, so that a person can tell before starting
	// whether the machine has room for it.
	Bytes int64

	// Licence is the position on redistribution, in one line.
	Licence string

	// Public says whether content from this dataset may appear outside the
	// machine it was built on. Aggregate numbers are always fine. A sample
	// document, a query that matched, or a failing test that prints a document
	// is not, for anything marked false.
	Public bool

	// About says what the dataset is and what it is uniquely good for, which
	// is the part that decides whether a number measured on it means anything.
	About string

	// Sidecar names files inside the archive that are written out next to the
	// corpus rather than turned into documents. Relevance judgments arrive
	// this way: they are part of the download and they are not documents.
	Sidecar []string

	// Build turns the downloaded archive into corpus documents.
	//
	// It is handed the archive path rather than a reader because two of these
	// need to walk an archive twice, and re-opening a file is cheaper than
	// buffering a gigabyte to rewind it.
	Build func(archive string, enc *json.Encoder, limit int, out string) (Stats, error)
}

// Datasets is every downloadable corpus, in the order they are worth pulling.
//
// The order is by how soon each one changes a decision rather than by size.
// Enron is first because it is half a million short documents with real
// duplication in them and it downloads in a few minutes. MS MARCO is second
// because it brings the queries and the judgments, and a ranking change with
// no judged query set behind it is a preference rather than an improvement.
// Wikipedia is third because it is the cheapest way to get documents long
// enough and numerous enough that index size stops being a rounding error.
func Datasets() []Dataset {
	return []Dataset{
		{
			Name:    "enron",
			URL:     "https://www.cs.cmu.edu/~enron/enron_mail_20150507.tar.gz",
			SHA256:  "",
			Bytes:   443254787,
			Licence: "released in a federal investigation, real personal data, local use only",
			Public:  false,
			About: "half a million real corporate emails, which is the only public corpus " +
				"with genuine threads, genuine quoting and genuine duplication in it",
			Build: buildEnron,
		},
		{
			Name:    "msmarco",
			URL:     "https://msmarco.z22.web.core.windows.net/msmarcoranking/collectionandqueries.tar.gz",
			SHA256:  "",
			Bytes:   1057717952,
			Licence: "Microsoft research licence, check it before anything ships",
			Public:  true,
			About: "8.8 million passages with a million real queries typed by real people " +
				"and the judgments that say which passage answered which query",
			Sidecar: []string{"queries.dev.small.tsv", "qrels.dev.small.tsv"},
			Build:   buildMSMARCO,
		},
		{
			Name:    "simplewiki",
			URL:     "https://dumps.wikimedia.org/simplewiki/20260801/simplewiki-20260801-pages-articles.xml.bz2",
			SHA256:  "",
			Bytes:   334000000,
			Licence: "CC BY-SA, free to redistribute with attribution",
			Public:  true,
			About: "every article of the Simple English Wikipedia, with the markup left on, " +
				"which is a link graph and a set of infoboxes as well as a set of documents",
			Build: buildWiki,
		},
	}
}

// LookupDataset finds one dataset by name.
func LookupDataset(name string) (Dataset, error) {
	all := Datasets()
	for _, d := range all {
		if d.Name == name {
			return d, nil
		}
	}
	names := make([]string, 0, len(all))
	for _, d := range all {
		names = append(names, d.Name)
	}
	return Dataset{}, fmt.Errorf("corpus: no dataset named %q, there is %s", name, strings.Join(names, " "))
}

// FetchDataset downloads a dataset into cache and returns where it landed.
//
// An archive that is already there with the right checksum is left alone, so
// this is cheap to run again and is how a machine confirms it is about to
// measure what it thinks it is. The download goes to a temporary name and is
// renamed only after the checksum matches, so an interrupted fetch never
// leaves something behind that looks finished.
func FetchDataset(ctx context.Context, d Dataset, cache string, log func(string, ...any)) (string, error) {
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(cache, filepath.Base(d.URL))

	if sum, err := checksum(path); err == nil {
		switch {
		case d.SHA256 == "":
			log("%s is already here, unverified because it has no checksum pinned", d.Name)
			return path, nil
		case sum == d.SHA256:
			log("%s is already here and matches", d.Name)
			return path, nil
		default:
			log("%s is here but is %s, wanted %s, fetching again", d.Name, sum, d.SHA256)
		}
	}

	log("fetching %s, %.0f MB, %s", d.Name, float64(d.Bytes)/(1<<20), d.About)
	tmp := path + ".part"
	if err := download(ctx, d.URL, tmp); err != nil {
		return "", err
	}
	sum, err := checksum(tmp)
	if err != nil {
		return "", err
	}
	if d.SHA256 != "" && sum != d.SHA256 {
		// Left on disk under the temporary name rather than deleted, because
		// the interesting case is a publisher who changed the file, and that
		// is something to look at rather than something to silently retry.
		return "", fmt.Errorf("corpus: %s downloaded as %s, wanted %s, left at %s", d.Name, sum, d.SHA256, tmp)
	}
	if d.SHA256 == "" {
		log("%s has no checksum pinned, it downloaded as %s", d.Name, sum)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// WriteDataset turns a downloaded archive into a corpus file.
//
// The out path is passed through to the builder because a dataset that carries
// judgments has to put them somewhere, and next to the corpus they describe is
// the only place they are still findable in six months.
func WriteDataset(d Dataset, archive string, w io.Writer, limit int, out string) (Stats, error) {
	// The sidecars come out first. If the archive is not the one we think it
	// is, failing before writing three gigabytes of corpus is the kinder order.
	if len(d.Sidecar) > 0 {
		if err := extract(archive, d.Sidecar, filepath.Dir(out)); err != nil {
			return Stats{}, err
		}
	}

	enc := json.NewEncoder(w)
	st, err := d.Build(archive, enc, limit, out)
	if err != nil {
		return st, err
	}
	if st.Documents == 0 {
		return st, fmt.Errorf("corpus: %s produced no documents from %s", d.Name, archive)
	}
	return st, nil
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// No overall timeout on the client, because the largest of these is a
	// gigabyte and a fixed deadline would fail on a slow link for a reason
	// that has nothing to do with the download being stuck. The context the
	// caller passes is the one that decides when to give up.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("corpus: %s returned %s", url, resp.Status)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DatasetTimeout is how long the whole fetch and build is given.
//
// It is generous because the largest dataset here is a gigabyte to download and
// several gigabytes to walk, and the machines this runs on are usually busy.
const DatasetTimeout = 6 * time.Hour
