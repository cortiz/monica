package load

import (
	"math"
	"math/bits"
	"time"
)

// subBits sets the histogram's precision: 2^subBits buckets per power of
// two, so a recorded value is off by less than 1/2^subBits (1.6%).
const subBits = 6

const subCount = 1 << subBits

// Histogram records durations (in microseconds) in log-linear buckets, like
// an HDR histogram: memory grows with the range of values (about 64
// buckets per power of two), not with the number recorded. Min, max, sum
// and count are exact; quantiles are within 1.6%. Not safe for concurrent
// use.
type Histogram struct {
	counts   []uint64
	n        uint64
	sum      float64 // microseconds
	min, max uint64
}

// bucketOf maps v to its bucket: values below subCount have their own;
// above, v's top subBits+1 bits pick one of subCount buckets in its power
// of two.
func bucketOf(v uint64) int {
	if v < subCount {
		return int(v)
	}
	shift := bits.Len64(v) - 1 - subBits
	return (shift+1)*subCount + int(v>>uint(shift)) - subCount
}

// bucketRange is the inclusive range of values in bucket i.
func bucketRange(i int) (lo, hi uint64) {
	if i < subCount {
		return uint64(i), uint64(i)
	}
	shift := uint(i/subCount - 1)
	m := uint64(i%subCount + subCount)
	return m << shift, (m+1)<<shift - 1
}

// Record adds one duration; negative durations count as zero.
func (h *Histogram) Record(d time.Duration) {
	v := uint64(0)
	if d > 0 {
		v = uint64(d / time.Microsecond)
	}
	i := bucketOf(v)
	if i >= len(h.counts) {
		grown := make([]uint64, i+1)
		copy(grown, h.counts)
		h.counts = grown
	}
	h.counts[i]++
	if h.n == 0 || v < h.min {
		h.min = v
	}
	if v > h.max {
		h.max = v
	}
	h.n++
	h.sum += float64(v)
}

// Merge adds o's values to h.
func (h *Histogram) Merge(o *Histogram) {
	if o.n == 0 {
		return
	}
	if len(o.counts) > len(h.counts) {
		grown := make([]uint64, len(o.counts))
		copy(grown, h.counts)
		h.counts = grown
	}
	for i, c := range o.counts {
		h.counts[i] += c
	}
	if h.n == 0 || o.min < h.min {
		h.min = o.min
	}
	if o.max > h.max {
		h.max = o.max
	}
	h.n += o.n
	h.sum += o.sum
}

// Count is the number of recorded values.
func (h *Histogram) Count() uint64 { return h.n }

// Min, Max and Mean are exact (to the microsecond); 0 when empty.
func (h *Histogram) Min() time.Duration { return us(h.min) }
func (h *Histogram) Max() time.Duration { return us(h.max) }
func (h *Histogram) Mean() time.Duration {
	if h.n == 0 {
		return 0
	}
	return time.Duration(math.Round(h.sum/float64(h.n))) * time.Microsecond
}

// Quantile returns the value below which a fraction q (0..1) of the
// recorded values fall (nearest rank), within the bucket precision; 0 when
// empty.
func (h *Histogram) Quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(h.n)))
	if rank < 1 {
		rank = 1
	}
	var seen uint64
	for i, c := range h.counts {
		seen += c
		if seen >= rank {
			lo, hi := bucketRange(i)
			v := lo + (hi-lo)/2
			if v < h.min {
				v = h.min
			}
			if v > h.max {
				v = h.max
			}
			return us(v)
		}
	}
	return us(h.max)
}

func us(v uint64) time.Duration { return time.Duration(v) * time.Microsecond }
