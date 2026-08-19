package graphs

import (
	"fmt"
	"sort"
)

// Subgraph keeps the n lowest identifiers and the edges between them.
//
// This is how a machine that cannot hold soc-LiveJournal gets a run that
// finishes, and it is done here rather than by handing a store fewer edges. The
// difference matters. Cutting an edge list off after a million records leaves a
// graph where half the nodes have lost most of their neighbours and an
// undirected graph where one direction of an edge survived and the other did
// not, and every answer worked out on the whole graph is then wrong about it in
// a way that no correctness column can distinguish from a broken engine.
//
// An induced subgraph is a real graph. Every edge it keeps has both ends, an
// undirected graph stays undirected, and the answers worked out on it are the
// right answers for it. What it is not is a sample of the original: taking the
// lowest identifiers follows whatever order the publisher assigned them in,
// which for a web crawl is roughly crawl order. It is a smaller real graph
// rather than a scale model of a large one, and a result on it says so.
func Subgraph(edges []uint32, n int) (int, []uint32, error) {
	if n <= 0 {
		return 0, nil, fmt.Errorf("a subgraph needs at least one node, not %d", n)
	}

	seen := make(map[uint32]struct{}, len(edges)/2)
	for _, id := range edges {
		seen[id] = struct{}{}
	}
	if n >= len(seen) {
		return len(seen), edges, nil
	}

	ids := make([]uint32, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	keep := make(map[uint32]struct{}, n)
	for _, id := range ids[:n] {
		keep[id] = struct{}{}
	}

	out := make([]uint32, 0, len(edges))
	kept := make(map[uint32]struct{}, n)
	for i := 0; i < len(edges); i += 2 {
		from, to := edges[i], edges[i+1]
		if _, ok := keep[from]; !ok {
			continue
		}
		if _, ok := keep[to]; !ok {
			continue
		}
		out = append(out, from, to)
		kept[from] = struct{}{}
		kept[to] = struct{}{}
	}
	if len(out) == 0 {
		return 0, nil, fmt.Errorf("the %d lowest identifiers have no edges between them", n)
	}
	// The count is of nodes that survived with an edge, not of identifiers that
	// were kept, because a node with nothing left attached to it is not in the
	// edge file and no store would ever hear about it.
	return len(kept), out, nil
}

// MaxID is the largest identifier in an edge list.
func MaxID(edges []uint32) uint32 {
	var top uint32
	for _, id := range edges {
		if id > top {
			top = id
		}
	}
	return top
}
