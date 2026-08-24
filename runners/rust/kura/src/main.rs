//! Measures kura against the same corpus every other engine here is given.
//!
//! kura is the engine this suite exists to hold to account, so it is measured
//! by the same harness, on the same corpus, with the same phase boundaries as
//! the rivals. Nothing here is tuned for kura that is not also available to the
//! others, and where kura does less work than a rival the comment says so
//! rather than letting the number stand on its own.
//!
//! One difference from the Tantivy runner is worth stating up front, because it
//! makes a number look better than a like for like comparison would.
//!
//! kura's index writer takes one text stream per document, so the title and the
//! body are concatenated rather than indexed as two fields. That is one term
//! dictionary instead of two and one length instead of two, and it means a
//! title match is not weighted above a body match the way a fielded engine can
//! weight it. It costs kura some relevance and saves it some work.
//!
//! The index is a store rather than a bare segment, and every document goes in
//! under its corpus identifier as a key. Both of those cost something and both
//! are what the rivals pay: Tantivy indexes an id field it can delete by, and
//! its index is a directory with a metadata file rather than a segment on its
//! own. Before this the runner wrote a segment with no key table, which made
//! the index phase faster than the phase every other engine here was timed on
//! and left the update phase with nothing to report. The store also means a
//! query opens through a manifest, so the open phase now reads a superblock and
//! a manifest slot before it reads the segment. The file also reserves a log
//! region that nothing here writes through, and the size of that reservation is
//! in the on disk figure, so it is set to 16 MB rather than left at the default
//! quarter of a gigabyte.
//!
//! The update phase replaces documents through the path a caller would use: a
//! batch looks each key up in the segments the store holds, and the new copy
//! and the deletion of the old one go into one commit, so a query sees one or
//! the other and never both. That is the same operation the Tantivy runner asks
//! for with a delete by term and an add.
//!
//! The index phase reads the corpus on every core. Each thread takes a byte
//! range of the file, parses the documents in it and fills a writer of its own,
//! and the writers are folded into one segment at the end. That means the JSON
//! parsing goes wide here where the Tantivy runner does it on one thread, so
//! part of the difference in wall time is the parser rather than the index.
//! Tantivy's own writer threads do the analysis, so the analysis is wide in
//! both. The single threaded number is in the pull request that added this, for
//! anyone who wants the engine on its own.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

use benchrs::config::Config;
use benchrs::{SEARCH_LIMIT, UPDATE_DOCUMENTS, config, corpus, machine, result, usage};

use kura_core::file::{Appending, Store};
use kura_core::index;
use kura_core::ingest::{self, Batch, Prepared};
use kura_core::residency;
use kura_core::search::Searcher;
use kura_core::store;

/// The one file a kura index is.
const INDEX_FILE: &str = "index.kura";

/// The identifier written into the superblock, so a file this made says so.
///
/// It is "kura-bench" in the low bytes, which is what a hex dump of the first
/// sixteen bytes of the file shows to anybody wondering where it came from.
const STORE: u128 = 0x6b75_7261_2d62_656e_6368_0000_0000_0001;

/// How much of the file is set aside for the log, which is a reservation and
/// not an index.
///
/// A store puts its log at a fixed offset and starts the segments after it, so
/// the length is chosen once when the file is made and the on disk figure this
/// run reports includes it. The default is a quarter of a gigabyte, which on a
/// corpus this size would be more than the index and would make the column
/// unreadable.
///
/// Nothing here writes through the log. A commit publishes a segment and syncs
/// it, which is the same durability the other engines report at the end of
/// their index phase, so the ring is sized to hold one batch of records for a
/// caller who wanted the logged path rather than to be a number.
const LOG_LEN: u64 = 16 * 1024 * 1024;

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

/// The wall clock in whole seconds, which is what a store records as the
/// moment a segment was made.
fn stamp() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |since| since.as_secs())
}

