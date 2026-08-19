//! hnsw_rs, the navigable small world graph.
//!
//! This is the shape of index most vector databases are built on, so it is the
//! one the rest of the table is really being compared against. It keeps the
//! vectors at full precision and buys its speed by visiting a small fraction of
//! them, which is why its index is larger than the data it indexes and why its
//! build phase is the longest number in the report.
//!
//! Its knob is at query time, which makes it the easy case for this suite: one
//! index, searched at a range of ef values, gives the whole recall against
//! speed curve without rebuilding anything. The graph parameters are held at
//! the values hnswlib has defaulted to for years, because a benchmark that
//! tuned one engine and not the others is measuring the tuning.

use std::path::Path;
use std::time::Instant;

use benchrs::vector::config::Config;
use benchrs::vector::search::Search;
use benchrs::vector::{config, data, result, search};
use benchrs::{machine, result as textresult, usage};
use hnsw_rs::prelude::*;

/// Neighbours kept per node, and the width of the search used while building.
///
/// These are hnswlib's long standing defaults. They are not the fastest
/// settings this crate can be pushed to, and that is deliberate: the point of
/// the row is what somebody gets from the library as it comes.
const CONNECTIONS: usize = 16;
const EF_CONSTRUCTION: usize = 200;

/// The query time settings the curve is drawn from.
///
/// The list starts below k times two, which is where an HNSW starts losing
/// neighbours, and ends where the returns have clearly stopped. Anything
/// outside that range is a point nobody would ever run.
const EF_SEARCH: [usize; 6] = [16, 32, 64, 128, 256, 512];

/// How many layers the graph is given room for.
///
/// The crate's examples derive this from the log of the corpus size, and that
/// is fine right up until you try to write the index out: the dump refuses any
/// graph whose layer count is not the library's maximum of sixteen. Since this
/// suite always writes the index and opens it in another process, sixteen is
/// the only value that works. Nothing is lost by it. The upper layers are
/// reached at a probability set by the level scale and stay empty if the corpus
/// is too small to reach them.
const LAYERS: usize = 16;

/// Vectors handed to the inserter per call.
const CHUNK: usize = 16_384;

/// The name the two dump files are built from.
const BASENAME: &str = "index";

