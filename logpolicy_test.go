package main

import (
	"testing"
	"time"
)

func TestShouldLog(t *testing.T) {
	base := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		temp           int
		lastLoggedTemp int
		haveLastLogged bool
		elapsedSince   time.Duration
		tickChanged    bool
		want           bool
	}{
		// First tick always logs.
		{"first tick, hot", 95, 0, false, 0, false, true},
		{"first tick, cold", 50, 0, false, 0, false, true},

		// Hot regime (>= 90): defer to tickChanged.
		{"hot and changed", 91, 80, true, time.Second, true, true},
		{"hot and unchanged", 91, 91, true, time.Second, false, false},
		{"exactly at threshold changed", 90, 80, true, time.Second, true, true},
		{"exactly at threshold unchanged", 90, 90, true, time.Second, false, false},

		// Cold regime (< 90): need big delta or heartbeat.
		{"cold, small delta, no heartbeat", 72, 70, true, time.Minute, false, false},
		{"cold, delta at threshold up", 80, 70, true, time.Minute, false, true},
		{"cold, delta at threshold down", 60, 70, true, time.Minute, false, true},
		{"cold, delta above threshold", 85, 70, true, time.Minute, false, true},
		{"cold, delta just below threshold", 79, 70, true, time.Minute, false, false},

		// Heartbeat.
		{"cold, heartbeat just reached", 70, 70, true, time.Hour, false, true},
		{"cold, heartbeat exceeded", 70, 70, true, 2 * time.Hour, false, true},
		{"cold, heartbeat just short", 70, 70, true, time.Hour - time.Second, false, false},

		// Tick changed doesn't matter in cold regime.
		{"cold, tick changed but small delta", 71, 70, true, time.Minute, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lastLogTime := base.Add(-tc.elapsedSince)
			now := base

			got := shouldLog(tc.temp, tc.lastLoggedTemp, tc.haveLastLogged, lastLogTime, now, tc.tickChanged)
			if got != tc.want {
				t.Errorf("shouldLog(temp=%d, last=%d, haveLast=%v, elapsed=%s, tickChanged=%v) = %v, want %v",
					tc.temp, tc.lastLoggedTemp, tc.haveLastLogged, tc.elapsedSince, tc.tickChanged, got, tc.want)
			}
		})
	}
}
