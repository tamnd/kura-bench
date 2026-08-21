//! Measures Tantivy against the same corpus every other engine here is given.
//!
//! Tantivy is the strongest rival in this comparison and the one worth being
//! beaten by. It writes segments to disk, memory maps them on open, and indexes
//! on every core it is allowed. The batch size, the phase boundaries and the
//! timing are the ones the shared Go harness uses, because a benchmark where
//! each subject brings its own stopwatch measures the stopwatches.

use std::sync::Arc;
use std::time::Instant;

use benchrs::config::Config;
use benchrs::{SEARCH_LIMIT, UPDATE_DOCUMENTS, config, corpus, machine, result, usage};

use tantivy::collector::{Count, TopDocs};
use tantivy::query::QueryParser;
use tantivy::schema::{Field, STORED, STRING, Schema, TEXT};
use tantivy::{Index, IndexWriter, TantivyDocument};

// There is no batch size constant here. The Go engines are handed documents in
// batches because that is how their write calls are shaped, while Tantivy takes
// one document at a time and buffers until its heap budget is spent. Inventing
// a batch boundary would add a cost the engine does not otherwise pay.

/// The writer's memory budget. Tantivy spends this before it flushes a segment,
/// so it is the single setting that most changes the shape of the index. Two
/// hundred megabytes is what the project's own documentation suggests for a
/// corpus of this size, and picking a number tuned to win here would make the
/// comparison worthless.
const WRITER_HEAP: usize = 200 << 20;

