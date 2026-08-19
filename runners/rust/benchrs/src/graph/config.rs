//! The flags a graph runner is invoked with.
//!
//! They are the Go harness's flags, spelled exactly the same way, because
//! kura-graphbench invokes every runner with the same command line and does not
//! know which language any of them is written in.

use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Config {
    /// The whole graph, the fixed width file kura-graphs wrote.
    pub edges: PathBuf,

    /// The nodes the run asks about, in the order it asks about them.
    pub seeds: PathBuf,

    /// What the operations should come back with, and the plan that says how
    /// many of each to run. The plan lives in this file rather than in a flag
    /// so that there is one copy of it and it is the copy the answers were
    /// worked out from.
    pub answers: PathBuf,

    /// The graph's name, carried into the result so a report says what it is a
    /// report about.
    pub dataset: String,

    /// The directory the store is built in.
    pub work: PathBuf,

    /// build, query, or all. The orchestrator runs build and query as two
    /// processes so that the open phase is a real cold start; all is for
    /// running a runner by hand.
    pub phase: String,

    /// The operations to run, empty for every one in the plan. It is here for
    /// the same reason kura-graphbench has an engine list: a run that is
    /// chasing one number should not have to wait for a full traversal of
    /// LiveJournal first.
    pub ops: Vec<String>,

    /// Queries in flight for the throughput run, zero for one per core.
    pub workers: usize,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            edges: PathBuf::new(),
            seeds: PathBuf::new(),
            answers: PathBuf::new(),
            dataset: String::new(),
            work: PathBuf::new(),
            phase: "all".to_string(),
            ops: Vec::new(),
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

    /// Whether this phase builds the store.
    pub fn builds(&self) -> bool {
        self.phase == "build" || self.phase == "all"
    }

    /// Whether this phase runs the operations.
    pub fn queries(&self) -> bool {
        self.phase == "query" || self.phase == "all"
    }

    /// Whether an operation was asked for, which it was if nothing was asked
    /// for.
    pub fn wants(&self, op: &str) -> bool {
        self.ops.is_empty() || self.ops.iter().any(|o| o == op)
    }
}

/// Parses the command line.
///
/// The flags are parsed by hand rather than with a crate because they are
/// somebody else's flags. A parser that spelled them its own way, or that
/// accepted `--ops=bfs` when the Go side does not, would be a difference
/// between runners that has nothing to do with the engines.
pub fn parse(args: &[String]) -> Result<Config, String> {
    let mut cfg = Config::default();

    let mut i = 0;
    while i < args.len() {
        let name = args[i].trim_start_matches('-');
        let value = args.get(i + 1).cloned().unwrap_or_default();
        match name {
            "edges" => cfg.edges = PathBuf::from(&value),
            "seeds" => cfg.seeds = PathBuf::from(&value),
            "answers" => cfg.answers = PathBuf::from(&value),
            "dataset" => cfg.dataset = value.clone(),
            "work" => cfg.work = PathBuf::from(&value),
            "phase" => cfg.phase = value.clone(),
            "ops" => {
                cfg.ops = value
                    .split(',')
                    .map(str::trim)
                    .filter(|s| !s.is_empty())
                    .map(str::to_string)
                    .collect()
            }
            "workers" => cfg.workers = parse_usize("workers", &value)?,
            other => return Err(format!("unknown flag {other}")),
        }
        i += 2;
    }

    if cfg.edges.as_os_str().is_empty() || cfg.work.as_os_str().is_empty() {
        return Err("both -edges and -work are required".to_string());
    }
    if cfg.queries() && (cfg.seeds.as_os_str().is_empty() || cfg.answers.as_os_str().is_empty()) {
        // A query phase without the answers would report a latency and nothing
        // else, and a latency on its own is not a result here for the same
        // reason it is not one in the vector suite. A store that forgot half
        // the edges answers every query faster than one that did not.
        return Err("-seeds and -answers are required for the query phase".to_string());
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
            "-edges",
            "edges.bin",
            "-seeds",
            "seeds.bin",
            "-answers",
            "answers.json",
            "-dataset",
            "web-google",
            "-work",
            "w",
            "-phase",
            "query",
            "-ops",
            "neighbours,two-hop",
            "-workers",
            "8",
        ]))
        .unwrap();

        assert_eq!(cfg.edges, PathBuf::from("edges.bin"));
        assert_eq!(cfg.answers, PathBuf::from("answers.json"));
        assert_eq!(cfg.dataset, "web-google");
        assert_eq!(cfg.phase, "query");
        assert_eq!(cfg.ops, vec!["neighbours", "two-hop"]);
        assert_eq!(cfg.workers, 8);
    }

    /// A build phase has nothing to check, so it needs no answers. A query
    /// phase without them would report a speed and call it a result.
    #[test]
    fn only_the_query_phase_needs_the_answers() {
        assert!(parse(&args(&["-edges", "e", "-work", "w", "-phase", "build"])).is_ok());
        assert!(parse(&args(&["-edges", "e", "-work", "w", "-phase", "query"])).is_err());
    }

    #[test]
    fn an_empty_operation_list_means_all_of_them() {
        let cfg = Config::default();
        assert!(cfg.wants("pagerank"));

        let cfg = Config {
            ops: vec!["bfs".to_string()],
            ..Default::default()
        };
        assert!(cfg.wants("bfs"));
        assert!(!cfg.wants("pagerank"));
    }

    #[test]
    fn an_unknown_flag_is_refused_rather_than_ignored() {
        assert!(parse(&args(&["-edges", "e", "-work", "w", "-depth", "3"])).is_err());
    }
}
