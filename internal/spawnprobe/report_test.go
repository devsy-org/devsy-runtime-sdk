package spawnprobe

import "testing"

func TestNearestRankPercentiles(t *testing.T) {
	samples := make([]Sample, 100)
	for i := range samples {
		samples[i].TotalNS = int64(100 - i)
	}
	want := Percentiles{P50NS: 50, P95NS: 95, P99NS: 99}
	if got := summarize(samples); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if samples[0].TotalNS != 100 {
		t.Fatal("summary mutated sample order")
	}
	if got := summarize(nil); got != (Percentiles{}) {
		t.Fatal("empty summary is not zero")
	}
	if got := summarize(
		[]Sample{{TotalNS: 7}},
	); got != (Percentiles{P50NS: 7, P95NS: 7, P99NS: 7}) {
		t.Fatalf("single sample: %v", got)
	}
}
