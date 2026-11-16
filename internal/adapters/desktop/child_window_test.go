package desktop

import (
	"errors"
	"sort"
	"testing"

	"github.com/egoist/mygo"
)

type settingsChildWindowStub struct {
	handle    uintptr
	visible   bool
	next      int
	events    map[string]map[int]func()
	close     map[int]func(*mygo.CloseEvent)
	handleGet int
}

func childWindowStub(handle uintptr, visible bool) *settingsChildWindowStub {
	return &settingsChildWindowStub{handle: handle, visible: visible, events: make(map[string]map[int]func()), close: make(map[int]func(*mygo.CloseEvent))}
}

func (w *settingsChildWindowStub) NativeHandle() uintptr { w.handleGet++; return w.handle }
func (w *settingsChildWindowStub) IsVisible() bool       { return w.visible }
func (w *settingsChildWindowStub) listen(event string, fn func()) func() {
	w.next++
	id := w.next
	if w.events[event] == nil {
		w.events[event] = make(map[int]func())
	}
	w.events[event][id] = fn
	return func() { delete(w.events[event], id) }
}
func (w *settingsChildWindowStub) OnShow(fn func()) func() { return w.listen("show", fn) }
func (w *settingsChildWindowStub) OnHide(fn func()) func() { return w.listen("hide", fn) }
func (w *settingsChildWindowStub) OnClosed(fn func()) func() {
	return w.listen("closed", fn)
}
func (w *settingsChildWindowStub) OnRestore(fn func()) func() {
	return w.listen("restore", fn)
}
func (w *settingsChildWindowStub) OnLeaveFullScreen(fn func()) func() {
	return w.listen("leave-fullscreen", fn)
}
func (w *settingsChildWindowStub) OnClose(fn func(*mygo.CloseEvent)) func() {
	w.next++
	id := w.next
	w.close[id] = fn
	return func() { delete(w.close, id) }
}
func (w *settingsChildWindowStub) emit(event string) {
	ids := make([]int, 0, len(w.events[event]))
	for id := range w.events[event] {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	callbacks := make([]func(), 0, len(ids))
	for _, id := range ids {
		if fn := w.events[event][id]; fn != nil {
			callbacks = append(callbacks, fn)
		}
	}
	// MyGO snapshots callbacks before delivery, so a removed listener can still
	// be present in an event that another callback is currently handling.
	for _, fn := range callbacks {
		fn()
	}
}
func (w *settingsChildWindowStub) requestClose(event *mygo.CloseEvent) {
	ids := make([]int, 0, len(w.close))
	for id := range w.close {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if fn := w.close[id]; fn != nil {
			fn(event)
		}
	}
}
func (w *settingsChildWindowStub) listenerCount() int {
	count := len(w.close)
	for _, listeners := range w.events {
		count += len(listeners)
	}
	return count
}

type settingsChildNativeStub struct {
	parents            map[uintptr]uintptr
	attaches, detaches int
	err                error
}

func childNativeStub() *settingsChildNativeStub {
	return &settingsChildNativeStub{parents: make(map[uintptr]uintptr)}
}
func (n *settingsChildNativeStub) attach(parent, child uintptr) error {
	n.attaches++
	if n.err != nil {
		return n.err
	}
	n.parents[child] = parent
	return nil
}
func (n *settingsChildNativeStub) detach(parent, child uintptr) {
	n.detaches++
	if n.parents[child] == parent {
		delete(n.parents, child)
	}
}

func TestSettingsNativeChildWaitsForFirstPaintAndKeepsExplicitHide(t *testing.T) {
	parent, child, native := childWindowStub(1, true), childWindowStub(2, false), childNativeStub()
	binding, err := bindSettingsChild(parent, child, native, nil)
	if err != nil {
		t.Fatal(err)
	}
	if native.attaches != 0 {
		t.Fatal("unpainted content window was ordered in by attachment")
	}
	child.visible = true
	child.emit("show")
	if native.parents[2] != 1 {
		t.Fatal("first painted show did not create a native parent relation")
	}
	child.visible = false
	child.emit("hide")
	if native.parents[2] != 0 {
		t.Fatal("explicitly hidden child retained its native attachment")
	}
	before := native.attaches
	for _, event := range []string{"show", "restore", "leave-fullscreen"} {
		parent.emit(event)
	}
	if native.attaches != before {
		t.Fatal("restoring the parent resurrected an explicitly hidden child")
	}
	child.visible = true
	child.emit("show")
	if native.parents[2] != 1 {
		t.Fatal("explicitly shown child did not regain its native attachment")
	}
	binding.dispose(true)
	if native.parents[2] != 0 || parent.listenerCount() != 0 || child.listenerCount() != 0 {
		t.Fatal("cleanup retained the native relation or lifecycle listeners")
	}
	detaches := native.detaches
	binding.dispose(true)
	if native.detaches != detaches {
		t.Fatal("repeated cleanup touched native windows again")
	}
}

func TestSettingsNativeChildRestoresAfterRawParentPropagationAndFullscreenExit(t *testing.T) {
	parent, child, native := childWindowStub(1, true), childWindowStub(2, true), childNativeStub()
	binding, err := bindSettingsChild(parent, child, native, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.dispose(true)
	for _, event := range []string{"show", "restore", "leave-fullscreen"} {
		// MyGO's parent-hide code uses raw orderOut, removing native attachment
		// without a public child OnHide. Restore callbacks precede its queued
		// orderChildren, so the child is still invisible here.
		child.visible = false
		delete(native.parents, 2)
		parent.emit(event)
		if native.parents[2] != 1 {
			t.Fatalf("%s did not restore an intended-visible child before deferred ordering", event)
		}
	}
}

func TestSettingsNativeChildCloseCancellationAndClosedHandlesAreSafe(t *testing.T) {
	for _, closed := range []string{"parent", "child"} {
		t.Run(closed, func(t *testing.T) {
			parent, child, native := childWindowStub(1, true), childWindowStub(2, true), childNativeStub()
			binding, err := bindSettingsChild(parent, child, native, nil)
			if err != nil {
				t.Fatal(err)
			}
			prevented := &mygo.CloseEvent{}
			prevented.PreventDefault()
			parent.requestClose(prevented)
			if native.parents[2] != 1 {
				t.Fatal("canceled parent close detached the settings window")
			}
			if closed == "parent" {
				parent.requestClose(&mygo.CloseEvent{})
				if native.parents[2] != 0 {
					t.Fatal("parent close did not detach while handles were alive")
				}
				parent.handle = 0
			} else {
				child.handle = 0
			}
			parentGets, childGets := parent.handleGet, child.handleGet
			attaches, detaches := native.attaches, native.detaches
			if closed == "parent" {
				parent.emit("closed")
			} else {
				child.emit("closed")
			}
			parent.emit("restore")
			child.emit("show")
			binding.dispose(true)
			if parent.handleGet != parentGets || child.handleGet != childGets || native.attaches != attaches || native.detaches != detaches {
				t.Fatal("closed-window callbacks dereferenced native handles")
			}
			if parent.listenerCount() != 0 || child.listenerCount() != 0 {
				t.Fatal("closing retained lifecycle listeners")
			}
		})
	}
}

func TestSettingsNativeChildReportsAssociationErrors(t *testing.T) {
	parent, child, native := childWindowStub(1, true), childWindowStub(2, true), childNativeStub()
	native.err = errors.New("native association rejected")
	if binding, err := bindSettingsChild(parent, child, native, nil); err == nil || binding != nil || parent.listenerCount() != 0 || child.listenerCount() != 0 {
		t.Fatal("initial attachment failure was not returned cleanly")
	}
	child.visible = false
	var reported error
	binding, err := bindSettingsChild(parent, child, native, func(err error) { reported = err })
	if err != nil {
		t.Fatal(err)
	}
	defer binding.dispose(true)
	child.visible = true
	child.emit("show")
	if reported == nil || reported.Error() != native.err.Error() {
		t.Fatal("deferred show attachment failure was not reported")
	}
	if _, err := bindSettingsChild(childWindowStub(0, true), child, native, nil); err == nil {
		t.Fatal("unavailable native window was accepted")
	}
}

func TestSettingsNativeChildAlreadySnapshottedHideDoesNotTouchClosedHandle(t *testing.T) {
	parent, child, native := childWindowStub(1, true), childWindowStub(2, true), childNativeStub()
	child.OnHide(func() {
		child.handle = 0
		child.emit("closed")
	})
	if _, err := bindSettingsChild(parent, child, native, nil); err != nil {
		t.Fatal(err)
	}
	parentGets, childGets := parent.handleGet, child.handleGet
	attaches, detaches := native.attaches, native.detaches
	child.visible = false
	child.emit("hide")
	if parent.handleGet != parentGets || child.handleGet != childGets || native.attaches != attaches || native.detaches != detaches {
		t.Fatal("a callback snapshotted before closure touched released native handles")
	}
}
