//go:build ladybug

// Command ladybug-graphrunner measures a property graph database.
//
// ladybug is the engine this suite was missing. The other three graph runners
// are a compressed sparse row array, an in memory adjacency library, and two
// integer columns in SQLite. None of them is a graph database, and a table that
// compares three ways of storing an adjacency list without a real one in it is
// not answering the question anybody asked.
//
// It is reached through its C API against the prebuilt shared library, which is
// the whole reason it is here at all: a benchmark that has to compile a C++
// tree from source is a benchmark that does not run on every machine, and that
// is what kept the earlier attempt at this row out of the suite.
package main

/*
#include <stdlib.h>
#include <lbug.h>
*/
import "C"

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"unsafe"

	"github.com/tamnd/kura-bench/runner"
)

func main() {
	runner.GraphMain(func(cfg runner.GraphConfig) (runner.GraphEngine, error) {
		return &engine{workers: cfg.WorkerCount()}, nil
	})
}

type engine struct {
	// The library's handles are allocated in C memory rather than declared as
	// Go values. Every one of them holds a void pointer, which cgo sees as a Go
	// pointer field, and passing the address of a Go struct that contains one
	// into C is exactly what the pointer rules forbid.
	db      *C.lbug_database
	open    bool
	workers int

	// Every operation runs on a connection of its own, taken from here and put
	// back. A connection is documented as thread safe and it is also the unit
	// the engine plans and executes on, so sharing one across the throughput
	// pass would measure a queue rather than the database.
	pool chan *conn
}

func (e *engine) Describe() runner.Info {
	return runner.Info{
		Name:     "ladybug",
		Version:  version(),
		Language: "c++",
	}
}

func (e *engine) Note() string {
	return "a property graph database reached through its C API, loaded with COPY from a CSV the way its own documentation loads a graph, and queried in Cypher rather than by walking the adjacency from the runner"
}

// Cannot explains the one empty cell in this row.
func (e *engine) Cannot(op string) string {
	if op == "pagerank" {
		return "its PageRank lives in an extension that is downloaded at first use, and a benchmark that reaches the network mid run is measuring the network"
	}
	return ""
}

// Create makes the database and the schema.
//
// The identifier is the publisher's own, declared as the primary key, so every
// lookup below goes through the engine's own index and the runner never holds a
// mapping of its own. That is the same rule the other runners follow and it is
// what makes the numbers comparable.
func (e *engine) Create(dir string) error {
	if err := e.attach(dir, false); err != nil {
		return err
	}
	c, err := e.take()
	if err != nil {
		return err
	}
	defer e.put(c)

	if err := c.exec(`CREATE NODE TABLE Node(id INT64 PRIMARY KEY)`); err != nil {
		return err
	}
	return c.exec(`CREATE REL TABLE Edge(FROM Node TO Node)`)
}

// Load writes the edge list out and copies it in.
//
// COPY from a file is how this engine is meant to be loaded and it is what its
// own documentation tells you to do, so a row that inserted five million edges
// one Cypher statement at a time would be measuring a mistake nobody makes. The
// CSV is written inside this call and its cost is counted, because the time to
// get a graph from an edge list into the database is the number the build phase
// is reporting.
func (e *engine) Load(edges []uint32) error {
	c, err := e.take()
	if err != nil {
		return err
	}
	defer e.put(c)

	// The node set has to be loaded first, because a relationship to a node
	// that does not exist is an error rather than a node.
	nodes, err := writeNodes(c.dir, edges)
	if err != nil {
		return err
	}
	if err := c.exec(`COPY Node FROM '` + nodes + `'`); err != nil {
		return err
	}

	list, err := writeEdges(c.dir, edges)
	if err != nil {
		return err
	}
	return c.exec(`COPY Edge FROM '` + list + `'`)
}

// Flush gets everything on disk and out of the write ahead log.
func (e *engine) Flush() error {
	c, err := e.take()
	if err != nil {
		return err
	}
	defer e.put(c)
	return c.exec(`CHECKPOINT`)
}

// Open attaches to the database the build phase left behind and prepares the
// statements the timed operations run.
func (e *engine) Open(dir string) error {
	return e.attach(dir, true)
}

func (e *engine) Close() error {
	if !e.open {
		return nil
	}
	for len(e.pool) > 0 {
		(<-e.pool).close()
	}
	C.lbug_database_destroy(e.db)
	C.free(unsafe.Pointer(e.db))
	e.db = nil
	e.open = false
	return nil
}

// Neighbours is the out degree.
func (e *engine) Neighbours(node uint32) int64 {
	return e.count(func(c *conn) (int64, error) { return c.one(c.degree, node) })
}

// TwoHop is the distinct nodes within two hops.
//
// The seed is excluded in the query rather than by subtracting one afterwards,
// because a node with an edge back to itself is in a real graph and the answers
// were worked out with it excluded.
func (e *engine) TwoHop(node uint32) int64 {
	return e.count(func(c *conn) (int64, error) { return c.one(c.twoHop, node) })
}

