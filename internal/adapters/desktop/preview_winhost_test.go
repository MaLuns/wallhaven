package desktop

import (
	"errors"
	"reflect"
	"testing"
)

const (
	previewTestProgman   = uintptr(10)
	previewTestIconsHost = uintptr(20)
	previewTestIcons     = uintptr(21)
	previewTestWorker    = uintptr(30)
	previewTestWindow    = uintptr(50)
)

type previewHostWindow struct {
	class   string
	pid     uint32
	visible bool
}

type previewHostAttachment struct {
	window uintptr
	host   windowsPreviewHost
	rect   windowsPreviewRect
}

type previewHostFixture struct {
	desktop                                                             windowsPreviewDesktop
	nodes                                                               map[uintptr]previewHostWindow
	children                                                            map[uintptr][]uintptr
	spawned                                                             []uintptr
	attachments                                                         []previewHostAttachment
	modern, compatibleDPI                                               bool
	spawnError, enumerationError, windowError, clientError, attachError error
	screen, parent                                                      windowsPreviewRect
	windowReads, clientReads                                            int
	dpiWindows                                                          []uintptr
}

func newPreviewHostFixture(modern bool) *previewHostFixture {
	f := &previewHostFixture{
		nodes: map[uintptr]previewHostWindow{
			previewTestProgman:   {class: "Progman", pid: 100, visible: true},
			previewTestIconsHost: {class: "WorkerW", pid: 100, visible: true},
			previewTestIcons:     {class: "SHELLDLL_DefView", pid: 100, visible: true},
			previewTestWorker:    {class: "WorkerW", pid: 100, visible: true},
		},
		children: make(map[uintptr][]uintptr),
		modern:   modern, compatibleDPI: true,
		// Native rectangles deliberately use a negative secondary monitor
		// origin and physical dimensions, independent of any logical DPI.
		screen: windowsPreviewRect{-2560, -1440, 0, 0},
		parent: windowsPreviewRect{-2560, -1440, 3840, 2160},
	}
	if modern {
		f.children[0] = []uintptr{previewTestProgman}
		f.children[previewTestProgman] = []uintptr{previewTestIcons, previewTestWorker}
	} else {
		f.children[0] = []uintptr{previewTestIconsHost, previewTestWorker, previewTestProgman}
		f.children[previewTestIconsHost] = []uintptr{previewTestIcons}
	}
	f.desktop = windowsPreviewDesktop{
		find: func(parent, after uintptr, class string) uintptr {
			remaining := f.children[parent]
			if after != 0 {
				for len(remaining) != 0 && remaining[0] != after {
					remaining = remaining[1:]
				}
				if len(remaining) == 0 {
					return 0
				}
				remaining = remaining[1:]
			}
			for _, handle := range remaining {
				if f.nodes[handle].class == class {
					return handle
				}
			}
			return 0
		},
		windows: func() ([]uintptr, error) {
			return append([]uintptr(nil), f.children[0]...), f.enumerationError
		},
		spawn: func(window uintptr) error {
			f.spawned = append(f.spawned, window)
			return f.spawnError
		},
		processID: func(window uintptr) uint32 { return f.nodes[window].pid },
		visible:   func(window uintptr) bool { return f.nodes[window].visible },
		modern:    func(uintptr) bool { return f.modern },
		sameDPI: func(window, parent uintptr) bool {
			f.dpiWindows = []uintptr{window, parent}
			return f.compatibleDPI
		},
		windowRect: func(window uintptr) (windowsPreviewRect, error) {
			if window != previewTestWindow {
				panic("read unexpected preview window")
			}
			f.windowReads++
			return f.screen, f.windowError
		},
		clientRect: func(uintptr) (windowsPreviewRect, error) {
			f.clientReads++
			return f.parent, f.clientError
		},
		attach: func(window uintptr, host windowsPreviewHost, screen windowsPreviewRect) error {
			f.attachments = append(f.attachments, previewHostAttachment{window, host, screen})
			return f.attachError
		},
	}
	return f
}

