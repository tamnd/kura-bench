// Command sqlitefts-runner measures SQLite's FTS5 extension.
//
// It is here because it is the answer a lot of teams give when asked why they
// do not need a search engine, and because it is the floor an engine has to
// beat to be worth deploying at all. The driver is the pure Go one, so nothing
// here needs a C toolchain and the numbers come from the same binary layout as
// the other Go runners.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tamnd/kura-bench/corpus"
	"github.com/tamnd/kura-bench/runner"
	_ "modernc.org/sqlite"
)

func main() {
	runner.Main(func(cfg runner.Config) (runner.Engine, error) {
		return &engine{}, nil
	})
}

type engine struct {
	db *sql.DB
}

func (e *engine) Describe() runner.Info {
	return runner.Info{
		Name:     "sqlite-fts5",
		Version:  runner.ModuleVersion("modernc.org/sqlite"),
		Language: "go",
	}
}

func (e *engine) Create(dir string) error {
	db, err := open(dir)
	if err != nil {
		return err
	}
	// The FTS5 table holds the content itself rather than referring back to a
	// separate one. That is what makes the file size comparable to the other
	// engines here, all of which keep the text they were given.
	//
	// The unindexed columns are metadata a result list shows and nobody
	// searches. Marking them so keeps them out of the term index, which is what
	// a schema written by someone who had read the manual would do.
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE docs USING fts5(
			id UNINDEXED,
			repo UNINDEXED,
			path UNINDEXED,
			title,
			body,
			ext UNINDEXED,
			tokenize = 'unicode61'
		)`)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

func (e *engine) Open(dir string) error {
	db, err := open(dir)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

func (e *engine) AddBatch(docs []corpus.Document) error {
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO docs(id, repo, path, title, body, ext) VALUES(?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, d := range docs {
		if _, err := stmt.Exec(d.ID, d.Repo, d.Path, d.Title, d.Body, d.Extension); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (e *engine) Flush() error {
	// Merging the b-tree levels is the part of the work FTS5 would otherwise do
	// during the first queries, and leaving it out here would move indexing
	// time into the search numbers.
	if _, err := e.db.Exec(`INSERT INTO docs(docs) VALUES('optimize')`); err != nil {
		return err
	}
	_, err := e.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (e *engine) Search(ctx context.Context, query string, limit int) (int, error) {
	match := matchExpression(query)
	if match == "" {
		return 0, nil
	}

	// The count and the page are two statements on purpose. A person asking for
	// ten results is served by the second one, and the first is what makes the
	// hit total comparable with engines that report it for free. The timing
	// covers both, which is the cost of getting a total out of FTS5.
	var total int
	if err := e.db.QueryRowContext(ctx,
		`SELECT count(*) FROM docs WHERE docs MATCH ?`, match).Scan(&total); err != nil {
		return 0, err
	}

	rows, err := e.db.QueryContext(ctx,
		`SELECT id, title FROM docs WHERE docs MATCH ? ORDER BY bm25(docs, 1.0, 3.0, 1.0) LIMIT ?`,
		match, limit)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return 0, err
		}
	}
	return total, rows.Err()
}

func (e *engine) Close() error {
	if e.db == nil {
		return nil
	}
	return e.db.Close()
}

func open(dir string) (*sql.DB, error) {
	name := filepath.Join(dir, "fts5.db")
	// The pragmas are the ones any deployment sets. Leaving them at the default
	// would measure a fsync per transaction, which is a fair number for a
	// ledger and not for a search index being built from a corpus file.
	dsn := "file:" + name + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// matchExpression turns a plain query into FTS5 syntax.
//
// Bare words in FTS5 are joined with AND, and every other engine here treats
// them as OR, so the OR is written out. The words are quoted because a query
// that happens to contain NEAR or a bare hyphen is otherwise a syntax error
// rather than a search.
func matchExpression(query string) string {
	var terms []string
	for _, f := range strings.Fields(query) {
		f = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				return r
			}
			return ' '
		}, f)
		for _, w := range strings.Fields(f) {
			terms = append(terms, fmt.Sprintf("%q", w))
		}
	}
	return strings.Join(terms, " OR ")
}
