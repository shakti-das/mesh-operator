package k8swebhook

import (
	"time"
)

// cooldown rate-limits webhook patch operations by tracking the last
// successful patch time.
// A zero duration disables the cooldown.
// Use in single go routine.
type cooldown struct {
	duration      time.Duration
	lastPatchTime time.Time
	timeProvider  timeProvider
}

func newCooldown(duration time.Duration, timeProvider timeProvider) cooldown {
	if timeProvider == nil {
		timeProvider = time.Now
	}
	return cooldown{duration: duration, timeProvider: timeProvider}
}

func (c *cooldown) ShouldSkip() bool {
	return c.duration > 0 && !c.lastPatchTime.IsZero() && c.timeProvider().Sub(c.lastPatchTime) < c.duration
}

func (c *cooldown) RecordPatchTime() {
	c.lastPatchTime = c.timeProvider()
}

// A function that can return current ("now") time. This is used for precide testing.
type timeProvider func() time.Time
