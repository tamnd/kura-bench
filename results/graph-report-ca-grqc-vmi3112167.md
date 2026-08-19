# Graph results on vmi3112167

Graph ca-grqc from graphdata/ca-grqc, The Arxiv General Relativity collaboration network, 5,242 authors and 14,490 collaborations stored in both directions.

The plan is 1000 neighbour lookups, 100 two hop lookups, 100 shortest paths, 10 full traversals, and pagerank over 20 iterations at damping 0.85.
The nodes are a fixed sample, so every engine is asked about the same ones in the same order, and a run with fewer of them is a subset of a run with more.

## Machine

vmi3112167, linux/x86_64, AMD EPYC Processor (with IBPB), 6 cores, 11.68 GB of memory.
Load before the run was 8.62 and 6.15 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

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
| csr | 6 ms | 4,525,936 | 0.0 | 1.0x | 6.0 MB |
| ladybug | 1.0 s | 27,952 | 0.9 | 0.9x | 79.3 MB |
| petgraph | 6 ms | 4,571,577 | 0.0 | 0.9x | 6.2 MB |
| sqlite | 405 ms | 71,520 | 0.3 | 0.8x | 15.1 MB |

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
| ladybug | 192 ms | 0.19 | 53.1 MB |
| petgraph | 5 ms | 0.00 | 3.3 MB |
| sqlite | 3 ms | 0.00 | 8.9 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Operations

### neighbours

One hop out of one node, the cheapest thing a graph store does and the one it does most.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1,000 | 90 ns | 110 ns | 390 ns | 821 ns | 278,501 | 6 in flight |
| ladybug | 1,000 | 6.05 ms | 16.03 ms | 64.61 ms | 146.18 ms | 142 | 6 in flight |
| petgraph | 1,000 | 150 ns | 360 ns | 631 ns | 1.0 us | 61,463 | 6 in flight |
| sqlite | 1,000 | 33.9 us | 62.5 us | 256.6 us | 6.13 ms | 13,732 | 6 in flight |

### two-hop

The distinct nodes within two hops, which is a friend of a friend, and the operation where the cost of a hub shows up.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 942 ns | 3.9 us | 82.2 us | 625.6 us | 32,264 | 6 in flight |
| ladybug | 100 | 9.80 ms | 17.61 ms | 58.36 ms | 85.55 ms | 111 | 6 in flight |
| petgraph | 100 | 1.4 us | 5.9 us | 23.6 us | 25.2 us | 17,330 | 6 in flight |
| sqlite | 100 | 147.7 us | 1.05 ms | 6.92 ms | 7.71 ms | 1,748 | 6 in flight |

### shortest-path

The hop count between two nodes, or nothing when they are not connected, which costs a full traversal of everything the start can reach.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 37.6 us | 150.5 us | 249.6 us | 2.98 ms | 3,223 | 6 in flight |
| ladybug | 100 | 19.29 ms | 47.74 ms | 89.22 ms | 100.91 ms | 122 | 6 in flight |
| petgraph | 100 | 59.5 us | 343.1 us | 3.75 ms | 5.10 ms | 9,240 | 6 in flight |
| sqlite | 100 | 29.40 ms | 213.37 ms | 345.82 ms | 435.67 ms | 14 | 6 in flight |

### bfs

The whole reachable set from one node, which touches everything and cannot be helped by any index.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 10 | 142.2 us | 231.1 us | 293.8 us | 293.8 us | 7,034 | one at a time |
| ladybug | 10 | 51.02 ms | 86.07 ms | 196.08 ms | 196.08 ms | 19 | one at a time |
| petgraph | 10 | 189.9 us | 1.61 ms | 1.81 ms | 1.81 ms | 5,267 | one at a time |
| sqlite | 10 | 246.79 ms | 416.52 ms | 450.05 ms | 450.05 ms | 4 | one at a time |

### pagerank

The whole graph, several times over, which is the analytics workload rather than the serving one.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1 | 2.46 ms | 2.46 ms | 2.46 ms | 2.46 ms | 406 | one at a time |
| ladybug | | | | | | | its PageRank lives in an extension that is downloaded at first use, and a benchmark that reaches the network mid run is measuring the network |
| petgraph | 1 | 14.19 ms | 14.19 ms | 14.19 ms | 14.19 ms | 70 | one at a time |
| sqlite | 1 | 876.14 ms | 876.14 ms | 876.14 ms | 876.14 ms | 1 | one at a time |

The maximum matters more here than in the other suites.
Most nodes in a real graph have a handful of neighbours and a few have a hundred thousand, so the median says what the common case costs and the maximum says what a hub costs.
A cell that says below the clock is a lookup that finished inside one tick of the monotonic timer, which is a real result on a small graph and not a missing one.

## Notes

- ladybug: a property graph database reached through its C API, loaded with COPY from a CSV the way its own documentation loads a graph, and queried in Cypher rather than by walking the adjacency from the runner
- petgraph: petgraph is an in memory library with no on disk form, so the build phase writes the edges back out and the cold start is the graph being constructed again, so its open figure is a rebuild rather than a file being mapped
- sqlite: the edges live in one table with a covering index on both columns, and the traversals walk it a level at a time from Go, which is how a relational store is used as a graph in practice

