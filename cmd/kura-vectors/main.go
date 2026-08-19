// Command kura-vectors fetches the vector datasets the vector suite runs on.
//
// It downloads the published files, checks them against a pinned size and
// SHA-256, and reports what is on disk. Nothing here generates vectors. A
// random million points has no structure, every index finds them equally
// easily, and a recall figure taken on them says nothing about the engine.
//
//	kura-vectors -dataset sift -out ~/vectors
//	kura-vectors -check -out ~/vectors
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/kura-bench/vectors"
)

func main() {
	var (
		name   = flag.String("dataset", "sift", "dataset to fetch, one of "+strings.Join(vectors.Names(), " "))
		metric = flag.String("metric", "euclidean", "also compute the exact ground truth for this metric, one of "+metricNames())
		out    = flag.String("out", "vecdata", "directory the datasets are kept in")
		check  = flag.Bool("check", false, "only report what is already on disk")
		force  = flag.Bool("force", false, "fetch again even if the files are already there")
	)
	flag.Parse()

	d, err := vectors.Lookup(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kura-vectors:", err)
		os.Exit(1)
	}
	m, err := vectors.ParseMetric(*metric)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kura-vectors:", err)
		os.Exit(1)
	}
	if err := run(d, m, *out, *check, *force); err != nil {
		fmt.Fprintln(os.Stderr, "kura-vectors:", err)
		os.Exit(1)
	}
}

func metricNames() string {
	out := make([]string, 0, len(vectors.Metrics()))
	for _, m := range vectors.Metrics() {
		out = append(out, string(m))
	}
	return strings.Join(out, " ")
}

func run(d vectors.Dataset, m vectors.Metric, root string, check, force bool) error {
	if check {
		return describe(d, m, root)
	}
	if force || d.Verify(root) != nil {
		if err := os.MkdirAll(d.Dir(root), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s, %s once unpacked\n", d.About, size(d.Bytes()))

		if d.Archive != nil {
			if err := fetchArchive(d, root, force); err != nil {
				return err
			}
		} else {
			for _, f := range d.Files {
				if err := fetchFile(d, root, f, force); err != nil {
					return err
				}
			}
		}
		if err := d.Verify(root); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "%s is already here and intact\n", d.Name)
	}

	if err := groundTruth(d, m, root, force); err != nil {
		return err
	}
	return describe(d, m, root)
}

// groundTruth computes the true neighbours for a metric the dataset did not
// ship any for.
//
// It is a full scan on every core and it takes minutes. That is the point: a
// ground truth produced by anything approximate would put a ceiling on every
// recall figure measured against it, and nothing in the report would say so.
// It is written once and every later run reads the file.
func groundTruth(d vectors.Dataset, m vectors.Metric, root string, force bool) error {
	if m.Published() {
		return nil
	}
	path := d.GroundTruthPath(root, m)
	if !force && d.VerifyGroundTruth(root, m) == nil {
		fmt.Fprintf(os.Stderr, "the %s ground truth is already here\n", m)
		return nil
	}

	fmt.Fprintf(os.Stderr, "computing the exact %s neighbours of %s queries over %s vectors, this is a full scan on every core\n",
		m, count(d.Queries), count(d.Count))

	shape, base, err := vectors.Fvecs(d.Path(root, vectors.Base))
	if err != nil {
		return err
	}
	qshape, queries, err := vectors.Fvecs(d.Path(root, vectors.Query))
	if err != nil {
		return err
	}
	if qshape.Dim != shape.Dim {
		return fmt.Errorf("the queries are %d components wide and the base vectors are %d", qshape.Dim, shape.Dim)
	}

	start := time.Now()
	ids, err := vectors.ExactTopK(base, shape.Dim, queries, d.Depth, m, progress(qshape.Count, start))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nscanned in %s\n", time.Since(start).Round(time.Second))
	return vectors.WriteIvecs(path, d.Depth, ids)
}

// progress prints a line that gets overwritten, since this step takes long
// enough that a silent terminal looks like a hang.
func progress(total int, start time.Time) func(done, total int) {
	every := total / 100
	if every < 1 {
		every = 1
	}
	return func(done, total int) {
		if done%every != 0 && done != total {
			return
		}
		elapsed := time.Since(start)
		left := time.Duration(float64(elapsed) / float64(done) * float64(total-done))
		fmt.Fprintf(os.Stderr, "\r%s of %s queries, %s left    ",
			count(done), count(total), left.Round(time.Second))
	}
}

