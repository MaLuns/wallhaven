package desktop

import (
	"errors"
	"testing"
	"time"
)

type previewApplicationStub struct {
	name                     string
	hidden, terminated       bool
	hides, unhides, releases int
}

func (a *previewApplicationStub) Name() string     { return a.name }
func (a *previewApplicationStub) Hidden() bool     { return a.hidden }
func (a *previewApplicationStub) Terminated() bool { return a.terminated }
func (a *previewApplicationStub) Hide()            { a.hides++ }
func (a *previewApplicationStub) Unhide()          { a.unhides++ }
func (a *previewApplicationStub) Release()         { a.releases++ }

type visibilityTimer struct {
	fn      func()
	stopped bool
}
type visibilityClock struct {
	now    time.Time
	timers []*visibilityTimer
}

func (c *visibilityClock) after(_ time.Duration, fn func()) func() {
	timer := &visibilityTimer{fn: fn}
	c.timers = append(c.timers, timer)
	return func() { timer.stopped = true }
}
func (c *visibilityClock) tick() {
	c.now = c.now.Add(100 * time.Millisecond)
	timers := c.timers
	c.timers = nil
	for _, timer := range timers {
		if !timer.stopped {
			timer.fn()
		}
	}
}
func visibilityFixture(applications ...previewApplication) (*previewVisibility, *visibilityClock) {
	clock := &visibilityClock{now: time.Unix(100, 0)}
	return &previewVisibility{snapshot: func() ([]previewApplication, error) { return applications, nil }, after: clock.after, now: func() time.Time { return clock.now }, timeout: 250 * time.Millisecond}, clock
}

func TestPreviewVisibilityWaitsForActualStateAndPreservesPreHiddenApps(t *testing.T) {
	visible := &previewApplicationStub{name: "visible"}
	hidden := &previewApplicationStub{name: "hidden", hidden: true}
	s, clock := visibilityFixture(visible, hidden)
	completed := false
	s.hide(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		completed = true
	})
	clock.tick()
	if completed || visible.hides != 1 || hidden.hides != 0 || hidden.releases != 1 {
		t.Fatal("request was confused with actual state, or pre-hidden application was changed")
	}
	visible.hidden = true
	clock.tick()
	if !completed {
		t.Fatal("confirmed hide did not complete")
	}
	completed = false
	s.restore(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		completed = true
	})
	clock.tick()
	if completed || hidden.unhides != 0 {
		t.Fatal("restoration completed early or unhid an originally hidden app")
	}
	visible.hidden = false
	clock.tick()
	if !completed || visible.releases != 1 || len(s.changed) != 0 {
		t.Fatal("confirmed restore did not release retained applications")
	}
}

func TestPreviewVisibilityCancellationRestoresEvenBeforeHideIsObserved(t *testing.T) {
	application := &previewApplicationStub{name: "app"}
	s, clock := visibilityFixture(application)
	var entryError error
	s.hide(func(err error) { entryError = err })
	queued := clock.timers[0].fn
	complete := false
	s.restore(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		complete = true
	})
	if !errors.Is(entryError, errPreviewInterrupted) || application.unhides != 1 || complete {
		t.Fatal("cancel lost the application or read a stale pre-hide state")
	}
	queued() // A timer already dispatched before cancellation must be harmless.
	clock.tick()
	if !complete || application.releases != 1 {
		t.Fatal("cancel did not finish restoration")
	}
}

func TestPreviewVisibilityTimeoutRetainsFailedAppsForRetryAndSkipsTerminated(t *testing.T) {
	a, b := &previewApplicationStub{name: "a"}, &previewApplicationStub{name: "b"}
	s, clock := visibilityFixture(a, b)
	s.hide(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	})
	a.hidden, b.hidden = true, true
	clock.tick()
	b.terminated = true
	var restoreError error
	s.restore(func(err error) { restoreError = err })
	for range 3 {
		clock.tick()
	}
	if restoreError == nil || len(s.changed) != 1 || a.releases != 0 || b.releases != 1 || b.unhides != 0 {
		t.Fatal("failed app was released or terminated app blocked restoration")
	}
	s.hide(func(err error) {
		if err == nil {
			t.Fatal("new session replaced an unrestored application")
		}
	})
	complete := false
	s.restore(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		complete = true
	})
	a.hidden = false
	clock.tick()
	if !complete || a.releases != 1 || len(s.changed) != 0 {
		t.Fatal("restore retry did not use the original application identity")
	}
}
