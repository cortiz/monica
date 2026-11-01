package load

import (
	"encoding/json"
	"time"
)

// jsonReport is the --out JSON document. Durations are milliseconds.
type jsonReport struct {
	Config     jsonConfig      `json:"config"`
	Aborted    bool            `json:"aborted"` // stopped early (Ctrl-C)
	DurationMS float64         `json:"durationMs"`
	VUs        int             `json:"vus"` // started
	Iterations int64           `json:"iterations"`
	Interrupt  int64           `json:"interrupted"`
	Dropped    int64           `json:"dropped"`
	Total      jsonStat        `json:"total"`
	Requests   []jsonStat      `json:"requests"`
	Thresholds []jsonThreshold `json:"thresholds"`
	Passed     bool            `json:"passed"` // every threshold passed
}

type jsonConfig struct {
	VUs        int         `json:"vus"`
	Rate       float64     `json:"rate,omitempty"` // iterations per second
	Stages     []jsonStage `json:"stages,omitempty"`
	DurationMS float64     `json:"durationMs,omitempty"`
	Iterations int         `json:"iterations,omitempty"`
}

type jsonStage struct {
	DurationMS float64 `json:"durationMs"`
	Target     float64 `json:"target"`
}

type jsonStat struct {
	Name          string      `json:"name"`
	File          string      `json:"file,omitempty"`
	Line          int         `json:"line,omitempty"`
	Requests      uint64      `json:"requests"`
	Failed        uint64      `json:"failed"`
	FailRate      float64     `json:"failRate"`
	RPS           float64     `json:"rps"`
	BytesSent     int64       `json:"bytesSent"`
	BytesReceived int64       `json:"bytesReceived"`
	Latency       jsonLatency `json:"latency"`
	Phases        jsonPhases  `json:"phases"`
}

type jsonLatency struct {
	Count uint64  `json:"count"` // requests that got a response
	Min   float64 `json:"min"`
	Avg   float64 `json:"avg"`
	Med   float64 `json:"med"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

type jsonPhase struct {
	Avg float64 `json:"avg"`
	P95 float64 `json:"p95"`
}

type jsonPhases struct {
	DNS       jsonPhase `json:"dns"`
	Connect   jsonPhase `json:"connect"`
	TLS       jsonPhase `json:"tls"`
	Waiting   jsonPhase `json:"waiting"`
	Receiving jsonPhase `json:"receiving"`
}

type jsonThreshold struct {
	Threshold string `json:"threshold"`
	Passed    bool   `json:"passed"`
	Actual    string `json:"actual"`
}

// JSON renders the result, the options it ran with and the checked
// thresholds as an indented JSON document. aborted marks a test stopped
// early.
func (r Result) JSON(opts Options, thresholds []ThresholdResult, aborted bool) ([]byte, error) {
	doc := jsonReport{
		Config: jsonConfig{
			VUs: opts.VUs, Rate: opts.Rate, DurationMS: ms(opts.Duration), Iterations: opts.Iterations,
		},
		Aborted:    aborted,
		DurationMS: ms(r.Duration),
		VUs:        r.VUs,
		Iterations: r.Iterations,
		Interrupt:  r.Interrupted,
		Dropped:    r.Dropped,
		Total:      statJSON(r.Total, r.Duration),
		Requests:   make([]jsonStat, 0, len(r.Stats)),
		Thresholds: make([]jsonThreshold, 0, len(thresholds)),
		Passed:     Failed(thresholds) == 0,
	}
	for _, st := range opts.Stages {
		doc.Config.Stages = append(doc.Config.Stages, jsonStage{DurationMS: ms(st.Duration), Target: st.Target})
	}
	for _, s := range r.Stats {
		doc.Requests = append(doc.Requests, statJSON(s, r.Duration))
	}
	for _, t := range thresholds {
		doc.Thresholds = append(doc.Thresholds, jsonThreshold{Threshold: t.Source, Passed: t.Passed, Actual: t.Actual})
	}
	return json.MarshalIndent(doc, "", "  ")
}

func statJSON(s Stat, d time.Duration) jsonStat {
	h := &s.Duration
	out := jsonStat{
		Name: s.Name, File: s.File, Line: s.Line,
		Requests: s.Requests, Failed: s.Failed, FailRate: s.FailRate(),
		BytesSent: s.BytesSent, BytesReceived: s.BytesReceived,
		Latency: jsonLatency{
			Count: h.Count(), Min: ms(h.Min()), Avg: ms(h.Mean()), Med: ms(h.Quantile(0.5)),
			P90: ms(h.Quantile(0.9)), P95: ms(h.Quantile(0.95)), P99: ms(h.Quantile(0.99)), Max: ms(h.Max()),
		},
		Phases: jsonPhases{
			DNS: phaseJSON(&s.DNS), Connect: phaseJSON(&s.Connect), TLS: phaseJSON(&s.TLS),
			Waiting: phaseJSON(&s.Waiting), Receiving: phaseJSON(&s.Receiving),
		},
	}
	if d > 0 {
		out.RPS = float64(s.Requests) / d.Seconds()
	}
	return out
}

func phaseJSON(h *Histogram) jsonPhase {
	return jsonPhase{Avg: ms(h.Mean()), P95: ms(h.Quantile(0.95))}
}

// ms is d in milliseconds, to the microsecond.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
