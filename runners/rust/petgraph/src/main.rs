//! petgraph, the graph data structure library most Rust programs reach for
//! first.
//!
//! It is in this suite because it is what somebody actually has when they say
//! they have a graph in memory. It is a library rather than a store: there is
//! no persistence, no query language and no transaction, and the whole graph
//! has to fit. What it does have is the adjacency representation, and that is
//! the thing the operations here spend all their time in.
//!
//! The operations are written against `neighbors`, which is petgraph's own
//! iterator over the edges leaving a node, rather than against its algorithm
//! module. That is deliberate and it is the only way the row means anything:
//! the algorithms in that module make their own choices about what a shortest
//! path costs and how PageRank handles a node with no outgoing edges, and two
//! engines answering different questions cannot be compared. What is left is
//! the comparison worth having, which is petgraph's adjacency against a flat
//! array of offsets.

use std::cell::RefCell;
use std::fs::File;
use std::io::{BufReader, BufWriter, Read, Write};
use std::path::{Path, PathBuf};

use petgraph::graph::{DiGraph, NodeIndex};

use benchrs::graph::config::Config;
use benchrs::graph::ops::Engine;
use benchrs::graph::{config, data, ops, result};
use benchrs::{machine, result as textresult, usage};

/// The four bytes at the front of the built store, so that a stale file from
/// another engine is refused rather than read as a graph.
const MAGIC: &[u8; 4] = b"KPET";

/// What every runner in this suite says about petgraph having no disk form.
const NO_DISK: &str = "petgraph is an in memory library with no on disk form, so the build phase writes the edges back out and the cold start is the graph being constructed again, so its open figure is a rebuild rather than a file being mapped";

