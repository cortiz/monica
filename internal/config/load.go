package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Stage is one step of a ramping load test: the VU count (or, with a rate,
// the iterations per second) moves linearly to Target over Duration.
type Stage struct {
	Duration Duration `yaml:"duration"`
	Target   float64  `yaml:"target"`
}

// ParseRate reads an arrival rate: "100" or "100/s" (per second), "6000/m",
// "90/h", or per any duration ("1/500ms"). It returns iterations per
// second.
func ParseRate(s string) (float64, error) {
	num, per, hasPer := strings.Cut(strings.TrimSpace(s), "/")
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid rate %q (expected a positive number of iterations such as 100/s or 6000/m)", s)
	}
	if !hasPer {
		return n, nil
	}
	per = strings.TrimSpace(per)
	var unit time.Duration
	switch per {
	case "s":
		unit = time.Second
	case "m":
		unit = time.Minute
	case "h":
		unit = time.Hour
	default:
		if unit, err = time.ParseDuration(per); err != nil || unit <= 0 {
			return 0, fmt.Errorf("invalid rate %q (expected a period such as /s, /m, /h or /500ms)", s)
		}
	}
	return n / unit.Seconds(), nil
}

// ParseStage reads a --stage flag, "<duration>:<target>" (e.g. "30s:10").
func ParseStage(s string) (Stage, error) {
	d, t, ok := strings.Cut(s, ":")
	dur, err := time.ParseDuration(strings.TrimSpace(d))
	if !ok || err != nil || dur <= 0 {
		return Stage{}, fmt.Errorf("invalid stage %q (expected duration:target, e.g. 30s:10)", s)
	}
	target, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || target < 0 {
		return Stage{}, fmt.Errorf("invalid stage %q (target must be a non-negative number)", s)
	}
	return Stage{Duration: Duration{dur}, Target: target}, nil
}

func (l LoadSettings) validate() error {
	if l.VUs < 0 {
		return fmt.Errorf("load.vus: must not be negative, got %d", l.VUs)
	}
	if l.Rate != "" {
		if _, err := ParseRate(l.Rate); err != nil {
			return fmt.Errorf("load.rate: %w", err)
		}
	}
	for i, st := range l.Stages {
		switch {
		case st.Duration.Duration <= 0:
			return fmt.Errorf("load.stages[%d]: missing duration", i)
		case st.Target < 0:
			return fmt.Errorf("load.stages[%d]: target must not be negative, got %g", i, st.Target)
		}
	}
	if len(l.Stages) > 0 && l.Duration.Duration > 0 {
		return errors.New("load: set either duration or stages (stages set the duration)")
	}
	return nil
}
