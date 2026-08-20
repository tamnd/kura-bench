// The measuring half of a Java runner.
//
// It is the same idea as benchrs on the Rust side and the runner package on the
// Go side: the corpus reader, the process counters, the machine description,
// the result shape and the flags all live here, so that the file next to it is
// an engine and nothing else. Two engines timed by two pieces of code are not
// being compared to each other, they are being compared to their own
// stopwatches.
//
// There is no JSON library and no argument parser here. The flags are somebody
// else's flags and the result shape is somebody else's shape, so both are
// written out by hand where a reader can check them against the Go definitions
// rather than against a library's idea of how to spell a field name.

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.function.Predicate;
import java.util.zip.GZIPInputStream;

final class Bench {
    private Bench() {}

    /** How many documents the update phase rewrites, the same number every
     * other runner here uses. A figure that means five thousand documents on
     * one engine and a fifth of the corpus on another is not comparable. */
    static final int UPDATE_DOCUMENTS = 5000;

    /** How many results a search asks for. A result list is ten long, and an
     * engine asked for ten thousand is doing a different job. */
    static final int SEARCH_LIMIT = 10;

    // The kernel reports processor time in clock ticks and there is no way to
    // ask sysconf for the rate from Java without native code. It has been a
    // hundred on every Linux this suite runs on for as long as the file has
    // existed, and a wrong value here would show up immediately as a phase
    // reporting more processor time than it had wall clock.
    private static final double USER_HZ = 100.0;

    // ---- flags ----------------------------------------------------------

    /** The flags the orchestrator invokes every runner with, spelled exactly
     * the way the Go harness spells them. */
    static final class Config {
        Path corpus;
        Path queries;
        Path work;
        String phase = "all";
        int repeat = 20;
        int limit = 0;
        int workers = 0;

        /** How many documents this run should touch, given the corpus limit. */
        int capped(int want) {
            return limit > 0 && limit < want ? limit : want;
        }
    }

    static Config parse(String[] args) {
        Config cfg = new Config();
        for (int i = 0; i < args.length; i += 2) {
            String name = args[i].replaceFirst("^-+", "");
            String value = i + 1 < args.length ? args[i + 1] : "";
            switch (name) {
                case "corpus" -> cfg.corpus = Paths.get(value);
                case "queries" -> cfg.queries = Paths.get(value);
                case "work" -> cfg.work = Paths.get(value);
                case "phase" -> cfg.phase = value;
                case "repeat" -> cfg.repeat = number("repeat", value);
                case "limit" -> cfg.limit = number("limit", value);
                case "workers" -> cfg.workers = number("workers", value);
                default -> throw new IllegalArgumentException("unknown flag " + name);
            }
        }
        if (cfg.corpus == null || cfg.work == null) {
            throw new IllegalArgumentException("both -corpus and -work are required");
        }
        if (!cfg.phase.equals("index") && cfg.queries == null) {
            throw new IllegalArgumentException("-queries is required for the query phase");
        }
        return cfg;
    }

    private static int number(String flag, String value) {
        try {
            return Integer.parseInt(value);
        } catch (NumberFormatException e) {
            throw new IllegalArgumentException("-" + flag + " wants a number, got " + value);
        }
    }

    // ---- corpus ---------------------------------------------------------

    /** One document as the corpus file holds it. */
    static final class Document {
        String id = "";
        String repo = "";
        String path = "";
        String title = "";
        String body = "";
        String ext = "";
    }

    /**
     * Reads the corpus and calls back for each document until the callback
     * returns false.
     *
     * The reader is written here rather than taken from a library because the
     * corpus is a fixed shape of six string fields and a parser that also
     * handles everything else would be measured in the indexing phase of every
     * engine. What it does refuse is anything that is not that shape, so a
     * malformed corpus is an error instead of a run that silently indexed
     * fewer documents and looked fast.
     */
    static void read(Path path, Predicate<Document> each) throws IOException {
        try (BufferedReader r = reader(path)) {
            String line;
            long number = 0;
            while ((line = r.readLine()) != null) {
                number++;
                if (line.isBlank()) {
                    continue;
                }
                Document d;
                try {
                    d = document(line);
                } catch (RuntimeException e) {
                    throw new IOException(path + ":" + number + ": " + e.getMessage(), e);
                }
                if (!each.test(d)) {
                    return;
                }
            }
        }
    }

