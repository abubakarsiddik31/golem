package golem

import (
	"testing"
	"time"
)

func TestExponentialBackoffDoublesUntilCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 500 * time.Millisecond},
		{attempt: 2, want: time.Second},
		{attempt: 3, want: 2 * time.Second},
		{attempt: 6, want: 16 * time.Second},
		{attempt: 7, want: 30 * time.Second}, // 32 s doubles past the cap
		{attempt: 8, want: 30 * time.Second},
		{attempt: 60, want: 30 * time.Second}, // shift overflow stays capped
	}
	for _, test := range tests {
		if got := exponentialBackoff(test.attempt); got != test.want {
			t.Fatalf("exponentialBackoff(%d) = %v, want %v", test.attempt, got, test.want)
		}
	}
}
