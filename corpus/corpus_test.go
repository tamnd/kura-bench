package corpus

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestItTurnsATreeOfCheckoutsIntoACorpus(t *testing.T) {
	root := t.TempDir()
	write(t, root, "kernel/README.md", "# the kernel\n")
	write(t, root, "kernel/mm/slab.c", "void *kmalloc(size_t n);\n")
	write(t, root, "kernel/.git/config", "[core]\n")
	write(t, root, "kernel/node_modules/dep/index.js", "module.exports = 1\n")
	write(t, root, "kernel/logo.png", "\x89PNG\x00\x01binary")
	write(t, root, "llvm/docs/intro.rst", "LLVM is a compiler.\n")

	var buf bytes.Buffer
	stats, err := Write(root, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Documents != 3 {
		t.Fatalf("indexed %d documents, want the three that are neither generated nor binary", stats.Documents)
	}

	var ids []string
	read, err := Read(&buf, func(d Document) error {
		ids = append(ids, d.ID)
		if d.Repo == "" || d.Path == "" || d.Body == "" {
			t.Errorf("%s came back with an empty field", d.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if read.Documents != stats.Documents || read.Bytes != stats.Bytes {
		t.Fatalf("read back %+v, wrote %+v", read, stats)
	}

	got := strings.Join(ids, " ")
	for _, want := range []string{"kernel:README.md", "kernel:mm/slab.c", "llvm:docs/intro.rst"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is missing from %s", want, got)
		}
	}
}

// The extension filter and the directory filter have to agree with the ones the
// platform's own directory connector uses, or a number measured here describes
// a different pile of files than the product reads.
func TestItSkipsWhatIsNotWorthIndexing(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "vendor", "target", ".venv"} {
		if !skipDir(name) {
			t.Errorf("%s should be skipped", name)
		}
	}
	for _, name := range []string{"mm", "docs", "src"} {
		if skipDir(name) {
			t.Errorf("%s should not be skipped", name)
		}
	}
	for _, name := range []string{"a.go", "b.md", "OWNERS", "Makefile", "c.rs"} {
		if !include(name) {
			t.Errorf("%s should be included", name)
		}
	}
	for _, name := range []string{"a.png", "b.tar.gz", ".hidden.go", "c.exe"} {
		if include(name) {
			t.Errorf("%s should not be included", name)
		}
	}
}

func TestAFileLargerThanTheLimitIsLeftOut(t *testing.T) {
	root := t.TempDir()
	write(t, root, "repo/small.md", "small\n")
	write(t, root, "repo/generated.go", strings.Repeat("x", MaxDocumentSize+1))

	var buf bytes.Buffer
	stats, err := Write(root, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Documents != 1 {
		t.Fatalf("took %d documents, want only the small one", stats.Documents)
	}
}

// A runner asked for a fixed number of documents should not have to read the
// rest of a multi gigabyte file to find out it is done.
func TestStoppingEarlyIsNotAnError(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		write(t, root, "repo/"+name, "body of "+name+"\n")
	}

	var buf bytes.Buffer
	if _, err := Write(root, &buf); err != nil {
		t.Fatal(err)
	}

	var seen int
	stats, err := Read(&buf, func(Document) error {
		if seen == 2 {
			return ErrStop
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("stopping early returned %v", err)
	}
	if stats.Documents != 2 {
		t.Fatalf("counted %d documents, want the two that were accepted", stats.Documents)
	}
}

func TestAnEmptyRootIsAnErrorRatherThanAnEmptyCorpus(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Write(t.TempDir(), &buf); err == nil {
		t.Fatal("an empty root produced a corpus, which would later look like an engine that indexes instantly")
	}
	if _, err := Read(strings.NewReader(""), func(Document) error { return nil }); err == nil {
		t.Fatal("an empty corpus file was accepted")
	}
}

func TestACallbackErrorComesBack(t *testing.T) {
	root := t.TempDir()
	write(t, root, "repo/a.md", "body\n")

	var buf bytes.Buffer
	if _, err := Write(root, &buf); err != nil {
		t.Fatal(err)
	}

	want := errors.New("the engine said no")
	if _, err := Read(&buf, func(Document) error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v, want the callback's own error", err)
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
