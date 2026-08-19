package graphs

import (
	"reflect"
	"testing"
)

func TestSubgraph(t *testing.T) {
	// A path 1-2-3-4-5 stored in both directions, which is the shape every
	// undirected dataset here arrives in.
	edges := []uint32{1, 2, 2, 1, 2, 3, 3, 2, 3, 4, 4, 3, 4, 5, 5, 4}

	t.Run("both ends survive or neither does", func(t *testing.T) {
		nodes, got, err := Subgraph(edges, 3)
		if err != nil {
			t.Fatal(err)
		}
		// Identifiers 1, 2 and 3 are kept, so 3-4 goes and its reverse goes
		// with it. An edge that kept one end would leave an undirected graph
		// that is no longer undirected.
		want := []uint32{1, 2, 2, 1, 2, 3, 3, 2}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("edges = %v, want %v", got, want)
		}
		if nodes != 3 {
			t.Errorf("nodes = %d, want 3", nodes)
		}
	})

	t.Run("asking for more than there is returns the whole graph", func(t *testing.T) {
		nodes, got, err := Subgraph(edges, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(edges) {
			t.Errorf("got %d values, want %d", len(got), len(edges))
		}
		if nodes != 5 {
			t.Errorf("nodes = %d, want 5", nodes)
		}
	})

	t.Run("a node left with nothing attached is not counted", func(t *testing.T) {
		// Node 1 is isolated, so keeping the two lowest identifiers keeps one
		// edge between 2 and 3 and leaves 1 out of the edge file entirely.
		nodes, got, err := Subgraph([]uint32{2, 3, 3, 2, 1, 9}, 3)
		if err != nil {
			t.Fatal(err)
		}
		if nodes != 2 {
			t.Errorf("nodes = %d, want 2", nodes)
		}
		if want := []uint32{2, 3, 3, 2}; !reflect.DeepEqual(got, want) {
			t.Errorf("edges = %v, want %v", got, want)
		}
	})

	t.Run("a subgraph with no edges is refused", func(t *testing.T) {
		if _, _, err := Subgraph(edges, 1); err == nil {
			t.Error("one node cannot have an edge to itself here, so this should fail")
		}
		if _, _, err := Subgraph(edges, 0); err == nil {
			t.Error("a subgraph of no nodes should fail")
		}
	})
}

func TestMaxID(t *testing.T) {
	if got := MaxID([]uint32{3, 9, 1, 4}); got != 9 {
		t.Errorf("MaxID = %d, want 9", got)
	}
	if got := MaxID(nil); got != 0 {
		t.Errorf("MaxID of nothing = %d, want 0", got)
	}
}
