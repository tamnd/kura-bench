package graphs

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// The canonical edge file.
//
// The published graphs are gzipped tab separated text with a comment header,
// and parsing that is the same work for every engine. It is also, on the larger
// graphs, more work than some of the queries being measured. So it happens once
// and every runner reads this instead: a fixed header and then the edges as
// pairs of little endian uint32, in the order the publisher wrote them.
//
// This is the same decision the text suite made when it turned a directory of
// checkouts into one JSON lines file. Reading it is sequential, it fits in the
// page cache, and what is left after that is the engine.
const (
	magic  = "kuragrf1"
	header = 32

	// Undirected marks a graph the publisher stored with both directions of
	// every edge written out.
	Undirected = 1 << 0
)

// Header is what the front of an edge file says about it.
type Header struct {
	// Nodes is how many distinct identifiers appear, on either side of an edge.
	Nodes int

	// Edges is how many records follow.
	Edges int

	// MaxID is the largest identifier. A store that wants dense indexes can
	// allocate from this rather than making two passes, and the gap between it
	// and Nodes is worth knowing: web-Google has 875,713 nodes and identifiers
	// up to 916,428, so an array indexed by identifier wastes five percent.
	MaxID uint32

	// Flags carries [Undirected].
	Flags uint32
}

// WriteEdges writes the canonical file.
//
// The edges are handed over as pairs already, from and to alternating, because
// that is the layout they are read back in and turning it into a slice of
// structs on the way through would double the memory for nothing.
func WriteEdges(path string, h Header, edges []uint32) error {
	if len(edges) != h.Edges*2 {
		return fmt.Errorf("the header says %d edges and there are %d identifiers, which is not twice that", h.Edges, len(edges))
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<20)
	var head [header]byte
	copy(head[0:8], magic)
	binary.LittleEndian.PutUint64(head[8:16], uint64(h.Nodes))
	binary.LittleEndian.PutUint64(head[16:24], uint64(h.Edges))
	binary.LittleEndian.PutUint32(head[24:28], h.MaxID)
	binary.LittleEndian.PutUint32(head[28:32], h.Flags)
	if _, err := w.Write(head[:]); err != nil {
		return err
	}

	var buf [4]byte
	for _, v := range edges {
		binary.LittleEndian.PutUint32(buf[:], v)
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// ReadHeader reads the front of an edge file without reading the edges, which
// is what a check before a run needs.
func ReadHeader(path string) (Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, err
	}
	defer func() { _ = f.Close() }()

	var head [header]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return Header{}, fmt.Errorf("%s: %w", path, err)
	}
	if string(head[0:8]) != magic {
		return Header{}, fmt.Errorf("%s does not start with %q, so it is not an edge file", path, magic)
	}
	h := Header{
		Nodes: int(binary.LittleEndian.Uint64(head[8:16])),
		Edges: int(binary.LittleEndian.Uint64(head[16:24])),
		MaxID: binary.LittleEndian.Uint32(head[24:28]),
		Flags: binary.LittleEndian.Uint32(head[28:32]),
	}

	// A file that is short is the failure worth catching here. It happens when
	// a conversion was killed partway, and every figure taken from it afterwards
	// is slightly optimistic and completely useless.
	info, err := f.Stat()
	if err != nil {
		return Header{}, err
	}
	if want := int64(header) + int64(h.Edges)*8; info.Size() != want {
		return Header{}, fmt.Errorf("%s is %d bytes, a graph of %d edges is %d", path, info.Size(), h.Edges, want)
	}
	return h, nil
}

// ReadEdges reads the whole file.
func ReadEdges(path string) (Header, []uint32, error) {
	h, err := ReadHeader(path)
	if err != nil {
		return Header{}, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Header{}, nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(header, io.SeekStart); err != nil {
		return Header{}, nil, err
	}

	raw := make([]byte, h.Edges*8)
	if _, err := io.ReadFull(bufio.NewReaderSize(f, 1<<20), raw); err != nil {
		return Header{}, nil, fmt.Errorf("%s: %w", path, err)
	}
	edges := make([]uint32, h.Edges*2)
	for i := range edges {
		edges[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	return h, edges, nil
}

// WriteIDs writes a list of node identifiers, which is what the seed file is.
func WriteIDs(path string, ids []uint32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<16)
	var buf [4]byte
	for _, id := range ids {
		binary.LittleEndian.PutUint32(buf[:], id)
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// ReadIDs reads a list of node identifiers.
func ReadIDs(path string) ([]uint32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("%s is %d bytes, which is not a whole number of identifiers", path, len(raw))
	}
	ids := make([]uint32, len(raw)/4)
	for i := range ids {
		ids[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	return ids, nil
}
