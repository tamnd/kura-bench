package vectors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func writeFvecs(t *testing.T, dir, name string, dim int, rows [][]float32) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var b []byte
	for _, r := range rows {
		b = binary.LittleEndian.AppendUint32(b, uint32(dim))
		for _, v := range r {
			b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
		}
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeIvecs(t *testing.T, dir, name string, dim int, rows [][]int32) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var b []byte
	for _, r := range rows {
		b = binary.LittleEndian.AppendUint32(b, uint32(dim))
		for _, v := range r {
			b = binary.LittleEndian.AppendUint32(b, uint32(v))
		}
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestItReadsAFloatVectorFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFvecs(t, dir, "base.fvecs", 3, [][]float32{
		{1, 2, 3},
		{4, 5, 6},
	})

	shape, data, err := Fvecs(path)
	if err != nil {
		t.Fatal(err)
	}
	if shape.Dim != 3 || shape.Count != 2 {
		t.Fatalf("read %d vectors of %d components, want 2 of 3", shape.Count, shape.Dim)
	}
	want := []float32{1, 2, 3, 4, 5, 6}
	for i := range want {
		if data[i] != want[i] {
			t.Fatalf("component %d is %v, want %v", i, data[i], want[i])
		}
	}
}

func TestItReadsAnIntegerVectorFile(t *testing.T) {
	dir := t.TempDir()
	path := writeIvecs(t, dir, "gt.ivecs", 2, [][]int32{{7, 8}, {9, 10}})

	shape, data, err := Ivecs(path)
	if err != nil {
		t.Fatal(err)
	}
	if shape.Dim != 2 || shape.Count != 2 {
		t.Fatalf("read %d rows of %d, want 2 of 2", shape.Count, shape.Dim)
	}
	if data[0] != 7 || data[3] != 10 {
		t.Fatalf("read %v, want 7 8 9 10", data)
	}
}

// A file that stopped halfway through a download reads perfectly for its first
// few hundred megabytes and then produces a recall number that is wrong for a
// reason nobody would guess, so a partial file is refused up front.
func TestAHalfDownloadedFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeFvecs(t, dir, "base.fvecs", 3, [][]float32{{1, 2, 3}, {4, 5, 6}})

	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, full[:len(full)-4], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadShape(path, 4); err == nil {
		t.Fatal("a truncated file was accepted")
	}
}

func TestSomethingThatIsNotAVectorFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(path, []byte("this is not a vector file at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadShape(path, 4); err == nil {
		t.Fatal("a text file was accepted as a vector file")
	}
}

func TestRecallCountsOnlyTheFirstKOfEachGroundTruthRow(t *testing.T) {
	// Two queries, ground truth five deep, asked for the top two. The first
	// query got both right, the second got one right and one wrong, and the
	// wrong one is a true neighbour that sits outside the top two, which does
	// not count.
	want := []int32{1, 2, 3, 4, 5, 10, 20, 30, 40, 50}
	got := []int32{1, 2, 10, 30}

	if r := Recall(got, want, 2, 2); r != 0.75 {
		t.Fatalf("recall is %v, want 0.75", r)
	}
}

func TestPerfectAndEmptyRecall(t *testing.T) {
	want := []int32{1, 2, 3, 4}
	if r := Recall([]int32{1, 2, 3, 4}, want, 2, 2); r != 1 {
		t.Fatalf("an engine that found everything scored %v", r)
	}
	if r := Recall([]int32{9, 9, 9, 9}, want, 2, 2); r != 0 {
		t.Fatalf("an engine that found nothing scored %v", r)
	}
	if r := Recall(nil, want, 2, 2); r != 0 {
		t.Fatalf("an engine that returned nothing scored %v", r)
	}
}
