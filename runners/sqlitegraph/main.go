// Command sqlite-graphrunner measures a graph kept in an ordinary SQL table.
//
// It is here for the same reason the FTS5 runner is here: it is the answer a
// lot of teams give when asked why they do not need a graph database. Two
// integer columns and an index on them is a real design that a lot of software
// is built on, and it is the floor a graph store has to beat to be worth
// deploying at all.
//
// The driver is the pure Go one, so nothing here needs a C toolchain.
package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/tamnd/kura-bench/runner"
)

func main() {
	runner.GraphMain(func(cfg runner.GraphConfig) (runner.GraphEngine, error) {
		return &engine{}, nil
	})
}

type engine struct {
	db *sql.DB

	// The statements are prepared once at open rather than per call, because a
	// benchmark that parses the same SQL a million times is measuring the
	// parser. Every deployment that cares prepares them too.
	degree *sql.Stmt
	out    *sql.Stmt
	twoHop *sql.Stmt
}

func (e *engine) Describe() runner.Info {
	return runner.Info{
		Name:     "sqlite",
		Version:  runner.ModuleVersion("modernc.org/sqlite"),
		Language: "go",
	}
}

func (e *engine) Note() string {
	return "the edges live in one table with a covering index on both columns, and the traversals walk it a level at a time from Go, which is how a relational store is used as a graph in practice"
}

func (e *engine) Cannot(op string) string { return "" }

