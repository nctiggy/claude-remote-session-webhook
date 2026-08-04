package auth

import "time"

// Clock is the daemon's own view of the current time.
//
// It exists so the signature window can be tested at its exact boundary — a
// second either side of 300 — without a test ever sleeping, and so the window
// is measured against a value the caller cannot influence.
//
// Declared here at its point of use rather than in a shared package: one
// obvious method is cheaper to restate than to depend on, and the reaper's
// clock (T036) answers a different question about a different lifetime.
type Clock interface {
	Now() time.Time
}

// systemClock is the only implementation outside tests, and the one New wires
// in. Its zero value works, so there is nothing to configure wrongly.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
