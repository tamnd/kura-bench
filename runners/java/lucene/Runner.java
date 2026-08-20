// Measures Lucene against the same corpus every other engine here is given.
//
// Lucene is in this table because it is what most enterprise search actually
// runs on. Elasticsearch and OpenSearch are Lucene with a cluster around them,
// and a comparison that leaves it out is measuring against the rivals that are
// convenient rather than the one people have deployed.
//
// The batch size, the phase boundaries and the timing are the ones the shared
// harness uses, because a benchmark where each subject brings its own stopwatch
// measures the stopwatches. The measuring half is in Bench.java next to this,
// so this file is an engine and nothing else.
//
// Two things about the Java Virtual Machine belong in the reader's head before
// the memory column does. Peak resident memory includes heap the collector has
// not handed back, so it is the memory the process asked for and not the memory
// it needed. And the code is interpreted until it is compiled, so the first
// runs of a phase are slower than the rest. Neither is a flaw in Lucene and
// both are what a deployment actually pays, which is why they are reported as
// they are rather than tuned away.

import java.io.IOException;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;

import org.apache.lucene.analysis.Analyzer;
import org.apache.lucene.analysis.CharArraySet;
import org.apache.lucene.analysis.standard.StandardAnalyzer;
import org.apache.lucene.document.Document;
import org.apache.lucene.document.Field;
import org.apache.lucene.document.StoredField;
import org.apache.lucene.document.StringField;
import org.apache.lucene.document.TextField;
import org.apache.lucene.index.DirectoryReader;
import org.apache.lucene.index.IndexWriter;
import org.apache.lucene.index.IndexWriterConfig;
import org.apache.lucene.index.StoredFields;
import org.apache.lucene.index.Term;
import org.apache.lucene.queryparser.classic.MultiFieldQueryParser;
import org.apache.lucene.queryparser.classic.ParseException;
import org.apache.lucene.queryparser.classic.QueryParser;
import org.apache.lucene.search.IndexSearcher;
import org.apache.lucene.search.Query;
import org.apache.lucene.search.ScoreDoc;
import org.apache.lucene.search.TopDocs;
import org.apache.lucene.store.Directory;
import org.apache.lucene.store.FSDirectory;
import org.apache.lucene.util.Version;

public final class Runner {
    private Runner() {}

    /**
     * The writer's memory budget before it flushes a segment. It is the single
     * setting that most changes the shape of the index, and it is the same two
     * hundred megabytes the Tantivy runner is given, so that the index phase
     * compares the two engines rather than comparing two numbers somebody
     * picked. Lucene's own default is sixteen, which is a figure for a machine
     * indexing alongside everything else it is doing and not one for a bulk
     * load.
     */
    private static final double WRITER_HEAP_MB = 200;

    // The fields searched by a bare query, which is title and body here for
    // the same reason it is on every other engine in this suite.
    private static final String[] SEARCHED = {"title", "body"};

    public static void main(String[] args) {
        try {
            run(args);
        } catch (Exception e) {
            System.err.println("lucene-runner: " + e);
            e.printStackTrace(System.err);
            System.exit(1);
        }
    }

    private static void run(String[] args) throws IOException {
        Bench.Config cfg = Bench.parse(args);
        Bench.Result res = new Bench.Result();
        res.engine = "lucene";
        // Lucene's version and not this file's. A number without the engine's
        // own version is not reproducible.
        res.version = Version.LATEST.toString();
        res.language = "java";
        res.machine = Bench.describe();
        res.notes = "peak resident memory is the whole virtual machine, so it includes heap the "
                + "collector has not returned and is the memory the process asked for rather "
                + "than the memory it needed. Documents are fed to the writer from one thread, "
                + "the way the harness feeds every other engine, so the index phase does not "
                + "use the several threads a Lucene bulk load usually gets.";

        switch (cfg.phase) {
            case "index" -> indexPhase(cfg, res);
            case "query" -> queryPhase(cfg, res);
            case "all" -> {
                indexPhase(cfg, res);
                res.notes = "the open phase ran in the same process as the build, so it is "
                        + "warmer than a real cold start. " + res.notes;
                queryPhase(cfg, res);
            }
            default -> throw new IllegalArgumentException("unknown phase " + cfg.phase);
        }

        System.out.println(Bench.json(res));
    }

