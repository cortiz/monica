package load

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

func TestBuckets(t *testing.T) {
	prev := -1
	for _, v := range []uint64{0, 1, 63, 64, 65, 127, 128, 129, 1000, 1 << 20, 1<<40 + 12345} {
		i := bucketOf(v)
		lo, hi := bucketRange(i)
		if v < lo || v > hi {
			t.Fatalf("value %d in bucket %d = [%d, %d]", v, i, lo, hi)
		}
		if width := hi - lo + 1; v >= subCount && float64(width)/float64(lo) > 1.0/subCount+1e-12 {
			t.Fatalf("bucket %d [%d, %d] wider than %.4f of its value", i, lo, hi, 1.0/subCount)
		}
		if i < prev {
			t.Fatalf("buckets not monotonic at %d", v)
		}
		prev = i
	}
	// Every value maps to the bucket whose range holds it, without gaps.
	for v := uint64(0); v < 5000; v++ {
		lo, hi := bucketRange(bucketOf(v))
		if v < lo || v > hi {
			t.Fatalf("value %d outside [%d, %d]", v, lo, hi)
		}
		if v > 0 {
			if _, prevHi := bucketRange(bucketOf(v - 1)); bucketOf(v) != bucketOf(v-1) && prevHi != v-1 {
				t.Fatalf("gap before %d", v)
			}
		}
	}
}

func TestHistogramQuantiles(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var h Histogram
	var values []time.Duration
	for i := 0; i < 20000; i++ {
		// Log-normal-ish latencies from ~100µs to seconds.
		d := time.Duration(rng.ExpFloat64()*float64(20*time.Millisecond)) + 100*time.Microsecond
		d = d.Truncate(time.Microsecond)
		values = append(values, d)
		h.Record(d)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	exact := func(q float64) time.Duration {
		rank := int(q*float64(len(values)) + 0.999999)
		if rank < 1 {
			rank = 1
		}
		return values[rank-1]
	}
	if h.Count() != 20000 || h.Min() != values[0] || h.Max() != values[len(values)-1] {
		t.Fatalf("count/min/max = %d/%s/%s", h.Count(), h.Min(), h.Max())
	}
	var sum time.Duration
	for _, v := range values {
		sum += v
	}
	if mean := sum / time.Duration(len(values)); absDiff(h.Mean(), mean) > time.Microsecond {
		t.Fatalf("mean %s, want %s", h.Mean(), mean)
	}
	for _, q := range []float64{0, 0.5, 0.9, 0.95, 0.99, 0.999, 1} {
		got, want := h.Quantile(q), exact(q)
		if float64(absDiff(got, want)) > float64(want)/subCount+float64(time.Microsecond) {
			t.Errorf("p%g = %s, want %s ±1.6%%", q*100, got, want)
		}
	}
}

func TestHistogramSmallAndEmpty(t *testing.T) {
	var h Histogram
	if h.Quantile(0.5) != 0 || h.Mean() != 0 || h.Min() != 0 || h.Max() != 0 {
		t.Fatal("empty histogram should report zeros")
	}
	for _, ms := range []int{1, 2, 3, 4, 100} {
		h.Record(time.Duration(ms) * time.Millisecond)
	}
	h.Record(-time.Second) // counts as 0
	tests := []struct {
		q    float64
		want time.Duration
	}{
		{0, 0}, {0.5, 2 * time.Millisecond}, {0.8, 4 * time.Millisecond}, {0.84, 100 * time.Millisecond}, {1, 100 * time.Millisecond},
	}
	for _, tt := range tests {
		if got := h.Quantile(tt.q); float64(absDiff(got, tt.want)) > float64(tt.want)/subCount {
			t.Errorf("p%g = %s, want %s", tt.q*100, got, tt.want)
		}
	}
	if h.Min() != 0 || h.Max() != 100*time.Millisecond || h.Count() != 6 {
		t.Fatalf("min/max/count = %s/%s/%d", h.Min(), h.Max(), h.Count())
	}
}

func TestHistogramMerge(t *testing.T) {
	var a, b, all Histogram
	for i := 1; i <= 1000; i++ {
		d := time.Duration(i*i) * time.Microsecond
		all.Record(d)
		if i%2 == 0 {
			a.Record(d)
		} else {
			b.Record(d)
		}
	}
	var merged Histogram
	merged.Merge(&a)
	merged.Merge(&b)
	merged.Merge(&Histogram{})
	for _, q := range []float64{0, 0.5, 0.99, 1} {
		if merged.Quantile(q) != all.Quantile(q) {
			t.Errorf("p%g: merged %s, direct %s", q*100, merged.Quantile(q), all.Quantile(q))
		}
	}
	if merged.Count() != all.Count() || merged.Mean() != all.Mean() || merged.Min() != all.Min() || merged.Max() != all.Max() {
		t.Fatal("merged stats differ")
	}
}

func absDiff(a, b time.Duration) time.Duration {
	if a > b {
		return a - b
	}
	return b - a
}
