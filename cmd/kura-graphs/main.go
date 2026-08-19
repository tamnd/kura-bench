// Command kura-graphs fetches a real graph and prepares it for the graph
// suite.
//
// It downloads the published edge list, checks it against the pinned checksum,
// turns it into the fixed width file every runner reads, picks the nodes the
// run asks about, and works out what the answers should be. All of that happens
// once per machine. What is left for a run is the engine.
//
//	kura-graphs -dataset web-google -out graphdata
//
// A machine that cannot hold the whole of a large graph prepares a smaller one
// once, with -nodes, and everything downstream treats it as the graph it is.
//
//	kura-graphs -dataset soc-livejournal -nodes 500000 -out graphdata
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/kura-bench/graphs"
)

func main() {
	var (
		name  = flag.String("dataset", "ca-grqc", "graph to fetch, one of "+strings.Join(graphs.Names(), " "))
		out   = flag.String("out", "graphdata", "directory the graphs are kept in")
		seeds = flag.Int("seeds", 0, "how many nodes a run asks about, zero for the default of 1000")
		nodes = flag.Int("nodes", 0, "prepare the subgraph on this many of the lowest identifiers, zero for the whole graph")
		force = flag.Bool("force", false, "redo the conversion and the answers even if they are already here")
	)
	flag.Parse()

	if err := run(*name, *out, *seeds, *nodes, *force); err != nil {
		fmt.Fprintln(os.Stderr, "kura-graphs:", err)
		os.Exit(1)
	}
}

func run(name, out string, seedCount, nodeCount int, force bool) error {
	d, err := graphs.Lookup(name)
	if err != nil {
		return err
	}
	log("%s, %s compressed", d.About, mb(d.ArchiveBytes))

	edgePath := d.Path(out, graphs.EdgeFile)
	if _, err := graphs.ReadHeader(edgePath); err != nil || force {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		client := &http.Client{Timeout: 0}
		if err := graphs.Fetch(ctx, client, d, out, log); err != nil {
			return err
		}
	} else {
		log("%s is already converted", d.Name)
	}

	h, edges, err := graphs.ReadEdges(edgePath)
	if err != nil {
		return err
	}

	// The subgraph is written next to the whole graph rather than over it, so
	// that preparing a small one does not cost a second download and so that a
	// result taken on one cannot be mistaken for a result taken on the other.
	dir, label := d.Dir(out), d.Name
	if nodeCount > 0 && nodeCount < h.Nodes {
		kept, sub, err := graphs.Subgraph(edges, nodeCount)
		if err != nil {
			return err
		}
		label = fmt.Sprintf("%s-n%d", d.Name, nodeCount)
		dir = filepath.Join(out, label)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		h = graphs.Header{Nodes: kept, Edges: len(sub) / 2, MaxID: graphs.MaxID(sub), Flags: h.Flags}
		edgePath = filepath.Join(dir, graphs.EdgeFile)
		if err := graphs.WriteEdges(edgePath, h, sub); err != nil {
			return err
		}
		edges = sub
		log("the subgraph on the %s lowest identifiers has %s nodes and %s edges",
			commas(nodeCount), commas(h.Nodes), commas(h.Edges))
	}

	plan := graphs.DefaultPlan()
	if seedCount > 0 {
		plan.Seeds = seedCount
	}
	plan = plan.Fit(h.Nodes)
	if err := plan.Check(); err != nil {
		return err
	}

	answerPath := filepath.Join(dir, graphs.AnswerFile)
	if a, err := graphs.ReadAnswers(answerPath); err == nil && !force && a.Nodes == h.Nodes && a.Plan == plan {
		log("the answers are already here and describe this graph")
		return report(label, dir, h, plan)
	}

	log("building the graph in memory")
	g := graphs.Build(edges)

	seedIDs := g.Seeds(plan.Seeds)
	if err := graphs.WriteIDs(filepath.Join(dir, graphs.SeedFile), seedIDs); err != nil {
		return err
	}

	// This is the slow part and it is slow on purpose. Every answer here is
	// worked out the plainest way there is, because a clever ground truth can
	// be wrong in the same way an engine is wrong and then neither of them
	// would ever be caught.
	log("working out the answers, which is a full traversal for some of them")
	a, err := g.Answer(seedIDs, plan, func(op string) { log("  %s", op) })
	if err != nil {
		return err
	}
	if err := graphs.WriteAnswers(answerPath, a); err != nil {
		return err
	}
	return report(label, dir, h, plan)
}

func report(label, dir string, h graphs.Header, p graphs.Plan) error {
	fmt.Printf("%s in %s\n", label, dir)
	fmt.Printf("  %s nodes, %s edges, identifiers up to %s\n", commas(h.Nodes), commas(h.Edges), commas(int(h.MaxID)))
	if h.Flags&graphs.Undirected != 0 {
		fmt.Printf("  undirected, stored with both directions of every edge\n")
	}
	fmt.Printf("  %s seeds, %s neighbour lookups, %s two hop, %s paths, %s traversals\n",
		commas(p.Seeds), commas(p.Neighbour), commas(p.TwoHop), commas(p.Path), commas(p.BFS))
	fmt.Printf("  pagerank over %d iterations at damping %v\n", p.Iterations, p.Damping)
	fmt.Printf("  run it with: kura-graphbench -graph %s -bin bin -out results\n", dir)
	return nil
}

func log(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func mb(n int64) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d MB", n>>20)
}

func commas(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	b := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, c)
	}
	return string(b)
}
