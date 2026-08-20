//! Measures SeekStorm against the same corpus every other engine here is given.
//!
//! SeekStorm is the newest engine in this comparison and the one making the
//! strongest claims, so it is worth having a number for it taken by the same
//! code that takes everybody else's. It is built around an async API and a
//! memory mapped index, which is a different shape from the rest of the field
//! and is exactly why it is interesting.
//!
//! The index configuration is the one the project's own README uses, with two
//! deliberate exceptions, both noted where they are made: the spelling
//! correction dictionary and the query completion dictionary are off, because
//! they are built during indexing and no other engine here is being asked to
//! build them.

use std::collections::HashSet;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

use benchrs::config::Config;
use benchrs::{SEARCH_LIMIT, UPDATE_DOCUMENTS, config, corpus, machine, result, usage};

use seekstorm::commit::Commit;
use seekstorm::index::{
    AccessType, Clustering, DeleteDocuments, Document, DocumentCompression, FieldType,
    FrequentwordType, IndexArc, IndexDocuments, IndexMetaObject, LexicalSimilarity, NgramSet,
    SchemaField, StemmerType, StopwordType, TokenizerType, create_index, open_index,
};
use seekstorm::iterator::GetIterator;
use seekstorm::search::{QueryRewriting, QueryType, ResultType, Search, SearchMode};
use seekstorm::vector::Inference;

/// The number of segment bits the README uses. It decides how the posting lists
/// are split, so it is a tuning knob, and the project's own suggested value is
/// the one to measure.
const SEGMENT_NUMBER_BITS: usize = 11;

/// Documents handed over per call, the same number every Go runner uses.
const BATCH_SIZE: usize = 500;

