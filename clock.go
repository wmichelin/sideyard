package main

import "time"

// Clock is the time capability used by date-sensitive business decisions.
// Production callers leave it nil and receive the system UTC clock; tests can
// supply a fixed clock at the consumer that needs deterministic behavior.
type Clock interface {
	Now() time.Time
}

func utcNow(clock Clock) time.Time {
	if clock == nil {
		return time.Now().UTC()
	}
	return clock.Now().UTC()
}
