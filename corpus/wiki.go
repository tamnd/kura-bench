package corpus

import (
	"bufio"
	"compress/bzip2"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// page is one entry of a MediaWiki export.
//
// Only the fields that decide what goes into the corpus are named. The export
// carries a contributor, a timestamp, a comment, a revision id and a checksum
// for every page, and reading them into memory to throw them away is most of
// the cost of parsing this file.
type page struct {
	Title     string `xml:"title"`
	Namespace int    `xml:"ns"`
	ID        int    `xml:"id"`
	Redirect  struct {
		Title string `xml:"title,attr"`
	} `xml:"redirect"`
	Revision struct {
		Text string `xml:"text"`
	} `xml:"revision"`
}

// buildWiki turns a MediaWiki export into one document per article.
//
// The markup is left on. That is deliberate and it is worth saying why, because
// every other corpus builder in the wild strips it. A search engine in front of
// a wiki is handed the wiki's own markup, and the templates, the infoboxes, the
// tables and the link syntax are a large fraction of the bytes and a large
// fraction of the distinct terms. Stripping them here would produce a cleaner
// corpus that measures something nobody runs. Every engine gets the same bytes,
// so the comparison is fair, and the bytes are the ones a real deployment sees.
//
// Two things are dropped. Pages outside the article namespace are talk pages,
// user pages and templates, which are a different corpus wearing the same file.
// Redirects are a title with no body, and half a million empty documents would
// move every average in the report without teaching anything.
func buildWiki(archive string, enc *json.Encoder, limit int, _ string) (Stats, error) {
	var st Stats

	f, err := os.Open(archive)
	if err != nil {
		return st, err
	}
	defer func() { _ = f.Close() }()

	// The decompressor is the slow part of this and it is single threaded, so
	// the read ahead in front of it matters more than usual.
	return walkWiki(bufio.NewReaderSize(bzip2.NewReader(bufio.NewReaderSize(f, 1<<20)), 1<<20), enc, limit)
}

// walkWiki is buildWiki once the archive is open.
//
// It is split out so that a test can hand it plain XML. The alternative is a
// compressed fixture checked into the repository, and a test whose input nobody
// can read is a test nobody will change.
func walkWiki(r io.Reader, enc *json.Encoder, limit int) (Stats, error) {
	var st Stats
	dec := xml.NewDecoder(r)

	for {
		tok, err := dec.Token()
		if err != nil {
			// The end of the file is the normal end. Anything else is a
			// truncated download, which is worth reporting rather than
			// quietly treating as a short corpus.
			if errors.Is(err, io.EOF) {
				return st, nil
			}
			return st, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "page" {
			continue
		}

		var p page
		if err := dec.DecodeElement(&p, &start); err != nil {
			return st, err
		}
		if p.Namespace != 0 || p.Redirect.Title != "" {
			continue
		}
		body := strings.TrimSpace(p.Revision.Text)
		if body == "" || len(body) > MaxDocumentSize {
			continue
		}

		doc := Document{
			ID:        strconv.Itoa(p.ID),
			Repo:      "wikipedia",
			Path:      strings.ReplaceAll(p.Title, " ", "_"),
			Title:     p.Title,
			Body:      body,
			Extension: "wiki",
		}
		if err := enc.Encode(doc); err != nil {
			return st, err
		}
		st.Documents++
		st.Bytes += int64(len(body))
		if limit > 0 && st.Documents >= limit {
			return st, nil
		}
	}
}
