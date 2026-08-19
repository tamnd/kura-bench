// Command bleve-runner measures Bleve.
//
// Bleve is the full text engine most Go projects reach for first, so it is the
// number every other engine here has to be read against. It writes a scorch
// index to disk, which makes it a fair comparison for on disk size and for cold
// start, and an unfair one for an engine that keeps everything in memory. Both
// numbers are reported and neither is adjusted.
package main

import (
	"context"

	"github.com/blevesearch/bleve/v2"
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
	// A match query with OR is the same reading of a bare query that the other
	// engines here are given: every word counts towards the score and none of
	// them is required. Making it AND would change what "hits" means and the
	// counts would stop being comparable.
	q := bleve.NewMatchQuery(query)
	q.SetOperator(bquery.MatchQueryOperatorOr)

	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = []string{"title", "path"}

	res, err := e.idx.SearchInContext(ctx, req)
	if err != nil {
		return 0, err
	}
	return int(res.Total), nil
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
	m.DefaultAnalyzer = "standard"
	m.StoreDynamic = false
	m.IndexDynamic = false
	return m
}
