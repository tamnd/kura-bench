package graphs

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

// Fetch downloads a dataset and turns it into the canonical edge file.
//
// The download is checked against the pinned checksum before anything is
// parsed, because a truncated graph parses perfectly well and produces answers
// that are wrong in a way no timing will reveal.
func Fetch(ctx context.Context, client *http.Client, d Dataset, root string, log func(string, ...any)) error {
	dir := d.Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	archive := d.Path(root, "source.txt.gz")
	if err := download(ctx, client, d, archive, log); err != nil {
		return err
	}

	log("parsing %s", archive)
	h, edges, err := parse(archive, d)
	if err != nil {
		return err
	}
	log("%s nodes, %s edges, identifiers up to %d", commas(h.Nodes), commas(h.Edges), h.MaxID)

	return WriteEdges(d.Path(root, EdgeFile), h, edges)
}

func download(ctx context.Context, client *http.Client, d Dataset, path string, log func(string, ...any)) error {
	if sum, err := checksum(path); err == nil && sum == d.SHA256 {
		log("%s is already here and intact", d.Name)
		return nil
	}

	log("fetching %s, %s", d.URL, mb(d.ArchiveBytes))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", d.URL, resp.Status)
	}

	// Written to a temporary name and renamed, so an interrupted download is
	// not left looking like a complete one.
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, sum), resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != d.SHA256 {
		return fmt.Errorf("%s came back with checksum %s, the pin says %s", d.URL, got, d.SHA256)
	}
	return os.Rename(tmp, path)
}

func checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, bufio.NewReaderSize(f, 1<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// parse reads a SNAP edge list.
//
// The format is comment lines starting with a hash and then one edge per line
// as two identifiers separated by a tab. It is parsed by hand rather than with
// a scanner and a split, because the largest of these files is seventy million
// lines and the allocations add up to more than the download.
func parse(path string, d Dataset) (Header, []uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, nil, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return Header{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = gz.Close() }()

	r := bufio.NewReaderSize(gz, 1<<20)
	edges := make([]uint32, 0, d.Edges*2)
	seen := make(map[uint32]struct{}, d.Nodes)
	var maxID uint32
	line := 0

	for {
		text, err := r.ReadString('\n')
		if text != "" {
			line++
			from, to, ok, perr := edge(text)
			if perr != nil {
				return Header{}, nil, fmt.Errorf("%s line %d: %w", path, line, perr)
			}
			if ok {
				edges = append(edges, from, to)
				seen[from] = struct{}{}
				seen[to] = struct{}{}
				maxID = max(maxID, from, to)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Header{}, nil, err
		}
	}

	h := Header{Nodes: len(seen), Edges: len(edges) / 2, MaxID: maxID}
	if d.Undirected {
		h.Flags |= Undirected
	}
	// The publisher's own header is the check. If it says 5,242 nodes and the
	// parse found 5,241, one of the two is wrong and it is not worth guessing
	// which at the point where a benchmark run is about to start.
	if h.Nodes != d.Nodes || h.Edges != d.Edges {
		return Header{}, nil, fmt.Errorf("%s parsed to %d nodes and %d edges, %s is published as %d and %d",
			path, h.Nodes, h.Edges, d.Name, d.Nodes, d.Edges)
	}
	return h, edges, nil
}

// edge pulls two identifiers out of one line, and says whether there were any.
func edge(text string) (from, to uint32, ok bool, err error) {
	i := 0
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	if i == len(text) || text[i] == '#' || text[i] == '\n' || text[i] == '\r' {
		return 0, 0, false, nil
	}

	a, i, err := number(text, i)
	if err != nil {
		return 0, 0, false, err
	}
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	b, _, err := number(text, i)
	if err != nil {
		return 0, 0, false, err
	}
	return a, b, true, nil
}

func number(text string, i int) (uint32, int, error) {
	start := i
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == start {
		return 0, i, fmt.Errorf("expected a node identifier at column %d", start+1)
	}
	v, err := strconv.ParseUint(text[start:i], 10, 32)
	if err != nil {
		return 0, i, err
	}
	return uint32(v), i, nil
}

func mb(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d MB", n>>20)
}

func commas(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
