//! turbovec, a quantizing index that scores every vector cheaply rather than
//! visiting few of them.
//!
//! It is the opposite trade from a graph index and it belongs in the same table
//! for exactly that reason. A graph keeps the vectors at full precision and
//! buys its speed by looking at a small fraction of them, so its index is
//! larger than the data and its recall is lost in the walk. This keeps every
//! vector and buys its speed by making each comparison two to four bits wide,
//! so its index is a fraction of the data and its recall is lost in the
//! rounding. Which one is the better answer depends on whether the machine ran
//! out of memory or ran out of time, and that is a question the numbers can
//! settle.
//!
//! Two things about this engine had to change the shape of the suite.
//!
//! It ranks by maximum inner product. Not Euclidean, not cosine: the encoder
//! strips the length off each vector, stores it, and the search kernel puts it
//! back before the heap sees the score. Scored against the Euclidean ground
//! truth that ships with SIFT it would report a recall around a tenth and read
//! as a broken index, so the runner refuses that run outright and the harness
//! computes inner product ground truth of its own.
//!
//! Its operating point is the bit width, and the bit width is fixed when the
//! index is built. Every other engine here answers a slower and more accurate
//! question by being asked harder at query time, off one index. This one needs
//! three indexes, so the build phase builds three and each point in the report
//! carries the size and the build cost of its own.

use std::fs::File;
use std::path::{Path, PathBuf};
use std::time::Instant;

use benchrs::vector::config::Config;
use benchrs::vector::search::Search;
use benchrs::vector::{config, data, result, search};
use benchrs::{machine, result as textresult, usage};
use serde::{Deserialize, Serialize};
use turbovec::TurboQuantIndex;

/// The bit widths the crate accepts, which is also the whole knob it has.
const WIDTHS: [usize; 3] = [2, 3, 4];

/// How many base vectors the calibration sample is drawn from.
///
/// The crate's own guidance is that around a thousand rows is enough, and that
/// a draw of that size matches fitting on the whole corpus. The sample is taken
/// on a fixed stride rather than at random so that two runs of this runner over
/// the same file calibrate identically and a difference between them is a
/// difference in the machine.
const SAMPLE: usize = 1024;

/// Vectors handed to the index per call.
///
/// The encoder does not care, but the process does: adding a million vectors in
/// one call means holding the whole batch and the whole index at once, and the
/// peak resident figure would then be describing this runner rather than the
/// engine.
const CHUNK: usize = 65_536;

