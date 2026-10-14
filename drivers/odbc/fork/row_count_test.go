package odbc

import (
	"math"
	"testing"
)

func TestAccumulateRowCountFailsClosedOnUnknownOrOverflow(t *testing.T) {
	for _, tc := range []struct{ current, next, want int64 }{
		{0, 2, 2},
		{2, 3, 5},
		{-1, 2, -1},
		{2, -1, -1},
		{-1, -1, -1},
		{math.MaxInt64, 1, -1},
	} {
		if got := accumulateRowCount(tc.current, tc.next); got != tc.want {
			t.Errorf("accumulate(%d,%d) = %d, want %d", tc.current, tc.next, got, tc.want)
		}
	}
}