fn main() {
    if let Err(err) = run() {
        eprintln!("petgraph-graphrunner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;

    let mut res = result::GraphResult {
        engine: "petgraph".to_string(),
        // The library's version, not this runner's. It is the pin in
        // Cargo.toml, and kura-versions is what notices when the two drift.
        version: "0.8.3".to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };

    match cfg.phase.as_str() {
        "build" => build_phase(&cfg, &mut res)?,
        "query" => query_phase(&cfg, &mut res)?,
        "all" => {
            build_phase(&cfg, &mut res)?;
            query_phase(&cfg, &mut res)?;
        }
        other => return Err(format!("unknown phase {other}").into()),
    }

    println!("{}", serde_json::to_string(&res)?);
    Ok(())
}

/// build_phase constructs the graph and writes the edges back out.
///
/// The write is not petgraph doing anything, it is this runner giving the query
/// process something to read, and it is counted in the build for the same
/// reason every other engine's write is: a graph nobody can get back is not a
/// build.
fn build_phase(
    cfg: &Config,
    res: &mut result::GraphResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let start = usage::take();

    let (header, edges) = data::edges(&cfg.edges)?;
    eprintln!("read {} edges over {} nodes", header.edges, header.nodes);
    let pet = Pet::build(&edges, header.undirected());
    write_edges(&store_path(&cfg.work), header.undirected(), &edges)?;

    res.build.usage = usage::measure(&start);
    let (bytes, files) = textresult::dir_size(&cfg.work);
    res.build.bytes = bytes;
    res.build.files = files;

    res.dataset = result::GraphStats {
        name: cfg.dataset.clone(),
        nodes: pet.nodes(),
        edges: pet.edges(),
        undirected: header.undirected(),
        seeds: 0,
    };
    res.notes = NO_DISK.to_string();
    Ok(())
}

/// query_phase constructs the graph in a process that has not seen it before,
/// then runs the operations.
fn query_phase(
    cfg: &Config,
    res: &mut result::GraphResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let open = usage::take();
    let (undirected, edges) = read_edges(&store_path(&cfg.work))?;
    let pet = Pet::build(&edges, undirected);

    let seeds = data::seeds(&cfg.seeds)?;
    if seeds.is_empty() {
        return Err("the seed file is empty".into());
    }
    let _ = pet.neighbours(seeds[0]);
    pet.warm();

    res.open.usage = usage::measure(&open);
    res.open.resident_bytes = res.open.usage.rss_bytes;

    let answers = data::answers(&cfg.answers)?;
    if answers.nodes != pet.nodes() || answers.edges != pet.edges() {
        return Err(format!(
            "the answers describe a graph of {} nodes and {} edges, this store holds {} and {}",
            answers.nodes,
            answers.edges,
            pet.nodes(),
            pet.edges()
        )
        .into());
    }

    let start = usage::take();
    res.query.ops = ops::run(&pet, cfg, &seeds, &answers);
    res.query.usage = usage::measure(&start);
    for op in &res.query.ops {
        eprintln!("{} scored {:.4} over {} runs", op.op, op.correct, op.runs);
    }

    res.dataset = result::GraphStats {
        name: cfg.dataset.clone(),
        nodes: pet.nodes(),
        edges: pet.edges(),
        undirected,
        seeds: seeds.len(),
    };
    if res.notes.is_empty() {
        res.notes = NO_DISK.to_string();
    }
    Ok(())
}

/// Pet is petgraph's directed graph, plus the lookup from the publisher's
/// identifiers to petgraph's.
struct Pet {
    graph: DiGraph<(), (), u32>,

    /// The distinct node identifiers, ascending. Node `i` of the graph is
    /// `ids[i]`, which makes the lookup in one direction an index and in the
    /// other a binary search.
    ids: Vec<u32>,
}

/// The per thread working set a traversal needs.
///
/// petgraph does not offer one, so this is the same one the baseline runner
/// keeps, for the same reason: a store that allocated five million counters
/// every time somebody asked for a two hop neighbourhood would be measuring the
/// allocator.
#[derive(Default)]
struct Scratch {
    seen: Vec<u32>,
    now: u32,
    frontier: Vec<u32>,
    next: Vec<u32>,
}

impl Scratch {
    fn reset(&mut self, nodes: usize) {
        if self.seen.len() != nodes {
            self.seen = vec![0; nodes];
            self.now = 0;
        }
        self.now += 1;
        if self.now == 0 {
            self.seen.iter_mut().for_each(|v| *v = 0);
            self.now = 1;
        }
        self.frontier.clear();
        self.next.clear();
    }

    fn mark(&mut self, i: u32) -> bool {
        let slot = &mut self.seen[i as usize];
        if *slot == self.now {
            return false;
        }
        *slot = self.now;
        true
    }
}

thread_local! {
    static SCRATCH: RefCell<Scratch> = RefCell::new(Scratch::default());
}

impl Pet {
    fn build(edges: &[u32], _undirected: bool) -> Pet {
        let mut ids: Vec<u32> = edges.to_vec();
        ids.sort_unstable();
        ids.dedup();

        // A directed graph in both cases. An undirected dataset arrives with
        // both directions of every edge already written out, so adding it as an
        // undirected graph would fold the pairs together and halve the degrees.
        let mut graph = DiGraph::with_capacity(ids.len(), edges.len() / 2);
        for _ in 0..ids.len() {
            graph.add_node(());
        }
        for pair in edges.as_chunks::<2>().0 {
            let from = ids
                .binary_search(&pair[0])
                .expect("edge endpoint is a node");
            let to = ids
                .binary_search(&pair[1])
                .expect("edge endpoint is a node");
            graph.add_edge(NodeIndex::new(from), NodeIndex::new(to), ());
        }
        Pet { graph, ids }
    }

    fn nodes(&self) -> usize {
        self.graph.node_count()
    }

    fn edges(&self) -> usize {
        self.graph.edge_count()
    }

    fn index(&self, id: u32) -> Option<u32> {
        self.ids.binary_search(&id).ok().map(|i| i as u32)
    }

    /// Sizes this thread's working set before anything is timed, so that the
    /// first allocation lands in the cold start rather than in the maximum of
    /// whichever operation happened to run first.
    fn warm(&self) {
        SCRATCH.with(|s| s.borrow_mut().reset(self.nodes()));
    }
}

impl Engine for Pet {
    fn neighbours(&self, node: u32) -> i64 {
        match self.index(node) {
            Some(i) => self.graph.neighbors(NodeIndex::new(i as usize)).count() as i64,
            None => -1,
        }
    }

    fn two_hop(&self, node: u32) -> i64 {
        let Some(from) = self.index(node) else {
            return -1;
        };
        SCRATCH.with(|cell| {
            let mut s = cell.borrow_mut();
            s.reset(self.nodes());
            s.mark(from);

            let mut n = 0i64;
            for one in self.graph.neighbors(NodeIndex::new(from as usize)) {
                if s.mark(one.index() as u32) {
                    n += 1;
                }
            }
            for one in self.graph.neighbors(NodeIndex::new(from as usize)) {
                for two in self.graph.neighbors(one) {
                    if s.mark(two.index() as u32) {
                        n += 1;
                    }
                }
            }
            n
        })
    }

    fn shortest_path(&self, from: u32, to: u32) -> i64 {
        let (Some(from), Some(to)) = (self.index(from), self.index(to)) else {
            return -1;
        };
        if from == to {
            return 0;
        }
        SCRATCH.with(|cell| {
            let mut s = cell.borrow_mut();
            s.reset(self.nodes());
            s.mark(from);

            let (mut cur, mut next) =
                (std::mem::take(&mut s.frontier), std::mem::take(&mut s.next));
            cur.push(from);

            let mut answer = -1i64;
            let mut depth = 1i64;
            'search: while !cur.is_empty() {
                next.clear();
                for n in &cur {
                    for m in self.graph.neighbors(NodeIndex::new(*n as usize)) {
                        let m = m.index() as u32;
                        if m == to {
                            answer = depth;
                            break 'search;
                        }
                        if s.mark(m) {
                            next.push(m);
                        }
                    }
                }
                std::mem::swap(&mut cur, &mut next);
                depth += 1;
            }

            s.frontier = cur;
            s.next = next;
            answer
        })
    }

    fn bfs(&self, node: u32) -> (i64, i64) {
        let Some(from) = self.index(node) else {
            return (-1, -1);
        };
        SCRATCH.with(|cell| {
            let mut s = cell.borrow_mut();
            s.reset(self.nodes());
            s.mark(from);

            let (mut cur, mut next) =
                (std::mem::take(&mut s.frontier), std::mem::take(&mut s.next));
            cur.push(from);

            let mut reached = 1i64;
            let mut depth = 0i64;
            while !cur.is_empty() {
                next.clear();
                for n in &cur {
                    for m in self.graph.neighbors(NodeIndex::new(*n as usize)) {
                        let m = m.index() as u32;
                        if s.mark(m) {
                            reached += 1;
                            next.push(m);
                        }
                    }
                }
                if !next.is_empty() {
                    depth += 1;
                }
                std::mem::swap(&mut cur, &mut next);
            }

            s.frontier = cur;
            s.next = next;
            (reached, depth)
        })
    }

    /// PageRank, with the mass on nodes that have no outgoing edges spread over
    /// every node rather than dropped.
    ///
    /// petgraph ships a `page_rank`, and it is not used, because it drops that
    /// mass instead. Both are defensible and they produce different rankings,
    /// so calling this runner's answer wrong against the reference would be
    /// reporting a disagreement about the definition as a defect in the
    /// library. What is timed here is petgraph's adjacency, iterated the same
    /// number of times over the same edges as everything else in the table.
    fn page_rank(&self, iterations: usize, damping: f64, top: usize) -> Vec<i64> {
        let n = self.nodes();
        if n == 0 {
            return Vec::new();
        }
        let mut rank = vec![1.0 / n as f64; n];
        let mut next = vec![0.0f64; n];

        for _ in 0..iterations {
            let mut dangling = 0.0f64;
            next.iter_mut().for_each(|v| *v = 0.0);
            for (i, r) in rank.iter().enumerate() {
                let out = self.graph.neighbors(NodeIndex::new(i)).count();
                if out == 0 {
                    dangling += r;
                    continue;
                }
                let share = r / out as f64;
                for m in self.graph.neighbors(NodeIndex::new(i)) {
                    next[m.index()] += share;
                }
            }
            let base = (1.0 - damping) / n as f64 + damping * dangling / n as f64;
            next.iter_mut().for_each(|v| *v = base + damping * *v);
            std::mem::swap(&mut rank, &mut next);
        }

        let mut order: Vec<u32> = (0..n as u32).collect();
        order.sort_by(|a, b| {
            let (x, y) = (rank[*a as usize], rank[*b as usize]);
            y.partial_cmp(&x)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| self.ids[*a as usize].cmp(&self.ids[*b as usize]))
        });
        order
            .into_iter()
            .take(top.min(n))
            .map(|i| self.ids[i as usize] as i64)
            .collect()
    }
}