/// Indexes one slice of the corpus, and returns the writer with what it read.
///
/// This is the whole of the per thread work: read the range, join the title and
/// the body, hand it to a writer of its own. Nothing is shared, so there is no
/// lock here and no channel.
fn index_slice(
    corpus: &std::path::Path,
    from: u64,
    to: u64,
    limit: usize,
) -> Result<(index::Writer, usize, i64), Box<dyn std::error::Error + Send + Sync>> {
    let mut writer = index::Writer::new();
    let mut documents = 0usize;
    let mut bytes = 0i64;
    let mut failed: Option<kura_core::error::Error> = None;
    // The analysed text is title and body joined, built once per document into
    // a buffer that is reused, because allocating a string per document would
    // be measuring the allocator.
    let mut text = String::new();

    corpus::read_range(corpus, from, to, |d| {
        if limit > 0 && documents >= limit {
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
        // Keyed, because a document that cannot be found by its identifier
        // cannot be replaced, and the update phase below is the reason the key
        // table is built at all. Tantivy indexes the same identifier as a field
        // for the same reason.
        if let Err(e) = writer.add_keyed_with_fields(d.id.as_bytes(), &text, stored) {
            failed = Some(e);
            return false;
        }
        true
    })?;
    if let Some(e) = failed {
        return Err(e.into());
    }
    Ok((writer, documents, bytes))
}

fn index_phase(cfg: &Config, res: &mut result::Result) -> Result<(), Box<dyn std::error::Error>> {
    std::fs::create_dir_all(&cfg.work)?;
    // One slice per core unless the run asked for a document limit, which is
    // there for smoke tests and means the first so many documents of the file
    // rather than the first so many of each slice.
    //
    // KURA_INDEX_THREADS overrides the count. It is there so a run can take the
    // whole scaling curve on one machine rather than one point on it, and the
    // default is every core, which is what a comparison uses.
    let threads = if cfg.limit > 0 {
        1
    } else {
        std::env::var("KURA_INDEX_THREADS")
            .ok()
            .and_then(|value| value.parse::<usize>().ok())
            .filter(|threads| *threads > 0)
            .unwrap_or_else(|| {
                std::thread::available_parallelism().map_or(1, std::num::NonZero::get)
            })
    };
    let ranges = corpus::shards(&cfg.corpus, threads)?;

    let start = usage::take();
    let parts = std::thread::scope(|scope| {
        let handles: Vec<_> = ranges
            .iter()
            .map(|&(from, to)| scope.spawn(move || index_slice(&cfg.corpus, from, to, cfg.limit)))
            .collect();
        handles
            .into_iter()
            .map(|handle| {
                handle
                    .join()
                    .unwrap_or_else(|_| Err("a slice panicked".into()))
            })
            .collect::<Result<Vec<_>, _>>()
    })
    .map_err(|e| e.to_string())?;

    let documents = parts.iter().map(|(_, count, _)| count).sum::<usize>();
    let bytes = parts.iter().map(|(_, _, bytes)| bytes).sum::<i64>();
    let writers: Vec<index::Writer> = parts.into_iter().map(|(writer, _, _)| writer).collect();

    // The phase is timed to the end of the write, not to the end of the last
    // add, because an engine that buffers and flushes later has not done less
    // work than one that flushed as it went.
    //
    // The segment is written straight into the store rather than asked for its
    // bytes, which would mean holding a second whole copy of the index in
    // memory, and on a corpus this size that copy is most of the peak.
    //
    // One commit of one segment, which is what the fold at the end of the
    // parallel build leaves. A store of one segment is what a query wants and
    // what the rivals end their index phase with too, since Tantivy's runner
    // waits for its merging threads before it stops the clock.
    let segment = index::Writer::build(writers)?;
    let path = cfg.work.join(INDEX_FILE);
    let now = stamp();
    let mut store = Store::create_with_log(&path, STORE, now, LOG_LEN)?;
    let docs = u32::try_from(documents)?;
    // The closure takes its argument type in writing because the parameter is
    // higher ranked over the lifetime inside Appending, and inference will not
    // guess that on its own.
    store.publish_with(
        Some((docs, |into: &mut Appending<'_>| segment.write_to(into))),
        now,
        &[],
        now,
    )?;
    drop(store);
    let phase = usage::measure(&start);

    let (size, files) = result::dir_size(&cfg.work);
    res.corpus = result::CorpusStats { documents, bytes };
    res.index = result::IndexPhase {
        usage: phase.clone(),
        bytes: size,
        files,
    };
    eprintln!(
        "indexed {documents} documents on {threads} threads in {:.1}s, {:.1} MB/s, index {:.1} MB",
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
    // is the only way a resident set figure means anything. Opening a store
    // reads the superblock and the newer of the two manifest slots and then
    // maps the rest, and the digests are not verified, for the same reason
    // Tantivy verifies nothing: it would fault in every page of the file and
    // the number would be a read throughput measurement wearing an open
    // phase's name.
    let open_start = usage::take();
    let store = Store::open(&cfg.work.join(INDEX_FILE))?;
    let view = store.view()?;
    let readers = view.readers()?;
    let reader = readers
        .first()
        .ok_or("the store the index phase wrote holds no segments")?;
    let mut scratch = store::Scratch::new();
    search_once(reader, &queries[0], SEARCH_LIMIT, &mut scratch)?;
    let open = usage::measure(&open_start);
    res.open = result::OpenPhase {
        resident_bytes: open.rss_bytes,
        usage: open,
    };

    // What the serial phase faults in, measured over the mapped segment rather
    // than over the process. A resident set figure covers the binary, the heap
    // and every other mapping the runner has, and the question here is how much
    // of the index a query had to fetch. The probe is started after the open
    // phase on purpose: the open phase answered a query, so some of the file is
    // already resident, and resident_before is what says how much.
    let probe = residency::Probe::start(view.bytes(0).unwrap_or_default());

    let search_start = usage::take();
    let mut stats = Vec::with_capacity(queries.len());
    for q in &queries {
        // One warm up that is not counted, because the first run of a query
        // pays for whatever the operating system has not faulted in yet and no
        // deployment sees that cost on every request.
        let mut hits = search_once(reader, q, SEARCH_LIMIT, &mut scratch)?;
        let mut runs = Vec::with_capacity(cfg.repeat);
        for _ in 0..cfg.repeat {
            let t = Instant::now();
            hits = search_once(reader, q, SEARCH_LIMIT, &mut scratch)?;
            runs.push(t.elapsed().as_secs_f64() * 1000.0);
        }
        stats.push(result::summarise(q, hits, runs));
    }
    let search = usage::measure(&search_start);
    // Read before the concurrent phase runs, because that phase walks the same
    // query set again on every core and would fold its faults into a figure the
    // report prints next to a serial latency.
    let touched = probe.finish();

    let concurrent = concurrent_phase(reader, &queries, cfg);
    res.search = result::SearchPhase {
        usage: search,
        queries: stats,
        concurrent,
        residency: Some(result::Residency {
            faults: touched.faults,
            faults_from_disk: touched.faults_from_disk,
            resident_before: touched.resident_before,
            total: touched.total,
            page: touched.page,
            note: touched.note.unwrap_or_default().to_string(),
        }),
    };
    if let (Some(faults), Some(before)) = (touched.faulted(), touched.resident_before) {
        eprintln!(
            "the query set faulted at least {:.1} MB of a {:.1} MB index that was {:.0}% resident when it started",
            faults as f64 / (1 << 20) as f64,
            touched.total as f64 / (1 << 20) as f64,
            before as f64 / touched.total.max(1) as f64 * 100.0,
        );
    }
    // Everything that read the store goes out of scope before the update runs,
    // because a batch needs the store back to commit into and the view is what
    // is holding it. The searcher, the readers and the view all borrow it.
    drop(view);
    res.update = Some(update_phase(cfg, store)?);
    Ok(())
}

/// How many threads the update phase analyses on.
///
/// KURA_UPDATE_THREADS overrides it, the way KURA_INDEX_THREADS overrides the
/// index phase, so one machine can take the whole curve rather than one point
/// on it. The default is every core, because that is what the engines this is
/// measured against do: Tantivy's update goes through the same writer thread
/// pool its index phase uses.
fn update_threads() -> usize {
    std::env::var("KURA_UPDATE_THREADS")
        .ok()
        .and_then(|value| value.parse::<usize>().ok())
        .filter(|threads| *threads > 0)
        .unwrap_or_else(|| std::thread::available_parallelism().map_or(1, std::num::NonZero::get))
}

/// Replaces the first documents of the corpus, which is what an update is.
///
/// The same operation the Tantivy runner asks for and on the same documents:
/// take the first so many out of the corpus file and put them in again under
/// the identifiers they already have. Adding them without the replacement would
/// double the corpus and report a rate for an operation nobody runs.
///
/// A batch is what carries it. Each keyed add looks the key up in the segments
/// the store holds, which is a filter and then a table per segment, and records
/// that the copy it found is to be deleted. The commit at the end writes the
/// new segment and the deletions in one go, so there is no window in which both
/// copies are visible and none in which neither is.
///
/// There is a batch per thread and one commit at the end. A batch is a single
/// threaded thing, and an update phase that ran on one thread would be measured
/// against engines whose updates do not: Tantivy hands its replacements to the
/// same writer pool its index phase uses. Every batch prepares against the same
/// view, each one takes a share of the documents, and the whole group is
/// published in one commit, so what a query sees is still one update rather
/// than as many as there are threads.
///
/// The set is not committed halfway through on purpose. A batch that filled and
/// flushed would be two commits, and the rivals here are timed on one, so this
/// measures the same shape. The memory that costs is in the peak the phase
/// reports.
///
/// It leaves as many segments as it had threads, where a single batch would
/// leave one, and that shows in the on disk figure and in what the next query
/// walks.
///
fn update_phase(
    cfg: &Config,
    mut store: Store,
) -> Result<result::UpdatePhase, Box<dyn std::error::Error>> {
    let want = cfg.capped(UPDATE_DOCUMENTS);
    let threads = update_threads();
    let mut documents = 0usize;
    let mut bytes = 0i64;

    let start = usage::take();
    let view = store.view()?;
    // One channel per worker rather than one shared queue, because a shared
    // queue needs a lock around the receiving end and the thing being handed
    // over is a whole document. Round robin gives every worker the same number
    // of documents and none of the contention.
    let batches = std::thread::scope(
        |scope| -> Result<Vec<(Prepared, usize)>, Box<dyn std::error::Error>> {
            let mut senders = Vec::with_capacity(threads);
            let mut handles = Vec::with_capacity(threads);
            for _ in 0..threads {
                // A bound rather than an unbounded channel, so that a reader
                // that is quicker than the analysers cannot pull the corpus
                // into memory ahead of them.
                let (tx, rx) = std::sync::mpsc::sync_channel::<corpus::Document>(64);
                senders.push(tx);
                let view = &view;
                handles.push(scope.spawn(move || -> Result<(Prepared, usize), String> {
                    let mut batch = Batch::over(view).map_err(|e| e.to_string())?;
                    let mut text = String::new();
                    while let Ok(d) = rx.recv() {
                        text.clear();
                        text.push_str(&d.title);
                        text.push(' ');
                        text.push_str(&d.body);
                        let stored = [
                            ("id", d.id.as_bytes()),
                            ("repo", d.repo.as_bytes()),
                            ("path", d.path.as_bytes()),
                            ("title", d.title.as_bytes()),
                            ("body", d.body.as_bytes()),
                            ("ext", d.ext.as_bytes()),
                        ];
                        batch
                            .add_keyed_with_fields(d.id.as_bytes(), &text, stored)
                            .map_err(|e| e.to_string())?;
                    }
                    let replaced = batch.replacements();
                    let prepared = batch.finish().map_err(|e| e.to_string())?;
                    Ok((prepared, replaced))
                }));
            }

            // The parsing stays on this thread, which is where the Tantivy
            // runner does it too, so the difference between the two update
            // phases is the engine and not the JSON.
            corpus::read(&cfg.corpus, |d| {
                if documents >= want {
                    return false;
                }
                bytes += d.body.len() as i64;
                let sent = senders[documents % threads].send(d).is_ok();
                documents += 1;
                sent
            })?;
            // The workers stop when their channel closes and not before, so the
            // senders go before the join and not after it.
            senders.clear();

            Ok(handles
                .into_iter()
                .map(|handle| {
                    handle
                        .join()
                        .unwrap_or_else(|_| Err("a batch panicked".to_string()))
                })
                .collect::<Result<Vec<_>, _>>()?)
        },
    )?;

    let replaced = batches.iter().map(|(_, count)| count).sum::<usize>();
    let prepared: Vec<Prepared> = batches.into_iter().map(|(prepared, _)| prepared).collect();
    drop(view);
    let now = stamp();
    // One commit of every batch, which is what makes this one update rather
    // than as many updates as there are threads. Each batch brings a segment
    // and the deletions of the copies it replaced, and the store publishes
    // them together.
    ingest::commit_all(&mut store, prepared, now, now)?;
    store.sync()?;
    drop(store);
    let usage = usage::measure(&start);

    // Every document handed in should have replaced one, because they came out
    // of the corpus the index phase read. A run where they did not has either
    // read a different corpus or lost a key table, and a rate for that is worse
    // than no rate at all.
    if replaced != documents {
        return Err(format!(
            "{documents} documents went in and {replaced} of them replaced a copy already in the store"
        )
        .into());
    }

    let (size, _) = result::dir_size(&cfg.work);
    eprintln!(
        "replaced {documents} documents in {:.1}s, {:.0} docs/s, index {:.1} MB",
        usage.wall_seconds,
        documents as f64 / usage.wall_seconds.max(f64::MIN_POSITIVE),
        size as f64 / (1 << 20) as f64,
    );
    Ok(result::UpdatePhase {
        usage,
        documents,
        bytes,
        index_bytes_after: size,
    })
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
    // to read it. The page and the total come out of one walk of the posting
    // lists, which is what Tantivy's runner asks for too when it hands the
    // search a Count and a TopDocs collector together.
    let (hits, total) = searcher.search_and_count(query, limit)?;
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
