package desktop

import (
	"errors"
	"testing"
)

func TestPreviewShortcutsReleaseOnlyOwnedKeysAndIgnoreQueuedCallbacks(t *testing.T) {
	bindings := map[string]func(){"Cmd+K": func() {}}
	s := previewShortcuts{
		register: func(key string, fn func()) error {
			if bindings[key] != nil {
				return errors.New("occupied")
			}
			bindings[key] = fn
			return nil
		},
		unregister: func(key string) { delete(bindings, key) },
	}
	previous, next, exits := 0, 0, 0
	if err := s.start(func() { previous++ }, func() { next++ }, func() { exits++ }); err != nil {
		t.Fatal(err)
	}
	oldNext, oldExit := bindings["Right"], bindings["Escape"]
	bindings["Left"]()
	bindings["Right"]()
	bindings["Escape"]()
	if previous != 1 || next != 1 || exits != 1 {
		t.Fatal("bindings did not dispatch their actions exactly once")
	}
	s.stop()
	oldNext()
	oldExit()
	if len(bindings) != 1 || bindings["Cmd+K"] == nil || next != 1 || exits != 1 {
		t.Fatal("exit removed another shortcut or accepted a queued callback")
	}
	if err := s.start(func() { previous++ }, func() { next++ }, func() { exits++ }); err != nil {
		t.Fatal(err)
	}
	oldNext()
	if next != 1 {
		t.Fatal("a previous session changed a new preview")
	}
	bindings["Right"]()
	if next != 2 {
		t.Fatal("new session could not use the shortcut")
	}
	s.stop()
}

func TestPreviewShortcutsPartialRegistrationFailureRollsBack(t *testing.T) {
	occupied := func() {}
	bindings := map[string]func(){"Right": occupied}
	var queued func()
	calls := 0
	s := previewShortcuts{
		register: func(key string, fn func()) error {
			if bindings[key] != nil {
				return errors.New("occupied")
			}
			bindings[key] = fn
			queued = fn
			return nil
		},
		unregister: func(key string) { delete(bindings, key) },
	}
	if err := s.start(func() { calls++ }, func() { calls++ }, func() { calls++ }); err == nil {
		t.Fatal("shortcut conflict was not reported")
	}
	if len(bindings) != 1 || bindings["Right"] == nil || len(s.owned) != 0 {
		t.Fatal("partial registration was not cleaned up, or an occupied binding was removed")
	}
	queued()
	if calls != 0 {
		t.Fatal("a callback from failed entry remained active")
	}
	s.stop()
	if len(bindings) != 1 {
		t.Fatal("repeated cleanup removed someone else's binding")
	}
}
