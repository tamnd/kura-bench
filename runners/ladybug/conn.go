//go:build ladybug

package main

/*
#cgo LDFLAGS: -llbug
#include <stdlib.h>
#include <lbug.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// maxDepth bounds the recursive joins.
//
// An unbounded variable length pattern is not something this engine will plan,
// so a number has to go here, and thirty is the largest it will accept. That is
// past the diameter of every graph in the suite, and a graph that exceeded it
// would come back with an answer the checker rejects rather than a slow one
// that slipped through, which is the right way round.
const maxDepth = 30

// conn is one connection and the statements prepared against it.
//
// Every handle here is C memory for the reason given on the engine: they are
// structs of void pointers, and cgo will not let the address of a Go struct
// holding one cross into C.
type conn struct {
	c   *C.lbug_connection
	dir string

	degree *C.lbug_prepared_statement
	twoHop *C.lbug_prepared_statement
	path   *C.lbug_prepared_statement
	reach  *C.lbug_prepared_statement
}

func newConn(db *C.lbug_database, dir string) (*conn, error) {
	c := &conn{dir: dir, c: (*C.lbug_connection)(C.calloc(1, C.sizeof_lbug_connection))}
	if C.lbug_connection_init(db, c.c) != C.LbugSuccess {
		C.free(unsafe.Pointer(c.c))
		return nil, errors.New("could not open a connection")
	}
	return c, nil
}

func (c *conn) close() {
	for _, s := range []**C.lbug_prepared_statement{&c.degree, &c.twoHop, &c.path, &c.reach} {
		if *s != nil {
			C.lbug_prepared_statement_destroy(*s)
			C.free(unsafe.Pointer(*s))
			*s = nil
		}
	}
	C.lbug_connection_destroy(c.c)
	C.free(unsafe.Pointer(c.c))
}

// prepareAll plans the four queries once, at open, so that the timed loop is
// executing a plan rather than making one. Every deployment that cares about
// these latencies prepares them too.
func (c *conn) prepareAll() error {
	var err error
	// One hop. The primary key on id is what turns the anchor into a lookup.
	if c.degree, err = c.prepare(
		`MATCH (a:Node {id: $seed})-[:Edge]->(b:Node) RETURN count(b)`); err != nil {
		return err
	}
	// Two hops, distinct, without the seed. DISTINCT is inside the count
	// because a node reachable by two routes is one node.
	if c.twoHop, err = c.prepare(
		`MATCH (a:Node {id: $seed})-[:Edge*1..2]->(b:Node)
		 WHERE b.id <> $seed
		 RETURN count(DISTINCT b)`); err != nil {
		return err
	}
	// The hop count of a shortest path, which is the engine's own traversal
	// rather than a level walk driven from here. That is the whole point of
	// having a graph database in the table.
	if c.path, err = c.prepare(fmt.Sprintf(
		`MATCH (a:Node {id: $from})-[e:Edge* SHORTEST 1..%d]->(b:Node {id: $to})
		 RETURN length(e)`, maxDepth)); err != nil {
		return err
	}
	// The reachable set and how deep it goes. ALL SHORTEST keeps one shortest
	// path per destination, so the count is the reachable set and the longest
	// of them is the depth. The two numbers come back as two rows rather than
	// two columns so that the reader below stays one column wide.
	c.reach, err = c.prepare(fmt.Sprintf(
		`MATCH (a:Node {id: $seed})-[e:Edge* ALL SHORTEST 1..%d]->(b:Node)
		 WITH count(DISTINCT b) AS reached, max(length(e)) AS depth
		 UNWIND [reached, depth] AS n
		 RETURN n`, maxDepth))
	return err
}

func (c *conn) prepare(query string) (*C.lbug_prepared_statement, error) {
	q := C.CString(query)
	defer C.free(unsafe.Pointer(q))

	stmt := (*C.lbug_prepared_statement)(C.calloc(1, C.sizeof_lbug_prepared_statement))
	if C.lbug_connection_prepare(c.c, q, stmt) != C.LbugSuccess ||
		!bool(C.lbug_prepared_statement_is_success(stmt)) {
		msg := message(C.lbug_prepared_statement_get_error_message(stmt))
		C.lbug_prepared_statement_destroy(stmt)
		C.free(unsafe.Pointer(stmt))
		return nil, fmt.Errorf("could not prepare %s: %s", query, msg)
	}
	return stmt, nil
}

// bind sets one parameter. Every parameter in this runner is a node
// identifier, so there is only ever one type to bind.
func (c *conn) bind(stmt *C.lbug_prepared_statement, name string, id uint32) error {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))

	// int64 rather than uint32, because the column is INT64 and binding a
	// narrower type would make the comparison a cast at every row.
	if C.lbug_prepared_statement_bind_int64(stmt, n, C.int64_t(id)) != C.LbugSuccess {
		return fmt.Errorf("could not bind %s", name)
	}
	return nil
}

// exec runs a statement that has no result worth reading.
func (c *conn) exec(query string) error {
	q := C.CString(query)
	defer C.free(unsafe.Pointer(q))

	res := (*C.lbug_query_result)(C.calloc(1, C.sizeof_lbug_query_result))
	state := C.lbug_connection_query(c.c, q, res)
	defer func() {
		C.lbug_query_result_destroy(res)
		C.free(unsafe.Pointer(res))
	}()

	if state != C.LbugSuccess || !bool(C.lbug_query_result_is_success(res)) {
		return fmt.Errorf("%s: %s", query, message(C.lbug_query_result_get_error_message(res)))
	}
	return nil
}

// one runs a prepared statement that returns a single number.
func (c *conn) one(stmt *C.lbug_prepared_statement, seed uint32) (int64, error) {
	if err := c.bind(stmt, "seed", seed); err != nil {
		return 0, err
	}
	rows, err := c.run(stmt)
	if err != nil {
		return 0, err
	}
	defer rows.close()

	n, ok, err := rows.next()
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errors.New("no row came back")
	}
	return n, nil
}

// run executes a prepared statement.
func (c *conn) run(stmt *C.lbug_prepared_statement) (*result, error) {
	res := (*C.lbug_query_result)(C.calloc(1, C.sizeof_lbug_query_result))
	state := C.lbug_connection_execute(c.c, stmt, res)
	if state != C.LbugSuccess || !bool(C.lbug_query_result_is_success(res)) {
		err := fmt.Errorf("query failed: %s", message(C.lbug_query_result_get_error_message(res)))
		C.lbug_query_result_destroy(res)
		C.free(unsafe.Pointer(res))
		return nil, err
	}
	return &result{res: res}, nil
}

// result is a cursor over one column of integers, which is all this runner ever
// asks for.
type result struct {
	res *C.lbug_query_result
}

func (r *result) close() {
	C.lbug_query_result_destroy(r.res)
	C.free(unsafe.Pointer(r.res))
}

// next reads the first column of the next row.
//
// A null comes back as zero and not as an error, because max over an empty
// group is null and that is the answer for a node with no outgoing edges.
func (r *result) next() (int64, bool, error) {
	if !bool(C.lbug_query_result_has_next(r.res)) {
		return 0, false, nil
	}
	tuple := (*C.lbug_flat_tuple)(C.calloc(1, C.sizeof_lbug_flat_tuple))
	defer func() {
		C.lbug_flat_tuple_destroy(tuple)
		C.free(unsafe.Pointer(tuple))
	}()
	if C.lbug_query_result_get_next(r.res, tuple) != C.LbugSuccess {
		return 0, false, errors.New("could not read the next row")
	}

	value := (*C.lbug_value)(C.calloc(1, C.sizeof_lbug_value))
	defer func() {
		C.lbug_value_destroy(value)
		C.free(unsafe.Pointer(value))
	}()
	if C.lbug_flat_tuple_get_value(tuple, 0, value) != C.LbugSuccess {
		return 0, false, errors.New("could not read the first column")
	}

	if bool(C.lbug_value_is_null(value)) {
		return 0, true, nil
	}
	var n C.int64_t
	if C.lbug_value_get_int64(value, &n) != C.LbugSuccess {
		return 0, false, errors.New("the first column is not an integer")
	}
	return int64(n), true, nil
}

// message turns one of the library's error strings into a Go one and frees it.
func message(s *C.char) string {
	if s == nil {
		return "no reason given"
	}
	defer C.lbug_destroy_string(s)
	return C.GoString(s)
}
