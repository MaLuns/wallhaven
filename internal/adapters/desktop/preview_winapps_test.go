package desktop

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type windowsPreviewAppsFixture struct {
	api                       windowsPreviewAppsAPI
	windows                   map[uintptr]windowsPreviewWindow
	handles                   []uintptr
	markers                   map[uintptr]string
	markErrors, inspectErrors map[uintptr]error
	marked, unmarked          []uintptr
	requests                  [][2]uintptr
	enumerateError            error
	onMark                    func(uintptr)
}

func newWindowsPreviewAppsFixture(windows ...windowsPreviewWindow) *windowsPreviewAppsFixture {
	f := &windowsPreviewAppsFixture{
		windows: make(map[uintptr]windowsPreviewWindow), markers: make(map[uintptr]string),
		markErrors: make(map[uintptr]error), inspectErrors: make(map[uintptr]error),
	}
	for _, window := range windows {
		f.windows[window.handle] = window
		f.handles = append(f.handles, window.handle)
	}
	f.api = windowsPreviewAppsAPI{
		process:   100,
		enumerate: func() ([]uintptr, error) { return f.handles, f.enumerateError },
		inspect:   func(handle uintptr) (windowsPreviewWindow, error) { return f.windows[handle], f.inspectErrors[handle] },
		state:     func(handle uintptr) windowsPreviewWindow { return f.windows[handle] },
		mark: func(handle uintptr, marker string) error {
			if err := f.markErrors[handle]; err != nil {
				return err
			}
			f.markers[handle] = marker
			f.marked = append(f.marked, handle)
			if f.onMark != nil {
				f.onMark(handle)
			}
			return nil
		},
		marked: func(handle uintptr, marker string) bool { return f.markers[handle] == marker },
		unmark: func(handle uintptr, marker string) {
			delete(f.markers, handle)
			f.unmarked = append(f.unmarked, handle)
		},
		showAsync: func(handle uintptr, command int) {
			f.requests = append(f.requests, [2]uintptr{handle, uintptr(command)})
		},
	}
	return f
}

func windowsPreviewTestWindow(handle uintptr) windowsPreviewWindow {
	return windowsPreviewWindow{handle: handle, process: 200, thread: 300, visible: true, class: "OrdinaryApp", name: "Window"}
}

func TestWindowsPreviewAppsSelectOnlyVisibleOrdinaryWindows(t *testing.T) {
	valid, dialog := windowsPreviewTestWindow(1), windowsPreviewTestWindow(2)
	dialog.class = "#32770" // An owned application dialog is still top-level.
	variants := []struct {
		name   string
		change func(*windowsPreviewWindow)
	}{
		{"own process", func(w *windowsPreviewWindow) { w.process = 100 }},
		{"hidden", func(w *windowsPreviewWindow) { w.visible = false }},
		{"minimized", func(w *windowsPreviewWindow) { w.minimized = true }},
		{"cloaked", func(w *windowsPreviewWindow) { w.cloaked = true }},
		{"child", func(w *windowsPreviewWindow) { w.style |= windowsPreviewChild }},
		{"tool", func(w *windowsPreviewWindow) { w.exStyle |= windowsPreviewToolWindow }},
		{"no activate", func(w *windowsPreviewWindow) { w.exStyle |= windowsPreviewNoActivate }},
		{"shell handle", func(w *windowsPreviewWindow) { w.shell = true }},
		{"desktop", func(w *windowsPreviewWindow) { w.class = "Progman" }},
		{"desktop worker", func(w *windowsPreviewWindow) { w.class = "WorkerW" }},
		{"taskbar", func(w *windowsPreviewWindow) { w.class = "Shell_TrayWnd" }},
		{"secondary taskbar", func(w *windowsPreviewWindow) { w.class = "Shell_SecondaryTrayWnd" }},
		{"closed", func(w *windowsPreviewWindow) { w.thread = 0 }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			excluded := windowsPreviewTestWindow(3)
			variant.change(&excluded)
			f := newWindowsPreviewAppsFixture(valid, dialog, excluded)
			apps, err := snapshotWindowsPreviewApps(&f.api, "session")
			if err != nil || len(apps) != 2 || !reflect.DeepEqual(f.marked, []uintptr{1, 2}) || len(f.requests) != 0 {
				t.Fatalf("selection changed unrelated windows: apps=%d marked=%v requests=%v error=%v", len(apps), f.marked, f.requests, err)
			}
			for _, app := range apps {
				app.Release()
			}
			if len(f.markers) != 0 {
				t.Fatal("snapshot markers were not released")
			}
		})
	}
}

func TestWindowsPreviewAppsSnapshotFailureReleasesMarkersBeforeHiding(t *testing.T) {
	for _, failure := range []string{"inspect", "mark"} {
		t.Run(failure, func(t *testing.T) {
			f := newWindowsPreviewAppsFixture(windowsPreviewTestWindow(1), windowsPreviewTestWindow(2))
			denied := errors.New("access denied")
			if failure == "inspect" {
				f.inspectErrors[2] = denied
			} else {
				f.markErrors[2] = denied
			}
			apps, err := snapshotWindowsPreviewApps(&f.api, "session")
			if !errors.Is(err, denied) || len(apps) != 0 || len(f.markers) != 0 || len(f.requests) != 0 || !reflect.DeepEqual(f.unmarked, []uintptr{1}) {
				t.Fatalf("partial snapshot leaked state: apps=%v markers=%v requests=%v unmarked=%v error=%v", apps, f.markers, f.requests, f.unmarked, err)
			}
			if failure == "mark" && !strings.Contains(err.Error(), "Window") {
				t.Fatal("permission failure did not identify the application window")
			}
		})
	}
}

