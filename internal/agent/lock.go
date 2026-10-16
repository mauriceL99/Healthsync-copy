package agent

import "time"

// How long a command waits for the state-directory lock, and how often it retries.
const (
	lockWait  = 10 * time.Second
	lockRetry = 100 * time.Millisecond
)