fn main() {
    if let Err(err) = run() {
        eprintln!("hnsw-vecrunner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;

    // Inner product is missing on purpose. The crate has a dot product distance
    // but it requires every vector to have been normalized to unit length
    // first, which makes it another spelling of cosine rather than the maximum
    // inner product ranking a recommender wants, and it asserts rather than
    // returns when handed a vector that is not.
    cfg.require_metric(&["euclidean", "cosine"])?;

    let mut res = result::VectorResult {
        engine: "hnsw".to_string(),
        version: "0.3.4".to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };

    match cfg.phase.as_str() {
        "build" => build_phase(&cfg, &mut res)?,
        "query" => query_phase(&cfg, &mut res)?,
        "all" => {
            build_phase(&cfg, &mut res)?;
            res.notes = "the open phase ran in the same process as the build, so it is warmer than a real cold start".to_string();
            query_phase(&cfg, &mut res)?;
        }
        other => return Err(format!("unknown phase {other}").into()),
    }

    println!("{}", serde_json::to_string(&res)?);
    Ok(())
}

fn build_phase(
    cfg: &Config,
    res: &mut result::VectorResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let start = usage::take();

    let (shape, base) = data::fvecs(&cfg.base, cfg.limit)?;
    eprintln!("read {} vectors of {}", shape.count, shape.dim);

    match cfg.metric.as_str() {
        "euclidean" => {
            let g = Hnsw::<f32, DistL2>::new(
                CONNECTIONS,
                shape.count,
                LAYERS,
                EF_CONSTRUCTION,
                DistL2 {},
            );
            insert(&g, &base, shape.dim, shape.count);
            g.file_dump(Path::new(&cfg.work), BASENAME)?;
        }
        _ => {
            let g = Hnsw::<f32, DistCosine>::new(
                CONNECTIONS,
                shape.count,
                LAYERS,
                EF_CONSTRUCTION,
                DistCosine {},
            );
            insert(&g, &base, shape.dim, shape.count);
            g.file_dump(Path::new(&cfg.work), BASENAME)?;
        }
    }

    res.build.usage = usage::measure(&start);
    let (bytes, files) = textresult::dir_size(&cfg.work);
    res.build.bytes = bytes;
    res.build.files = files;

    res.dataset = result::DatasetStats {
        name: cfg.dataset.clone(),
        dim: shape.dim,
        vectors: shape.count,
        queries: 0,
        k: cfg.k,
        metric: cfg.metric.clone(),
    };
    Ok(())
}

/// insert adds the base vectors, in order, so that a vector's identifier is its
/// row in the base file and no mapping is needed at query time.
///
/// It goes in parallel because that is how the library is meant to be fed and
/// how anybody building an index of this size would feed it. The chunking is
/// only to keep the vector of slice references from being as large as the data
/// again.
fn insert<D>(graph: &Hnsw<'_, f32, D>, base: &[f32], dim: usize, count: usize)
where
    D: Distance<f32> + Send + Sync,
{
    let mut done = 0;
    while done < count {
        let end = (done + CHUNK).min(count);
        let batch: Vec<(&[f32], usize)> = (done..end)
            .map(|i| (&base[i * dim..(i + 1) * dim], i))
            .collect();
        graph.parallel_insert_slice(&batch);
        done = end;
        eprintln!("inserted {done} of {count}");
    }
}

fn query_phase(
    cfg: &Config,
    res: &mut result::VectorResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let (queries, count) = data::fvecs(&cfg.query, cfg.queries).map(|(s, v)| (v, s.count))?;
    if count == 0 {
        return Err("the query file is empty".into());
    }

    let open = usage::take();
    let graph = load(cfg)?;
    let dim = queries.len() / count;

    // One query at the widest setting, so that whatever the first search has to
    // touch is inside the cold start rather than inside the first point.
    let warm = Index {
        graph: &graph,
        ef: *EF_SEARCH.last().unwrap_or(&64),
    };
    let _ = warm.nearest(&queries[..dim], cfg.k);
    res.open.usage = usage::measure(&open);
    res.open.resident_bytes = res.open.usage.rss_bytes;

    let (gt_shape, truth) = data::ivecs(&cfg.groundtruth, count)?;
    if gt_shape.count < count {
        return Err(format!(
            "ground truth covers {} queries and {count} were asked",
            gt_shape.count
        )
        .into());
    }
    if cfg.k > gt_shape.dim {
        return Err(format!(
            "k of {} cannot be scored against ground truth that is {} deep",
            cfg.k, gt_shape.dim
        )
        .into());
    }

    let searching = usage::take();
    for ef in EF_SEARCH {
        // An ef below k cannot return k neighbours at all, so the point would be
        // measuring a truncated answer rather than a fast one.
        if ef < cfg.k {
            continue;
        }
        let index = Index { graph: &graph, ef };
        let at = Instant::now();
        let answers = search::serial(&index, &queries, dim, cfg.k);
        let recall = data::recall(&answers.ids, &truth, cfg.k, count);
        eprintln!(
            "ef {ef} recall at {} is {recall:.4}, {:.1}s",
            cfg.k,
            at.elapsed().as_secs_f64()
        );

        let mut point = result::summarise(&format!("ef={ef}"), recall, answers.ms);
        point.concurrent = Some(search::concurrent(
            &index,
            &queries,
            dim,
            cfg.k,
            cfg.worker_count(),
        ));
        res.search.points.push(point);
    }
    res.search.usage = usage::measure(&searching);

    res.dataset = result::DatasetStats {
        name: cfg.dataset.clone(),
        dim,
        vectors: graph.points(),
        queries: count,
        k: cfg.k,
        metric: cfg.metric.clone(),
    };
    res.notes = format!(
        "built with {CONNECTIONS} connections per node and an ef of {EF_CONSTRUCTION}, which are the defaults the library is usually run at rather than settings picked for this run"
    );
    Ok(())
}

/// Graph is the loaded index, in whichever distance the run asked for.
///
/// Two distances mean two concrete types, because the distance is a type
/// parameter rather than a value, so the runner carries both arms and picks one
/// at load. There is nothing clever available here: the alternative is a
/// dynamic distance, which would put a virtual call in the innermost loop and
/// report a slower engine than the one anybody uses.
enum Graph {
    L2(Hnsw<'static, f32, DistL2>),
    Cosine(Hnsw<'static, f32, DistCosine>),
}

impl Graph {
    fn points(&self) -> usize {
        match self {
            Graph::L2(g) => g.get_nb_point(),
            Graph::Cosine(g) => g.get_nb_point(),
        }
    }

    fn search(&self, query: &[f32], k: usize, ef: usize) -> Vec<Neighbour> {
        match self {
            Graph::L2(g) => g.search(query, k, ef),
            Graph::Cosine(g) => g.search(query, k, ef),
        }
    }
}

/// load reads the dumped graph back.
///
/// The reader owns the buffers the graph points into, so the graph borrows it
/// and the two cannot be returned together. Leaking the reader is what makes
/// the lifetime work out, and it is the right answer in a process whose whole
/// job is to search that one graph and exit: the memory is held until the
/// process ends either way, and the alternative is a self referential struct or
/// a copy of the whole index.
fn load(cfg: &Config) -> Result<Graph, Box<dyn std::error::Error>> {
    let io: &'static mut HnswIo = Box::leak(Box::new(HnswIo::new(Path::new(&cfg.work), BASENAME)));
    match cfg.metric.as_str() {
        "euclidean" => Ok(Graph::L2(io.load_hnsw::<f32, DistL2>()?)),
        _ => Ok(Graph::Cosine(io.load_hnsw::<f32, DistCosine>()?)),
    }
}

/// Index is one operating point: the graph, searched at one ef.
struct Index<'a> {
    graph: &'a Graph,
    ef: usize,
}

impl Search for Index<'_> {
    fn nearest(&self, query: &[f32], k: usize) -> Vec<i32> {
        self.graph
            .search(query, k, self.ef)
            .into_iter()
            .map(|n| n.d_id as i32)
            .collect()
    }
}
