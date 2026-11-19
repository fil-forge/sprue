package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Sweeping every draw in [0, sum(weights)) gives each index's exact share, so
// the expected distribution holds with zero tolerance.
func TestPickWeighted(t *testing.T) {
	tests := []struct {
		name    string
		weights []uint64
	}{
		{"spreads draws evenly at equal weights", []uint64{100, 100, 100}},
		{"chooses every index at weight 1", []uint64{1, 1, 1}},
		{"spreads draws in proportion to unequal weights", []uint64{1, 2, 3, 4}},
		{"never chooses an index of weight 0", []uint64{0, 5, 0, 5, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var total uint64
			for _, w := range tc.weights {
				total += w
			}
			counts := make([]uint64, len(tc.weights))
			for draw := range total {
				counts[pickWeighted(tc.weights, draw)]++
			}
			require.Equal(t, tc.weights, counts)
		})
	}
}