    /**
     * The analyzer, spelled out rather than defaulted.
     *
     * Lowercasing and no stopwords is what the other engines here do, and
     * naming the empty set means a change in what the no argument constructor
     * defaults to cannot quietly change what is being compared. No stemming,
     * because none of the others stem either.
     */
    private static Analyzer analyzer() {
        return new StandardAnalyzer(CharArraySet.EMPTY_SET);
    }

    /**
     * The document, stored as well as indexed.
     *
     * Every engine in this comparison is asked to be able to show a result to a
     * person, and one that keeps nothing would look smaller on disk for a
     * reason that has nothing to do with the index.
     */
    private static Document document(Bench.Document d) {
        Document doc = new Document();
        doc.add(new StringField("id", d.id, Field.Store.YES));
        doc.add(new StringField("repo", d.repo, Field.Store.YES));
        // The path is worth showing and not worth searching as one term, so it
        // is stored and not indexed.
        doc.add(new StoredField("path", d.path));
        doc.add(new TextField("title", d.title, Field.Store.YES));
        doc.add(new TextField("body", d.body, Field.Store.YES));
        doc.add(new StringField("ext", d.ext, Field.Store.YES));
        return doc;
    }

    private static IndexWriterConfig writerConfig(IndexWriterConfig.OpenMode mode) {
        IndexWriterConfig conf = new IndexWriterConfig(analyzer());
        conf.setOpenMode(mode);
        conf.setRAMBufferSizeMB(WRITER_HEAP_MB);
        return conf;
    }

    private static void indexPhase(Bench.Config cfg, Bench.Result res) throws IOException {
        Files.createDirectories(cfg.work);

        int[] documents = {0};
        long[] bytes = {0};
        Bench.Snapshot start = Bench.take();
        try (Directory dir = FSDirectory.open(cfg.work);
                IndexWriter writer = new IndexWriter(dir, writerConfig(IndexWriterConfig.OpenMode.CREATE))) {
            Bench.read(cfg.corpus, d -> {
                if (cfg.limit > 0 && documents[0] >= cfg.limit) {
                    return false;
                }
                documents[0]++;
                bytes[0] += Bench.utf8Length(d.body);
                try {
                    writer.addDocument(document(d));
                } catch (IOException e) {
                    throw new UncheckedWrite(e);
                }
                return true;
            });
            // The phase is timed to the end of the commit and the close, not
            // to the end of the last add, because an engine that returns early
            // from writes and finishes the merge in the background has not
            // done less work. Closing the writer is what waits for it.
            writer.commit();
        } catch (UncheckedWrite e) {
            throw e.cause();
        }
        Bench.Usage phase = Bench.measure(start);

        long[] size = Bench.dirSize(cfg.work);
        res.documents = documents[0];
        res.corpusBytes = bytes[0];
        res.indexUsage = phase;
        res.indexBytes = size[0];
        res.indexFiles = (int) size[1];
        System.err.printf(
                "indexed %d documents in %.1fs, %.1f MB/s, index %.1f MB%n",
                documents[0],
                phase.wallSeconds,
                bytes[0] / (double) (1 << 20) / phase.wallSeconds,
                size[0] / (double) (1 << 20));
    }

