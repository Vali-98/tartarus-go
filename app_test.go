package main

import (
	"fmt"
	"testing"
)

func TestWorkersReserveGameCapacity(t *testing.T) {
	tests := []struct {
		mode string
		cpus int
		want int
	}{
		{"game", 1, 1}, {"most", 1, 1}, {"half", 2, 1}, {"most", 2, 1},
		{"game", 4, 1}, {"quarter", 4, 1}, {"half", 4, 1}, {"most", 4, 3},
		{"most", 8, 7}, {"most", 12, 10}, {"half", 12, 5}, {"most", 16, 14},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%d-cpus", tt.mode, tt.cpus), func(t *testing.T) {
			if got := workersFor(tt.mode, tt.cpus); got != tt.want {
				t.Fatalf("workersFor(%q, %d) = %d, want %d", tt.mode, tt.cpus, got, tt.want)
			}
		})
	}
}
