package load

import (
	"math"
	"time"
)

// Stage is one step of a ramping test: the VU count (or, in rate mode, the
// iterations per second) moves linearly to Target over Duration.
type Stage struct {
	Duration time.Duration
	Target   float64
}

// segment is a stretch where a value moves linearly from a to c.
type segment struct {
	start, length time.Duration // length < 0: open-ended
	a, c          float64
}

// segments turns a start value and stages into linear segments; without
// stages, the value stays at start forever.
func segments(start float64, stages []Stage) []segment {
	if len(stages) == 0 {
		return []segment{{length: -1, a: start, c: start}}
	}
	out := make([]segment, len(stages))
	var at time.Duration
	prev := start
	for i, st := range stages {
		out[i] = segment{start: at, length: st.Duration, a: prev, c: st.Target}
		at += st.Duration
		prev = st.Target
	}
	return out
}

// valueAt is the ramped value at offset t (the last target after the end).
func valueAt(segs []segment, t time.Duration) float64 {
	for _, s := range segs {
		if s.length < 0 || t < s.start+s.length {
			if s.length <= 0 {
				return s.a
			}
			f := float64(t-s.start) / float64(s.length)
			return s.a + (s.c-s.a)*f
		}
	}
	return segs[len(segs)-1].c
}

// arrivals returns the start offset of iteration k (0-based) when
// iterations arrive at a rate (per second) ramped over segs: iteration k
// starts when the integral of the rate reaches k. ok is false once the
// stages end before iteration k is due.
func arrivals(segs []segment) func(k int64) (offset time.Duration, ok bool) {
	return func(k int64) (time.Duration, bool) {
		m := float64(k) // iterations still to place, counted from the segment start
		for _, s := range segs {
			a := s.a
			if s.length < 0 { // constant forever
				if a <= 0 {
					return 0, false
				}
				return s.start + seconds(m/a), true
			}
			d := s.length.Seconds()
			area := (s.a + s.c) / 2 * d
			if m > area || area == 0 { // a zero-rate stage is a pause
				m -= area
				continue
			}
			b := (s.c - s.a) / d // rate change per second
			var t float64
			if math.Abs(b) < 1e-12 {
				if a <= 0 {
					return 0, false
				}
				t = m / a
			} else {
				// a·t + b·t²/2 = m
				disc := a*a + 2*b*m
				if disc < 0 {
					disc = 0
				}
				t = (math.Sqrt(disc) - a) / b
			}
			if t > d {
				t = d
			}
			return s.start + seconds(t), true
		}
		return 0, false
	}
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// stagesDuration is the total length of stages.
func stagesDuration(stages []Stage) time.Duration {
	var d time.Duration
	for _, st := range stages {
		d += st.Duration
	}
	return d
}
