package main

import "time"

// Logging policy thresholds.
//
// At or above hotThresholdC we log every tick that changed vs the previous
// tick (the "hot" regime — dense logs when things are actually warm).
//
// Below hotThresholdC we log only when the temperature has moved at least
// coldDeltaC from the last *logged* temperature, or when heartbeatInterval
// has elapsed since the last log line (liveness proof). Either event resets
// the heartbeat.
const (
	hotThresholdC     = 90
	coldDeltaC        = 10
	heartbeatInterval = time.Hour
)

func abs(x int) int {
	if x < 0 {
		return -x
	}

	return x
}

// shouldLog decides whether the current tick should emit a log line.
//
// haveLastLogged indicates whether any prior tick has been logged. On the
// very first tick we always log (baseline).
//
// tickChanged is whether temp/pwm/rpm changed vs the previous *tick* (used
// only in the hot regime).
func shouldLog(
	temp int,
	lastLoggedTemp int,
	haveLastLogged bool,
	lastLogTime time.Time,
	now time.Time,
	tickChanged bool,
) bool {
	if !haveLastLogged {
		return true
	}

	if temp >= hotThresholdC {
		return tickChanged
	}

	if abs(temp-lastLoggedTemp) >= coldDeltaC {
		return true
	}

	if now.Sub(lastLogTime) >= heartbeatInterval {
		return true
	}

	return false
}
