package vectors

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
)

// A Metric is what "nearest" means.
//
// It has to be a first class thing here because the engines disagree about it
// and the disagreement is invisible in the numbers. A quantizing index built
// for maximum inner product, scored against Euclidean ground truth, reports a
// recall of about a tenth and looks like a bad index. It is not a bad index,
// it is answering a different question. Every result carries the metric it was
// measured under and engines are only ever compared within one.
type Metric string

const (
	// Euclidean is the straight line distance, and the one the published SIFT
	// and GIST ground truth is computed under.
	Euclidean Metric = "euclidean"

	// Cosine is the angle, ignoring how long the vectors are. This is what
	// almost every text embedding model is trained for.
	Cosine Metric = "cosine"

	// InnerProduct is the dot product, which is cosine with the lengths left
	// in. It is what a recommender ranks by and what several quantizing
	// indexes are built for, and its answers are not the cosine answers.
	InnerProduct Metric = "inner-product"
)

// Metrics lists them in a stable order.
func Metrics() []Metric { return []Metric{Euclidean, Cosine, InnerProduct} }

// ParseMetric turns a flag value into a metric.
func ParseMetric(s string) (Metric, error) {
	for _, m := range Metrics() {
		if Metric(s) == m {
			return m, nil
		}
	}
	return "", fmt.Errorf("no metric called %q, there is %v", s, Metrics())
}

// GroundTruth is the file the true neighbours for this metric live in.
//
// Euclidean gets the published file, which is the one worth trusting because
// somebody else computed it. The other two are computed here and named after
// themselves, so that a directory holding all three is unambiguous and a run
// can never pick up the wrong one.
func (m Metric) GroundTruth() string {
	if m == Euclidean {
		return GroundTruth
	}
	return "groundtruth-" + string(m) + ".ivecs"
}

// Published says whether the ground truth for this metric came with the
// dataset rather than being worked out here.
//
// It matters for reading a report. Against published ground truth, an exact
// scan scoring one is a real check on the whole suite. Against ground truth
// this repository computed with the same scan, it is a tautology, and the
// report says so rather than letting a row of ones look like evidence.
func (m Metric) Published() bool { return m == Euclidean }

// GroundTruthPath is where the true neighbours for a metric live.
func (d Dataset) GroundTruthPath(root string, m Metric) string {
	return d.Path(root, m.GroundTruth())
}

// VerifyGroundTruth checks that the ground truth for a metric is present and
// has the shape the dataset says it should.
func (d Dataset) VerifyGroundTruth(root string, m Metric) error {
	path := d.GroundTruthPath(root, m)
	shape, err := ReadShape(path, 4)
	if err != nil {
		return err
	}
	if shape.Count != d.Queries || shape.Dim != d.Depth {
		return fmt.Errorf("%s holds %d rows of %d, the %s dataset says %d of %d",
			path, shape.Count, shape.Dim, d.Name, d.Queries, d.Depth)
	}
	return nil
}

// ExactTopK is the true nearest neighbours of every query under a metric.
//
// This is a full scan, on every core, and it is slow on purpose. It is what
// produces the ground truth for the metrics that did not come with any, and a
// ground truth built by anything approximate would put a ceiling on every
// recall figure measured against it without saying so.
//
// The result is depth identifiers per query, laid out one query after another,
// which is the layout an ivecs file already has.
func ExactTopK(base []float32, dim int, queries []float32, depth int, m Metric, progress func(done, total int)) ([]int32, error) {
	if dim <= 0 || depth <= 0 {
		return nil, fmt.Errorf("dim %d and depth %d both have to be positive", dim, depth)
	}
	if len(base)%dim != 0 || len(queries)%dim != 0 {
		return nil, fmt.Errorf("the vectors are not a whole number of %d component rows", dim)
	}
	count := len(base) / dim
	nq := len(queries) / dim
	if count < depth {
		return nil, fmt.Errorf("cannot take %d neighbours out of %d vectors", depth, count)
	}

	// Cosine divides by the length of each base vector on every comparison, so
	// the lengths are computed once. It is the difference between one pass over
	// the base set and one pass per query.
	var norms []float32
	if m == Cosine {
		norms = make([]float32, count)
		for i := range count {
			row := base[i*dim : (i+1)*dim]
			var s float64
			for _, v := range row {
				s += float64(v) * float64(v)
			}
			norms[i] = float32(math.Sqrt(s))
			if norms[i] == 0 {
				norms[i] = 1
			}
		}
	}

	out := make([]int32, nq*depth)
	var (
		wg   sync.WaitGroup
		next = make(chan int, nq)
		done int
		mu   sync.Mutex
	)
	for q := range nq {
		next <- q
	}
	close(next)

	workers := runtime.NumCPU()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			best := make([]pair, 0, depth+1)
			for q := range next {
				query := queries[q*dim : (q+1)*dim]
				best = topK(best[:0], base, norms, dim, count, query, depth, m)
				for i, p := range best {
					out[q*depth+i] = p.id
				}
				if progress != nil {
					mu.Lock()
					done++
					progress(done, nq)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return out, nil
}

type pair struct {
	score float32
	id    int32
}

// topK keeps the best depth candidates for one query, worst last.
//
// Every metric is turned into something smaller-is-better so that one
// selection loop serves all three. Ties are broken by the lower identifier,
// which is arbitrary and is at least the same arbitrary choice every time.
func topK(best []pair, base []float32, norms []float32, dim, count int, query []float32, depth int, m Metric) []pair {
	var qnorm float32 = 1
	if m == Cosine {
		var s float64
		for _, v := range query {
			s += float64(v) * float64(v)
		}
		qnorm = float32(math.Sqrt(s))
		if qnorm == 0 {
			qnorm = 1
		}
	}

	for i := range count {
		row := base[i*dim : (i+1)*dim]
		var s float32
		switch m {
		case Euclidean:
			for j, v := range row {
				d := query[j] - v
				s += d * d
			}
		case InnerProduct:
			for j, v := range row {
				s += query[j] * v
			}
			s = -s
		case Cosine:
			for j, v := range row {
				s += query[j] * v
			}
			s = -s / (qnorm * norms[i])
		}

		if len(best) == depth && s >= best[depth-1].score {
			continue
		}
		at := sort.Search(len(best), func(k int) bool { return best[k].score > s })
		best = append(best, pair{})
		copy(best[at+1:], best[at:])
		best[at] = pair{score: s, id: int32(i)}
		if len(best) > depth {
			best = best[:depth]
		}
	}
	return best
}
