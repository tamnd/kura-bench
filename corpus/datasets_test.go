package corpus

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixtures below are written by hand rather than taken from the corpora
// they stand for. That is not the usual preference here, and for these three it
// is the right call: the mail corpus carries the personal data of real people
// and nothing from it may appear in this repository, and the other two are a
// gigabyte each, which is not a test fixture.
//
// What each fixture stands in for is named in the test, so that a person
// reading it can go and check the claim against the real archive.

func TestDatasetsAreDescribed(t *testing.T) {
	for _, d := range Datasets() {
		if d.Name == "" || d.URL == "" || d.About == "" || d.Licence == "" {
			t.Errorf("dataset %q is missing a name, a url, an about or a licence", d.Name)
		}
		if d.Build == nil {
			t.Errorf("dataset %q has no builder", d.Name)
		}
		if d.Bytes <= 0 {
			t.Errorf("dataset %q does not say how large the download is", d.Name)
		}
		// An unpinned corpus is not a measuring instrument. Two runs a month
		// apart have to read the same bytes or the difference between them is
		// not a fact about either engine, and an empty checksum here is only
		// ever right for the few minutes between adding a dataset and
		// downloading it once.
		if len(d.SHA256) != 64 || strings.Trim(d.SHA256, "0123456789abcdef") != "" {
			t.Errorf("dataset %q has %q where a lower case hex checksum should be", d.Name, d.SHA256)
		}
	}
}

