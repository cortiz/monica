package config

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseRate(t *testing.T) {
	tests := []struct {
		in   string
		want float64
	}{
		{"100", 100}, {"100/s", 100}, {" 6000 / m ", 100}, {"90/h", 0.025}, {"1/500ms", 2}, {"2.5/s", 2.5}, {"3/2s", 1.5},
	}
	for _, tt := range tests {
		got, err := ParseRate(tt.in)
		if err != nil || math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("ParseRate(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "0", "-1/s", "fast", "10/x", "10/0s", "10/-1s", "/s"} {
		if _, err := ParseRate(bad); err == nil || !strings.Contains(err.Error(), "invalid rate") {
			t.Errorf("ParseRate(%q): err = %v", bad, err)
		}
	}
}

func TestParseStage(t *testing.T) {
	got, err := ParseStage(" 30s : 10 ")
	if err != nil || got != (Stage{Duration: dur(30 * time.Second), Target: 10}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := ParseStage("1m:0"); err != nil || got.Target != 0 {
		t.Fatalf("zero target: %+v, %v", got, err)
	}
	for _, bad := range []string{"30s", "10:30s", "0s:5", "-1s:5", "30s:-1", "30s:many", ":5"} {
		if _, err := ParseStage(bad); err == nil || !strings.Contains(err.Error(), "invalid stage") {
			t.Errorf("ParseStage(%q): err = %v", bad, err)
		}
	}
}

func TestLoadSettings(t *testing.T) {
	c, err := Load(writeFile(t, t.TempDir(), FileName, "version: 1\nload:\n  vus: 20\n  rate: 50/s\n  stages:\n    - {duration: 10s, target: 50}\n    - duration: 1m\n      target: 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := LoadSettings{VUs: 20, Rate: "50/s", Stages: []Stage{{dur(10 * time.Second), 50}, {dur(time.Minute), 0}}}
	if !reflect.DeepEqual(c.Load, want) {
		t.Fatalf("got %+v, want %+v", c.Load, want)
	}

	errs := []struct{ content, wantErr string }{
		{"load:\n  rate: lots\n", `load.rate: invalid rate "lots"`},
		{"load:\n  stages:\n    - {target: 5}\n", "load.stages[0]: missing duration"},
		{"load:\n  stages:\n    - {duration: 5s, target: 1}\n    - {duration: 5s, target: -1}\n", "load.stages[1]: target must not be negative"},
		{"load:\n  duration: 30s\n  stages:\n    - {duration: 5s, target: 1}\n", "set either duration or stages"},
		{"load:\n  stages:\n    - {duration: 5s, target: 1, ramp: x}\n", "field ramp not found"},
	}
	for _, tt := range errs {
		_, err := Load(writeFile(t, t.TempDir(), FileName, "version: 1\n"+tt.content))
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%q: err = %v, want %q", tt.content, err, tt.wantErr)
		}
	}
}

func TestLocalLoadDurationAndStagesReplaceEachOther(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, FileName, "version: 1\nload:\n  stages: [{duration: 5s, target: 1}]\n")
	writeFile(t, dir, LocalFileName, "load:\n  duration: 1m\n")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Load.Stages != nil || c.Load.Duration.Duration != time.Minute {
		t.Fatalf("duration should replace stages: %+v", c.Load)
	}

	dir = t.TempDir()
	path = writeFile(t, dir, FileName, "version: 1\nload:\n  duration: 1m\n  rate: 5/s\n")
	writeFile(t, dir, LocalFileName, "load:\n  stages: [{duration: 5s, target: 1}]\n  rate: 10/s\n")
	if c, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if c.Load.Duration.Duration != 0 || len(c.Load.Stages) != 1 || c.Load.Rate != "10/s" {
		t.Fatalf("stages should replace duration: %+v", c.Load)
	}
}
