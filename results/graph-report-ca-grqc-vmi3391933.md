# Graph results on vmi3391933

Graph ca-grqc from graphdata/ca-grqc, The Arxiv General Relativity collaboration network, 5,242 authors and 14,490 collaborations stored in both directions.

The plan is 1000 neighbour lookups, 100 two hop lookups, 100 shortest paths, 10 full traversals, and pagerank over 20 iterations at damping 0.85.
The nodes are a fixed sample, so every engine is asked about the same ones in the same order, and a run with fewer of them is a subset of a run with more.

## Machine

vmi3391933, linux/x86_64, AMD EPYC Processor (with IBPB), 8 cores, 23.47 GB of memory.
Load before the run was 13.95 and 5.44 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Graph

ca-grqc, 5,242 nodes and 28,980 edges, undirected, stored with both directions of every edge.
The run asked about 1,000 nodes, the same ones in the same order for every engine.
The edge list is 226 KB as pairs of uint32, which is what the store sizes below are measured against.

## Correctness

| engine | version | neighbours | two-hop | shortest-path | bfs | pagerank |
| --- | --- | --- | --- | --- | --- | --- |
| csr | 0.1.0 | agrees | agrees | agrees | agrees | agrees |
| ladybug | 0.19.1 | agrees | agrees | agrees | agrees | cannot |
| petgraph | 0.8.3 | agrees | agrees | agrees | agrees | agrees |
| sqlite | v1.56.0 | agrees | agrees | agrees | agrees | agrees |

The answers were worked out separately, in Go, by walking the same edge list the plainest way there is.
Agreement between that and an engine is two independent implementations landing on the same numbers, which is the only reason the timings below are worth reading.

## Loading the graph

| engine | wall | edges/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| csr | 6 ms | 5,025,488 | 0.0 | 1.0x | 6.0 MB |
| ladybug | 662 ms | 43,774 | 0.6 | 0.8x | 105.2 MB |
| petgraph | 9 ms | 3,396,950 | 0.0 | 0.6x | 6.4 MB |
| sqlite | 593 ms | 48,880 | 0.3 | 0.6x | 15.3 MB |

This is the cost of getting a graph into the store in the first place, which on a large one is the largest number in this report.

## Storage

| engine | store on disk | files | bytes per edge | store over edge list |
| --- | --- | --- | --- | --- |
| csr | 175 KB | 1 | 6.2 | 0.77x |
| ladybug | 2.7 MB | 3 | 96.9 | 12.12x |
| petgraph | 226 KB | 1 | 8.0 | 1.00x |
| sqlite | 824 KB | 3 | 29.1 | 3.64x |

Below one means the store is keeping less than the eight bytes an edge arrived as, which is what a dense adjacency buys.
Above one is the cost of an index, a property store or a page layout, and it should be buying something back in the tables below.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| csr | 1 ms | 0.00 | 2.7 MB |
| ladybug | 231 ms | 0.23 | 72.6 MB |
| petgraph | 9 ms | 0.01 | 3.3 MB |
| sqlite | 4 ms | 0.00 | 8.8 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Operations

### neighbours

One hop out of one node, the cheapest thing a graph store does and the one it does most.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1,000 | 90 ns | 110 ns | 290 ns | 27.9 us | 210,121 | 8 in flight |
| ladybug | 1,000 | 3.67 ms | 12.98 ms | 44.67 ms | 67.92 ms | 381 | 8 in flight |
| petgraph | 1,000 | 230 ns | 481 ns | 811 ns | 14.8 us | 191,073 | 8 in flight |
| sqlite | 1,000 | 28.7 us | 55.0 us | 417.1 us | 4.28 ms | 9,169 | 8 in flight |

### two-hop

The distinct nodes within two hops, which is a friend of a friend, and the operation where the cost of a hub shows up.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 791 ns | 2.8 us | 8.1 us | 8.6 us | 2,589 | 8 in flight |
| ladybug | 100 | 4.32 ms | 17.49 ms | 48.96 ms | 63.32 ms | 142 | 8 in flight |
| petgraph | 100 | 1.4 us | 4.6 us | 13.3 us | 13.4 us | 3,359 | 8 in flight |
| sqlite | 100 | 152.9 us | 682.6 us | 2.22 ms | 2.38 ms | 1,284 | 8 in flight |

### shortest-path

The hop count between two nodes, or nothing when they are not connected, which costs a full traversal of everything the start can reach.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 41.9 us | 161.3 us | 1.81 ms | 14.13 ms | 19,814 | 8 in flight |
| ladybug | 100 | 14.20 ms | 56.63 ms | 98.80 ms | 119.37 ms | 136 | 8 in flight |
| petgraph | 100 | 48.8 us | 188.3 us | 424.5 us | 6.45 ms | 17,283 | 8 in flight |
| sqlite | 100 | 46.69 ms | 250.16 ms | 362.61 ms | 420.02 ms | 14 | 8 in flight |

### bfs

The whole reachable set from one node, which touches everything and cannot be helped by any index.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 10 | 145.9 us | 216.1 us | 410.8 us | 410.8 us | 6,853 | one at a time |
| ladybug | 10 | 69.13 ms | 76.80 ms | 106.60 ms | 106.60 ms | 14 | one at a time |
| petgraph | 10 | 215.9 us | 538.6 us | 716.2 us | 716.2 us | 4,631 | one at a time |
| sqlite | 10 | 349.94 ms | 386.69 ms | 407.20 ms | 407.20 ms | 2 | one at a time |

### pagerank

The whole graph, several times over, which is the analytics workload rather than the serving one.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1 | 2.69 ms | 2.69 ms | 2.69 ms | 2.69 ms | 371 | one at a time |
| ladybug | | | | | | | its PageRank lives in an extension that is downloaded at first use, and a benchmark that reaches the network mid run is measuring the network |
| petgraph | 1 | 8.61 ms | 8.61 ms | 8.61 ms | 8.61 ms | 116 | one at a time |
| sqlite | 1 | 1.07 s | 1.07 s | 1.07 s | 1.07 s | 0 | one at a time |

The maximum matters more here than in the other suites.
Most nodes in a real graph have a handful of neighbours and a few have a hundred thousand, so the median says what the common case costs and the maximum says what a hub costs.
A cell that says below the clock is a lookup that finished inside one tick of the monotonic timer, which is a real result on a small graph and not a missing one.

## Notes

- ladybug: a property graph database reached through its C API, loaded with COPY from a CSV the way its own documentation loads a graph, and queried in Cypher rather than by walking the adjacency from the runner
- petgraph: petgraph is an in memory library with no on disk form, so the build phase writes the edges back out and the cold start is the graph being constructed again, so its open figure is a rebuild rather than a file being mapped
- sqlite: the edges live in one table with a covering index on both columns, and the traversals walk it a level at a time from Go, which is how a relational store is used as a graph in practice