func TestWindowsPreviewAppsDoNotRestoreAReusedWindowHandle(t *testing.T) {
	for _, change := range []string{"missing marker", "different marker", "different process", "different thread", "closed"} {
		t.Run(change, func(t *testing.T) {
			f := newWindowsPreviewAppsFixture(windowsPreviewTestWindow(1))
			apps, err := snapshotWindowsPreviewApps(&f.api, "session")
			if err != nil {
				t.Fatal(err)
			}
			apps[0].Hide()
			state := f.windows[1]
			switch change {
			case "missing marker":
				delete(f.markers, 1) // Same PID/TID is insufficient after HWND reuse.
			case "different marker":
				f.markers[1] = "another session"
			case "different process":
				state.process++
			case "different thread":
				state.thread++
			case "closed":
				state = windowsPreviewWindow{}
			}
			f.windows[1] = state
			if !apps[0].Terminated() {
				t.Fatal("retained identity accepted a replacement window")
			}
			apps[0].Unhide()
			apps[0].Release()
			apps[0].Release()
			if len(f.requests) != 1 || len(f.unmarked) > 1 {
				t.Fatalf("restored replacement or released twice: requests=%v unmarked=%v", f.requests, f.unmarked)
			}
			if change == "different marker" && f.markers[1] != "another session" {
				t.Fatal("removed a marker belonging to a different window/session")
			}
		})
	}
}

func TestWindowsPreviewAppsSkipWindowClosedWhileMarking(t *testing.T) {
	f := newWindowsPreviewAppsFixture(windowsPreviewTestWindow(1))
	f.onMark = func(handle uintptr) { delete(f.windows, handle) }
	apps, err := snapshotWindowsPreviewApps(&f.api, "session")
	if err != nil || len(apps) != 0 || len(f.markers) != 0 || len(f.requests) != 0 {
		t.Fatalf("closed window blocked entry or retained marker: apps=%v markers=%v requests=%v error=%v", apps, f.markers, f.requests, err)
	}
}

func TestWindowsPreviewAppsCancellationQueuesNonactivatingInverse(t *testing.T) {
	f := newWindowsPreviewAppsFixture(windowsPreviewTestWindow(1))
	apps, err := snapshotWindowsPreviewApps(&f.api, "session")
	if err != nil {
		t.Fatal(err)
	}
	s, clock := visibilityFixture(apps...)
	var entryError error
	s.hide(func(err error) { entryError = err })
	restored := false
	s.restore(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		restored = true
	})
	if !errors.Is(entryError, errPreviewInterrupted) || !reflect.DeepEqual(f.requests, [][2]uintptr{{1, 0}, {1, 8}}) {
		t.Fatalf("cancel did not queue SW_SHOWNA after pending SW_HIDE: requests=%v error=%v", f.requests, entryError)
	}
	// Simulate the foreign UI queue processing both requests in order.
	for _, request := range f.requests {
		state := f.windows[request[0]]
		state.visible = request[1] != 0
		f.windows[request[0]] = state
	}
	clock.tick()
	if !restored || len(f.markers) != 0 {
		t.Fatal("cancel completed without releasing the original identity")
	}
}

func TestWindowsPreviewAppsLeaveNewlyMinimizedWindowsAlone(t *testing.T) {
	for _, beforeHideCheck := range []bool{true, false} {
		f := newWindowsPreviewAppsFixture(windowsPreviewTestWindow(1))
		apps, err := snapshotWindowsPreviewApps(&f.api, "session")
		if err != nil {
			t.Fatal(err)
		}
		state := f.windows[1]
		state.minimized = true
		f.windows[1] = state
		if beforeHideCheck && !apps[0].Hidden() {
			t.Fatal("window minimized since snapshot was still selected for hiding")
		}
		apps[0].Hide()
		apps[0].Unhide()
		apps[0].Release()
		if len(f.requests) != 0 || len(f.markers) != 0 {
			t.Fatal("a window minimized before hiding was changed")
		}
	}
}

func TestWindowsPreviewAppsFailedRestoreRetainsOriginalIdentityForRetry(t *testing.T) {
	f := newWindowsPreviewAppsFixture(windowsPreviewTestWindow(1))
	apps, err := snapshotWindowsPreviewApps(&f.api, "session")
	if err != nil {
		t.Fatal(err)
	}
	s, clock := visibilityFixture(apps...)
	s.hide(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	})
	state := f.windows[1]
	state.visible = false
	f.windows[1] = state
	clock.tick()
	var restoreError error
	s.restore(func(err error) { restoreError = err })
	for range 3 {
		clock.tick()
	}
	if restoreError == nil || len(f.markers) != 1 || len(s.changed) != 1 {
		t.Fatal("failed restoration lost the original identity")
	}
	completed := false
	s.restore(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		completed = true
	})
	state.visible = true
	f.windows[1] = state
	clock.tick()
	if !completed || len(f.markers) != 0 || len(s.changed) != 0 || !reflect.DeepEqual(f.requests, [][2]uintptr{{1, 0}, {1, 8}, {1, 8}}) {
		t.Fatalf("retry did not restore/release original window: requests=%v markers=%v changed=%v", f.requests, f.markers, s.changed)
	}
}
