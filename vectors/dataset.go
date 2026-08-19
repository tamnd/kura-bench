package vectors

import (
	"fmt"
	"path/filepath"
	"sort"
)

// A Dataset is one of the published vector search datasets, described down to
// the byte so that two machines can prove they are running the same numbers.
//
// The sizes and checksums here are pinned. A vector benchmark is a comparison
// against a published recall figure, and a base file that is one vector short
// of the published one produces a recall that is close enough to look right and
// is not the same measurement.
type Dataset struct {
	// Name is what the flag takes and what the directory is called.
	Name string

	// Dim is the number of components in each vector.
	Dim int

	// Count is how many vectors are in the base set.
	Count int

	// Queries is how many query vectors there are.
	Queries int

	// Depth is how many true neighbours the ground truth lists per query,
	// which is the largest k that can be scored against it.
	Depth int

	// About is a sentence for the report, saying what the vectors are.
	About string

	// Archive is set when the files come out of one tarball rather than being
	// served individually.
	Archive *Archive

	// Files are the three files a run needs, in the order they are fetched.
	Files []File
}

// A File is one member of a dataset.
type File struct {
	// Name is what it is saved as, which is the same across every dataset so
	// that a runner takes the same three flags whichever one it is given.
	Name string

	// URL is where it is fetched from, empty when it comes out of an archive.
	URL string

	// Member is its path inside the archive, empty when it is fetched directly.
	Member string

	// Bytes is its exact size.
	Bytes int64

	// SHA256 is its checksum, empty for a member of an archive whose own
	// checksum is verified instead.
	SHA256 string
}

// An Archive is a single compressed download holding every file of a dataset.
type Archive struct {
	URL    string
	Bytes  int64
	SHA256 string
}

// Base, Query and GroundTruth are the names every dataset is saved under, so
// that a runner is handed three paths and never has to know which dataset it
// is looking at.
const (
	Base        = "base.fvecs"
	Query       = "query.fvecs"
	GroundTruth = "groundtruth.ivecs"
)

// Datasets are the ones that can be fetched.
//
// SIFT is the one every paper reports, which is what makes it worth running
// even though 128 components is small by the standards of anything trained
// this decade. GIST is here because 960 components is much closer to what a
// real embedding looks like, and several engines that are quick on SIFT are
// not quick on GIST. siftsmall is the same corpus cut to ten thousand vectors
// by the people who published it, which is what makes it usable as the check
// that runs on every pull request.
var Datasets = map[string]Dataset{
	"siftsmall": {
		Name:    "siftsmall",
		Dim:     128,
		Count:   10_000,
		Queries: 100,
		Depth:   100,
		About:   "Ten thousand 128 component SIFT descriptors from the TEXMEX corpus, with exact ground truth",
		// Five megabytes of real descriptors with real published ground truth,
		// which is the whole reason it is here. Continuous integration needs a
		// dataset small enough to fetch on every run, and the alternative to a
		// small real one is a generated one. A million random points have no
		// structure, every index finds them equally easily, and a recall figure
		// taken on them would say nothing about the engine.
		//
		// The archive is a mirror rather than the original, because the original
		// is served over FTP and this fetches over HTTP. It was checked against
		// the TEXMEX copy: all three members are byte for byte the same.
		Archive: &Archive{
			URL:    "https://huggingface.co/datasets/vecdata/siftsmall/resolve/main/siftsmall.tar.gz",
			Bytes:  5_304_531,
			SHA256: "987b526d24e749082ba27ee8068003836eb17a61b34f09b4db865750f8a43487",
		},
		Files: []File{
			{Name: Base, Member: "siftsmall/siftsmall_base.fvecs", Bytes: 5_160_000},
			{Name: Query, Member: "siftsmall/siftsmall_query.fvecs", Bytes: 51_600},
			{Name: GroundTruth, Member: "siftsmall/siftsmall_groundtruth.ivecs", Bytes: 40_400},
		},
	},
	"sift": {
		Name:    "sift",
		Dim:     128,
		Count:   1_000_000,
		Queries: 10_000,
		Depth:   100,
		About:   "One million 128 component SIFT descriptors from the TEXMEX corpus, with exact ground truth",
		Files: []File{
			{
				Name:   Base,
				URL:    "https://huggingface.co/datasets/qbo-odp/sift1m/resolve/main/sift_base.fvecs",
				Bytes:  516_000_000,
				SHA256: "21f66e2975057b5728ba56de1c825bac4f4d89d596609ae985741c6242631816",
			},
			{
				Name:   Query,
				URL:    "https://huggingface.co/datasets/qbo-odp/sift1m/resolve/main/sift_query.fvecs",
				Bytes:  5_160_000,
				SHA256: "f7fc9be140accdfd64116c2fa2365ecdb69b8f084970c6b0532db5ff79ac8fdc",
			},
			{
				Name:   GroundTruth,
				URL:    "https://huggingface.co/datasets/qbo-odp/sift1m/resolve/main/sift_groundtruth.ivecs",
				Bytes:  4_040_000,
				SHA256: "2b71de0a8d5a83e6a84eec3e23fb8b611d8801dd9b3a6cd62f070ab65ea65f4f",
			},
		},
	},
	"gist": {
		Name:    "gist",
		Dim:     960,
		Count:   1_000_000,
		Queries: 1_000,
		Depth:   100,
		About:   "One million 960 component GIST descriptors from the TEXMEX corpus, with exact ground truth",
		Archive: &Archive{
			URL:    "https://huggingface.co/datasets/fzliu/gist1m/resolve/main/gist.tar.gz",
			Bytes:  2_740_172_684,
			SHA256: "01469a7f1c3768853525e543d537e2dfa1adece927616405e360952e3f67df73",
		},
		Files: []File{
			{Name: Base, Member: "gist/gist_base.fvecs", Bytes: 3_844_000_000},
			{Name: Query, Member: "gist/gist_query.fvecs", Bytes: 3_844_000},
			{Name: GroundTruth, Member: "gist/gist_groundtruth.ivecs", Bytes: 404_000},
		},
	},
}

