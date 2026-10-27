package main

import "testing"

func TestNearestRank(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	if got := nearestRank(sorted, 0.50); got != 10 {
		t.Fatalf("p50 = %v, want 10", got)
	}
	if got := nearestRank(sorted, 0.95); got != 19 {
		t.Fatalf("p95 = %v, want 19", got)
	}
	if got := nearestRank(sorted[:1], 0.95); got != 1 {
		t.Fatalf("n=1 p95 = %v, want 1", got)
	}
	p50, p95 := quantiles([]float64{20, 10, 30})
	if p50 != 20 || p95 != 30 {
		t.Fatalf("quantiles = %v %v", p50, p95)
	}
}