    private static void queryPhase(Bench.Config cfg, Bench.Result res) throws IOException {
        List<String> queries = Bench.queries(cfg.queries);
        if (queries.isEmpty()) {
            throw new IllegalArgumentException("the query file has no queries in it");
        }

        // Opening and answering one query is the cold start, and the one query
        // is part of it on purpose. Lucene does very little in open and pays
        // for it on the first search, and an open timed without a query would
        // report that as free.
        Bench.Snapshot openStart = Bench.take();
        Directory dir = FSDirectory.open(cfg.work);
        DirectoryReader reader = DirectoryReader.open(dir);
        // No executor, so a query runs on the thread that asked for it. That is
        // what the other engines here do, and handing Lucene a pool would make
        // its latency column describe a different amount of hardware.
        IndexSearcher searcher = new IndexSearcher(reader);
        QueryParser parser = parser();
        searchOnce(searcher, parser, queries.get(0), null);
        Bench.Usage open = Bench.measure(openStart);
        res.openUsage = open;
        res.openResidentBytes = open.rssBytes;

        Bench.Snapshot searchStart = Bench.take();
        List<Bench.QueryStat> stats = new ArrayList<>(queries.size());
        List<String> ids = new ArrayList<>(Bench.SEARCH_LIMIT);
        for (String q : queries) {
            // One warm up that is not counted, because the first run of a
            // query pays for whatever the engine caches per term and no
            // deployment sees that cost on every request.
            //
            // The page comes off this run for the same reason. Keeping the
            // identifiers allocates, and the run that pays for it is the one
            // whose time nobody reads.
            ids.clear();
            int hits = searchOnce(searcher, parser, q, ids);
            List<Double> runs = new ArrayList<>(cfg.repeat);
            for (int i = 0; i < cfg.repeat; i++) {
                long t = System.nanoTime();
                hits = searchOnce(searcher, parser, q, null);
                runs.add((System.nanoTime() - t) / 1e6);
            }
            Bench.QueryStat stat = Bench.summarise(q, hits, runs);
            stat.ids.addAll(ids);
            stats.add(stat);
        }
        Bench.Usage search = Bench.measure(searchStart);

        res.searchUsage = search;
        res.queries = stats;
        res.concurrent = concurrentPhase(searcher, queries, cfg);

        reader.close();
        dir.close();
        updatePhase(cfg, res);
    }

    private static QueryParser parser() {
        MultiFieldQueryParser p = new MultiFieldQueryParser(SEARCHED, analyzer());
        // A bare query is read as OR, which is how every other engine here is
        // asked to read it. Lucene defaults to that already and it is set
        // explicitly so that a change in its default does not silently change
        // what is being compared.
        p.setDefaultOperator(QueryParser.Operator.OR);
        return p;
    }

    /**
     * Runs one query and returns the total number of matches, which is not the
     * same as the number returned. Both the count and the page are asked for,
     * because the other engines here report both and dropping one would make
     * the numbers describe different work.
     *
     * When ids is given it collects the identifiers of the page. The documents
     * are fetched either way, so the only extra work is reading a field out of
     * one that is already in hand.
     */
    private static int searchOnce(
            IndexSearcher searcher, QueryParser parser, String text, List<String> ids)
            throws IOException {
        Query query = parse(parser, text);
        int count = searcher.count(query);
        TopDocs top = searcher.search(query, Bench.SEARCH_LIMIT);
        StoredFields stored = searcher.storedFields();
        for (ScoreDoc hit : top.scoreDocs) {
            Document doc = stored.document(hit.doc);
            if (ids != null) {
                String id = doc.get("id");
                if (id != null) {
                    ids.add(id);
                }
            }
        }
        return count;
    }

    // The query set is plain terms, but a corpus derived set can turn up a
    // token the parser treats as syntax. Tantivy's parser is lenient about
    // that and Lucene's is not, so a query it refuses is retried as literal
    // text rather than failing the run or being quietly dropped.
    private static Query parse(QueryParser parser, String text) {
        try {
            return parser.parse(text);
        } catch (ParseException e) {
            try {
                return parser.parse(QueryParser.escape(text));
            } catch (ParseException fatal) {
                throw new IllegalArgumentException("cannot parse query " + text, fatal);
            }
        }
    }

