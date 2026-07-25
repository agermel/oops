package utils

import "testing"

func TestEstimateTokensForCharsRoundsUp(t *testing.T) {
	for _, test := range []struct {
		chars int
		want  int
	}{
		{chars: 0, want: 0},
		{chars: 1, want: 1},
		{chars: 4, want: 1},
		{chars: 5, want: 2},
	} {
		if got := EstimateTokensForChars(test.chars); got != test.want {
			t.Fatalf("EstimateTokensForChars(%d) = %d, want %d", test.chars, got, test.want)
		}
	}
}