    private static BufferedReader reader(Path path) throws IOException {
        var in = Files.newInputStream(path);
        if (path.toString().endsWith(".gz")) {
            return new BufferedReader(
                    new InputStreamReader(new GZIPInputStream(in, 1 << 16), StandardCharsets.UTF_8),
                    1 << 16);
        }
        return new BufferedReader(new InputStreamReader(in, StandardCharsets.UTF_8), 1 << 16);
    }

    /** The query file: one query per line, blank lines and comments dropped. */
    static List<String> queries(Path path) throws IOException {
        List<String> out = new ArrayList<>();
        for (String line : Files.readAllLines(path, StandardCharsets.UTF_8)) {
            String q = line.trim();
            if (!q.isEmpty() && !q.startsWith("#")) {
                out.add(q);
            }
        }
        return out;
    }

    // A hand written reader for one flat JSON object of string fields. It
    // takes the six keys the corpus has and steps over anything else rather
    // than failing, so a corpus that grows a field does not need this changed.
    private static Document document(String line) {
        Document d = new Document();
        int i = skip(line, 0);
        if (i >= line.length() || line.charAt(i) != '{') {
            throw new IllegalArgumentException("not a JSON object");
        }
        i = skip(line, i + 1);
        if (i < line.length() && line.charAt(i) == '}') {
            return d;
        }
        while (i < line.length()) {
            StringBuilder key = new StringBuilder();
            i = string(line, i, key);
            i = skip(line, i);
            if (i >= line.length() || line.charAt(i) != ':') {
                throw new IllegalArgumentException("a key with no value after it");
            }
            i = skip(line, i + 1);

            if (i < line.length() && line.charAt(i) == '"') {
                StringBuilder value = new StringBuilder();
                i = string(line, i, value);
                switch (key.toString()) {
                    case "id" -> d.id = value.toString();
                    case "repo" -> d.repo = value.toString();
                    case "path" -> d.path = value.toString();
                    case "title" -> d.title = value.toString();
                    case "body" -> d.body = value.toString();
                    case "ext" -> d.ext = value.toString();
                    default -> { }
                }
            } else {
                i = value(line, i);
            }

            i = skip(line, i);
            if (i < line.length() && line.charAt(i) == ',') {
                i = skip(line, i + 1);
                continue;
            }
            if (i < line.length() && line.charAt(i) == '}') {
                return d;
            }
            throw new IllegalArgumentException("a value with neither a comma nor a brace after it");
        }
        throw new IllegalArgumentException("the object never closed");
    }

    private static int skip(String s, int i) {
        while (i < s.length() && Character.isWhitespace(s.charAt(i))) {
            i++;
        }
        return i;
    }

    private static int string(String s, int i, StringBuilder out) {
        if (i >= s.length() || s.charAt(i) != '"') {
            throw new IllegalArgumentException("a string was expected");
        }
        i++;
        while (i < s.length()) {
            char c = s.charAt(i++);
            if (c == '"') {
                return i;
            }
            if (c != '\\') {
                out.append(c);
                continue;
            }
            if (i >= s.length()) {
                break;
            }
            char e = s.charAt(i++);
            switch (e) {
                case '"', '\\', '/' -> out.append(e);
                case 'b' -> out.append('\b');
                case 'f' -> out.append('\f');
                case 'n' -> out.append('\n');
                case 'r' -> out.append('\r');
                case 't' -> out.append('\t');
                case 'u' -> {
                    if (i + 4 > s.length()) {
                        throw new IllegalArgumentException("a short escape at the end of the line");
                    }
                    out.append((char) Integer.parseInt(s.substring(i, i + 4), 16));
                    i += 4;
                }
                default -> throw new IllegalArgumentException("an escape of \\" + e);
            }
        }
        throw new IllegalArgumentException("a string that never closed");
    }

