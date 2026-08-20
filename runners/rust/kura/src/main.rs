//! Measures kura against the same corpus every other engine here is given.
//!
//! kura is the engine this suite exists to hold to account, so it is measured
//! by the same harness, on the same corpus, with the same phase boundaries as
//! the rivals. Nothing here is tuned for kura that is not also available to the
//! others, and where kura does less work than a rival the comment says so
//! rather than letting the number stand on its own.
//!
//! Two differences from the Tantivy runner are worth stating up front, because
//! both make a number look better than a like for like comparison would.
//!
//! kura's index writer takes one text stream per document, so the title and the
//! body are concatenated rather than indexed as two fields. That is one term
//! dictionary instead of two and one length instead of two, and it means a
//! title match is not weighted above a body match the way a fielded engine can
//! weight it. It costs kura some relevance and saves it some work.
//!
//! kura has no tombstones yet, so there is no update phase. Reindexing a slice
//! of the corpus is not an operation this engine can do, and reporting the time
//! to append the same documents twice would be reporting a different operation
//! under the same name.

use std::io::Write;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

use benchrs::config::Config;
use benchrs::{SEARCH_LIMIT, config, corpus, machine, result, usage};

use kura_core::index;
use kura_core::search::Searcher;
use kura_core::segment::Segment;
use kura_core::store;

/// The one file a kura index is.
const INDEX_FILE: &str = "index.kura";

