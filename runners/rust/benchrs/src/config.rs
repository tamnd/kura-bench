//! The flags a runner is invoked with.
//!
//! They are the Go harness's flags, spelled exactly the same way, because the
//! orchestrator invokes every runner with the same command line and does not
//! know which language any of them is written in.

use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Config {
    /// The corpus file, one JSON object per line.
    pub corpus: PathBuf,

    /// The query file.
    pub queries: PathBuf,

    /// The directory the index is built in.
    pub work: PathBuf,

    /// index, query, or all. The orchestrator runs index and query as two
    /// processes so that the open phase is a real cold start; all is for
    /// running a runner by hand.
    pub phase: String,

    /// How many times each query is timed.
    pub repeat: usize,

    /// Stop after this many documents, zero for the whole corpus.
    pub limit: usize,

    /// Queries in flight for the concurrent phase, zero for one per query.
    pub workers: usize,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            corpus: PathBuf::new(),
            queries: PathBuf::new(),
            work: PathBuf::new(),
            phase: "all".to_string(),
            repeat: 20,
            limit: 0,
            workers: 0,
        }
    }
}

impl Config {
    /// How many documents this run should index, given the corpus limit.
    pub fn capped(&self, want: usize) -> usize {
        if self.limit > 0 && self.limit < want {
            self.limit
        } else {
            want
        }
    }
}

/// Parses the command line.
///
/// The flags are parsed by hand rather than with a crate because they are
/// somebody else's flags. A parser that spelled them its own way, or that
/// accepted `--limit=10` when the Go side does not, would be a difference
/// between runners that has nothing to do with the engines.
pub fn parse(args: &[String]) -> Result<Config, String> {
    let mut cfg = Config::default();

    let mut i = 0;
    while i < args.len() {
        let name = args[i].trim_start_matches('-');
        let value = args.get(i + 1).cloned().unwrap_or_default();
        match name {
            "corpus" => cfg.corpus = PathBuf::from(&value),
            "queries" => cfg.queries = PathBuf::from(&value),
            "work" => cfg.work = PathBuf::from(&value),
            "phase" => cfg.phase = value.clone(),
            "repeat" => cfg.repeat = parse_usize("repeat", &value)?,
            "limit" => cfg.limit = parse_usize("limit", &value)?,
            "workers" => cfg.workers = parse_usize("workers", &value)?,
            other => return Err(format!("unknown flag {other}")),
        }
        i += 2;
    }

    if cfg.corpus.as_os_str().is_empty() || cfg.work.as_os_str().is_empty() {
        return Err("both -corpus and -work are required".to_string());
    }
    if cfg.phase != "index" && cfg.queries.as_os_str().is_empty() {
        return Err("-queries is required for the query phase".to_string());
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
            "-corpus", "c.jsonl", "-queries", "q.txt", "-work", "w", "-phase", "query", "-repeat",
            "7", "-limit", "100", "-workers", "4",
        ]))
        .unwrap();

        assert_eq!(cfg.corpus, PathBuf::from("c.jsonl"));
        assert_eq!(cfg.queries, PathBuf::from("q.txt"));
        assert_eq!(cfg.work, PathBuf::from("w"));
        assert_eq!(cfg.phase, "query");
        assert_eq!(cfg.repeat, 7);
        assert_eq!(cfg.limit, 100);
        assert_eq!(cfg.workers, 4);
    }

    #[test]
    fn a_run_without_a_corpus_or_a_work_directory_is_refused() {
        assert!(parse(&args(&["-work", "w"])).is_err());
        assert!(parse(&args(&["-corpus", "c.jsonl"])).is_err());
    }

    /// An index phase has nothing to search, so it does not need a query file.
    /// A query phase without one would build an index and measure nothing.
    #[test]
    fn only_the_query_phase_needs_a_query_file() {
        assert!(parse(&args(&["-corpus", "c", "-work", "w", "-phase", "index"])).is_ok());
        assert!(parse(&args(&["-corpus", "c", "-work", "w", "-phase", "query"])).is_err());
    }

    #[test]
    fn an_unknown_flag_is_refused_rather_than_ignored() {
        assert!(parse(&args(&["-corpus", "c", "-work", "w", "-warmup", "3"])).is_err());
    }

    #[test]
    fn the_limit_caps_the_update_size() {
        let cfg = Config {
            limit: 100,
            ..Default::default()
        };
        assert_eq!(cfg.capped(5000), 100);
        assert_eq!(Config::default().capped(5000), 5000);
    }
}