    /**
     * The query set run with several in flight, which is the only way to get a
     * throughput number that means anything. Dividing one second by the serial
     * latency gives a figure no deployment has ever reached.
     */
    private static Bench.ConcurrentStat concurrentPhase(
            IndexSearcher searcher, List<String> queries, Bench.Config cfg) {
        int workers = cfg.workers == 0 ? queries.size() : cfg.workers;
        workers = Math.clamp(workers, 1, 64);

        List<String> jobs = Bench.jobs(queries, cfg.repeat);
        AtomicInteger next = new AtomicInteger();
        List<List<Double>> collected = new ArrayList<>(workers);
        List<Thread> threads = new ArrayList<>(workers);
        boolean[] failed = {false};

        long start = System.nanoTime();
        for (int w = 0; w < workers; w++) {
            List<Double> mine = new ArrayList<>();
            collected.add(mine);
            // Every worker parses for itself. The parser holds state while it
            // is working and sharing one would serialise the phase that exists
            // to measure what happens when nothing is serialised.
            QueryParser parser = parser();
            Thread t = new Thread(() -> {
                while (true) {
                    int i = next.getAndIncrement();
                    if (i >= jobs.size()) {
                        return;
                    }
                    long at = System.nanoTime();
                    try {
                        searchOnce(searcher, parser, jobs.get(i), null);
                    } catch (IOException | RuntimeException e) {
                        failed[0] = true;
                        return;
                    }
                    mine.add((System.nanoTime() - at) / 1e6);
                }
            });
            threads.add(t);
            t.start();
        }
        for (Thread t : threads) {
            try {
                t.join();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return null;
            }
        }
        double elapsed = (System.nanoTime() - start) / 1e9;
        if (failed[0]) {
            // A worker that failed means the throughput figure would describe
            // whatever the failure happened to produce, so it is left out.
            return null;
        }

        List<Double> all = new ArrayList<>();
        for (List<Double> some : collected) {
            all.addAll(some);
        }
        Bench.QueryStat summary = Bench.summarise("", 0, all);
        Bench.ConcurrentStat stat = new Bench.ConcurrentStat();
        stat.workers = workers;
        stat.queries = all.size();
        stat.seconds = elapsed;
        stat.medianMs = summary.medianMs;
        stat.p99Ms = summary.p99Ms;
        return stat;
    }

    /**
     * Reindexes a slice of the corpus into the index that is already built,
     * which is what an incremental sync does and is not the same operation as
     * building from empty.
     */
    private static void updatePhase(Bench.Config cfg, Bench.Result res) throws IOException {
        int want = cfg.capped(Bench.UPDATE_DOCUMENTS);

        int[] documents = {0};
        long[] bytes = {0};
        Bench.Snapshot start = Bench.take();
        try (Directory dir = FSDirectory.open(cfg.work);
                IndexWriter writer = new IndexWriter(dir, writerConfig(IndexWriterConfig.OpenMode.APPEND))) {
            Bench.read(cfg.corpus, d -> {
                if (documents[0] >= want) {
                    return false;
                }
                documents[0]++;
                bytes[0] += Bench.utf8Length(d.body);
                try {
                    // Replacing by term is what an update is. Adding without
                    // it would double the corpus and report a rate for an
                    // operation nobody runs.
                    writer.updateDocument(new Term("id", d.id), document(d));
                } catch (IOException e) {
                    throw new UncheckedWrite(e);
                }
                return true;
            });
            writer.commit();
        } catch (UncheckedWrite e) {
            throw e.cause();
        }
        Bench.Usage usage = Bench.measure(start);

        long[] size = Bench.dirSize(cfg.work);
        res.updateUsage = usage;
        res.updateDocuments = documents[0];
        res.updateBytes = bytes[0];
        res.updateIndexBytesAfter = size[0];
    }

    /** Carries a write failure out of the corpus callback, which cannot throw
     * a checked exception, without losing it. */
    private static final class UncheckedWrite extends RuntimeException {
        private static final long serialVersionUID = 1L;

        UncheckedWrite(IOException cause) {
            super(cause);
        }

        IOException cause() {
            return (IOException) getCause();
        }
    }
}