func TestWindowsPreviewDesktopSelectsExistingShellHierarchy(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "classic WorkerW"
		want := windowsPreviewHost{parent: previewTestWorker}
		if modern {
			name = "layered Progman"
			want = windowsPreviewHost{parent: previewTestProgman, after: previewTestIcons}
		}
		t.Run(name, func(t *testing.T) {
			f := newPreviewHostFixture(modern)
			before := make(map[uintptr][]uintptr)
			for parent, children := range f.children {
				before[parent] = append([]uintptr(nil), children...)
			}
			host, err := f.desktop.host()
			if err != nil || host != want {
				t.Fatalf("host = %+v, %v; want %+v", host, err, want)
			}
			if !reflect.DeepEqual(f.spawned, []uintptr{previewTestProgman}) {
				t.Fatalf("spawn targets = %v", f.spawned)
			}
			if len(f.attachments) != 0 || !reflect.DeepEqual(before, f.children) {
				t.Fatal("host discovery attached a window or changed shell ordering")
			}
		})
	}
	t.Run("classic Progman icon host", func(t *testing.T) {
		f := newPreviewHostFixture(false)
		f.children[0] = []uintptr{previewTestProgman, previewTestWorker}
		f.children[previewTestProgman] = []uintptr{previewTestIcons}
		host, err := f.desktop.host()
		if err != nil || host != (windowsPreviewHost{parent: previewTestWorker}) {
			t.Fatalf("host = %+v, %v", host, err)
		}
	})
	t.Run("modern ignores legacy enumeration", func(t *testing.T) {
		f := newPreviewHostFixture(true)
		f.enumerationError = errors.New("legacy enumeration must not be used")
		if _, err := f.desktop.host(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWindowsPreviewDesktopRejectsMissingOrForeignShellLayers(t *testing.T) {
	cases := []struct {
		name   string
		modern bool
		change func(*previewHostFixture)
	}{
		{"no Explorer", false, func(f *previewHostFixture) { delete(f.nodes, previewTestProgman) }},
		{"unknown Explorer identity", false, func(f *previewHostFixture) {
			n := f.nodes[previewTestProgman]
			n.pid = 0
			f.nodes[previewTestProgman] = n
		}},
		{"hidden Explorer", false, func(f *previewHostFixture) {
			n := f.nodes[previewTestProgman]
			n.visible = false
			f.nodes[previewTestProgman] = n
		}},
		{"classic missing icon view", false, func(f *previewHostFixture) { delete(f.children, previewTestIconsHost) }},
		{"classic foreign icon host", false, func(f *previewHostFixture) {
			n := f.nodes[previewTestIconsHost]
			n.pid++
			f.nodes[previewTestIconsHost] = n
		}},
		{"classic foreign icon view", false, func(f *previewHostFixture) { n := f.nodes[previewTestIcons]; n.pid++; f.nodes[previewTestIcons] = n }},
		{"classic missing WorkerW", false, func(f *previewHostFixture) { f.children[0] = []uintptr{previewTestIconsHost, previewTestProgman} }},
		{"classic foreign WorkerW", false, func(f *previewHostFixture) { n := f.nodes[previewTestWorker]; n.pid++; f.nodes[previewTestWorker] = n }},
		{"classic hidden WorkerW", false, func(f *previewHostFixture) {
			n := f.nodes[previewTestWorker]
			n.visible = false
			f.nodes[previewTestWorker] = n
		}},
		{"classic WorkerW contains icons", false, func(f *previewHostFixture) { f.children[previewTestWorker] = []uintptr{previewTestIcons} }},
		{"modern missing icon view", true, func(f *previewHostFixture) { f.children[previewTestProgman] = []uintptr{previewTestWorker} }},
		{"modern foreign icon view", true, func(f *previewHostFixture) { n := f.nodes[previewTestIcons]; n.pid++; f.nodes[previewTestIcons] = n }},
		{"modern missing WorkerW", true, func(f *previewHostFixture) { f.children[previewTestProgman] = []uintptr{previewTestIcons} }},
		{"modern foreign WorkerW", true, func(f *previewHostFixture) { n := f.nodes[previewTestWorker]; n.pid++; f.nodes[previewTestWorker] = n }},
		{"modern wallpaper above icons", true, func(f *previewHostFixture) {
			f.children[previewTestProgman] = []uintptr{previewTestWorker, previewTestIcons}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newPreviewHostFixture(tt.modern)
			tt.change(f)
			if err := f.desktop.prepare(previewTestWindow); err == nil {
				t.Fatal("incompatible shell hierarchy was accepted")
			}
			if len(f.attachments) != 0 || f.windowReads != 0 {
				t.Fatal("failed host discovery continued to native attachment")
			}
		})
	}
}

func TestWindowsPreviewDesktopPreservesNativePixelRectangle(t *testing.T) {
	for _, modern := range []bool{false, true} {
		f := newPreviewHostFixture(modern)
		if err := f.desktop.prepare(previewTestWindow); err != nil {
			t.Fatal(err)
		}
		parent := previewTestWorker
		if modern {
			parent = previewTestProgman
		}
		if !reflect.DeepEqual(f.dpiWindows, []uintptr{previewTestWindow, parent}) {
			t.Fatalf("DPI contexts = %v", f.dpiWindows)
		}
		if len(f.attachments) != 1 {
			t.Fatalf("attachments = %v", f.attachments)
		}
		attachment := f.attachments[0]
		if attachment.window != previewTestWindow || attachment.host.parent != parent || attachment.rect != f.screen {
			t.Fatalf("physical screen rectangle or attachment target changed: %+v", attachment)
		}
		if f.windowReads != 1 || f.clientReads != 1 {
			t.Fatal("native bounds were not checked before attachment")
		}
	}
}

func TestWindowsPreviewDesktopRejectsUnsafeBoundsAndDPI(t *testing.T) {
	cases := []struct {
		name   string
		change func(*previewHostFixture)
	}{
		{"DPI context mismatch", func(f *previewHostFixture) { f.compatibleDPI = false }},
		{"empty width", func(f *previewHostFixture) { f.screen.right = f.screen.left }},
		{"negative height", func(f *previewHostFixture) { f.screen.bottom = f.screen.top - 1 }},
		{"left outside parent", func(f *previewHostFixture) { f.parent.left++ }},
		{"top outside parent", func(f *previewHostFixture) { f.parent.top++ }},
		{"right outside parent", func(f *previewHostFixture) { f.parent.right = -1 }},
		{"bottom outside parent", func(f *previewHostFixture) { f.parent.bottom = -1 }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newPreviewHostFixture(false)
			tt.change(f)
			if err := f.desktop.prepare(previewTestWindow); err == nil {
				t.Fatal("unsafe attachment was accepted")
			}
			if len(f.attachments) != 0 {
				t.Fatal("unsafe window was reparented")
			}
			if !f.compatibleDPI && (f.windowReads != 0 || f.clientReads != 0) {
				t.Fatal("DPI rejection did not stop before native placement")
			}
		})
	}
	// Exact containment is valid even with negative screen coordinates.
	f := newPreviewHostFixture(false)
	f.parent = f.screen
	if err := f.desktop.prepare(previewTestWindow); err != nil {
		t.Fatalf("matching native bounds rejected: %v", err)
	}
}

func TestWindowsPreviewDesktopPropagatesNativeFailures(t *testing.T) {
	cases := []struct {
		name        string
		set         func(*previewHostFixture, error)
		attachments int
	}{
		{"spawn timeout", func(f *previewHostFixture, err error) { f.spawnError = err }, 0},
		{"window enumeration", func(f *previewHostFixture, err error) { f.enumerationError = err }, 0},
		{"preview bounds", func(f *previewHostFixture, err error) { f.windowError = err }, 0},
		{"host client bounds", func(f *previewHostFixture, err error) { f.clientError = err }, 0},
		{"native attachment", func(f *previewHostFixture, err error) { f.attachError = err }, 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newPreviewHostFixture(false)
			want := errors.New(tt.name)
			tt.set(f, want)
			if err := f.desktop.prepare(previewTestWindow); !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			if len(f.attachments) != tt.attachments {
				t.Fatalf("attachments = %d, want %d", len(f.attachments), tt.attachments)
			}
		})
	}
	f := newPreviewHostFixture(false)
	if err := f.desktop.prepare(0); err == nil || len(f.spawned) != 0 {
		t.Fatal("invalid native handle reached the shell")
	}
}