fn store_path(work: &Path) -> PathBuf {
    work.join("graph.kpet")
}

fn write_edges(path: &Path, undirected: bool, edges: &[u32]) -> std::io::Result<()> {
    let mut f = BufWriter::with_capacity(1 << 20, File::create(path)?);
    f.write_all(MAGIC)?;
    f.write_all(&((edges.len() / 2) as u64).to_le_bytes())?;
    f.write_all(&u32::from(undirected).to_le_bytes())?;
    for v in edges {
        f.write_all(&v.to_le_bytes())?;
    }
    f.flush()
}

fn read_edges(path: &Path) -> Result<(bool, Vec<u32>), Box<dyn std::error::Error>> {
    let mut f = BufReader::with_capacity(1 << 20, File::open(path)?);

    let mut magic = [0u8; 4];
    f.read_exact(&mut magic)?;
    if &magic != MAGIC {
        return Err(format!("{} was not written by this runner", path.display()).into());
    }
    let mut head = [0u8; 12];
    f.read_exact(&mut head)?;
    let count = u64::from_le_bytes(head[0..8].try_into().unwrap()) as usize;
    let undirected = u32::from_le_bytes(head[8..12].try_into().unwrap()) != 0;

    let mut raw = vec![0u8; count * 8];
    f.read_exact(&mut raw)?;
    Ok((
        undirected,
        raw.as_chunks::<4>()
            .0
            .iter()
            .map(|c| u32::from_le_bytes(*c))
            .collect(),
    ))
}