// count groups thousands, because these are six figure numbers and unreadable
// without it.
func count(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// describe prints the shape of each file, which is the only report that proves
// the download is usable rather than merely present.
func describe(d vectors.Dataset, m vectors.Metric, root string) error {
	if err := d.Verify(root); err != nil {
		return err
	}
	names := make([]string, 0, len(d.Files)+1)
	for _, f := range d.Files {
		names = append(names, f.Name)
	}
	if !m.Published() {
		names = append(names, m.GroundTruth())
	}

	fmt.Printf("%s in %s\n", d.Name, d.Dir(root))
	for _, name := range names {
		shape, err := vectors.ReadShape(d.Path(root, name), 4)
		if err != nil {
			return err
		}
		fmt.Printf("  %-28s %9d rows of %4d, %s\n", name, shape.Count, shape.Dim, size(shape.Bytes))
	}
	return nil
}

// fetchFile downloads one file, resuming a partial one where it stopped.
//
// The partial download keeps a .part suffix until it has been checked, because
// a file with the right name and the wrong contents is worse than no file. Half
// a gigabyte over a home connection is long enough that a run being interrupted
// is normal rather than exceptional, so the second attempt asks for the rest
// rather than starting again.
func fetchFile(d vectors.Dataset, root string, f vectors.File, force bool) error {
	final := d.Path(root, f.Name)
	if !force {
		if sum, err := checksum(final); err == nil && sum == f.SHA256 {
			fmt.Fprintf(os.Stderr, "%s is already here\n", f.Name)
			return nil
		}
	}
	return download(f.URL, final, f.Bytes, f.SHA256, force)
}

// fetchArchive downloads one tarball and unpacks the three files out of it.
//
// The archive is checked as a whole and the members are checked by size, since
// the published checksum covers the archive and rehashing four gigabytes of
// extracted files to compare against numbers this repository worked out itself
// would prove only that the extraction is deterministic.
func fetchArchive(d vectors.Dataset, root string, force bool) error {
	a := d.Archive
	tarball := filepath.Join(d.Dir(root), "archive.tar.gz")
	if err := download(a.URL, tarball, a.Bytes, a.SHA256, force); err != nil {
		return err
	}

	want := map[string]vectors.File{}
	for _, f := range d.Files {
		want[f.Member] = f
	}

	fh, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()

	gz, err := gzip.NewReader(fh)
	if err != nil {
		return fmt.Errorf("%s: %w", tarball, err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", tarball, err)
		}
		member := filepath.ToSlash(filepath.Clean(h.Name))
		f, ok := want[member]
		if !ok {
			continue
		}
		path := d.Path(root, f.Name)
		fmt.Fprintf(os.Stderr, "unpacking %s, %s\n", f.Name, size(f.Bytes))
		// A tar member is written through a bounded copy rather than straight
		// out, because the size is known from the dataset and a member that
		// disagrees with it is a different file wearing the same name.
		if err := extract(tr, path, f.Bytes); err != nil {
			return err
		}
		delete(want, member)
	}
	if len(want) > 0 {
		return fmt.Errorf("%s does not contain %v", tarball, missing(want))
	}

	// The tarball is three gigabytes and its whole job is done. Leaving it
	// behind fills the disk of exactly the machines that have least of it.
	return os.Remove(tarball)
}

func missing(want map[string]vectors.File) []string {
	var out []string
	for member := range want {
		out = append(out, member)
	}
	return out
}

func extract(r io.Reader, path string, want int64) error {
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	n, err := io.Copy(dst, r)
	if err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("%s unpacked to %d bytes, expected %d", path, n, want)
	}
	return nil
}

// download fetches a URL to a path, resuming and verifying.
func download(url, path string, want int64, sum string, force bool) error {
	part := path + ".part"
	if force {
		_ = os.Remove(part)
	}

	h := sha256.New()
	var have int64
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Whatever is already in the part file is hashed before anything is asked
	// for, so that a resumed download ends up with the checksum of the whole
	// file rather than of its tail.
	if have, err = io.Copy(h, f); err != nil {
		return err
	}
	if have > want {
		// Longer than the real file means it is not the real file. Start over
		// rather than try to work out what happened.
		if err := restart(f, h); err != nil {
			return err
		}
		have = 0
	}

	if have < want {
		if have, err = fetch(url, f, h, have, want); err != nil {
			return err
		}
	}
	if have != want {
		return fmt.Errorf("%s: got %d bytes, expected %d", url, have, want)
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%s: checksum %s, expected %s, the file is not the one this benchmark is pinned to", url, got, sum)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
}

func restart(f *os.File, h hash.Hash) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h.Reset()
	return nil
}

// fetch appends the rest of a URL to an open file, hashing as it goes.
func fetch(url string, f *os.File, h hash.Hash, have, want int64) (int64, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		fmt.Fprintf(os.Stderr, "resuming %s at %s of %s\n", filepath.Base(f.Name()), size(have), size(want))
	} else {
		fmt.Fprintf(os.Stderr, "fetching %s, %s\n", url, size(want))
	}

	// No overall deadline. These files are gigabytes and a slow connection is
	// not an error, but a stalled one is, so the timeout is on the response
	// header only and the body is left to take as long as it takes.
	client := &http.Client{Timeout: 0, Transport: &http.Transport{
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
	}}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusPartialContent:
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return 0, err
		}
	case http.StatusOK:
		// The server ignored the range and is sending the whole thing, which
		// happens often enough to handle rather than fail on.
		if have > 0 {
			if err := restart(f, h); err != nil {
				return 0, err
			}
			have = 0
		}
	default:
		return 0, fmt.Errorf("%s: %s", url, res.Status)
	}

	n, err := io.Copy(io.MultiWriter(f, h), res.Body)
	if err != nil {
		return 0, err
	}
	return have + n, nil
}

// checksum hashes a file that is already in place, used to decide whether it
// needs fetching at all.
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

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
