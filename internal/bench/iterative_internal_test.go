package bench

import (
	"testing"
	"time"
)

func TestIterativeThresholdZeroBaselineAndDecreases(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  ThresholdResult
		pass bool
		zero bool
	}{
		{name: "zero-equal", got: atMostDurationIncrease("latency", 0, 0, 15), pass: true, zero: true},
		{name: "zero-positive", got: atMostDurationIncrease("latency", 0, time.Nanosecond, 15), pass: false, zero: true},
		{name: "memory-decrease", got: atMostUintIncrease("memory", 100, 75, 25), pass: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Pass != tc.pass || tc.got.ZeroBase != tc.zero {
				t.Fatalf("threshold = %+v, want pass=%t zero=%t", tc.got, tc.pass, tc.zero)
			}
		})
	}
}