func (e *engine) Create(dir string) error {
	db, err := open(dir)
	if err != nil {
		return err
	}
	// A plain rowid table rather than WITHOUT ROWID, because a primary key on
	// the pair would silently drop a repeated edge and change the graph. The
	// index is created after the load instead of before it, which is the
	// standard advice and is what anyone loading five million rows would do.
	//
	// The node table exists because PageRank needs to know about a node with no
	// outgoing edges, and a table of edges does not contain that.
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE edge(src INTEGER NOT NULL, dst INTEGER NOT NULL);
		CREATE TABLE node(id INTEGER PRIMARY KEY)`)
	if err != nil {
		return err
	}
	e.db = db
	return nil
}

// insertBatch is how many edges go into one statement.
//
// SQLite's default limit on bound parameters is 999, so 400 pairs is 800 of
// them and leaves room. One statement per edge spends most of the load in the
// round trip rather than in the b-tree.
const insertBatch = 400

// insertFor is the insert statement for a batch of n edges.
func insertFor(n int) string {
	return "INSERT INTO edge(src, dst) VALUES(?, ?)" + strings.Repeat(", (?, ?)", n-1)
}

func (e *engine) Load(edges []uint32) error {
	ctx := context.Background()
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	full, err := tx.PrepareContext(ctx, insertFor(insertBatch))
	if err != nil {
		return err
	}
	defer func() { _ = full.Close() }()

	args := make([]any, 0, insertBatch*2)
	flush := func() error {
		if len(args) == 0 {
			return nil
		}
		if len(args) == insertBatch*2 {
			_, err := full.ExecContext(ctx, args...)
			return err
		}
		// The last batch is short, so it gets its own statement. Padding it out
		// with repeated rows would be faster and would also be a lie about how
		// many edges the graph has.
		_, err := tx.ExecContext(ctx, insertFor(len(args)/2), args...)
		return err
	}

	for i := 0; i < len(edges); i += 2 {
		args = append(args, int64(edges[i]), int64(edges[i+1]))
		if len(args) == insertBatch*2 {
			if err := flush(); err != nil {
				return err
			}
			args = args[:0]
		}
	}
	if err := flush(); err != nil {
		return err
	}

	// The node set is worked out in SQL rather than in Go, because doing it in
	// Go would mean holding a second copy of every identifier in a map while
	// the database is already holding all of them.
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO node(id) SELECT src FROM edge`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO node(id) SELECT dst FROM edge`); err != nil {
		return err
	}
	return tx.Commit()
}

func (e *engine) Flush() error {
	ctx := context.Background()
	// Both columns are in the index, so a neighbour lookup never touches the
	// table itself. Leaving dst out would halve the index and turn every lookup
	// into a row fetch per edge, which is a worse design and would produce a
	// worse number honestly rather than a better one dishonestly.
	if _, err := e.db.ExecContext(ctx, `CREATE INDEX edge_src_dst ON edge(src, dst)`); err != nil {
		return err
	}
	if _, err := e.db.ExecContext(ctx, `ANALYZE`); err != nil {
		return err
	}
	_, err := e.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (e *engine) Open(dir string) error {
	db, err := open(dir)
	if err != nil {
		return err
	}
	e.db = db
	ctx := context.Background()

	if e.degree, err = db.PrepareContext(ctx,
		`SELECT count(*) FROM edge WHERE src = ?`); err != nil {
		return err
	}
	if e.out, err = db.PrepareContext(ctx,
		`SELECT dst FROM edge WHERE src = ?`); err != nil {
		return err
	}
	// The distinct nodes within two hops, in one statement, because this is the
	// one traversal a relational store can do without going back and forth. The
	// UNION is what makes them distinct and the last line drops the seed, which
	// is how the answers were worked out.
	e.twoHop, err = db.PrepareContext(ctx, `
		SELECT count(*) FROM (
			SELECT dst FROM edge WHERE src = ?
			UNION
			SELECT b.dst FROM edge a JOIN edge b ON b.src = a.dst WHERE a.src = ?
		) WHERE dst <> ?`)
	return err
}

func (e *engine) Close() error {
	if e.db == nil {
		return nil
	}
	return e.db.Close()
}

func (e *engine) Neighbours(node uint32) int64 {
	var n int64
	if err := e.degree.QueryRowContext(context.Background(), int64(node)).Scan(&n); err != nil {
		return -1
	}
	return n
}

func (e *engine) TwoHop(node uint32) int64 {
	var n int64
	id := int64(node)
	if err := e.twoHop.QueryRowContext(context.Background(), id, id, id).Scan(&n); err != nil {
		return -1
	}
	return n
}

func (e *engine) ShortestPath(from, to uint32) int64 {
	if from == to {
		return 0
	}
	seen := map[uint32]struct{}{from: {}}
	frontier := []uint32{from}
	for depth := int64(1); len(frontier) > 0; depth++ {
		var next []uint32
		for _, n := range frontier {
			out, err := e.neighbours(n)
			if err != nil {
				return -1
			}
			for _, m := range out {
				if m == to {
					return depth
				}
				if _, ok := seen[m]; !ok {
					seen[m] = struct{}{}
					next = append(next, m)
				}
			}
		}
		frontier = next
	}
	return -1
}

func (e *engine) BFS(node uint32) (int64, int64) {
	seen := map[uint32]struct{}{node: {}}
	frontier := []uint32{node}
	reached, depth := int64(1), int64(0)
	for len(frontier) > 0 {
		var next []uint32
		for _, n := range frontier {
			out, err := e.neighbours(n)
			if err != nil {
				return -1, -1
			}
			for _, m := range out {
				if _, ok := seen[m]; !ok {
					seen[m] = struct{}{}
					reached++
					next = append(next, m)
				}
			}
		}
		if len(next) > 0 {
			depth++
		}
		frontier = next
	}
	return reached, depth
}

// neighbours is one hop, and it is one query.
//
// Batching the whole frontier into an IN list would be faster and would need a
// statement per frontier size, which means preparing SQL inside the timed loop.
// A query per node is what the level walk actually costs, and it is the number
// this runner exists to show.
func (e *engine) neighbours(node uint32) ([]uint32, error) {
	rows, err := e.out.QueryContext(context.Background(), int64(node))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []uint32
	for rows.Next() {
		var dst int64
		if err := rows.Scan(&dst); err != nil {
			return nil, err
		}
		out = append(out, uint32(dst))
	}
	return out, rows.Err()
}

func (e *engine) PageRank(iterations int, damping float64, top int) []int64 {
	ids, index, err := e.nodes()
	if err != nil || len(ids) == 0 {
		return nil
	}
	n := len(ids)

	degree := make([]int32, n)
	rows, err := e.db.QueryContext(context.Background(), `SELECT src, count(*) FROM edge GROUP BY src`)
	if err != nil {
		return nil
	}
	for rows.Next() {
		var src, count int64
		if err := rows.Scan(&src, &count); err != nil {
			_ = rows.Close()
			return nil
		}
		degree[index[uint32(src)]] = int32(count)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil
	}
	_ = rows.Close()

	rank := make([]float64, n)
	next := make([]float64, n)
	for i := range rank {
		rank[i] = 1 / float64(n)
	}

	for range iterations {
		for i := range next {
			next[i] = 0
		}
		var dangling float64
		for i := range n {
			if degree[i] == 0 {
				dangling += rank[i]
			}
		}
		// One pass over every edge, in source order so the index can serve it
		// without touching the table. This is the whole of the difference
		// between this runner and the array based ones: they walk a slice and
		// this walks a b-tree, twenty times over.
		if err := e.spread(index, degree, rank, next, damping); err != nil {
			return nil
		}
		base := (1-damping)/float64(n) + damping*dangling/float64(n)
		for i := range next {
			next[i] = base + damping*next[i]
		}
		rank, next = next, rank
	}

	order := make([]int32, n)
	for i := range order {
		order[i] = int32(i)
	}
	// The identifiers came back ascending, so a stable sort by rank leaves a tie
	// broken by the lower identifier, which is what the answers do.
	sort.SliceStable(order, func(i, j int) bool { return rank[order[i]] > rank[order[j]] })
	if top > n {
		top = n
	}
	out := make([]int64, top)
	for i := range out {
		out[i] = int64(ids[order[i]])
	}
	return out
}

// spread pushes every node's share along its outgoing edges.
func (e *engine) spread(index map[uint32]int32, degree []int32, rank, next []float64, damping float64) error {
	rows, err := e.db.QueryContext(context.Background(), `SELECT src, dst FROM edge ORDER BY src`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var src, dst int64
		if err := rows.Scan(&src, &dst); err != nil {
			return err
		}
		from := index[uint32(src)]
		next[index[uint32(dst)]] += rank[from] / float64(degree[from])
	}
	return rows.Err()
}

// nodes reads the identifiers, ascending, and their dense positions.
func (e *engine) nodes() ([]uint32, map[uint32]int32, error) {
	rows, err := e.db.QueryContext(context.Background(), `SELECT id FROM node ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []uint32
	index := map[uint32]int32{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		index[uint32(id)] = int32(len(ids))
		ids = append(ids, uint32(id))
	}
	return ids, index, rows.Err()
}

func open(dir string) (*sql.DB, error) {
	name := filepath.Join(dir, "graph.db")
	// The same pragmas the FTS5 runner sets, for the same reason. A fsync per
	// transaction is the right default for a ledger and not for a graph being
	// loaded from a file.
	dsn := "file:" + name + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=cache_size(-262144)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(context.Background()); err != nil {
		return nil, err
	}
	return db, nil
}
