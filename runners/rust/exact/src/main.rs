//! The brute force baseline every approximate index here is measured against.
//!
//! It keeps the vectors as they came and compares a query against all of them.
//! That makes it the slowest engine in the report and the only one whose recall
//! is one by construction, which is exactly what it is for. An approximate
//! index is a trade of accuracy for speed, and without the price of not trading
//! at all there is nothing to say whether the trade was worth making.
//!
//! It is also the check on the rest of the suite. If this runner does not score
//! a recall of one against the published ground truth then the ground truth is
//! not being read correctly, and every other recall figure in the report is
//! wrong in the same way.

use std::fs::File;
use std::io::{BufReader, BufWriter, Read, Write};
use std::path::{Path, PathBuf};

use benchrs::vector::config::Config;
use benchrs::vector::search::Search;
use benchrs::vector::{config, data, result, search};
use benchrs::{machine, result as textresult, usage};

/// The four bytes at the front of the built index, so that a stale file from
/// another engine is refused rather than read as vectors.
const MAGIC: &[u8; 4] = b"KVEC";

fn main() {
    if let Err(err) = run() {
        eprintln!("exact-vecrunner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;

    // A scan can answer all three, which is the reason it is here for all three.
    // It is still asked, so that adding a fourth metric to the harness without
    // teaching this file about it fails loudly rather than quietly ranking by
    // the wrong thing.
    cfg.require_metric(&["euclidean", "cosine", "inner-product"])?;

    let mut res = result::VectorResult {
        engine: "exact".to_string(),
        // There is no library here, so the version is this runner's. Brute
        // force does not have releases.
        version: env!("CARGO_PKG_VERSION").to_string(),
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

/// build_phase reads the base vectors and writes them back out as one flat
/// block of f32.
///
/// Dropping the per record dimension header is the only thing that happens to
/// them, and it is not a favour: the header is four bytes on every vector and
/// no index in this report keeps it either. What this measures is the floor for
/// the whole suite, the cost of getting the vectors onto disk in a form that
/// can be searched at all.
///
/// The file is the same whatever the metric is. A scan has nothing to prepare,
/// so the question only shows up at query time.
fn build_phase(
    cfg: &Config,
    res: &mut result::VectorResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let start = usage::take();

    let (shape, base) = data::fvecs(&cfg.base, cfg.limit)?;
    eprintln!("read {} vectors of {}", shape.count, shape.dim);
    write_index(&index_path(&cfg.work), shape.dim, shape.count, &base)?;

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

/// query_phase opens the index in a process that has not read it before, then
/// runs the query set.
fn query_phase(
    cfg: &Config,
    res: &mut result::VectorResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let metric = Metric::parse(&cfg.metric)?;
    let open = usage::take();
    let index = Flat::open(&index_path(&cfg.work), metric)?;

    // One query, so that the open figure includes whatever the first search
    // costs. On a flat index that is the page cache filling, and it is the
    // largest part of a restart.
    let (queries, count) = load_queries(cfg)?;
    if count == 0 {
        return Err("the query file is empty".into());
    }
    let _ = index.nearest(&queries[..index.dim], cfg.k);

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

    let start = usage::take();
    let answers = search::serial(&index, &queries, index.dim, cfg.k);
    let recall = data::recall(&answers.ids, &truth, cfg.k, count);
    eprintln!("recall at {} is {recall:.4}", cfg.k);
    // Worth saying out loud either way, because a brute force scan that does not
    // score one looks like a broken benchmark and it is worth knowing which of
    // the two reasons it is.
    if recall < 1.0 && cfg.limit > 0 {
        // The run was given part of the base set and the ground truth is the
        // exact answer over all of it, so the true neighbours that were never
        // indexed cannot be found by anything. This is the floor every engine in
        // the run is measured against, not a fault of this one.
        res.notes = format!(
            "the scan scored {recall:.4} rather than 1 because the run indexed {} of the base vectors and the ground truth covers all of them, so this figure is the ceiling every other engine in the run is really being measured against",
            cfg.limit
        );
    } else if recall < 1.0 {
        // A tie is the only innocent explanation, and it is checked rather than
        // asserted. Saying "this is a tie" in a report without having looked is
        // how a scan that is quietly missing neighbours goes on being the thing
        // every other engine is scored against.
        let (missed, wrong) = disagreements(
            &index,
            &queries,
            &truth,
            gt_shape.dim,
            &answers.ids,
            cfg.k,
            count,
        );
        if wrong > 0 {
            return Err(format!(
                "the scan scored {recall:.4} and {wrong} of the {missed} neighbours it did not return are strictly nearer than something it did, so it is not finding the nearest vectors and every recall in this run is measured against a broken baseline"
            )
            .into());
        }
        res.notes = format!(
            "the scan scored {recall:.4} rather than 1, and all {missed} of the neighbours it did not return sit at exactly the same distance as one it did, so both answers are right and the tie cannot be resolved"
        );
    }

    // One point, no settings. There is no knob on a scan, which is the other
    // half of what makes it the baseline.
    let mut point = result::summarise("", recall, answers.ms);
    point.concurrent = Some(search::concurrent(
        &index,
        &queries,
        index.dim,
        cfg.k,
        cfg.worker_count(),
    ));
    res.search.points = vec![point];
    res.search.usage = usage::measure(&start);

    res.dataset = result::DatasetStats {
        name: cfg.dataset.clone(),
        dim: index.dim,
        vectors: index.count,
        queries: count,
        k: cfg.k,
        metric: cfg.metric.clone(),
    };
    Ok(())
}

/// Flat is the whole engine: the vectors, in order, in memory.
struct Flat {
    dim: usize,
    count: usize,
    data: Vec<f32>,
    metric: Metric,

    /// The length of each base vector, for cosine. Computing them once at open
    /// is the only preparation this index does, and it is counted in the cold
    /// start rather than hidden.
    norms: Vec<f32>,
}

/// What nearest means here.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Metric {
    Euclidean,
    Cosine,
    InnerProduct,
}

impl Metric {
    fn parse(s: &str) -> Result<Metric, String> {
        match s {
            "euclidean" => Ok(Metric::Euclidean),
            "cosine" => Ok(Metric::Cosine),
            "inner-product" => Ok(Metric::InnerProduct),
            other => Err(format!("no metric called {other}")),
        }
    }
}

impl Flat {
    fn open(path: &Path, metric: Metric) -> Result<Flat, Box<dyn std::error::Error>> {
        let mut f = BufReader::with_capacity(1 << 20, File::open(path)?);

        let mut magic = [0u8; 4];
        f.read_exact(&mut magic)?;
        if &magic != MAGIC {
            return Err(format!("{} was not written by this runner", path.display()).into());
        }
        let mut head = [0u8; 12];
        f.read_exact(&mut head)?;
        let dim = u32::from_le_bytes([head[0], head[1], head[2], head[3]]) as usize;
        let count = u64::from_le_bytes([
            head[4], head[5], head[6], head[7], head[8], head[9], head[10], head[11],
        ]) as usize;

        let mut raw = vec![0u8; count * dim * 4];
        f.read_exact(&mut raw)?;
        let mut values = Vec::with_capacity(count * dim);
        for chunk in raw.chunks_exact(4) {
            values.push(f32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]));
        }

        let mut norms = Vec::new();
        if metric == Metric::Cosine {
            norms = (0..count)
                .map(|i| {
                    let row = &values[i * dim..(i + 1) * dim];
                    let s: f32 = row.iter().map(|v| v * v).sum();
                    if s > 0.0 { s.sqrt() } else { 1.0 }
                })
                .collect();
        }
        Ok(Flat {
            dim,
            count,
            data: values,
            metric,
            norms,
        })
    }

    /// score is what [Flat::nearest] ranks by, for one base vector, with
    /// nothing abandoned part way.
    ///
    /// The search stops adding to a squared distance the moment it cannot win,
    /// which is worth doing a million times a query and is no use at all when
    /// the question is what the distance actually came to.
    fn score(&self, query: &[f32], i: usize) -> f32 {
        let row = &self.data[i * self.dim..(i + 1) * self.dim];
        match self.metric {
            Metric::Euclidean => squared(query, row, f32::INFINITY).unwrap_or(f32::INFINITY),
            Metric::InnerProduct => -dot(query, row),
            Metric::Cosine => -dot(query, row) / self.norms[i],
        }
    }
}

/// disagreements counts the true neighbours this scan did not return, and how
/// many of those it had no right to leave out.
///
/// There is one innocent reason a scan can differ from the ground truth: a
/// distance tie. Several base vectors sit at exactly the same distance from a
/// query, the answer only has room for k of them, and which k come back is
/// arbitrary. The ground truth kept one, this kept another, and both are right.
///
/// What is not innocent is a true neighbour that is strictly nearer than
/// something the scan did return, because that is the scan failing at the only
/// thing it is here to do. Counting the two separately is the difference
/// between a report that explains a number below one and a report that excuses
/// it.
///
/// The comparison is exact rather than within a tolerance, which on this data
/// it can afford to be. A SIFT component is a whole number below 256, so a
/// squared distance over 128 of them is a whole number below sixteen million,
/// and an f32 carries those without rounding any of them.
fn disagreements(
    index: &Flat,
    queries: &[f32],
    truth: &[i32],
    depth: usize,
    got: &[i32],
    k: usize,
    count: usize,
) -> (usize, usize) {
    // Nothing to check against. Reporting every neighbour as unexplained is the
    // honest answer: the run cannot show the difference is harmless, so it
    // should not be saying that it is.
    if k == 0 || depth < k || got.len() < count * k || truth.len() < count * depth {
        return (count * k, count * k);
    }

    let mut missed = 0;
    let mut wrong = 0;
    let known = |id: i32| id >= 0 && (id as usize) < index.count;

    for q in 0..count {
        let query = &queries[q * index.dim..(q + 1) * index.dim];
        let mine = &got[q * k..(q + 1) * k];
        let want = &truth[q * depth..q * depth + k];

        // The furthest thing the scan kept. A true neighbour it left out has to
        // be at least this far away, or the answer should have made room.
        let mut worst = f32::NEG_INFINITY;
        for &id in mine {
            if known(id) {
                worst = worst.max(index.score(query, id as usize));
            }
        }
        for &id in want {
            if mine.contains(&id) {
                continue;
            }
            missed += 1;
            if !known(id) || index.score(query, id as usize) < worst {
                wrong += 1;
            }
        }
    }
    (missed, wrong)
}

impl Search for Flat {
    fn nearest(&self, query: &[f32], k: usize) -> Vec<i32> {
        // A sorted list of the best k so far, worst last. It is a list rather
        // than a heap because k is ten: at that size the linear insert is
        // faster than the heap, and the insert happens on a vanishing fraction
        // of candidates once the list has filled.
        //
        // Every metric is turned into something smaller is better, so one
        // selection loop serves all three and no engine gets a different
        // selection than the others.
        let mut best: Vec<(f32, i32)> = Vec::with_capacity(k + 1);

        for i in 0..self.count {
            let row = &self.data[i * self.dim..(i + 1) * self.dim];
            let worst = if best.len() == k {
                best[k - 1].0
            } else {
                f32::INFINITY
            };
            let score = match self.metric {
                // Early abandoning applies to the sum of squares and to nothing
                // else: it works because the partial sum only ever grows, and
                // an inner product's does not.
                Metric::Euclidean => match squared(query, row, worst) {
                    Some(d) => d,
                    None => continue,
                },
                Metric::InnerProduct => -dot(query, row),
                // The query's own length is left out. It is the same positive
                // number for every candidate of a query, so it cannot change
                // which ten come back, and it would cost a square root per
                // query for nothing.
                Metric::Cosine => -dot(query, row) / self.norms[i],
            };
            if best.len() == k && score >= worst {
                continue;
            }
            let at = best.partition_point(|(x, _)| *x <= score);
            best.insert(at, (score, i as i32));
            best.truncate(k);
        }
        best.into_iter().map(|(_, id)| id).collect()
    }
}

/// Squared Euclidean distance, abandoned as soon as it cannot win.
///
/// The square root is not taken because it is monotone and nothing here needs
/// the distance itself, only the order. Early abandoning is the one
/// optimisation applied, and it is applied because every serious brute force
/// implementation has it, so leaving it out would make the baseline slower than
/// the thing it is meant to be a baseline for.
fn squared(a: &[f32], b: &[f32], worst: f32) -> Option<f32> {
    let mut sum = 0.0f32;
    for (chunk_a, chunk_b) in a.chunks_exact(8).zip(b.chunks_exact(8)) {
        for i in 0..8 {
            let d = chunk_a[i] - chunk_b[i];
            sum += d * d;
        }
        if sum >= worst {
            return None;
        }
    }
    for i in (a.len() - a.len() % 8)..a.len() {
        let d = a[i] - b[i];
        sum += d * d;
    }
    if sum >= worst { None } else { Some(sum) }
}

fn dot(a: &[f32], b: &[f32]) -> f32 {
    a.iter().zip(b).map(|(x, y)| x * y).sum()
}

fn index_path(work: &Path) -> PathBuf {
    work.join("vectors.kvec")
}

fn write_index(path: &Path, dim: usize, count: usize, values: &[f32]) -> std::io::Result<()> {
    let mut f = BufWriter::with_capacity(1 << 20, File::create(path)?);
    f.write_all(MAGIC)?;
    f.write_all(&(dim as u32).to_le_bytes())?;
    f.write_all(&(count as u64).to_le_bytes())?;
    for v in values {
        f.write_all(&v.to_le_bytes())?;
    }
    f.flush()
}

/// load_queries reads the query vectors and says how many there are.
fn load_queries(cfg: &Config) -> Result<(Vec<f32>, usize), Box<dyn std::error::Error>> {
    let (shape, queries) = data::fvecs(&cfg.query, cfg.queries)?;
    Ok((queries, shape.count))
}

#[cfg(test)]
mod tests {
    use super::*;

    /// flat builds an index in memory, so a test can put vectors at distances it
    /// chose rather than at whatever a dataset happens to contain.
    fn flat(dim: usize, rows: &[&[f32]]) -> Flat {
        Flat {
            dim,
            count: rows.len(),
            data: rows.iter().flat_map(|r| r.iter().copied()).collect(),
            metric: Metric::Euclidean,
            norms: Vec::new(),
        }
    }

    #[test]
    fn a_neighbour_at_the_same_distance_is_a_tie_and_not_a_miss() {
        // Two base vectors either side of the query at the same distance, and
        // room in the answer for one of them. Whichever comes back, the other
        // one is a true neighbour that was left out for a reason.
        let index = flat(1, &[&[-1.0], &[1.0]]);
        let (missed, wrong) = disagreements(&index, &[0.0], &[1], 1, &[0], 1, 1);
        assert_eq!(missed, 1, "the ground truth's neighbour was not returned");
        assert_eq!(
            wrong, 0,
            "it is the same distance away, so nothing is wrong"
        );
    }

    #[test]
    fn a_nearer_neighbour_that_was_missed_is_wrong() {
        // The ground truth's neighbour sits on the query and the scan came back
        // with one further away, which no tie can explain.
        let index = flat(1, &[&[5.0], &[0.0]]);
        let (missed, wrong) = disagreements(&index, &[0.0], &[1], 1, &[0], 1, 1);
        assert_eq!(missed, 1);
        assert_eq!(wrong, 1, "the neighbour it missed was strictly nearer");
    }

    #[test]
    fn agreeing_with_the_ground_truth_leaves_nothing_to_explain() {
        let index = flat(1, &[&[0.0], &[9.0]]);
        assert_eq!(disagreements(&index, &[0.0], &[0], 1, &[0], 1, 1), (0, 0));
    }

    #[test]
    fn ground_truth_that_is_too_shallow_to_check_is_not_called_a_tie() {
        // Nothing to compare against, so every neighbour counts as unexplained
        // and the caller refuses the run rather than reporting a tie it never
        // looked for.
        let index = flat(1, &[&[0.0], &[9.0]]);
        let (missed, wrong) = disagreements(&index, &[0.0], &[0], 1, &[0, 1], 2, 1);
        assert_eq!(missed, 2);
        assert_eq!(wrong, 2);
    }
}