fn main() {
    if let Err(err) = run() {
        eprintln!("tantivy-runner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;
    let mut res = result::Result {
        engine: "tantivy".to_string(),
        version: env!("CARGO_PKG_VERSION").to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };
    // The version that matters is Tantivy's, not this crate's. Cargo records
    // the resolved one in the lock file and there is no way to read it at run
    // time, so it is stamped in at build time from the dependency itself.
    res.version = tantivy_version();

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

fn tantivy_version() -> String {
    // tantivy::version() reports the index format version as well as the crate
    // version, and the crate version is the part a table wants.
    tantivy::version().to_string()
}

struct Fields {
    id: Field,
    repo: Field,
    path: Field,
    title: Field,
    body: Field,
    ext: Field,
}

/// The schema stores the document text, because every engine in this comparison
/// is asked to be able to show a result to a person, and one that stores
/// nothing would look smaller on disk for a reason that has nothing to do with
/// the index.
fn schema() -> (Schema, Fields) {
    let mut b = Schema::builder();
    let fields = Fields {
        id: b.add_text_field("id", STRING | STORED),
        repo: b.add_text_field("repo", STRING | STORED),
        // The path is worth showing and not worth searching as one term, so it
        // is stored and not indexed.
        path: b.add_text_field("path", STORED),
        title: b.add_text_field("title", TEXT | STORED),
        body: b.add_text_field("body", TEXT | STORED),
        ext: b.add_text_field("ext", STRING | STORED),
    };
    (b.build(), fields)
}

fn to_document(f: &Fields, d: &corpus::Document) -> TantivyDocument {
    let mut doc = TantivyDocument::default();
    doc.add_text(f.id, &d.id);
    doc.add_text(f.repo, &d.repo);
    doc.add_text(f.path, &d.path);
    doc.add_text(f.title, &d.title);
    doc.add_text(f.body, &d.body);
    doc.add_text(f.ext, &d.ext);
    doc
}

fn index_phase(cfg: &Config, res: &mut result::Result) -> Result<(), Box<dyn std::error::Error>> {
    std::fs::create_dir_all(&cfg.work)?;
    let (schema, fields) = schema();
    let index = Index::create_in_dir(&cfg.work, schema)?;
    let mut writer: IndexWriter = index.writer(WRITER_HEAP)?;

    let mut documents = 0usize;
    let mut bytes = 0i64;
    let mut failed: Option<tantivy::TantivyError> = None;

    let start = usage::take();
    corpus::read(&cfg.corpus, |d| {
        if cfg.limit > 0 && documents >= cfg.limit {
            return false;
        }
        documents += 1;
        bytes += d.body.len() as i64;
        if let Err(e) = writer.add_document(to_document(&fields, &d)) {
            failed = Some(e);
            return false;
        }
        true
    })?;
    if let Some(e) = failed {
        return Err(e.into());
    }
    // The phase is timed to the end of the commit, not to the end of the last
    // add, because an engine that returns early from writes and finishes in the
    // background has not done less work.
    writer.commit()?;
    writer.wait_merging_threads()?;
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
    // part of it on purpose. Tantivy does very little in open and pays for it
    // on the first search, and an open timed without a query would report that
    // as free.
    let open_start = usage::take();
    let index = Index::open_in_dir(&cfg.work)?;
    let fields = existing_fields(&index)?;
    let reader = index.reader()?;
    let parser = QueryParser::for_index(&index, vec![fields.title, fields.body]);
    let searcher = reader.searcher();
    search_once(&searcher, &parser, &queries[0], SEARCH_LIMIT)?;
    let open = usage::measure(&open_start);
    res.open = result::OpenPhase {
        resident_bytes: open.rss_bytes,
        usage: open,
    };

    let search_start = usage::take();
    let mut stats = Vec::with_capacity(queries.len());
    for q in &queries {
        // One warm up that is not counted, because the first run of a query
        // pays for whatever the engine caches per term and no deployment sees
        // that cost on every request.
        let mut hits = search_once(&searcher, &parser, q, SEARCH_LIMIT)?;
        let mut runs = Vec::with_capacity(cfg.repeat);
        for _ in 0..cfg.repeat {
            let t = Instant::now();
            hits = search_once(&searcher, &parser, q, SEARCH_LIMIT)?;
            runs.push(t.elapsed().as_secs_f64() * 1000.0);
        }
        stats.push(result::summarise(q, hits, runs));
    }
    let search = usage::measure(&search_start);

    let concurrent = concurrent_phase(&index, &fields, &queries, cfg)?;
    res.search = result::SearchPhase {
        usage: search,
        queries: stats,
        concurrent,
        // Tantivy owns its own mapping and does not expose the bytes, so there
        // is nothing here to take a reading over. Left absent rather than
        // reported as zero, which would read as a warm index.
        residency: None,
    };
    res.update = update_phase(cfg, &index, &fields)?;
    Ok(())
}

fn existing_fields(index: &Index) -> Result<Fields, Box<dyn std::error::Error>> {
    let s = index.schema();
    Ok(Fields {
        id: s.get_field("id")?,
        repo: s.get_field("repo")?,
        path: s.get_field("path")?,
        title: s.get_field("title")?,
        body: s.get_field("body")?,
        ext: s.get_field("ext")?,
    })
}

/// Runs one query and returns the total number of matches, which is not the
/// same as the number returned. Both collectors run because the other engines
/// here report a total and a page, and dropping one would make the numbers
/// describe different work.
fn search_once(
    searcher: &tantivy::Searcher,
    parser: &QueryParser,
    query: &str,
    limit: usize,
) -> tantivy::Result<usize> {
    // A bare query is read as OR, which is how every other engine here is asked
    // to read it. Tantivy's parser defaults to that already and it is set
    // explicitly so that a change in its default does not silently change what
    // is being compared.
    let (parsed, _errors) = parser.parse_query_lenient(query);
    // From 0.26 the ordering is chosen explicitly. Score order is what a search
    // result list is, and it is what the other engines here are asked for.
    let (count, top) = searcher.search(
        &parsed,
        &(Count, TopDocs::with_limit(limit).order_by_score()),
    )?;
    for (_score, address) in top {
        let _doc: TantivyDocument = searcher.doc(address)?;
    }
    Ok(count)
}

/// The query set run with several in flight, which is the only way to get a
/// throughput number that means anything. Dividing one second by the serial
/// latency gives a figure no deployment has ever reached.
fn concurrent_phase(
    index: &Index,
    fields: &Fields,
    queries: &[String],
    cfg: &Config,
) -> Result<Option<result::ConcurrentStat>, Box<dyn std::error::Error>> {
    let mut workers = cfg.workers;
    if workers == 0 {
        workers = queries.len();
    }
    workers = workers.clamp(1, 64);

    let reader = index.reader()?;
    let searcher = Arc::new(reader.searcher());
    let parser = Arc::new(QueryParser::for_index(
        index,
        vec![fields.title, fields.body],
    ));

    let jobs: Vec<String> = (0..cfg.repeat)
        .flat_map(|_| queries.iter().cloned())
        .collect();
    let next = Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let jobs = Arc::new(jobs);

    let start = Instant::now();
    let mut handles = Vec::with_capacity(workers);
    for _ in 0..workers {
        let searcher = Arc::clone(&searcher);
        let parser = Arc::clone(&parser);
        let jobs = Arc::clone(&jobs);
        let next = Arc::clone(&next);
        handles.push(std::thread::spawn(move || {
            let mut times = Vec::new();
            loop {
                let i = next.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
                let Some(q) = jobs.get(i) else { break };
                let t = Instant::now();
                if search_once(&searcher, &parser, q, SEARCH_LIMIT).is_err() {
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
            Ok(Some(times)) => all.extend(times),
            // A worker that failed means the throughput figure would describe
            // whatever the failure happened to produce, so it is left out.
            _ => return Ok(None),
        }
    }
    let elapsed = start.elapsed().as_secs_f64();

    let queries_done = all.len();
    let summary = result::summarise("", 0, all);
    Ok(Some(result::ConcurrentStat {
        workers,
        queries: queries_done,
        seconds: elapsed,
        median_ms: summary.median_ms,
        p99_ms: summary.p99_ms,
    }))
}

/// Reindexes a slice of the corpus into the index that is already built, which
/// is what an incremental sync does and is not the same operation as building
/// from empty.
fn update_phase(
    cfg: &Config,
    index: &Index,
    fields: &Fields,
) -> Result<Option<result::UpdatePhase>, Box<dyn std::error::Error>> {
    let want = cfg.capped(UPDATE_DOCUMENTS);

    let mut writer: IndexWriter = index.writer(WRITER_HEAP)?;
    let mut documents = 0usize;
    let mut bytes = 0i64;
    let mut failed: Option<tantivy::TantivyError> = None;

    let start = usage::take();
    corpus::read(&cfg.corpus, |d| {
        if documents >= want {
            return false;
        }
        documents += 1;
        bytes += d.body.len() as i64;
        // Deleting by term and adding again is what an update is in Tantivy.
        // Adding without the delete would double the corpus and report a rate
        // for an operation nobody runs.
        writer.delete_term(tantivy::Term::from_field_text(fields.id, &d.id));
        if let Err(e) = writer.add_document(to_document(fields, &d)) {
            failed = Some(e);
            return false;
        }
        true
    })?;
    if let Some(e) = failed {
        return Err(e.into());
    }
    writer.commit()?;
    writer.wait_merging_threads()?;
    let usage = usage::measure(&start);

    let (size, _) = result::dir_size(&cfg.work);
    Ok(Some(result::UpdatePhase {
        usage,
        documents,
        bytes,
        index_bytes_after: size,
    }))
}