// Names lists the datasets in a stable order, for a flag's help text and for
// anything that prints them.
func Names() []string {
	out := make([]string, 0, len(Datasets))
	for name := range Datasets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Lookup finds a dataset by name and says what the choices were when it does
// not exist, since a typo in a flag should not turn into a download of nothing.
func Lookup(name string) (Dataset, error) {
	d, ok := Datasets[name]
	if !ok {
		return Dataset{}, fmt.Errorf("no dataset called %q, there is %v", name, Names())
	}
	return d, nil
}

// Dir is where a dataset's files live under a root directory.
func (d Dataset) Dir(root string) string { return filepath.Join(root, d.Name) }

// Path is where one of a dataset's files lives, named by [Base], [Query] or
// [GroundTruth].
func (d Dataset) Path(root, name string) string { return filepath.Join(d.Dir(root), name) }

// Bytes is what the whole dataset weighs once it is unpacked, which is worth
// printing before starting on a machine with seventeen gigabytes free.
func (d Dataset) Bytes() int64 {
	var n int64
	for _, f := range d.Files {
		n += f.Bytes
	}
	return n
}

// Verify checks that a dataset on disk is the one it claims to be, by reading
// the shape of each file rather than rehashing half a gigabyte.
//
// The checksum is the download's job and happens once. This is the check a run
// makes every time it starts, because the failure it catches is a base file
// that was truncated by a full disk months ago, and it costs three reads.
func (d Dataset) Verify(root string) error {
	for _, f := range d.Files {
		path := d.Path(root, f.Name)
		shape, err := ReadShape(path, 4)
		if err != nil {
			return err
		}
		if shape.Bytes != f.Bytes {
			return fmt.Errorf("%s is %d bytes, the %s dataset says %d", path, shape.Bytes, d.Name, f.Bytes)
		}
		want, count := d.Dim, d.Count
		switch f.Name {
		case Query:
			count = d.Queries
		case GroundTruth:
			want, count = d.Depth, d.Queries
		}
		if shape.Dim != want || shape.Count != count {
			return fmt.Errorf("%s holds %d rows of %d, the %s dataset says %d of %d",
				path, shape.Count, shape.Dim, d.Name, count, want)
		}
	}
	return nil
}
