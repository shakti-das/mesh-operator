package k8swebhook

import (
	"testing"
	"time"
)

func TestCooldown_FirstCallAllowed(t *testing.T) {
	now := time.Now()
	fakeTimeProvider := func() time.Time { return now }
	subject := newCooldown(time.Minute, fakeTimeProvider)
	if subject.ShouldSkip() {
		t.Fatal("first call should not be skipped")
	}
}

func TestCooldown_NextCallUnderDurationSkipped(t *testing.T) {
	now := time.Now()
	fakeTimeProvider := func() time.Time { return now }
	subject := newCooldown(time.Minute, fakeTimeProvider)
	subject.RecordPatchTime()
	// call immediately - no time has changed
	if !subject.ShouldSkip() {
		t.Fatal("call within cooldown should be skipped")
	}
}

func TestCooldown_NextCallUnderDurationSkippedElapsedTime(t *testing.T) {
	now := time.Now()
	fakeTimeProvider := func() time.Time { return now }
	subject := newCooldown(time.Minute, fakeTimeProvider)
	subject.RecordPatchTime()
	// call after 30 seconds, still under
	now = now.Add(30 * time.Second)
	if !subject.ShouldSkip() {
		t.Fatal("call within cooldown should be skipped")
	}
}

func TestCooldown_NextCallAtDurationNoSkip(t *testing.T) {
	now := time.Now()
	fakeTimeProvider := func() time.Time { return now }
	subject := newCooldown(time.Minute, fakeTimeProvider)
	subject.RecordPatchTime()
	// call at 1 minute
	now = now.Add(60 * time.Second)
	if subject.ShouldSkip() {
		t.Fatal("call after cooldown duration should not be skipped")
	}
}

func TestCooldown_NextCallAfterDurationNoSkip(t *testing.T) {
	now := time.Now()
	fakeTimeProvider := func() time.Time { return now }
	subject := newCooldown(time.Minute, fakeTimeProvider)
	subject.RecordPatchTime()
	// call at 1 minute
	now = now.Add(60 * time.Second).Add(time.Millisecond)
	if subject.ShouldSkip() {
		t.Fatal("call after cooldown duration should not be skipped")
	}
}

func TestCooldown_ZeroDurationAlwaysNoSkip(t *testing.T) {
	now := time.Now()
	fakeTimeProvider := func() time.Time { return now }
	subject := newCooldown(0*time.Minute, fakeTimeProvider)
	subject.RecordPatchTime()
	now = now.Add(60 * time.Second)
	if subject.ShouldSkip() {
		t.Fatal("call with zero duration should not be skipped")
	}
	now = now.Add(1 * time.Microsecond)
	if subject.ShouldSkip() {
		t.Fatal("call with zero duration should not be skipped")
	}
}
