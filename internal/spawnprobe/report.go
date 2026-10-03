// Package spawnprobe measures fresh-process runtime calls without choosing a host lifetime policy.
package spawnprobe

import (
	"slices"
	"time"
)

// Sample records wall-clock nanoseconds for one complete plugin operation.
type Sample struct {
	PID        int   `json:"pid"`
	Reaped     bool  `json:"reaped"`
	StartupNS  int64 `json:"startup_ns"`
	DispenseNS int64 `json:"dispense_ns"`
	RPCNS      int64 `json:"rpc_ns"`
	ReapNS     int64 `json:"reap_ns"`
	TotalNS    int64 `json:"total_ns"`
}

// Percentiles uses nearest-rank percentiles, preserving individual measurements.
type Percentiles struct {
	P50NS int64 `json:"p50_ns"`
	P95NS int64 `json:"p95_ns"`
	P99NS int64 `json:"p99_ns"`
}

// Measurements separates a first launch from repeated warm launches.
type Measurements struct {
	Operation string      `json:"operation"`
	First     Sample      `json:"first_launch"`
	Warm      []Sample    `json:"warm_samples"`
	Total     Percentiles `json:"total"`
}

// Report contains reproducibility metadata, never executable arguments or environment values.
type Report struct {
	SchemaVersion     int          `json:"schema_version"`
	GOOS              string       `json:"goos"`
	GOARCH            string       `json:"goarch"`
	GoVersion         string       `json:"go_version"`
	CPUs              int          `json:"cpus"`
	Revision          string       `json:"revision,omitempty"`
	BinarySHA256      string       `json:"binary_sha256"`
	RecordedAt        time.Time    `json:"recorded_at"`
	ColdCacheMeasured bool         `json:"cold_cache_measured"`
	Info              Measurements `json:"info"`
	Find              Measurements `json:"find"`
}

func summarize(samples []Sample) Percentiles {
	totals := make([]int64, len(samples))
	for i, sample := range samples {
		totals[i] = sample.TotalNS
	}
	slices.Sort(totals)
	return Percentiles{P50NS: rank(totals, 50), P95NS: rank(totals, 95), P99NS: rank(totals, 99)}
}

func rank(sorted []int64, percent int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[(len(sorted)*percent+99)/100-1]
}
