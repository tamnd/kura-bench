//! The flags a vector runner is invoked with.
//!
//! They are the Go harness's flags, spelled exactly the same way, because
//! kura-vecbench invokes every runner with the same command line and does not
//! know which language any of them is written in.

use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Config {
    /// The base vectors, an fvecs file.
    pub base: PathBuf,

    /// The query vectors, an fvecs file.
    pub query: PathBuf,

    /// The exact nearest neighbours of each query, an ivecs file.
    pub groundtruth: PathBuf,

    /// The dataset's name, carried into the result so a report says what it is
    /// a report about.
    pub dataset: String,

    /// What nearest means: euclidean, cosine or inner-product.
    ///
    /// The ground truth file the harness passes was computed under this metric
    /// and no other. An engine that only implements one of the three has to
    /// refuse the others rather than answer them, because answering the wrong
    /// question produces a recall figure that reads as a bad index.
    pub metric: String,

    /// The directory the index is built in.
    pub work: PathBuf,

    /// build, query, or all. The orchestrator runs build and query as two
    /// processes so that the open phase is a real cold start; all is for
    /// running a runner by hand.
    pub phase: String,

    /// How many neighbours each query asks for, and the depth recall is scored
    /// at.
    pub k: usize,

    /// Index only this many base vectors, zero for all of them.
    pub limit: usize,

    /// Use this many query vectors, zero for all of them.
    pub queries: usize,

    /// Queries in flight for the throughput run, zero for one per core.
    pub workers: usize,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            base: PathBuf::new(),
            query: PathBuf::new(),
            groundtruth: PathBuf::new(),
            dataset: String::new(),
            metric: "euclidean".to_string(),
            work: PathBuf::new(),
            phase: "all".to_string(),
            k: 10,
            limit: 0,
            queries: 0,
            workers: 0,
        }
    }
}

impl Config {
    /// How many workers the throughput run should use.
    pub fn worker_count(&self) -> usize {
        if self.workers > 0 {
            return self.workers;
        }
        std::thread::available_parallelism()
            .map(|n| n.get())
            .unwrap_or(1)
    }

    /// Whether this phase builds the index.
    pub fn builds(&self) -> bool {
        self.phase == "build" || self.phase == "all"
    }

    /// Whether this phase searches.
    pub fn searches(&self) -> bool {
        self.phase == "query" || self.phase == "all"
    }

    /// Refuses a metric this engine cannot answer.
    ///
    /// It is a refusal rather than a warning. A runner that quietly ranked by
    /// inner product against Euclidean ground truth would report a recall of
    /// about a tenth and look like a bad index, and nothing in the table would
    /// say why. What the caller does with it is write it into the result and
    /// exit cleanly, so the engine keeps its row and the row says why it is
    /// empty.
    pub fn require_metric(&self, supported: &[&str]) -> Result<(), String> {
        if supported.contains(&self.metric.as_str()) {
            return Ok(());
        }
        Err(format!(
            "this engine ranks by {}, and the run asked for {}",
            supported.join(" or "),
            self.metric
        ))
    }
}

/// Parses the command line.
///
/// The flags are parsed by hand rather than with a crate because they are
/// somebody else's flags. A parser that spelled them its own way, or that
/// accepted `--k=10` when the Go side does not, would be a difference between
/// runners that has nothing to do with the engines.
pub fn parse(args: &[String]) -> Result<Config, String> {
    let mut cfg = Config::default();

    let mut i = 0;
    while i < args.len() {
        let name = args[i].trim_start_matches('-');
        let value = args.get(i + 1).cloned().unwrap_or_default();
        match name {
            "base" => cfg.base = PathBuf::from(&value),
            "query" => cfg.query = PathBuf::from(&value),
            "groundtruth" => cfg.groundtruth = PathBuf::from(&value),
            "dataset" => cfg.dataset = value.clone(),
            "metric" => cfg.metric = value.clone(),
            "work" => cfg.work = PathBuf::from(&value),
            "phase" => cfg.phase = value.clone(),
            "k" => cfg.k = parse_usize("k", &value)?,
            "limit" => cfg.limit = parse_usize("limit", &value)?,
            "queries" => cfg.queries = parse_usize("queries", &value)?,
            "workers" => cfg.workers = parse_usize("workers", &value)?,
            other => return Err(format!("unknown flag {other}")),
        }
        i += 2;
    }

    if cfg.base.as_os_str().is_empty() || cfg.work.as_os_str().is_empty() {
        return Err("both -base and -work are required".to_string());
    }
    if cfg.searches()
        && (cfg.query.as_os_str().is_empty() || cfg.groundtruth.as_os_str().is_empty())
    {
        // A query phase without ground truth would report a latency and no
        // recall, and a latency without a recall is not a result. An
        // approximate index can answer instantly by answering badly.
        return Err("-query and -groundtruth are required for the query phase".to_string());
    }
    if cfg.k == 0 {
        return Err("-k must be at least one".to_string());
    }
    Ok(cfg)
}

/// Parses the process's own arguments.
pub fn from_env() -> Result<Config, String> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    parse(&args)
}

fn parse_usize(flag: &str, value: &str) -> Result<usize, String> {
    value
        .parse()
        .map_err(|_| format!("-{flag} wants a number, got {value:?}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args(s: &[&str]) -> Vec<String> {
        s.iter().map(|x| x.to_string()).collect()
    }

    #[test]
    fn it_takes_the_flags_the_go_harness_sends() {
        let cfg = parse(&args(&[
            "-base",
            "base.fvecs",
            "-query",
            "query.fvecs",
            "-groundtruth",
            "gt.ivecs",
            "-dataset",
            "sift",
            "-metric",
            "cosine",
            "-work",
            "w",
            "-phase",
            "query",
            "-k",
            "10",
            "-limit",
            "100000",
            "-queries",
            "1000",
            "-workers",
            "8",
        ]))
        .unwrap();

        assert_eq!(cfg.base, PathBuf::from("base.fvecs"));
        assert_eq!(cfg.groundtruth, PathBuf::from("gt.ivecs"));
        assert_eq!(cfg.dataset, "sift");
        assert_eq!(cfg.metric, "cosine");
        assert_eq!(cfg.phase, "query");
        assert_eq!(cfg.k, 10);
        assert_eq!(cfg.limit, 100_000);
        assert_eq!(cfg.queries, 1000);
        assert_eq!(cfg.workers, 8);
    }

    /// A build phase has nothing to score, so it needs no ground truth. A query
    /// phase without one would report a speed and call it a result.
    #[test]
    fn only_the_query_phase_needs_ground_truth() {
        assert!(parse(&args(&["-base", "b", "-work", "w", "-phase", "build"])).is_ok());
        assert!(parse(&args(&["-base", "b", "-work", "w", "-phase", "query"])).is_err());
    }

    /// An engine that cannot answer the question being asked says so instead
    /// of answering a different one.
    #[test]
    fn a_metric_the_engine_does_not_do_is_refused() {
        let cfg = Config {
            metric: "euclidean".to_string(),
            ..Default::default()
        };
        assert!(cfg.require_metric(&["inner-product"]).is_err());
        assert!(cfg.require_metric(&["euclidean", "cosine"]).is_ok());
    }

    #[test]
    fn an_unknown_flag_is_refused_rather_than_ignored() {
        assert!(parse(&args(&["-base", "b", "-work", "w", "-ef", "64"])).is_err());
    }

    #[test]
    fn asking_for_no_neighbours_is_refused() {
        assert!(
            parse(&args(&[
                "-base", "b", "-work", "w", "-phase", "build", "-k", "0"
            ]))
            .is_err()
        );
    }
}
