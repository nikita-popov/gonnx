package api

import "time"

// msDuration converts milliseconds to time.Duration.
func msDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
