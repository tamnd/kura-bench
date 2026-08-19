package graphs

import "fmt"

// The five operations, which are the five shapes of work a graph store is
// actually asked to do.
//
// They are named here rather than in each runner so that a report from one
// engine can be laid next to a report from another and mean the same thing.
const (
	// Neighbours is one hop out of one node. It is the cheapest thing a graph
	// store does and the one it does most, and on a store that keeps its edges
	// in a sorted table it is a range scan rather than a pointer walk.
	Neighbours = "neighbours"

	// TwoHop is the distinct nodes within two hops, which is a friend of a
	// friend. It is where the cost of a hub shows up: most nodes have three
	// neighbours and a few have a hundred thousand, so this is the operation
	// whose average tells you nothing and whose tail tells you everything.
	TwoHop = "two-hop"

	// ShortestPath is the hop count between two nodes, or nothing when they
	// are not connected.
	ShortestPath = "shortest-path"

	// BFS is the whole reachable set from one node, which is the operation that
	// touches everything and cannot be helped by any index.
	BFS = "bfs"

	// PageRank is the whole graph, several times over. It is the analytics
	// workload rather than the serving workload, and it is here because a store
	// laid out for point lookups and a store laid out for scans are very
	// different, and nothing else in this list would show that.
	PageRank = "pagerank"
)

// Operations lists them in the order the report shows them, cheapest first.
func Operations() []string {
	return []string{Neighbours, TwoHop, ShortestPath, BFS, PageRank}
}

// A Plan is how much of each operation a run does.
//
// The counts differ by two orders of magnitude on purpose. A neighbour lookup
// is microseconds and a breadth first search over LiveJournal is seconds, so
// asking for a thousand of each would mean the run never finished and the
// neighbour figure had no samples worth calling a percentile.
type Plan struct {
	// Seeds is how many nodes the seed file holds. Every other count here
	// takes its nodes from the front of that list, so a run with fewer seeds
	// is a subset of a run with more rather than a different sample.
	Seeds int `json:"seeds"`

	// Neighbour is how many seeds get a one hop lookup.
	Neighbour int `json:"neighbour"`

	// TwoHop is how many seeds get a two hop lookup.
	TwoHop int `json:"two_hop"`

	// Path is how many pairs get a shortest path. The pairs are taken from the
	// seed list two at a time, so a path run needs twice this many seeds.
	Path int `json:"path"`

	// BFS is how many seeds get a full traversal.
	BFS int `json:"bfs"`

	// Iterations and Damping are PageRank's, written down because a PageRank
	// figure without them is not a measurement of anything.
	Iterations int     `json:"iterations"`
	Damping    float64 `json:"damping"`

	// Top is how many of the highest ranked nodes the answer records.
	Top int `json:"top"`
}

// DefaultPlan is what a run does unless it is told otherwise.
func DefaultPlan() Plan {
	return Plan{
		Seeds:      1000,
		Neighbour:  1000,
		TwoHop:     100,
		Path:       100,
		BFS:        10,
		Iterations: 20,
		Damping:    0.85,
		Top:        10,
	}
}

// Fit shrinks a plan to a graph that is too small for it, so that a five
// thousand node graph runs the same operations as a five million node one
// rather than failing.
func (p Plan) Fit(nodes int) Plan {
	if nodes <= 0 {
		return p
	}
	p.Seeds = min(p.Seeds, nodes)
	p.Neighbour = min(p.Neighbour, p.Seeds)
	p.TwoHop = min(p.TwoHop, p.Seeds)
	p.BFS = min(p.BFS, p.Seeds)
	p.Path = min(p.Path, p.Seeds/2)
	return p
}

// Check says whether a plan can be carried out with the seeds it asks for.
func (p Plan) Check() error {
	switch {
	case p.Seeds <= 0:
		return fmt.Errorf("a plan needs at least one seed, it has %d", p.Seeds)
	case p.Neighbour > p.Seeds, p.TwoHop > p.Seeds, p.BFS > p.Seeds:
		return fmt.Errorf("an operation asks for more than the %d seeds in the plan", p.Seeds)
	case p.Path*2 > p.Seeds:
		return fmt.Errorf("%d pairs need %d seeds and the plan has %d", p.Path, p.Path*2, p.Seeds)
	case p.Iterations <= 0 || p.Damping <= 0 || p.Damping >= 1:
		return fmt.Errorf("pagerank needs a positive iteration count and a damping between zero and one, it has %d and %v", p.Iterations, p.Damping)
	case p.Top <= 0:
		return fmt.Errorf("the answer has to record at least one ranked node, it records %d", p.Top)
	}
	return nil
}