fn main() {
    if let Err(err) = run() {
        eprintln!("kura-runner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;
    let mut res = result::Result {
        engine: "kura".to_string(),
        version: kura_core::VERSION.to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };

    match cfg.phase.as_str() {
        "index" => index_phase(&cfg, &mut res)?,
        "query" => query_phase(&cfg, &mut res)?,
        "all" => {
            index_phase(&cfg, &mut res)?;
            res.notes = "the open phase ran in the same process as the build, so it is warmer than a real cold start".to_string();
            query_phase(&cfg, &mut res)?;
        }
        other => return Err(format!("unknown phase {other}").into()),
    }
    println!("{}", serde_json::to_string(&res)?);
    Ok(())
}

const NO_UPDATES: &str =
    "there is no update phase because the engine has no tombstones yet, so it cannot replace a document";

fn index_phase(cfg: &Config, res: &mut result::Result) -> Result<(), Box<dyn std::error::Error>> {
    std::fs::create_dir_all(&cfg.work)?;
    let mut writer = index::Writer::new();

    let mut documents = 0usize;
    let mut bytes = 0i64;
    let mut failed: Option<kura_core::error::Error> = None;
    // The analysed text is title and body joined, built once per document into
    // a buffer that is reused, because allocating a string per document would
    // be measuring the allocator.
    let mut text = String::new();

    let start = usage::take();
    corpus::read(&cfg.corpus, |d| {
        if cfg.limit > 0 && documents >= cfg.limit {
            return false;
        }
        documents += 1;
        bytes += d.body.len() as i64;
        text.clear();
        text.push_str(&d.title);
        text.push(' ');
        text.push_str(&d.body);
        // The same six fields Tantivy is asked to store. An engine that keeps
        // nothing would report a smaller index for a reason that has nothing to
        // do with how it indexes.
        let stored = [
            ("id", d.id.as_bytes()),
            ("repo", d.repo.as_bytes()),
            ("path", d.path.as_bytes()),
            ("title", d.title.as_bytes()),
            ("body", d.body.as_bytes()),
            ("ext", d.ext.as_bytes()),
        ];
        if let Err(e) = writer.add_with_fields(&text, stored) {
            failed = Some(e);
            return false;
        }
        true
    })?;
    if let Some(e) = failed {
        return Err(e.into());
    }

    // The phase is timed to the end of the write, not to the end of the last
    // add, because an engine that buffers and flushes later has not done less
    // work than one that flushed as it went.
    let segment = writer.finish()?;
    let path = cfg.work.join(INDEX_FILE);
    let mut file = std::fs::File::create(&path)?;
    file.write_all(&segment)?;
    file.sync_all()?;
    drop(file);
    let phase = usage::measure(&start);

    let (size, files) = result::dir_size(&cfg.work);
    res.corpus = result::CorpusStats { documents, bytes };
    res.index = result::IndexPhase {
        usage: phase.clone(),
        bytes: size,
        files,
    };
    eprintln!(
        "indexed {documents} documents in {:.1}s, {:.1} MB/s, index {:.1} MB",
        phase.wall_seconds,
        bytes as f64 / (1 << 20) as f64 / phase.wall_seconds,
        size as f64 / (1 << 20) as f64,
    );
    Ok(())
}

fn query_phase(cfg: &Config, res: &mut result::Result) -> Result<(), Box<dyn std::error::Error>> {
    let queries = corpus::queries(&cfg.queries)?;
    if queries.is_empty() {
        return Err("the query file has no queries in it".into());
    }

    // Opening and answering one query is the cold start, and the one query is
    // part of it on purpose, because an engine that defers its work to the
    // first search would otherwise report that work as free.
    //
    // The file is mapped rather than read, which is what the rivals here do and
    // is the only way a resident set figure means anything. The checksum is not
    // verified, for the same reason Tantivy verifies nothing: it would fault in
    // every page of the file and the number would be a read throughput
    // measurement wearing an open phase's name.
    let open_start = usage::take();
    let file = std::fs::File::open(cfg.work.join(INDEX_FILE))?;
    // SAFETY: the benchmark owns the work directory and nothing else writes to
    // the file while it is mapped, which is the condition a map relies on.
    let map = unsafe { memmap2::Mmap::map(&file)? };
    let segment = Segment::open_without_checksum(&map)?;
    let reader = index::Reader::open(&segment)?;
    let mut scratch = store::Scratch::new();
    search_once(&reader, &queries[0], SEARCH_LIMIT, &mut scratch)?;
    let open = usage::measure(&open_start);
    res.open = result::OpenPhase {
        resident_bytes: open.rss_bytes,
        usage: open,
    };

    let search_start = usage::take();
    let mut stats = Vec::with_capacity(queries.len());
    for q in &queries {
        // One warm up that is not counted, because the first run of a query
        // pays for whatever the operating system has not faulted in yet and no
        // deployment sees that cost on every request.
        let mut hits = search_once(&reader, q, SEARCH_LIMIT, &mut scratch)?;
        let mut runs = Vec::with_capacity(cfg.repeat);
        for _ in 0..cfg.repeat {
            let t = Instant::now();
            hits = search_once(&reader, q, SEARCH_LIMIT, &mut scratch)?;
            runs.push(t.elapsed().as_secs_f64() * 1000.0);
        }
        stats.push(result::summarise(q, hits, runs));
    }
    let search = usage::measure(&search_start);

    let concurrent = concurrent_phase(&reader, &queries, cfg);
    res.search = result::SearchPhase {
        usage: search,
        queries: stats,
        concurrent,
    };
    // No update phase. See the note at the top of the file. The note is set
    // here rather than in run, because the harness runs the phases as separate
    // processes and merges the two results, and a note set in both would be
    // written down twice.
    res.update = None;
    if res.notes.is_empty() {
        res.notes = NO_UPDATES.to_string();
    } else {
        res.notes = format!("{}; {NO_UPDATES}", res.notes);
    }
    Ok(())
}

/// Runs one query and returns the total number of matches, which is not the
/// same as the number returned. Both are asked for because every other engine
/// here reports a total and a page, and dropping one would make the numbers
/// describe different work.
fn search_once(
    reader: &index::Reader<'_>,
    query: &str,
    limit: usize,
    scratch: &mut store::Scratch,
) -> kura_core::error::Result<usize> {
    let searcher = Searcher::new(reader);
    // A bare query is read as OR, which is how every other engine here is asked
    // to read it.
    let total = searcher.count(query)?;
    let hits = searcher.search(query, limit)?;
    // The stored fields are read for the page, because a result nobody can show
    // is not a result and the rivals pay for this too. The store is compressed
    // in blocks, so this is a decompression per block the page touches, which
    // is what a caller building a result set would pay.
    if let Some(store) = reader.store() {
        for hit in &hits {
            let mut fields = store.get(hit.doc, scratch)?;
            while fields.next_field()?.is_some() {}
        }
    }
    Ok(usize::try_from(total).unwrap_or(usize::MAX))
}

/// The query set run with several in flight, which is the only way to get a
/// throughput number that means anything. Dividing one second by the serial
/// latency gives a figure no deployment has ever reached.
///
/// A reader is a slice of a mapped file and a searcher borrows it, so every
/// worker shares one reader and holds no state of its own. There is no lock
/// here because there is nothing to lock: the file is immutable and the search
/// path allocates only what it returns.
fn concurrent_phase(
    reader: &index::Reader<'_>,
    queries: &[String],
    cfg: &Config,
) -> Option<result::ConcurrentStat> {
    let mut workers = cfg.workers;
    if workers == 0 {
        workers = queries.len();
    }
    workers = workers.clamp(1, 64);

    let jobs: Vec<&String> = (0..cfg.repeat).flat_map(|_| queries.iter()).collect();
    let next = AtomicUsize::new(0);

    let start = Instant::now();
    let all = std::thread::scope(|scope| {
        let mut handles = Vec::with_capacity(workers);
        for _ in 0..workers {
            let jobs = &jobs;
            let next = &next;
            handles.push(scope.spawn(move || {
                // A scratch per worker, because it is where a block is
                // decompressed and sharing one would be sharing a buffer.
                let mut scratch = store::Scratch::new();
                let mut times = Vec::new();
                loop {
                    let i = next.fetch_add(1, Ordering::Relaxed);
                    let Some(q) = jobs.get(i) else { break };
                    let t = Instant::now();
                    if search_once(reader, q, SEARCH_LIMIT, &mut scratch).is_err() {
                        return None;
                    }
                    times.push(t.elapsed().as_secs_f64() * 1000.0);
                }
                Some(times)
            }));
        }
        let mut all = Vec::new();
        for h in handles {
            match h.join() {
                // A worker that failed means the throughput figure would
                // describe whatever the failure happened to produce, so it is
                // left out rather than reported short.
                Ok(Some(times)) => all.extend(times),
                _ => return None,
            }
        }
        Some(all)
    })?;
    let elapsed = start.elapsed().as_secs_f64();

    let queries_done = all.len();
    let summary = result::summarise("", 0, all);
    Some(result::ConcurrentStat {
        workers,
        queries: queries_done,
        seconds: elapsed,
        median_ms: summary.median_ms,
        p99_ms: summary.p99_ms,
    })
}
