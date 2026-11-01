package load

import (
	"math"
	"testing"
	"time"
)

func TestValueAt(t *testing.T) {
	segs := segments(2, []Stage{{10 * time.Second, 12}, {5 * time.Second, 12}, {10 * time.Second, 0}})
	tests := []struct {
		at   time.Duration
		want float64
	}{
		{0, 2}, {5 * time.Second, 7}, {10 * time.Second, 12}, {12 * time.Second, 12},
		{20 * time.Second, 6}, {25 * time.Second, 0}, {time.Hour, 0},
	}
	for _, tt := range tests {
		if got := valueAt(segs, tt.at); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("valueAt(%s) = %g, want %g", tt.at, got, tt.want)
		}
	}
	if got := valueAt(segments(3, nil), time.Hour); got != 3 {
		t.Errorf("no stages: %g", got)
	}
}

func TestArrivals(t *testing.T) {
	near := func(t *testing.T, got, want time.Duration) {
		t.Helper()
		if d := got - want; d < -time.Microsecond || d > time.Microsecond {
			t.Errorf("got %s, want %s", got, want)
		}
	}
	t.Run("constant rate", func(t *testing.T) {
		next := arrivals(segments(4, nil))
		for k, want := range []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, 10 * time.Second} {
			kk := []int64{0, 1, 2, 40}[k]
			got, ok := next(kk)
			if !ok {
				t.Fatal("constant rate never ends")
			}
			near(t, got, want)
		}
	})
	t.Run("linear ramp up from 0", func(t *testing.T) {
		// 0 → 10/s over 10s: N(t) = t²/2, so iteration k at sqrt(2k).
		next := arrivals(segments(0, []Stage{{10 * time.Second, 10}}))
		for _, k := range []int64{0, 2, 8, 32, 50} {
			got, ok := next(k)
			if !ok {
				t.Fatalf("iteration %d not scheduled", k)
			}
			near(t, got, seconds(math.Sqrt(2*float64(k))))
		}
		if _, ok := next(51); ok {
			t.Fatal("only 50 iterations fit in the ramp")
		}
	})
	t.Run("plateau then ramp down", func(t *testing.T) {
		// 10/s for 2s (20 iterations), then 10 → 0 over 2s (10 more).
		next := arrivals(segments(10, []Stage{{2 * time.Second, 10}, {2 * time.Second, 0}}))
		got, _ := next(10)
		near(t, got, time.Second)
		got, _ = next(20)
		near(t, got, 2*time.Second)
		// In the ramp down: 10t - 2.5t² = 7 → t = (10 - √30) / 5.
		got, _ = next(27)
		near(t, got, 2*time.Second+seconds((10-math.Sqrt(30))/5))
		got, ok := next(30)
		if !ok {
			t.Fatal("last iteration missing")
		}
		near(t, got, 4*time.Second)
		if _, ok := next(31); ok {
			t.Fatal("past the end")
		}
	})
	t.Run("a zero-rate stage is a pause", func(t *testing.T) {
		next := arrivals(segments(0, []Stage{{time.Second, 0}, {time.Second, 4}, {time.Second, 4}}))
		got, _ := next(0)
		near(t, got, time.Second) // no arrivals during the pause; k=0 at its end
		got, _ = next(2)          // 2 iterations in the ramp: 2t² = 2 → t = 1s
		near(t, got, 2*time.Second)
		got, _ = next(4)
		near(t, got, 2500*time.Millisecond)
	})
	t.Run("monotonic", func(t *testing.T) {
		next := arrivals(segments(1, []Stage{{3 * time.Second, 50}, {time.Second, 50}, {3 * time.Second, 1}}))
		prev := time.Duration(-1)
		for k := int64(0); ; k++ {
			got, ok := next(k)
			if !ok {
				break
			}
			if got < prev || got > 7*time.Second {
				t.Fatalf("iteration %d at %s after %s", k, got, prev)
			}
			prev = got
		}
	})
}

func TestVUCount(t *testing.T) {
	tests := []struct {
		v    float64
		want int
	}{
		{0, 0}, {0.49, 0}, {0.5, 1}, {1, 1}, {1.49, 1}, {2.5, 3}, {9.6, 10},
	}
	for _, tt := range tests {
		if got := vuCount(tt.v); got != tt.want {
			t.Errorf("vuCount(%g) = %d, want %d", tt.v, got, tt.want)
		}
	}
}
