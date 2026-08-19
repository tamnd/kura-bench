// Command bleve-runner measures Bleve.
//
// Bleve is the full text engine most Go projects reach for first, so it is the
// number every other engine here has to be read against. It writes a scorch
// index to disk, which makes it a fair comparison for on disk size and for cold
// start, and an unfair one for an engine that keeps everything in memory. Both
// numbers are reported and neither is adjusted.
//
// The segment library is pinned above what bleve v2.6.0 requires, and that pin
// is what makes this engine measurable at all. Bleve writes its postings in
// chunks, and up to zapx v17.1.3 the chunk lengths were converted to offsets in
// the same slice they were then written back into, so any chunk a term does not
// appear in was left holding an offset where a length belonged. Everything
// after that chunk was then read from the wrong place, which surfaces as a
// bogus frequency, an overflow reading a norm, a panic off the end of the
// chunk, or a request to allocate a hundred terabytes for a location list.
// Only a term whose postings span more than one chunk can hit it, which under
// the default chunking means a term in more than 1024 documents, and only if it
// also skips a chunk entirely. That is why the corpus's commonest words failed
// and its rare ones did not. zapx v17.1.4 fixed it, and the pin can go when a
// bleve release requires that or later.
package main

import (
	"context"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/simple"
	"github.com/blevesearch/bleve/v2/mapping"
	bquery "github.com/blevesearch/bleve/v2/search/query"

	"github.com/tamnd/kura-bench/corpus"
	"github.com/tamnd/kura-bench/runner"
)

func main() {
	runner.Main(func(cfg runner.Config) (runner.Engine, error) {
		return &engine{}, nil
	})
}

type engine struct {
	idx bleve.Index

	// closed records that Flush already closed the index. Bleve persists on a
	// background goroutine and the only public call that waits for it is Close,
	// so a flush that did not close would report a build time that leaves some
	// of the writing out.
	closed bool
}

func (e *engine) Describe() runner.Info {
	return runner.Info{
		Name:     "bleve",
		Version:  runner.ModuleVersion("github.com/blevesearch/bleve/v2"),
		Language: "go",
	}
}

func (e *engine) Create(dir string) error {
	idx, err := bleve.New(indexPath(dir), indexMapping())
	if err != nil {
		return err
	}
	e.idx = idx
	return nil
}

func (e *engine) Open(dir string) error {
	idx, err := bleve.Open(indexPath(dir))
	if err != nil {
		return err
	}
	e.idx = idx
	e.closed = false
	return nil
}

func (e *engine) AddBatch(docs []corpus.Document) error {
	batch := e.idx.NewBatch()
	for _, d := range docs {
		if err := batch.Index(d.ID, record{
			Repo:  d.Repo,
			Path:  d.Path,
			Title: d.Title,
			Body:  d.Body,
			Ext:   d.Extension,
		}); err != nil {
			return err
		}
	}
	return e.idx.Batch(batch)
}

func (e *engine) Flush() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.idx.Close()
}

func (e *engine) Search(ctx context.Context, query string, limit int) (int, error) {
	// Title and body, named. The composite _all field is off in the mapping,
	// and a match query with no field goes to _all, so an unnamed query here
	// searches a field that was never written and returns nothing at all.
	//
	// A match query with OR is the same reading of a bare query that the other
	// engines here are given: every word counts towards the score and none of
	// them is required. Making it AND would change what "hits" means and the
	// counts would stop being comparable.
	names := []string{"title", "body"}
	fields := make([]bquery.Query, 0, len(names))
	for _, f := range names {
		q := bleve.NewMatchQuery(query)
		q.SetOperator(bquery.MatchQueryOperatorOr)
		q.SetField(f)
		fields = append(fields, q)
	}
	// A document matching in both fields is one hit, which is what the other
	// engines count. Bleve's disjunction deduplicates by document, so this is
	// the same set that a query parser over two fields produces elsewhere.
	q := bleve.NewDisjunctionQuery(fields...)

	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = []string{"title", "path"}

	res, err := e.idx.SearchInContext(ctx, req)
	if err != nil {
		return 0, err
	}
	return int(res.Total), nil
}

// Note says which segment library produced these numbers.
//
// The version column says bleve, and bleve on its own is not what was measured
// here. The release the version column names pulls in a zapx that cannot read
// back the postings of a common term, so a report that named only bleve would
// be pointing at a build that answers none of these queries.
func (e *engine) Note() string {
	return "measured with its segment library held at zapx " +
		runner.ModuleVersion("github.com/blevesearch/zapx/v17") +
		", which is newer than this release of bleve asks for, because the version it asks for" +
		" cannot read back the postings of a term that appears in more than a thousand documents"
}

func (e *engine) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.idx.Close()
}

// record is what one document looks like to Bleve. It is a separate type from
// [corpus.Document] because the field names here are the ones a query refers
// to, and they should not change when the corpus format does.
type record struct {
	Repo  string `json:"repo"`
	Path  string `json:"path"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Ext   string `json:"ext"`
}

func indexPath(dir string) string { return dir + "/bleve.idx" }

// indexMapping keeps the body stored, because every engine here is asked to be
// able to show a result to a person and the ones that store nothing would look
// smaller for a reason that has nothing to do with the index.
//
// The composite _all field is off. It is on by default and it doubles the
// posting lists, and nothing in the query set searches it.
func indexMapping() mapping.IndexMapping {
	text := bleve.NewTextFieldMapping()
	text.Store = true
	text.IncludeTermVectors = false
	text.IncludeInAll = false

	keyword := bleve.NewKeywordFieldMapping()
	keyword.Store = true
	keyword.IncludeInAll = false

	// The path is worth showing and not worth searching as one term. Indexing
	// it would put a hundred thousand unique terms in the dictionary that no
	// query will ever ask for.
	stored := bleve.NewTextFieldMapping()
	stored.Store = true
	stored.Index = false
	stored.IncludeInAll = false

	d := bleve.NewDocumentMapping()
	d.AddFieldMappingsAt("title", text)
	d.AddFieldMappingsAt("body", text)
	d.AddFieldMappingsAt("ext", keyword)
	d.AddFieldMappingsAt("repo", keyword)
	d.AddFieldMappingsAt("path", stored)

	m := bleve.NewIndexMapping()
	m.DefaultMapping = d
	// Simple, not standard. Bleve's standard analyzer removes English stopwords
	// and none of the other engines here is asked to. One of the queries is the
	// single word "the", on purpose, and an engine that threw it away would
	// report no hits for it and a latency for having done no work.
	m.DefaultAnalyzer = simple.Name
	m.StoreDynamic = false
	m.IndexDynamic = false
	return m
}
