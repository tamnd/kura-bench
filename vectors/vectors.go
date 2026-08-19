// Package vectors reads the fvecs and ivecs files that vector search
// benchmarks have shipped in for twenty years.
//
// The format is as simple as it looks. A file is a sequence of records, each of
// which is a little endian int32 giving the number of components followed by
// that many float32 values, or int32 values for an ivecs file. There is no
// header, so the number of records is the file size divided by the size of the
// first record, which is also the check that the file is what it claims to be.
//
// This package deliberately does not download anything. Fetching is
// cmd/kura-vectors and reading is here, because the runners need the reading
// and have no business reaching for the network.
package vectors

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// Shape is what a file turns out to hold.
type Shape struct {
	// Dim is the number of components in each record, read from the first one.
	Dim int

	// Count is how many records the file holds.
	Count int

	// Bytes is the size of the file.
	Bytes int64
}

// ReadShape reads the first record header and derives the rest from the file
// size, without reading the body.
//
// A file whose size is not a whole number of records is refused rather than
// truncated. A vector file that is half downloaded reads perfectly well for the
// first few hundred megabytes and then produces a recall figure that is wrong
// for a reason nobody would ever guess.
func ReadShape(path string, elem int) (Shape, error) {
	f, err := os.Open(path)
	if err != nil {
		return Shape{}, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Shape{}, err
	}

	var dim int32
	if err := binary.Read(f, binary.LittleEndian, &dim); err != nil {
		return Shape{}, fmt.Errorf("%s: %w", path, err)
	}
	if dim <= 0 || dim > 1<<20 {
		return Shape{}, fmt.Errorf("%s: first record claims %d components, which is not a vector file", path, dim)
	}

	record := int64(4 + int(dim)*elem)
	if info.Size()%record != 0 {
		return Shape{}, fmt.Errorf("%s: %d bytes is not a whole number of %d component records, the file is truncated",
			path, info.Size(), dim)
	}
	return Shape{Dim: int(dim), Count: int(info.Size() / record), Bytes: info.Size()}, nil
}

// Fvecs reads a whole float vector file into one flat slice of Count*Dim
// values.
//
// It is one allocation because that is how every engine here wants the data:
// a base set of a million vectors is half a gigabyte and handing it over as a
// million small slices would measure the allocator.
func Fvecs(path string) (Shape, []float32, error) {
	shape, err := ReadShape(path, 4)
	if err != nil {
		return Shape{}, nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return Shape{}, nil, err
	}
	defer func() { _ = f.Close() }()

	out := make([]float32, shape.Count*shape.Dim)
	buf := make([]byte, 4+shape.Dim*4)
	for i := range shape.Count {
		if _, err := io.ReadFull(f, buf); err != nil {
			return Shape{}, nil, fmt.Errorf("%s record %d: %w", path, i, err)
		}
		if got := int(binary.LittleEndian.Uint32(buf)); got != shape.Dim {
			return Shape{}, nil, fmt.Errorf("%s record %d has %d components, the first had %d", path, i, got, shape.Dim)
		}
		row := out[i*shape.Dim : (i+1)*shape.Dim]
		for j := range row {
			row[j] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4+j*4:]))
		}
	}
	return shape, out, nil
}

// Ivecs reads a whole integer vector file, which is the shape ground truth
// comes in: one row per query, holding the identifiers of its true nearest
// neighbours in order.
func Ivecs(path string) (Shape, []int32, error) {
	shape, err := ReadShape(path, 4)
	if err != nil {
		return Shape{}, nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return Shape{}, nil, err
	}
	defer func() { _ = f.Close() }()

	out := make([]int32, shape.Count*shape.Dim)
	buf := make([]byte, 4+shape.Dim*4)
	for i := range shape.Count {
		if _, err := io.ReadFull(f, buf); err != nil {
			return Shape{}, nil, fmt.Errorf("%s record %d: %w", path, i, err)
		}
		row := out[i*shape.Dim : (i+1)*shape.Dim]
		for j := range row {
			row[j] = int32(binary.LittleEndian.Uint32(buf[4+j*4:]))
		}
	}
	return shape, out, nil
}

// WriteIvecs writes rows of identifiers in the ivecs format, which is how a
// ground truth this repository computed becomes a file that reads back exactly
// like the published one.
//
// It writes to a temporary name and renames, so that a run interrupted three
// hours into a scan leaves nothing behind that would read as ground truth.
func WriteIvecs(path string, dim int, data []int32) error {
	if dim <= 0 || len(data)%dim != 0 {
		return fmt.Errorf("%d values is not a whole number of %d wide rows", len(data), dim)
	}

	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	w := bufio.NewWriterSize(f, 1<<20)
	buf := make([]byte, 4)
	for i := 0; i < len(data); i += dim {
		binary.LittleEndian.PutUint32(buf, uint32(dim))
		if _, err := w.Write(buf); err != nil {
			return closeAndRemove(f, tmp, err)
		}
		for _, v := range data[i : i+dim] {
			binary.LittleEndian.PutUint32(buf, uint32(v))
			if _, err := w.Write(buf); err != nil {
				return closeAndRemove(f, tmp, err)
			}
		}
	}
	if err := w.Flush(); err != nil {
		return closeAndRemove(f, tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func closeAndRemove(f *os.File, path string, err error) error {
	_ = f.Close()
	_ = os.Remove(path)
	return err
}

// Recall is the fraction of the true nearest neighbours an engine found.
//
// Only the first k of each ground truth row counts, because an engine asked for
// ten results cannot be marked down for missing the eleventh. Ties at the
// boundary are not corrected for: two vectors at exactly the same distance make
// the ordering arbitrary and every engine here pays that the same way.
func Recall(got, want []int32, k, queries int) float64 {
	if k <= 0 || queries <= 0 || len(got) < queries*k {
		return 0
	}
	depth := len(want) / queries
	if depth < k {
		return 0
	}

	found := 0
	truth := make(map[int32]struct{}, k)
	for q := range queries {
		clear(truth)
		for _, id := range want[q*depth : q*depth+k] {
			truth[id] = struct{}{}
		}
		for _, id := range got[q*k : (q+1)*k] {
			if _, ok := truth[id]; ok {
				found++
			}
		}
	}
	return float64(found) / float64(queries*k)
}
