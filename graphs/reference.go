package graphs

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Answers is what the operations should come back with.
//
// This is the graph suite's ground truth, and it exists for the same reason the
// vector suite's does. A store that answers a two hop query in a microsecond
// because it forgot half the edges looks like the fastest thing in the table,
// and no timing column would ever say otherwise.
//
// It is worked out here, in Go, and every runner is a separate implementation
// in another language. Two independent implementations agreeing is real
// evidence. One implementation agreeing with itself is not.
type Answers struct {
	// Nodes and Edges are the graph these answers are about, so a stale answers
	// file cannot be used against a graph it does not describe.
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`

	// Plan is what was asked, since the answers are meaningless without it.
	Plan Plan `json:"plan"`

	// Answers is one vector per operation, named by the constants in plan.go.
	//
	// Every operation reduces to a list of whole numbers on purpose. It makes a
	// disagreement a comparison rather than a schema, and it is the same three
	// lines of code in every runner in every language.
	//
	//	neighbours     one out degree per seed
	//	two-hop        one distinct count per seed, not counting the seed
	//	shortest-path  one hop count per pair, -1 when there is no path
	//	bfs            two per seed, the reachable count then the depth
	//	pagerank       the highest ranked nodes, best first
	Answers map[string][]int64 `json:"answers"`
}

// WriteAnswers writes the answers file.
func WriteAnswers(path string, a Answers) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadAnswers reads the answers file.
func ReadAnswers(path string) (Answers, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Answers{}, err
	}
	var a Answers
	if err := json.Unmarshal(b, &a); err != nil {
		return Answers{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(a.Answers) == 0 {
		return Answers{}, fmt.Errorf("%s: no answers in it", path)
	}
	return a, nil
}

// A Graph is the edge list in compressed sparse row form: every node's
// neighbours laid out one node after another, with an offset per node.
//
// This is the layout a graph store is trying to beat, and it is also the only
// honest way to work out the answers, since anything cleverer would be another
// implementation that could be wrong in the same way a store is.
type Graph struct {
	// IDs are the distinct node identifiers, ascending. A node's index in this
	// slice is its dense index everywhere else.
	IDs []uint32

	// Offset has one more entry than IDs. The neighbours of node i are
	// Target[Offset[i]:Offset[i+1]].
	Offset []int64

	// Target holds the dense index of the far end of every edge.
	Target []int32
}

// Build turns an edge list into a graph.
//
// The identifiers are mapped to dense indexes because that is what every store
// does internally and because an array indexed by identifier wastes whatever
// gap the publisher left. On web-Google that gap is five percent, and on a
// graph that assigned identifiers by hash it would be everything.
func Build(edges []uint32) *Graph {
	seen := make(map[uint32]struct{}, len(edges)/2)
	for _, id := range edges {
		seen[id] = struct{}{}
	}
	ids := make([]uint32, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	index := make(map[uint32]int32, len(ids))
	for i, id := range ids {
		index[id] = int32(i)
	}

	g := &Graph{IDs: ids, Offset: make([]int64, len(ids)+1), Target: make([]int32, len(edges)/2)}
	for i := 0; i < len(edges); i += 2 {
		g.Offset[index[edges[i]]+1]++
	}
	for i := 1; i <= len(ids); i++ {
		g.Offset[i] += g.Offset[i-1]
	}

	// A second pass fills the rows. The cursor starts as a copy of the offsets
	// so the offsets themselves survive, which is cheaper than rebuilding them.
	cursor := make([]int64, len(ids))
	copy(cursor, g.Offset[:len(ids)])
	for i := 0; i < len(edges); i += 2 {
		from := index[edges[i]]
		g.Target[cursor[from]] = index[edges[i+1]]
		cursor[from]++
	}
	return g
}

// Nodes is how many nodes the graph has.
func (g *Graph) Nodes() int { return len(g.IDs) }

// Edges is how many edges the graph has.
func (g *Graph) Edges() int { return len(g.Target) }

// Index finds a node's dense index, or -1.
func (g *Graph) Index(id uint32) int32 {
	i := sort.Search(len(g.IDs), func(k int) bool { return g.IDs[k] >= id })
	if i < len(g.IDs) && g.IDs[i] == id {
		return int32(i)
	}
	return -1
}

// Neighbours is the dense indexes one hop out of a node.
func (g *Graph) Neighbours(i int32) []int32 {
	return g.Target[g.Offset[i]:g.Offset[i+1]]
}

// Seeds picks the nodes a run asks about.
//
// They are a fixed pseudo random sample rather than every nth node in
// identifier order, because identifiers in a real graph are assigned by
// whatever crawled it and taking every nth one follows that order. A uniform
// sample of nodes is what a random query looks like, which does mean it almost
// never lands on a hub, and that is the honest shape of the workload rather
// than a flattering one.
//
// The generator is splitmix64 with a fixed seed, so the same graph produces the
// same sample on every machine and in every language.
func (g *Graph) Seeds(n int) []uint32 {
	if n > len(g.IDs) {
		n = len(g.IDs)
	}
	order := make([]int32, len(g.IDs))
	for i := range order {
		order[i] = int32(i)
	}

	// Fisher and Yates, run only as far as the sample needs.
	state := uint64(0x9e3779b97f4a7c15)
	for i := 0; i < n; i++ {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		j := i + int(z%uint64(len(order)-i))
		order[i], order[j] = order[j], order[i]
	}

	out := make([]uint32, n)
	for i := range out {
		out[i] = g.IDs[order[i]]
	}
	return out
}

// Answer works out what every operation should come back with.
func (g *Graph) Answer(seeds []uint32, p Plan, progress func(op string)) (Answers, error) {
	if err := p.Check(); err != nil {
		return Answers{}, err
	}
	if len(seeds) < p.Seeds {
		return Answers{}, fmt.Errorf("the plan wants %d seeds and the seed file has %d", p.Seeds, len(seeds))
	}

	dense := make([]int32, len(seeds))
	for i, id := range seeds {
		dense[i] = g.Index(id)
		if dense[i] < 0 {
			return Answers{}, fmt.Errorf("seed %d is not in the graph", id)
		}
	}

	a := Answers{Nodes: g.Nodes(), Edges: g.Edges(), Plan: p, Answers: map[string][]int64{}}
	note := func(op string) {
		if progress != nil {
			progress(op)
		}
	}

	note(Neighbours)
	degrees := make([]int64, p.Neighbour)
	for i := range degrees {
		degrees[i] = int64(len(g.Neighbours(dense[i])))
	}
	a.Answers[Neighbours] = degrees

	note(TwoHop)
	counts := make([]int64, p.TwoHop)
	mark := newStamps(g.Nodes())
	for i := range counts {
		counts[i] = int64(g.twoHop(dense[i], mark))
	}
	a.Answers[TwoHop] = counts

	note(ShortestPath)
	hops := make([]int64, p.Path)
	for i := range hops {
		hops[i] = int64(g.hops(dense[2*i], dense[2*i+1], mark))
	}
	a.Answers[ShortestPath] = hops

	note(BFS)
	reach := make([]int64, 0, p.BFS*2)
	for i := range p.BFS {
		reached, depth := g.reach(dense[i], mark)
		reach = append(reach, int64(reached), int64(depth))
	}
	a.Answers[BFS] = reach

	note(PageRank)
	a.Answers[PageRank] = g.pageRank(p)

	return a, nil
}

// stamps is a visited set that is cleared by bumping a counter rather than by
// walking it, which matters when a thousand traversals each touch a handful of
// nodes in a graph with five million.
type stamps struct {
	seen []uint32
	now  uint32
}

func newStamps(n int) *stamps { return &stamps{seen: make([]uint32, n)} }

func (s *stamps) reset() {
	s.now++
	if s.now == 0 {
		// Wrapped, which takes four billion traversals and is still worth
		// handling because the alternative is a wrong answer rather than a
		// slow one.
		for i := range s.seen {
			s.seen[i] = 0
		}
		s.now = 1
	}
}

func (s *stamps) mark(i int32) bool {
	if s.seen[i] == s.now {
		return false
	}
	s.seen[i] = s.now
	return true
}

// twoHop counts the distinct nodes within two hops, not counting the seed.
func (g *Graph) twoHop(from int32, mark *stamps) int {
	mark.reset()
	mark.mark(from)
	n := 0
	for _, one := range g.Neighbours(from) {
		if mark.mark(one) {
			n++
		}
	}
	// The frontier is re-read from the adjacency rather than collected, because
	// the marks already say which of them were new and the row is still warm.
	for _, one := range g.Neighbours(from) {
		for _, two := range g.Neighbours(one) {
			if mark.mark(two) {
				n++
			}
		}
	}
	return n
}

// hops is the shortest path length, or -1.
//
// A pair that is not connected costs a full traversal of everything the start
// can reach, which is the honest cost of answering that question and is why
// the plan asks for a hundred pairs rather than a thousand.
func (g *Graph) hops(from, to int32, mark *stamps) int {
	if from == to {
		return 0
	}
	mark.reset()
	mark.mark(from)
	frontier := []int32{from}
	for depth := 1; len(frontier) > 0; depth++ {
		var next []int32
		for _, n := range frontier {
			for _, m := range g.Neighbours(n) {
				if m == to {
					return depth
				}
				if mark.mark(m) {
					next = append(next, m)
				}
			}
		}
		frontier = next
	}
	return -1
}

// reach is the size of the reachable set and how deep it goes.
func (g *Graph) reach(from int32, mark *stamps) (reached, depth int) {
	mark.reset()
	mark.mark(from)
	frontier := []int32{from}
	reached = 1
	for len(frontier) > 0 {
		var next []int32
		for _, n := range frontier {
			for _, m := range g.Neighbours(n) {
				if mark.mark(m) {
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

// pageRank runs the plan's iterations and returns the highest ranked nodes.
//
// The mass on nodes with no outgoing edges is spread over every node rather
// than dropped, which is what keeps the total at one. Implementations differ
// here and the difference is visible in the ranking, so it is written down.
func (g *Graph) pageRank(p Plan) []int64 {
	n := g.Nodes()
	if n == 0 {
		return nil
	}
	rank := make([]float64, n)
	next := make([]float64, n)
	for i := range rank {
		rank[i] = 1 / float64(n)
	}

	for range p.Iterations {
		var dangling float64
		for i := range next {
			next[i] = 0
		}
		for i := range n {
			out := g.Neighbours(int32(i))
			if len(out) == 0 {
				dangling += rank[i]
				continue
			}
			share := rank[i] / float64(len(out))
			for _, m := range out {
				next[m] += share
			}
		}
		base := (1-p.Damping)/float64(n) + p.Damping*dangling/float64(n)
		for i := range next {
			next[i] = base + p.Damping*next[i]
		}
		rank, next = next, rank
	}

	order := make([]int32, n)
	for i := range order {
		order[i] = int32(i)
	}
	// Ties are broken by the lower identifier, which is arbitrary and is at
	// least the same arbitrary choice in every implementation.
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if rank[a] != rank[b] {
			return rank[a] > rank[b]
		}
		return g.IDs[a] < g.IDs[b]
	})

	top := min(p.Top, n)
	out := make([]int64, top)
	for i := range out {
		out[i] = int64(g.IDs[order[i]])
	}
	return out
}
