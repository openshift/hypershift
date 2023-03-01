// Package histogram provides an histogram interface and a Wavefront histogram implementation (https://docs.wavefront.com/proxies_histograms.html).
package histogram

import (
	"math"
	"sync"
	"time"

	tdigest "github.com/caio/go-tdigest"
)

// Histogram a quantile approximation data structure
type Histogram interface {
	Update(v float64)
	Distributions() []Distribution
	Snapshot() []Distribution
	Count() uint64
	Quantile(q float64) float64
	Max() float64
	Min() float64
	Sum() float64
	Mean() float64
	Granularity() Granularity
}

// Option allows histogram customization
type Option func(*histogramImpl)

// GranularityOption of the histogram
func GranularityOption(g Granularity) Option {
	return func(args *histogramImpl) {
		args.granularity = g
	}
}

// Compression of the histogram
func Compression(c float64) Option {
	return func(args *histogramImpl) {
		args.compression = c
	}
}

// MaxBins of the histogram
func MaxBins(c int) Option {
	return func(args *histogramImpl) {
		args.maxBins = c
	}
}

// TimeSupplier for the histogram time computations
func TimeSupplier(supplier func() time.Time) Option {
	return func(args *histogramImpl) {
		args.timeSupplier = supplier
	}
}

func defaultHistogramImpl() *histogramImpl {
	return &histogramImpl{
		maxBins:      10,
		granularity:  MINUTE,
		compression:  3.2,
		timeSupplier: time.Now,
	}
}

// New creates a new Wavefront histogram
func New(setters ...Option) Histogram {
	h := defaultHistogramImpl()
	for _, setter := range setters {
		setter(h)
	}
	return h
}

type histogramImpl struct {
	mutex              sync.Mutex
	priorTimedBinsList []*timedBin
	currentTimedBin    *timedBin

	granularity  Granularity
	compression  float64
	maxBins      int
	timeSupplier func() time.Time
}

type timedBin struct {
	tdigest   *tdigest.TDigest
	timestamp time.Time
}

// Distribution holds the samples and its timestamp.
type Distribution struct {
	Centroids []Centroid
	Timestamp time.Time
}

// Update registers a new sample in the histogram.
func (h *histogramImpl) Update(v float64) {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.currentTimedBin.tdigest.Add(v)
}

// Count returns the total number of samples on this histogram.
func (h *histogramImpl) Count() uint64 {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	res := uint64(0)
	for _, bin := range h.priorTimedBinsList {
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			res++
			return true
		})
	}
	return res
}

// Quantile returns the desired percentile estimation.
func (h *histogramImpl) Quantile(q float64) float64 {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	tempTdigest, _ := tdigest.New()
	for _, bin := range h.priorTimedBinsList {
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			tempTdigest.Add(mean)
			return true
		})
	}

	return tempTdigest.Quantile(q)
}

// Max returns the maximum value of samples on this histogram.
func (h *histogramImpl) Max() float64 {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	if len(h.priorTimedBinsList) == 0 {
		return math.NaN()
	}
	max := math.SmallestNonzeroFloat64
	for _, bin := range h.priorTimedBinsList {
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			max = math.Max(max, mean)
			return true
		})
	}
	return max
}

// Min returns the minimum value of samples on this histogram.
func (h *histogramImpl) Min() float64 {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	if len(h.priorTimedBinsList) == 0 {
		return math.NaN()
	}
	min := math.MaxFloat64
	for _, bin := range h.priorTimedBinsList {
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			min = math.Min(min, mean)
			return true
		})
	}
	return min
}

// Sum returns the sum of all values on this histogram.
func (h *histogramImpl) Sum() float64 {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	sum := float64(0)
	for _, bin := range h.priorTimedBinsList {
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			sum += mean * float64(count)
			return true
		})
	}
	return sum
}

// Mean returns the mean values of samples on this histogram.
func (h *histogramImpl) Mean() float64 {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	if len(h.priorTimedBinsList) == 0 {
		return math.NaN()
	}
	t := float64(0)
	c := uint64(0)
	for _, bin := range h.priorTimedBinsList {
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			t += mean * float64(count)
			c += count
			return true
		})
	}
	return t / float64(c)
}

// Granularity value
func (h *histogramImpl) Granularity() Granularity {
	return h.granularity
}

// Snapshot returns a copy of all samples on completed time slices
func (h *histogramImpl) Snapshot() []Distribution {
	return h.distributions(false)
}

// Distributions returns all samples on completed time slices, and clears the histogram
func (h *histogramImpl) Distributions() []Distribution {
	return h.distributions(true)
}

func (h *histogramImpl) distributions(clean bool) []Distribution {
	h.rotateCurrentTDigestIfNeedIt()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	distributions := make([]Distribution, len(h.priorTimedBinsList))
	for idx, bin := range h.priorTimedBinsList {
		var centroids []Centroid
		bin.tdigest.ForEachCentroid(func(mean float64, count uint64) bool {
			centroids = append(centroids, Centroid{Value: mean, Count: int(count)})
			return true
		})
		distributions[idx] = Distribution{Timestamp: bin.timestamp, Centroids: centroids}
	}
	if clean {
		h.priorTimedBinsList = h.priorTimedBinsList[:0]
	}
	return distributions
}

func (h *histogramImpl) rotateCurrentTDigestIfNeedIt() {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.currentTimedBin == nil {
		h.currentTimedBin = h.newTimedBin()
	} else if h.currentTimedBin.timestamp != h.now() {
		h.priorTimedBinsList = append(h.priorTimedBinsList, h.currentTimedBin)
		if len(h.priorTimedBinsList) > h.maxBins {
			h.priorTimedBinsList = h.priorTimedBinsList[1:]
		}
		h.currentTimedBin = h.newTimedBin()
	}
}

func (h *histogramImpl) now() time.Time {
	return h.timeSupplier().Truncate(h.granularity.Duration())
}

func (h *histogramImpl) newTimedBin() *timedBin {
	td, _ := tdigest.New(tdigest.Compression(h.compression))
	return &timedBin{timestamp: h.now(), tdigest: td}
}
