// Command genba-runner measures the platform's own store and searcher.
//
// It runs on the SQLite driver, which is the one a deployment without a data
// warehouse behind it actually uses. That is the whole reason to pick it here:
// it has an on disk form, so the store size and the cold start columns mean
// what they say, and it applies the terms and the permission rule inside the
// statement rather than handing every document it holds to the ranker.
//
// The in memory driver is the reference implementation and is deliberately not
// measured. It has no index, so a query walks the corpus and tokenises every
// document in it, which on eighty thousand documents is tens of seconds per
// query. That is the correct shape for something whose job is to be obviously
// right, and putting it in a table next to a memory mapped index would be
// comparing a specification with a product.
package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/tamnd/genba/acl"
	"github.com/tamnd/genba/doc"
	"github.com/tamnd/genba/index"
	"github.com/tamnd/genba/store"
	"github.com/tamnd/genba/store/sqlitestore"

	"github.com/tamnd/kura-bench/corpus"
	"github.com/tamnd/kura-bench/runner"
)

// tenant is the one tenant everything in the benchmark belongs to. The
// permission check runs whether or not there is more than one, which is the
// point of measuring it here rather than measuring a searcher with the check
// compiled out.
const tenant = "bench"

func main() {
	runner.Main(func(cfg runner.Config) (runner.Engine, error) {
		return &engine{cfg: cfg}, nil
	})
}

type engine struct {
	cfg      runner.Config
	store    store.Store
	searcher *index.Searcher

	// principal is the subject every query runs as. It is a member of the
	// tenant and nothing else, so every document in the corpus is visible to it
	// and the filter still has to decide that for each one.
	principal *acl.Principal
}

func (e *engine) Describe() runner.Info {
	return runner.Info{
		Name:     "genba",
		Version:  runner.ModuleVersion("github.com/tamnd/genba"),
		Language: "go",
	}
}

func (e *engine) Create(dir string) error {
	return e.start(dir)
}

// Open attaches to the database the build phase left behind, which is what a
// restart looks like.
func (e *engine) Open(dir string) error {
	return e.start(dir)
}

func (e *engine) start(dir string) error {
	st, err := sqlitestore.Open(context.Background(), filepath.Join(dir, "genba.db"))
	if err != nil {
		return err
	}
	e.store = st
	e.searcher = index.New(st)
	e.principal = &acl.Principal{
		Tenant:  tenant,
		Subject: "bench",
		Kind:    acl.KindUser,
	}
	return nil
}

func (e *engine) AddBatch(docs []corpus.Document) error {
	out := make([]doc.Document, 0, len(docs))
	now := time.Now()
	for _, d := range docs {
		out = append(out, doc.Document{
			ID:         d.ID,
			Tenant:     tenant,
			Source:     "corpus",
			Kind:       doc.KindCode,
			Title:      d.Title,
			Body:       d.Body,
			Container:  d.Repo,
			ModifiedAt: now,
			IndexedAt:  now,
			Permissions: acl.Permissions{
				Mode:   acl.ModePublicToTenant,
				Source: "corpus",
			},
			Properties: map[string]string{"ext": d.Extension},
		})
	}
	return e.store.Put(context.Background(), out...)
}

// Flush has nothing to do. Put commits, so everything written is already on
// disk, and sleeping here to look busy would be a fake number.
func (e *engine) Flush() error { return nil }

func (e *engine) Search(ctx context.Context, query string, limit int) (int, error) {
	res, err := e.searcher.Search(ctx, e.principal, index.Query{
		Text:  query,
		Limit: limit,
	})
	if err != nil {
		return 0, err
	}
	return res.Total, nil
}

// Note says that this engine is not reading the query the way the rest of the
// table is.
//
// Every other engine here is asked for an OR over the words as they were
// written, so the hit counts can be put side by side. Genba drops stopwords
// from a query before it runs one, which is the right thing for somebody typing
// a question and the wrong thing for a column that is meant to be comparable:
// on "deprecated in favour of" it searches two words where the others search
// four, matches a fiftieth as many documents, and is quick about it because it
// did less. Rewriting the query in this runner to put the words back would
// measure something nobody can deploy, so the numbers stay as they are and the
// report says why they are lower.
func (e *engine) Note() string {
	return "it drops stopwords from a query before running it, unlike every other engine here," +
		" so a query containing one matches fewer documents and its latency is the cost of a smaller search"
}

func (e *engine) Close() error {
	if e.store == nil {
		return nil
	}
	return e.store.Close()
}