// ShortestPath is the hop count, or -1 when there is no path.
//
// The bound is the diameter the plan allows for. An unbounded recursive join is
// not a thing this engine will run, and a bound the graph exceeds would give a
// wrong answer rather than a slow one, so the checker catching it is the point.
func (e *engine) ShortestPath(from, to uint32) int64 {
	if from == to {
		return 0
	}
	c, err := e.take()
	if err != nil {
		return -1
	}
	defer e.put(c)

	if err := c.bind(c.path, "from", from); err != nil {
		return -1
	}
	if err := c.bind(c.path, "to", to); err != nil {
		return -1
	}
	rows, err := c.run(c.path)
	if err != nil {
		return -1
	}
	defer rows.close()

	// No row is no path, which is the -1 every other runner returns.
	n, ok, err := rows.next()
	if err != nil || !ok {
		return -1
	}
	return n
}

// BFS is the reachable set and its depth, in one traversal.
func (e *engine) BFS(node uint32) (int64, int64) {
	c, err := e.take()
	if err != nil {
		return -1, -1
	}
	defer e.put(c)

	if err := c.bind(c.reach, "seed", node); err != nil {
		return -1, -1
	}
	rows, err := c.run(c.reach)
	if err != nil {
		return -1, -1
	}
	defer rows.close()

	reached, ok, err := rows.next()
	if err != nil || !ok {
		return -1, -1
	}
	depth, ok, err := rows.next()
	if err != nil || !ok {
		return -1, -1
	}
	// The seed is reachable from itself and the traversal does not return it,
	// so it is added back here. Every other runner counts it.
	return reached + 1, depth
}

// PageRank is not measured. See Cannot.
func (e *engine) PageRank(int, float64, int) []int64 { return nil }

// count runs a one number query and turns any failure into -1, which is what
// the checker reads as "this engine did not answer".
func (e *engine) count(f func(*conn) (int64, error)) int64 {
	c, err := e.take()
	if err != nil {
		return -1
	}
	defer e.put(c)
	n, err := f(c)
	if err != nil {
		return -1
	}
	return n
}

// attach opens the database and fills the connection pool.
//
// The buffer pool is left at the default rather than sized to the machine. A
// benchmark that tunes one engine to the hardware and takes the defaults for
// the rest is not comparing engines, and the default is what a deployment gets.
func (e *engine) attach(dir string, prepare bool) error {
	path := filepath.Join(dir, "graph")
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	cfg := C.lbug_default_system_config()
	cfg.max_num_threads = C.uint64_t(runtime.NumCPU())

	e.db = (*C.lbug_database)(C.calloc(1, C.sizeof_lbug_database))
	if C.lbug_database_init(cpath, cfg, e.db) != C.LbugSuccess {
		C.free(unsafe.Pointer(e.db))
		e.db = nil
		return fmt.Errorf("could not open the database at %s", path)
	}
	e.open = true

	// One connection per worker plus one, so that the serial passes never wait
	// on a worker and the throughput pass never has to share.
	e.pool = make(chan *conn, e.workers+1)
	for range cap(e.pool) {
		c, err := newConn(e.db, dir)
		if err != nil {
			return err
		}
		if prepare {
			if err := c.prepareAll(); err != nil {
				return err
			}
		}
		e.pool <- c
	}
	return nil
}

func (e *engine) take() (*conn, error) {
	select {
	case c := <-e.pool:
		return c, nil
	default:
	}
	// Blocking is correct rather than growing the pool: a pool that grows under
	// load hides the queueing the throughput number exists to show.
	c, ok := <-e.pool
	if !ok {
		return nil, errors.New("the database is closed")
	}
	return c, nil
}

func (e *engine) put(c *conn) { e.pool <- c }

// version reads what the library says about itself rather than what the
// manifest pinned, so that a machine running a different library than the one
// we thought reports the one it is running.
func version() string {
	s := C.lbug_get_version()
	if s == nil {
		return "unknown"
	}
	defer C.lbug_destroy_string(s)
	return C.GoString(s)
}

// writeNodes writes the distinct identifiers, one per line.
func writeNodes(dir string, edges []uint32) (string, error) {
	seen := make(map[uint32]struct{}, len(edges)/2)
	path := filepath.Join(dir, "nodes.csv")
	return path, write(path, func(w *bufio.Writer) error {
		for _, id := range edges {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			if _, err := w.WriteString(strconv.FormatUint(uint64(id), 10)); err != nil {
				return err
			}
			if err := w.WriteByte('\n'); err != nil {
				return err
			}
		}
		return nil
	})
}

// writeEdges writes the pairs, one per line.
func writeEdges(dir string, edges []uint32) (string, error) {
	path := filepath.Join(dir, "edges.csv")
	return path, write(path, func(w *bufio.Writer) error {
		for i := 0; i < len(edges); i += 2 {
			line := strconv.FormatUint(uint64(edges[i]), 10) + "," +
				strconv.FormatUint(uint64(edges[i+1]), 10) + "\n"
			if _, err := w.WriteString(line); err != nil {
				return err
			}
		}
		return nil
	})
}

func write(path string, f func(*bufio.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(file, 1<<20)
	if err := f(w); err != nil {
		_ = file.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
