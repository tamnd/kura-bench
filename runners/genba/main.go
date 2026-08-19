// Command genba-runner measures the platform's own store and searcher.
//
// The store it uses keeps everything in memory and has no on disk form yet, so
// there is nothing to reopen. Open therefore rebuilds the index from the corpus,
// which is exactly what this deployment shape has to do after a restart, and it
// is the honest thing to put next to an engine that memory maps a file and is
// answering queries in a few milliseconds. The index size on disk is zero for
// the same reason, and the resident memory figure is where the cost shows up.
package main

import (
	"context"
	"errors"
	"time"

	"github.com/tamnd/genba/acl"
	"github.com/tamnd/genba/doc"
	"github.com/tamnd/genba/index"
	"github.com/tamnd/genba/store"
	"github.com/tamnd/genba/store/memstore"
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
	e.start()
	return nil
}

// Open rebuilds from the corpus, because an in memory store has nothing to
// attach to. The time it takes is a real cost of running this way and it
// belongs in the cold start column rather than in a footnote.
func (e *engine) Open(dir string) error {
	e.start()

	batch := make([]corpus.Document, 0, runner.BatchSize)
	var seen int
	_, err := corpus.ReadFile(e.cfg.Corpus, func(d corpus.Document) error {
		if e.cfg.Limit > 0 && seen >= e.cfg.Limit {
			return corpus.ErrStop
		}
		seen++
		batch = append(batch, d)
		if len(batch) < runner.BatchSize {
			return nil
		}
		if err := e.AddBatch(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	})
	if err != nil && !errors.Is(err, corpus.ErrStop) {
		return err
	}
	if len(batch) > 0 {
		if err := e.AddBatch(batch); err != nil {
			return err
		}
	}
	return e.Flush()
}

func (e *engine) start() {
	st := memstore.New()
	e.store = st
	e.searcher = index.New(st)
	e.principal = &acl.Principal{
		Tenant:  tenant,
		Subject: "bench",
		Kind:    acl.KindUser,
	}
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

// Flush has nothing to do. Put is durable in the only sense this store has, and
// pretending otherwise by sleeping here would be a fake number.
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

func (e *engine) Close() error {
	if e.store == nil {
		return nil
	}
	return e.store.Close()
}