fn main() {
    // The engine's API is async throughout, so it gets a runtime. The runtime
    // threads are inside the same process, which is where the CPU counters are
    // read, so the work they do is counted.
    let runtime = match tokio::runtime::Runtime::new() {
        Ok(r) => r,
        Err(e) => {
            eprintln!("seekstorm-runner: {e}");
            std::process::exit(1);
        }
    };
    if let Err(err) = runtime.block_on(run()) {
        eprintln!("seekstorm-runner: {err}");
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;
    let mut res = result::Result {
        engine: "seekstorm".to_string(),
        version: env!("SEEKSTORM_VERSION").to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };

    match cfg.phase.as_str() {
        "index" => index_phase(&cfg, &mut res).await?,
        "query" => query_phase(&cfg, &mut res).await?,
        "all" => {
            index_phase(&cfg, &mut res).await?;
            res.notes = "the open phase ran in the same process as the build, so it is warmer than a real cold start".to_string();
            query_phase(&cfg, &mut res).await?;
        }
        other => return Err(format!("unknown phase {other}").into()),
    }

    println!("{}", serde_json::to_string(&res)?);
    Ok(())
}

/// The schema, matched to what every other engine here is asked to store.
///
/// Title and body are indexed and stored, the identifier and the metadata are
/// stored so a result can be shown, and the path is stored without being
/// indexed because nobody searches a path as one term.
fn schema() -> Vec<SchemaField> {
    let text = |name: &str, index: bool, store: bool, longest: bool| {
        SchemaField::new(
            name.to_string(),
            store,
            index,
            false,
            FieldType::Text,
            false,
            longest,
            1.0,
            false,
            false,
        )
    };
    vec![
        text("id", false, true, false),
        text("repo", false, true, false),
        text("path", false, true, false),
        text("title", true, true, false),
        // The body is named as the longest field rather than left to be
        // detected. Detection looks at the first document of each shard, so on
        // a sharded index two shards can pick differently and BM25F then scores
        // the same corpus two ways.
        text("body", true, true, true),
        text("ext", false, true, false),
    ]
}

fn meta() -> IndexMetaObject {
    IndexMetaObject {
        id: 0,
        name: "kura-bench".to_string(),
        lexical_similarity: LexicalSimilarity::Bm25f,
        tokenizer: TokenizerType::UnicodeAlphanumeric,
        // No stemming, because the query set is written to be answerable
        // without it and the engines that do stem are not all stemming the
        // same way.
        stemmer: StemmerType::None,
        // No stopword removal. One of the queries is the single word "the", on
        // purpose, and an engine that throws it away is answering a different
        // question rather than answering it quickly.
        stop_words: StopwordType::None,
        frequent_words: FrequentwordType::English,
        ngram_indexing: NgramSet::NgramFF as u8,
        document_compression: DocumentCompression::Snappy,
        access_type: AccessType::Mmap,
        // Off on purpose. Both of these build a dictionary while the corpus is
        // being indexed, and charging SeekStorm for work no other engine here
        // is doing would make the indexing number a comparison of feature sets.
        spelling_correction: None,
        query_completion: None,
        clustering: Clustering::None,
        inference: Inference::None,
    }
}

fn to_document(d: &corpus::Document) -> Document {
    let field = |name: &str, value: &str| {
        (
            name.to_string(),
            serde_json::Value::String(value.to_string()),
        )
    };
    Document::from_iter([
        field("id", &d.id),
        field("repo", &d.repo),
        field("path", &d.path),
        field("title", &d.title),
        field("body", &d.body),
        field("ext", &d.ext),
    ])
}

async fn index_phase(
    cfg: &Config,
    res: &mut result::Result,
) -> Result<(), Box<dyn std::error::Error>> {
    std::fs::create_dir_all(&cfg.work)?;
    let index = create_index(
        &cfg.work,
        meta(),
        &schema(),
        &Vec::new(),
        SEGMENT_NUMBER_BITS,
        true,
        None,
    )
    .await?;

    let mut documents = 0usize;
    let mut bytes = 0i64;
    let mut batch: Vec<Document> = Vec::with_capacity(BATCH_SIZE);

    // The corpus reader is synchronous and every batch is handed over as soon
    // as it is full, rather than collecting the corpus first. Buffering it
    // would put the whole corpus in this process's memory and the peak resident
    // figure would then describe the buffer instead of the engine.
    let handle = tokio::runtime::Handle::current();
    let start = usage::take();
    tokio::task::block_in_place(|| {
        corpus::read(&cfg.corpus, |d| {
            if cfg.limit > 0 && documents >= cfg.limit {
                return false;
            }
            documents += 1;
            bytes += d.body.len() as i64;
            batch.push(to_document(&d));
            if batch.len() == BATCH_SIZE {
                let full = std::mem::replace(&mut batch, Vec::with_capacity(BATCH_SIZE));
                handle.block_on(index.index_documents(full));
            }
            true
        })
    })?;
    if !batch.is_empty() {
        index.index_documents(batch).await;
    }
    // Timed to the end of the commit. An engine that returns early from its
    // writes and finishes in the background has not done less work.
    index.commit().await;
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

async fn query_phase(
    cfg: &Config,
    res: &mut result::Result,
) -> Result<(), Box<dyn std::error::Error>> {
    let queries = corpus::queries(&cfg.queries)?;
    if queries.is_empty() {
        return Err("the query file has no queries in it".into());
    }

    // Open and one query together. SeekStorm memory maps its index and does
    // very little else in open, so an open timed without a search would report
    // the first touch of every page as free.
    let open_start = usage::take();
    let index = open_index(&cfg.work).await?;
    search_once(&index, &queries[0], None).await;
    let open = usage::measure(&open_start);
    res.open = result::OpenPhase {
        resident_bytes: open.rss_bytes,
        usage: open,
    };

    let search_start = usage::take();
    let mut stats = Vec::with_capacity(queries.len());
    let mut ids = Vec::with_capacity(SEARCH_LIMIT);
    for q in &queries {
        // One warm up that is not counted. The first run of a query pays for
        // whatever the engine caches per term, and no deployment sees that on
        // every request.
        //
        // The page comes off the warm up run for the same reason. Keeping the
        // identifiers allocates, and the run that pays for it is the one whose
        // time nobody reads.
        ids.clear();
        let mut hits = search_once(&index, q, Some(&mut ids)).await;
        let mut runs = Vec::with_capacity(cfg.repeat);
        for _ in 0..cfg.repeat {
            let t = Instant::now();
            hits = search_once(&index, q, None).await;
            runs.push(t.elapsed().as_secs_f64() * 1000.0);
        }
        let mut stat = result::summarise(q, hits, runs);
        stat.ids.clone_from(&ids);
        stats.push(stat);
    }
    let search = usage::measure(&search_start);

    let concurrent = concurrent_phase(&index, &queries, cfg).await;
    res.search = result::SearchPhase {
        usage: search,
        queries: stats,
        concurrent,
    };
    res.update = update_phase(cfg, &index).await;
    Ok(())
}

/// One query, and the documents it returned.
///
/// The total and the page are both asked for, because every other engine here
/// reports a total, and fetching the page matters because a result list is
/// shown to somebody.
async fn search_once(index: &IndexArc, query: &str, mut ids: Option<&mut Vec<String>>) -> usize {
    let found = index
        .search(
            query.to_string(),
            None,
            // Union is OR. Every engine here is asked to read a bare query as
            // OR, so that the hit counts describe the same set.
            QueryType::Union,
            SearchMode::Lexical,
            false,
            0,
            SEARCH_LIMIT,
            // TopkCount is the mode that returns an accurate total as well as
            // the page.
            ResultType::TopkCount,
            false,
            Vec::new(),
            Vec::new(),
            Vec::new(),
            Vec::new(),
            QueryRewriting::SearchOnly,
        )
        .await;

    let fields = HashSet::new();
    let guard = index.read().await;
    for r in found.results.iter() {
        let doc = guard
            .get_document(r.doc_id, false, &None, &fields, &[])
            .await;
        if let Some(out) = ids.as_deref_mut()
            && let Ok(doc) = doc
            && let Some(text) = doc.get("id").and_then(|v| v.as_str())
        {
            out.push(text.to_string());
        }
    }
    found.result_count_total
}

/// The query set with several in flight, which is the only throughput figure
/// worth reporting. Dividing a second by the serial latency gives a number no
/// deployment has ever reached.
async fn concurrent_phase(
    index: &IndexArc,
    queries: &[String],
    cfg: &Config,
) -> Option<result::ConcurrentStat> {
    let mut workers = cfg.workers;
    if workers == 0 {
        workers = queries.len();
    }
    workers = workers.clamp(1, 64);

    let jobs: Arc<Vec<String>> = Arc::new(
        (0..cfg.repeat)
            .flat_map(|_| queries.iter().cloned())
            .collect(),
    );
    let next = Arc::new(AtomicUsize::new(0));

    let start = Instant::now();
    let mut tasks = Vec::with_capacity(workers);
    for _ in 0..workers {
        let index = Arc::clone(index);
        let jobs = Arc::clone(&jobs);
        let next = Arc::clone(&next);
        tasks.push(tokio::spawn(async move {
            let mut times = Vec::new();
            loop {
                let i = next.fetch_add(1, Ordering::Relaxed);
                let Some(q) = jobs.get(i) else { break };
                let t = Instant::now();
                search_once(&index, q, None).await;
                times.push(t.elapsed().as_secs_f64() * 1000.0);
            }
            times
        }));
    }

    let mut all = Vec::new();
    for t in tasks {
        match t.await {
            Ok(times) => all.extend(times),
            // A worker that died means the throughput figure would describe
            // whatever the failure happened to produce, so it is left out.
            Err(_) => return None,
        }
    }
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

/// Reindexes a slice of the corpus into the index that is already built, which
/// is what an incremental sync does and is not the same operation as building
/// from empty.
async fn update_phase(cfg: &Config, index: &IndexArc) -> Option<result::UpdatePhase> {
    let want = cfg.capped(UPDATE_DOCUMENTS);

    // The engine warns that document ids are not continuous, so the ids to
    // rewrite are asked for rather than assumed. This happens before the timer
    // starts, because looking them up is not part of the update.
    let listed = index
        .get_iterator(None, 0, want as isize, false, false, Vec::new())
        .await;
    let ids: Vec<u64> = listed.results.iter().map(|r| r.doc_id).collect();
    if ids.is_empty() {
        return None;
    }

    let mut documents = 0usize;
    let mut bytes = 0i64;
    let mut batch: Vec<(u64, Document)> = Vec::with_capacity(ids.len());
    let read = corpus::read(&cfg.corpus, |d| {
        if documents >= ids.len() {
            return false;
        }
        batch.push((ids[documents], to_document(&d)));
        documents += 1;
        bytes += d.body.len() as i64;
        true
    });
    if read.is_err() {
        return None;
    }

    let start = usage::take();
    // Delete and add is what an update is here, and it is the same pair the
    // other engines are asked for. Adding without the delete would double the
    // documents and report a rate for an operation nobody runs.
    let (delete, add): (Vec<_>, Vec<_>) = batch.into_iter().unzip();
    index.delete_documents(delete).await;
    index.index_documents(add).await;
    index.commit().await;
    let usage = usage::measure(&start);

    let (size, _) = result::dir_size(&cfg.work);
    Some(result::UpdatePhase {
        usage,
        documents,
        bytes,
        index_bytes_after: size,
    })
}