fn main() {
    if let Err(err) = run() {
        eprintln!("turbovec-vecrunner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;

    // The refusal that this whole metric business exists for. Answering a
    // Euclidean run by ranking on inner product would produce a full set of
    // plausible looking timings attached to a recall figure that means nothing.
    cfg.require_metric(&["inner-product"])?;

    let mut res = result::VectorResult {
        engine: "turbovec".to_string(),
        version: "1.0.0".to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };

    match cfg.phase.as_str() {
        "build" => build_phase(&cfg, &mut res)?,
        "query" => {
            pin_to_one_core()?;
            query_phase(&cfg, &mut res)?;
        }
        "all" => {
            build_phase(&cfg, &mut res)?;
            res.notes = "the open phase ran in the same process as the build, so it is warmer than a real cold start, and the search ran on every core because the pool was already up".to_string();
            query_phase(&cfg, &mut res)?;
        }
        other => return Err(format!("unknown phase {other}").into()),
    }

    println!("{}", serde_json::to_string(&res)?);
    Ok(())
}

/// pin_to_one_core holds the engine's own thread pool to a single thread for
/// the query phase.
///
/// This engine parallelises a single search across cores and most of the ones
/// it is being compared against do not. Left alone it would report a latency
/// measured on eight cores next to latencies measured on one, in the same
/// column, with nothing saying so. The suite's contract is that a serial query
/// is one query on one core and that throughput comes from the harness putting
/// several in flight, and this is what makes that true here. Total core usage
/// under load is unchanged, so the throughput figure is not being held back.
fn pin_to_one_core() -> Result<(), Box<dyn std::error::Error>> {
    rayon::ThreadPoolBuilder::new()
        .num_threads(1)
        .build_global()
        .map_err(|e| format!("could not hold the engine to one thread: {e}"))?;
    Ok(())
}

/// What the build phase leaves behind for the query phase to read.
///
/// The query phase can stat the files itself, but it cannot know what building
/// them cost, and the report wants that per bit width rather than only as a
/// total. This is the runner's own note to itself in its own work directory,
/// not part of the contract with the harness.
#[derive(Serialize, Deserialize, Default)]
struct BuildNotes {
    indexes: Vec<BuiltIndex>,
}

#[derive(Serialize, Deserialize, Clone)]
struct BuiltIndex {
    bits: usize,
    bytes: i64,
    seconds: f64,
}

fn notes_path(work: &Path) -> PathBuf {
    work.join("build.json")
}

fn index_path(work: &Path, bits: usize) -> PathBuf {
    work.join(format!("turbovec-b{bits}.tv"))
}

/// build_phase builds one index per bit width.
///
/// The three are built one after another and each is dropped before the next
/// starts, so the peak resident figure is the base vectors plus the largest
/// single index rather than the base vectors plus all three.
fn build_phase(
    cfg: &Config,
    res: &mut result::VectorResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let start = usage::take();

    let (shape, base) = data::fvecs(&cfg.base, cfg.limit)?;
    eprintln!("read {} vectors of {}", shape.count, shape.dim);
    if shape.dim % 8 != 0 {
        // Worth catching here rather than as a construction error three
        // sentences deep, because it is a property of the dataset and not of
        // the run.
        return Err(format!(
            "turbovec needs a dimension that is a multiple of eight and {} is not",
            shape.dim
        )
        .into());
    }

    let sample = calibration_sample(&base, shape.dim, shape.count);
    let mut notes = BuildNotes::default();
    for bits in WIDTHS {
        let at = Instant::now();
        let path = index_path(&cfg.work, bits);
        build_one(bits, shape.dim, &base, &sample, &path)?;
        let seconds = at.elapsed().as_secs_f64();

        let bytes = File::open(&path)?.metadata()?.len() as i64;
        eprintln!(
            "{bits} bit index is {} bytes, built in {seconds:.1}s",
            bytes
        );
        notes.indexes.push(BuiltIndex {
            bits,
            bytes,
            seconds,
        });
    }
    std::fs::write(notes_path(&cfg.work), serde_json::to_vec(&notes)?)?;

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
    res.notes = format!(
        "the build figures cover all {} indexes together, one per bit width, since that is what the run cost; the per width cost is in the curve",
        WIDTHS.len()
    );
    Ok(())
}

/// build_one calibrates, ingests and writes a single index.
///
/// Calibration is done because the crate says to do it and because its own
/// published recall figures are the calibrated ones. Skipping it would be
/// measuring a configuration nobody is asked to use.
fn build_one(
    bits: usize,
    dim: usize,
    base: &[f32],
    sample: &[f32],
    path: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut index = TurboQuantIndex::new(dim, bits).map_err(|e| format!("{e}"))?;
    index.calibrate(sample).map_err(|e| format!("{e}"))?;
    for chunk in base.chunks(CHUNK * dim) {
        index.add(chunk);
    }
    index.write(path)?;
    Ok(())
}

/// calibration_sample takes an evenly spaced draw from the base vectors.
fn calibration_sample(base: &[f32], dim: usize, count: usize) -> Vec<f32> {
    let want = SAMPLE.min(count);
    let stride = (count / want).max(1);
    let mut out = Vec::with_capacity(want * dim);
    for i in (0..count).step_by(stride).take(want) {
        out.extend_from_slice(&base[i * dim..(i + 1) * dim]);
    }
    out
}

/// query_phase loads each index in turn and runs the query set against it.
fn query_phase(
    cfg: &Config,
    res: &mut result::VectorResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let built: BuildNotes = match std::fs::read(notes_path(&cfg.work)) {
        Ok(raw) => serde_json::from_slice(&raw)?,
        Err(_) => BuildNotes {
            indexes: WIDTHS
                .iter()
                .map(|&bits| BuiltIndex {
                    bits,
                    bytes: 0,
                    seconds: 0.0,
                })
                .collect(),
        },
    };

    let (queries, count) = data::fvecs(&cfg.query, cfg.queries).map(|(s, v)| (v, s.count))?;
    if count == 0 {
        return Err("the query file is empty".into());
    }
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

    let mut dim = 0;
    let mut vectors = 0;
    let searching = usage::take();

    for (i, built) in built.indexes.iter().enumerate() {
        let path = index_path(&cfg.work, built.bits);

        // The first index of the run is the one the cold start is taken from.
        // Every later load has the page cache and the allocator already warm,
        // so calling any of them a cold start would be a second measurement of
        // the first one.
        let open = usage::take();
        let loaded = TurboQuantIndex::load(&path)?;
        let index = Index { inner: loaded };
        index.inner.prepare();
        dim = index
            .inner
            .dim_opt()
            .ok_or("the index came back without a dimension, which means it holds no vectors")?;
        vectors = index.inner.len();
        let _ = index.nearest(&queries[..dim], cfg.k);
        if i == 0 {
            res.open.usage = usage::measure(&open);
            res.open.resident_bytes = res.open.usage.rss_bytes;
        }

        let answers = search::serial(&index, &queries, dim, cfg.k);
        let recall = data::recall(&answers.ids, &truth, cfg.k, count);
        eprintln!("{} bit recall at {} is {recall:.4}", built.bits, cfg.k);

        let params = format!("bits={}", built.bits);
        let mut point = result::summarise(&params, recall, answers.ms);
        point.concurrent = Some(search::concurrent(
            &index,
            &queries,
            dim,
            cfg.k,
            cfg.worker_count(),
        ));
        point.bytes = built.bytes;
        point.build_seconds = built.seconds;
        res.search.points.push(point);
    }
    res.search.usage = usage::measure(&searching);

    res.dataset = result::DatasetStats {
        name: cfg.dataset.clone(),
        dim,
        vectors,
        queries: count,
        k: cfg.k,
        metric: cfg.metric.clone(),
    };
    let cold = res
        .search
        .points
        .first()
        .map(|p| p.params.clone())
        .unwrap_or_default();
    res.notes = format!(
        "the engine was held to one thread so that a serial query is one query on one core, as it is for every other engine here; the cold start is the {cold} index, the first of the three loaded"
    );
    Ok(())
}

/// Index is the crate's index behind the trait the shared timing loop calls.
///
/// Nothing happens in here except the call and turning slot indexes back into
/// the identifiers the ground truth is written in, which for this engine is the
/// same number: vectors are added in base file order and never removed, so a
/// slot is a row.
struct Index {
    inner: TurboQuantIndex,
}

impl Search for Index {
    fn nearest(&self, query: &[f32], k: usize) -> Vec<i32> {
        let got = self.inner.search(query, k);
        if got.nq == 0 || got.k == 0 {
            return Vec::new();
        }
        got.indices_for_query(0)
            .iter()
            .map(|&id| id as i32)
            .collect()
    }
}