    // Steps over a value this reader has no use for, which is anything that is
    // not one of the six strings. Nesting is counted so that an object or an
    // array does not end the scan at its first inner brace.
    private static int value(String s, int i) {
        int depth = 0;
        while (i < s.length()) {
            char c = s.charAt(i);
            if (c == '"') {
                i = string(s, i, new StringBuilder());
                if (depth == 0) {
                    return i;
                }
                continue;
            }
            if (c == '{' || c == '[') {
                depth++;
            } else if (c == '}' || c == ']') {
                if (depth == 0) {
                    return i;
                }
                depth--;
            } else if (depth == 0 && (c == ',' || Character.isWhitespace(c))) {
                return i;
            }
            i++;
        }
        return i;
    }

    /** The bytes a string takes as UTF-8, which is what the other runners
     * count, without building the array to find out. */
    static long utf8Length(String s) {
        long n = 0;
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (c < 0x80) {
                n += 1;
            } else if (c < 0x800) {
                n += 2;
            } else if (Character.isHighSurrogate(c) && i + 1 < s.length()
                    && Character.isLowSurrogate(s.charAt(i + 1))) {
                n += 4;
                i++;
            } else {
                n += 3;
            }
        }
        return n;
    }

    // ---- process counters -----------------------------------------------

    /** What one phase cost. The fields are the ones bench.Usage has, because
     * the orchestrator reads this JSON into that struct. */
    static final class Usage {
        double wallSeconds;
        double userSeconds;
        double sysSeconds;
        long peakRssBytes;
        long rssBytes;
        long readBytes;
        long writeBytes;
    }

    /** A reading of the counters at one instant. Pair it with measure. */
    static final class Snapshot {
        long nanos;
        double user;
        double sys;
        long read;
        long write;
    }

    static Snapshot take() {
        Snapshot s = new Snapshot();
        s.nanos = System.nanoTime();
        double[] cpu = cpuTime();
        s.user = cpu[0];
        s.sys = cpu[1];
        long[] io = ioBytes();
        s.read = io[0];
        s.write = io[1];
        return s;
    }

    /**
     * The usage since a snapshot.
     *
     * Processor time and input and output are differences, so they describe
     * the phase. Memory is not: the kernel only keeps the high water mark and
     * it never goes down, so a later phase inherits the peak of every phase
     * before it. That is the honest reading of the number the kernel offers.
     */
    static Usage measure(Snapshot start) {
        Snapshot now = take();
        Usage u = new Usage();
        u.wallSeconds = (now.nanos - start.nanos) / 1e9;
        u.userSeconds = now.user - start.user;
        u.sysSeconds = now.sys - start.sys;
        u.peakRssBytes = statusField("VmHWM:") * 1024;
        u.rssBytes = statusField("VmRSS:") * 1024;
        u.readBytes = now.read - start.read;
        u.writeBytes = now.write - start.write;
        return u;
    }

    // Processor time in and out of the kernel, from the process's own stat
    // file. The management bean offers a total and not the split, and the split
    // is the half worth having: a large system share usually means a syscall
    // per document, which is a fixable thing.
    private static double[] cpuTime() {
        String stat = slurp("/proc/self/stat");
        if (stat == null) {
            return new double[] {0, 0};
        }
        // The second field is the command in parentheses and may itself hold
        // spaces, so the split starts after the last closing one.
        int close = stat.lastIndexOf(')');
        if (close < 0 || close + 2 >= stat.length()) {
            return new double[] {0, 0};
        }
        String[] f = stat.substring(close + 2).trim().split("\\s+");
        // The fields after the command start at the third, so the fourteenth
        // and fifteenth of the file are the eleventh and twelfth here.
        if (f.length < 13) {
            return new double[] {0, 0};
        }
        try {
            return new double[] {
                Long.parseLong(f[11]) / USER_HZ, Long.parseLong(f[12]) / USER_HZ,
            };
        } catch (NumberFormatException e) {
            return new double[] {0, 0};
        }
    }

    // Bytes this process moved to and from storage. These are the counters
    // that see through the page cache, so a phase that looks fast because
    // everything was already cached reports a read figure far below the bytes
    // it processed.
    private static long[] ioBytes() {
        String io = slurp("/proc/self/io");
        if (io == null) {
            return new long[] {0, 0};
        }
        long read = 0;
        long write = 0;
        for (String line : io.split("\n")) {
            int colon = line.indexOf(':');
            if (colon < 0) {
                continue;
            }
            long n;
            try {
                n = Long.parseLong(line.substring(colon + 1).trim());
            } catch (NumberFormatException e) {
                continue;
            }
            switch (line.substring(0, colon)) {
                case "read_bytes" -> read = n;
                case "write_bytes" -> write = n;
                default -> { }
            }
        }
        return new long[] {read, write};
    }

    private static long statusField(String prefix) {
        String status = slurp("/proc/self/status");
        if (status == null) {
            return 0;
        }
        for (String line : status.split("\n")) {
            if (!line.startsWith(prefix)) {
                continue;
            }
            String[] f = line.substring(prefix.length()).trim().split("\\s+");
            try {
                return Long.parseLong(f[0]);
            } catch (RuntimeException e) {
                return 0;
            }
        }
        return 0;
    }

    private static String slurp(String path) {
        try {
            return Files.readString(Paths.get(path), StandardCharsets.UTF_8);
        } catch (IOException | RuntimeException e) {
            // A platform without the file leaves the field empty rather than
            // filled with a guess, which is what the Go side does too.
            return null;
        }
    }

    // ---- machine --------------------------------------------------------

    /** Where a result was taken. Every field here has changed a number by more
     * than any code change in this repository has. */
    static final class Machine {
        String host = "";
        String os = "";
        String arch = "";
        String cpu = "";
        int cores;
        long memoryBytes;
        double loadBefore;
        long memoryFreeBytes;
        boolean dedicated;
    }

    static Machine describe() {
        Machine m = new Machine();
        String host = slurp("/proc/sys/kernel/hostname");
        m.host = host == null ? "" : host.trim();
        m.os = System.getProperty("os.name", "").toLowerCase();
        if (m.os.startsWith("linux")) {
            m.os = "linux";
        }
        // The names are the Go ones, because a table that spells the same
        // machine two ways cannot be grouped by it.
        m.arch = switch (System.getProperty("os.arch", "")) {
            case "amd64", "x86_64" -> "amd64";
            case "aarch64", "arm64" -> "arm64";
            default -> System.getProperty("os.arch", "");
        };
        m.cores = Runtime.getRuntime().availableProcessors();
        m.cpu = cpuModel();

        String meminfo = slurp("/proc/meminfo");
        if (meminfo != null) {
            m.memoryBytes = meminfoField(meminfo, "MemTotal:");
            m.memoryFreeBytes = meminfoField(meminfo, "MemAvailable:");
        }
        String load = slurp("/proc/loadavg");
        if (load != null) {
            String[] f = load.trim().split("\\s+");
            try {
                m.loadBefore = Double.parseDouble(f[0]);
            } catch (RuntimeException e) {
                m.loadBefore = 0;
            }
        }
        // A machine under load is not a benchmark machine. The threshold is
        // per core and it is the Go one, so that the two agree about whether a
        // given run was dedicated.
        if (m.cores > 0) {
            m.dedicated = m.loadBefore / m.cores < 0.2;
        }
        return m;
    }

    private static String cpuModel() {
        String info = slurp("/proc/cpuinfo");
        if (info == null) {
            return "";
        }
        for (String line : info.split("\n")) {
            if (line.startsWith("model name")) {
                int colon = line.indexOf(':');
                if (colon >= 0) {
                    return line.substring(colon + 1).trim();
                }
            }
        }
        return "";
    }

    private static long meminfoField(String info, String prefix) {
        for (String line : info.split("\n")) {
            if (!line.startsWith(prefix)) {
                continue;
            }
            String[] f = line.substring(prefix.length()).trim().split("\\s+");
            try {
                return Long.parseLong(f[0]) * 1024;
            } catch (RuntimeException e) {
                return 0;
            }
        }
        return 0;
    }

    // ---- result ---------------------------------------------------------

    /** One query measured over several runs. */
    static final class QueryStat {
        String query = "";
        int hits;
        int runs;
        double minMs;
        double medianMs;
        double p90Ms;
        double p99Ms;
        double maxMs;
        List<String> ids = new ArrayList<>();
    }

    /** The query set run with several in flight. */
    static final class ConcurrentStat {
        int workers;
        int queries;
        double seconds;
        double medianMs;
        double p99Ms;
    }

    /** What a runner writes to standard output. */
    static final class Result {
        String engine = "";
        String version = "";
        String language = "";
        int documents;
        long corpusBytes;

        Usage indexUsage;
        long indexBytes;
        int indexFiles;

        Usage openUsage;
        long openResidentBytes;

        Usage searchUsage;
        List<QueryStat> queries = new ArrayList<>();
        ConcurrentStat concurrent;

        Usage updateUsage;
        int updateDocuments;
        long updateBytes;
        long updateIndexBytesAfter;

        Machine machine = new Machine();
        String notes = "";
    }

    /**
     * Turns a set of timings into a stat.
     *
     * Every run the caller passes is counted and nothing is discarded here.
     * Deciding what to throw away is the runner's job and belongs where a
     * reader can see it, not hidden in a helper every engine shares.
     */
    static QueryStat summarise(String query, int hits, List<Double> runs) {
        QueryStat s = new QueryStat();
        s.query = query;
        s.hits = hits;
        if (runs.isEmpty()) {
            return s;
        }
        double[] ms = new double[runs.size()];
        for (int i = 0; i < ms.length; i++) {
            ms[i] = runs.get(i);
        }
        Arrays.sort(ms);
        s.runs = ms.length;
        s.minMs = ms[0];
        s.medianMs = percentile(ms, 0.50);
        s.p90Ms = percentile(ms, 0.90);
        s.p99Ms = percentile(ms, 0.99);
        s.maxMs = ms[ms.length - 1];
        return s;
    }

    /** The nearest rank on an already sorted array, which is the definition
     * that does not invent a value nobody measured. */
    static double percentile(double[] sorted, double p) {
        if (sorted.length == 0) {
            return 0;
        }
        int i = (int) Math.ceil(p * sorted.length) - 1;
        return sorted[Math.clamp(i, 0, sorted.length - 1)];
    }

    /** The files under a path and how many there are, which is how an index
     * that is a directory of segments gets compared with one that is a single
     * file. A path that does not exist is not an error. */
    static long[] dirSize(Path dir) {
        if (!Files.exists(dir)) {
            return new long[] {0, 0};
        }
        long total = 0;
        long files = 0;
        try (var walk = Files.walk(dir)) {
            for (Path p : (Iterable<Path>) walk::iterator) {
                if (Files.isRegularFile(p)) {
                    total += Files.size(p);
                    files++;
                }
            }
        } catch (IOException | UncheckedIOException e) {
            return new long[] {total, files};
        }
        return new long[] {total, files};
    }

    // ---- writing it out -------------------------------------------------

    /** The result as the single line of JSON the orchestrator reads. The field
     * names are the JSON tags on bench.Result and are checked against that file
     * rather than derived from anything here. */
    static String json(Result r) {
        StringBuilder b = new StringBuilder(1 << 14);
        b.append('{');
        str(b, "engine", r.engine).append(',');
        str(b, "version", r.version).append(',');
        str(b, "language", r.language).append(',');

        b.append("\"corpus\":{");
        num(b, "documents", r.documents).append(',');
        num(b, "bytes", r.corpusBytes);
        b.append("},");

        b.append("\"index\":{\"usage\":");
        usage(b, r.indexUsage).append(',');
        num(b, "bytes", r.indexBytes).append(',');
        num(b, "files", r.indexFiles);
        b.append("},");

        b.append("\"open\":{\"usage\":");
        usage(b, r.openUsage).append(',');
        num(b, "resident_bytes", r.openResidentBytes);
        b.append("},");

        b.append("\"search\":{\"usage\":");
        usage(b, r.searchUsage).append(',');
        b.append("\"queries\":[");
        for (int i = 0; i < r.queries.size(); i++) {
            if (i > 0) {
                b.append(',');
            }
            query(b, r.queries.get(i));
        }
        b.append(']');
        if (r.concurrent != null) {
            b.append(",\"concurrent\":{");
            num(b, "workers", r.concurrent.workers).append(',');
            num(b, "queries", r.concurrent.queries).append(',');
            real(b, "seconds", r.concurrent.seconds).append(',');
            real(b, "median_ms", r.concurrent.medianMs).append(',');
            real(b, "p99_ms", r.concurrent.p99Ms);
            b.append('}');
        }
        b.append("},");

        if (r.updateUsage != null) {
            b.append("\"update\":{\"usage\":");
            usage(b, r.updateUsage).append(',');
            num(b, "documents", r.updateDocuments).append(',');
            num(b, "bytes", r.updateBytes).append(',');
            num(b, "index_bytes_after", r.updateIndexBytesAfter);
            b.append("},");
        }

        b.append("\"machine\":{");
        str(b, "host", r.machine.host).append(',');
        str(b, "os", r.machine.os).append(',');
        str(b, "arch", r.machine.arch).append(',');
        str(b, "cpu", r.machine.cpu).append(',');
        num(b, "cores", r.machine.cores).append(',');
        num(b, "memory_bytes", r.machine.memoryBytes).append(',');
        real(b, "load_before", r.machine.loadBefore).append(',');
        num(b, "memory_free_bytes", r.machine.memoryFreeBytes).append(',');
        b.append("\"dedicated\":").append(r.machine.dedicated);
        b.append('}');

        if (!r.notes.isEmpty()) {
            b.append(',');
            str(b, "notes", r.notes);
        }
        b.append('}');
        return b.toString();
    }

    private static StringBuilder usage(StringBuilder b, Usage u) {
        Usage v = u == null ? new Usage() : u;
        b.append('{');
        real(b, "wall_seconds", v.wallSeconds).append(',');
        real(b, "user_seconds", v.userSeconds).append(',');
        real(b, "sys_seconds", v.sysSeconds).append(',');
        num(b, "peak_rss_bytes", v.peakRssBytes).append(',');
        num(b, "rss_bytes", v.rssBytes).append(',');
        num(b, "read_bytes", v.readBytes).append(',');
        num(b, "write_bytes", v.writeBytes);
        return b.append('}');
    }

    private static void query(StringBuilder b, QueryStat q) {
        b.append('{');
        str(b, "query", q.query).append(',');
        num(b, "hits", q.hits).append(',');
        num(b, "runs", q.runs).append(',');
        real(b, "min_ms", q.minMs).append(',');
        real(b, "median_ms", q.medianMs).append(',');
        real(b, "p90_ms", q.p90Ms).append(',');
        real(b, "p99_ms", q.p99Ms).append(',');
        real(b, "max_ms", q.maxMs);
        if (!q.ids.isEmpty()) {
            b.append(",\"ids\":[");
            for (int i = 0; i < q.ids.size(); i++) {
                if (i > 0) {
                    b.append(',');
                }
                quote(b, q.ids.get(i));
            }
            b.append(']');
        }
        b.append('}');
    }

    private static StringBuilder str(StringBuilder b, String key, String value) {
        quote(b, key).append(':');
        return quote(b, value == null ? "" : value);
    }

    private static StringBuilder num(StringBuilder b, String key, long value) {
        return quote(b, key).append(':').append(value);
    }

    private static StringBuilder real(StringBuilder b, String key, double value) {
        quote(b, key).append(':');
        // A phase that somehow produced one of these would otherwise emit a
        // token no JSON reader accepts, which turns a strange number into a
        // parse failure three steps away from the cause.
        if (Double.isNaN(value) || Double.isInfinite(value)) {
            return b.append('0');
        }
        return b.append(value);
    }

    private static StringBuilder quote(StringBuilder b, String s) {
        b.append('"');
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"' -> b.append("\\\"");
                case '\\' -> b.append("\\\\");
                case '\n' -> b.append("\\n");
                case '\r' -> b.append("\\r");
                case '\t' -> b.append("\\t");
                default -> {
                    if (c < 0x20) {
                        b.append(String.format("\\u%04x", (int) c));
                    } else {
                        b.append(c);
                    }
                }
            }
        }
        return b.append('"');
    }

    /** The jobs for the concurrent phase: the whole query set, repeated. */
    static List<String> jobs(List<String> queries, int repeat) {
        List<String> out = new ArrayList<>(queries.size() * Math.max(repeat, 1));
        for (int i = 0; i < repeat; i++) {
            out.addAll(queries);
        }
        return Collections.unmodifiableList(out);
    }
}