func TestLookupDatasetNamesTheOnesThereAre(t *testing.T) {
	if _, err := LookupDataset("enron"); err != nil {
		t.Fatalf("enron: %v", err)
	}
	_, err := LookupDataset("nothing")
	if err == nil {
		t.Fatal("looking up a dataset that does not exist should fail")
	}
	// A person who mistypes a name should be told what the names are, because
	// the alternative is reading the source to find out.
	for _, want := range []string{"enron", "msmarco", "simplewiki"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestMailIsSplitIntoMessages covers the shapes the real mail archive has in
// it. The first message is ordinary. The second has a header that continues on
// a line the mail parser rejects, which is what several thousand messages in
// the archive look like after twenty years of mail clients. The third is a body
// with no headers at all, which the archive also contains.
func TestMailIsSplitIntoMessages(t *testing.T) {
	archive := writeTar(t, map[string]string{
		"maildir/finance-a/inbox/1.": "From: a@example.com\r\n" +
			"Subject: quarterly numbers\r\n" +
			"\r\n" +
			"They are attached.\r\n",
		"maildir/finance-b/sent/7.": "Subject: re: quarterly numbers\n" +
			"To: a@example.com, and then a line that is not a header\n" +
			"\n" +
			"Looks fine to me.\n",
		"maildir/finance-b/sent/8.": "no headers here at all\n",
	})

	docs := build(t, buildEnron, archive, 0)
	if len(docs) != 3 {
		t.Fatalf("got %d messages, want 3", len(docs))
	}

	first := byID(t, docs, "finance-a/inbox/1.")
	if first.Title != "quarterly numbers" {
		t.Errorf("subject is %q", first.Title)
	}
	if first.Body != "They are attached." {
		t.Errorf("body is %q", first.Body)
	}
	if first.Repo != "finance-a" || first.Path != "inbox/1." {
		t.Errorf("the account and the folder are %q and %q", first.Repo, first.Path)
	}

	// The point of the fallback: a message the mail parser will not touch still
	// gives up its subject and its body.
	second := byID(t, docs, "finance-b/sent/7.")
	if second.Title != "re: quarterly numbers" {
		t.Errorf("the fallback did not find the subject, it found %q", second.Title)
	}
	if second.Body != "Looks fine to me." {
		t.Errorf("the fallback body is %q", second.Body)
	}

	third := byID(t, docs, "finance-b/sent/8.")
	if third.Title != "" || third.Body != "no headers here at all" {
		t.Errorf("a message with no headers came out as %q and %q", third.Title, third.Body)
	}
}

// TestMailLeavesOutWhatCannotBeIndexed covers the two reasons a real message is
// dropped: it is not text, or it is an attachment somebody pasted inline and is
// large enough on its own to move an average over half a million documents.
func TestMailLeavesOutWhatCannotBeIndexed(t *testing.T) {
	archive := writeTar(t, map[string]string{
		"maildir/a/inbox/1.": "Subject: fine\n\nkeep me\n",
		"maildir/a/inbox/2.": "Subject: latin1\n\n\xff\xfe not utf8\n",
		"maildir/a/inbox/3.": "Subject: huge\n\n" + strings.Repeat("x", maxMessageSize+1),
		"maildir/a/inbox/4.": "Subject: empty\n\n",
	})

	docs := build(t, buildEnron, archive, 0)
	if len(docs) != 1 {
		t.Fatalf("got %d messages, want only the one that is text and a sane size", len(docs))
	}
	if docs[0].Body != "keep me" {
		t.Errorf("the wrong message survived: %q", docs[0].Body)
	}
}

func TestMailStopsAtTheLimit(t *testing.T) {
	archive := writeTar(t, map[string]string{
		"maildir/a/inbox/1.": "Subject: one\n\nfirst\n",
		"maildir/a/inbox/2.": "Subject: two\n\nsecond\n",
		"maildir/a/inbox/3.": "Subject: three\n\nthird\n",
	})
	if docs := build(t, buildEnron, archive, 2); len(docs) != 2 {
		t.Fatalf("got %d messages, want 2", len(docs))
	}
}

// TestPassagesBecomeDocuments covers the passage collection, which is a tab
// separated identifier and paragraph and nothing else. The empty title is the
// point: the collection has no titles and synthesising one would hand every
// engine a document the dataset does not contain.
func TestPassagesBecomeDocuments(t *testing.T) {
	archive := writeTar(t, map[string]string{
		"collection.tsv": "0\tThe presence of communication amid scientific minds.\n" +
			"1\tThe Manhattan Project was the name for a project.\n" +
			"2\t\n" +
			"a line with no tab on it at all\n" +
			"3\tThe project was started in 1939.\n",
		"queries.dev.small.tsv": "1048578\tcost of endless pools\n",
		"qrels.dev.small.tsv":   "1048578\t0\t7187158\t1\n",
	})

	docs := build(t, buildMSMARCO, archive, 0)
	if len(docs) != 3 {
		t.Fatalf("got %d passages, want 3, the empty one and the malformed one are dropped", len(docs))
	}
	if docs[0].ID != "0" || docs[0].Repo != "msmarco" || docs[0].Title != "" {
		t.Errorf("first passage came out as %+v", docs[0])
	}
	if !strings.HasPrefix(docs[1].Body, "The Manhattan Project") {
		t.Errorf("second passage body is %q", docs[1].Body)
	}
}

func TestJudgmentsAreWrittenBesideTheCorpus(t *testing.T) {
	archive := writeTar(t, map[string]string{
		"collection.tsv":        "0\ta passage\n",
		"queries.dev.small.tsv": "1048578\tcost of endless pools\n",
		"qrels.dev.small.tsv":   "1048578\t0\t7187158\t1\n",
	})

	dir := t.TempDir()
	out := filepath.Join(dir, "msmarco.jsonl")
	d, err := LookupDataset("msmarco")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := WriteDataset(d, archive, &buf, 0, out); err != nil {
		t.Fatal(err)
	}

	// A ranking change is only an improvement if a judged query set says so,
	// and a judged query set that is not next to the corpus it judges is one
	// nobody will find.
	for _, name := range d.Sidecar {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(body) == 0 {
			t.Errorf("%s came out empty", name)
		}
	}
}

func TestAnArchiveWithoutJudgmentsIsRejected(t *testing.T) {
	archive := writeTar(t, map[string]string{"collection.tsv": "0\ta passage\n"})
	d, err := LookupDataset("msmarco")
	if err != nil {
		t.Fatal(err)
	}
	// Failing here rather than after writing three gigabytes of corpus is the
	// whole reason the judgments are pulled out first.
	if _, err := WriteDataset(d, archive, &bytes.Buffer{}, 0, filepath.Join(t.TempDir(), "out.jsonl")); err == nil {
		t.Fatal("an archive that is missing the judgments should be rejected")
	}
}

// TestArticlesBecomeDocuments covers the export format. The redirect and the
// talk page are the two things dropped, and the markup on the article that
// stays is deliberately left alone.
func TestArticlesBecomeDocuments(t *testing.T) {
	const export = `<mediawiki>
<page><title>Earth</title><ns>0</ns><id>9228</id>
<revision><text>{{Infobox planet}} The '''Earth''' is a [[planet]].</text></revision></page>
<page><title>Terra</title><ns>0</ns><id>9229</id><redirect title="Earth" />
<revision><text></text></revision></page>
<page><title>Talk:Earth</title><ns>1</ns><id>9230</id>
<revision><text>Should this mention the moon?</text></revision></page>
<page><title>Moon</title><ns>0</ns><id>9231</id>
<revision><text>The Moon orbits the [[Earth]].</text></revision></page>
</mediawiki>`

	var buf bytes.Buffer
	st, err := walkWiki(strings.NewReader(export), json.NewEncoder(&buf), 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Documents != 2 {
		t.Fatalf("got %d articles, want 2, the redirect and the talk page are not articles", st.Documents)
	}

	docs := decode(t, buf.Bytes())
	if docs[0].Title != "Earth" || docs[0].ID != "9228" || docs[0].Path != "Earth" {
		t.Errorf("first article came out as %+v", docs[0])
	}
	// The markup stays. An engine in front of a wiki is handed the wiki's own
	// markup, and a corpus that strips it measures something nobody runs.
	if !strings.Contains(docs[0].Body, "{{Infobox planet}}") || !strings.Contains(docs[0].Body, "[[planet]]") {
		t.Errorf("the markup was stripped: %q", docs[0].Body)
	}
	if docs[1].Title != "Moon" {
		t.Errorf("second article is %q", docs[1].Title)
	}
}

func TestArticlesStopAtTheLimit(t *testing.T) {
	const export = `<mediawiki>
<page><title>A</title><ns>0</ns><id>1</id><revision><text>first</text></revision></page>
<page><title>B</title><ns>0</ns><id>2</id><revision><text>second</text></revision></page>
</mediawiki>`
	var buf bytes.Buffer
	st, err := walkWiki(strings.NewReader(export), json.NewEncoder(&buf), 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Documents != 1 {
		t.Fatalf("got %d articles, want 1", st.Documents)
	}
}

func TestATruncatedExportIsAnError(t *testing.T) {
	// A download that stopped halfway produces a shorter corpus and no
	// complaint unless this is checked, and a shorter corpus is a set of
	// numbers that look fine and are not comparable with anybody else's.
	const cut = `<mediawiki><page><title>A</title><ns>0</ns><id>1</id><revision><text>first`
	if _, err := walkWiki(strings.NewReader(cut), json.NewEncoder(&bytes.Buffer{}), 0); err == nil {
		t.Fatal("a truncated export should be an error")
	}
}

func writeTar(t *testing.T, files map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fixture.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// Sorted, because a map is not, and a corpus whose document order changes
	// between runs is a corpus two runs cannot be compared across.
	for _, name := range slices.Sorted(maps.Keys(files)) {
		body := files[name]
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func build(t *testing.T, fn func(string, *json.Encoder, int, string) (Stats, error), archive string, limit int) []Document {
	t.Helper()

	var buf bytes.Buffer
	st, err := fn(archive, json.NewEncoder(&buf), limit, filepath.Join(t.TempDir(), "out.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	docs := decode(t, buf.Bytes())
	if st.Documents != len(docs) {
		t.Fatalf("the count says %d documents and the file holds %d", st.Documents, len(docs))
	}
	return docs
}

func decode(t *testing.T, body []byte) []Document {
	t.Helper()

	var docs []Document
	dec := json.NewDecoder(bytes.NewReader(body))
	for dec.More() {
		var d Document
		if err := dec.Decode(&d); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, d)
	}
	return docs
}

func byID(t *testing.T, docs []Document, id string) Document {
	t.Helper()

	for _, d := range docs {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no document with id %q", id)
	return Document{}
}
