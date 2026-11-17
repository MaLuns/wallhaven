package desktop

import (
	"fmt"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

// Keep selection and lifetime checks independent of Win32 so that rollback,
// closed windows and reused HWNDs can be exercised on every development host.
type windowsPreviewWindow struct {
	handle                      uintptr
	process, thread             uint32
	name, class                 string
	style, exStyle              uint32
	visible, minimized, cloaked bool
	shell                       bool
}

const (
	windowsPreviewChild      = uint32(0x40000000)
	windowsPreviewToolWindow = uint32(0x00000080)
	windowsPreviewNoActivate = uint32(0x08000000)
	windowsPreviewHide       = 0
	windowsPreviewShowNA     = 8
)

func (w windowsPreviewWindow) eligible(process uint32) bool {
	if w.handle == 0 || w.process == 0 || w.thread == 0 || w.process == process ||
		!w.visible || w.minimized || w.cloaked || w.shell ||
		w.style&windowsPreviewChild != 0 || w.exStyle&(windowsPreviewToolWindow|windowsPreviewNoActivate) != 0 {
		return false
	}
	switch w.class {
	case "Progman", "WorkerW", "Shell_TrayWnd", "Shell_SecondaryTrayWnd":
		return false
	}
	return true
}

type windowsPreviewAppsAPI struct {
	process   uint32
	enumerate func() ([]uintptr, error)
	inspect   func(uintptr) (windowsPreviewWindow, error)
	state     func(uintptr) windowsPreviewWindow
	mark      func(uintptr, string) error
	marked    func(uintptr, string) bool
	unmark    func(uintptr, string)
	showAsync func(uintptr, int)
}

type windowsPreviewApplication struct {
	api                    *windowsPreviewAppsAPI
	window                 windowsPreviewWindow
	marker                 string
	hideRequested, ignored bool
	released               bool
}

func snapshotWindowsPreviewApps(api *windowsPreviewAppsAPI, marker string) ([]previewApplication, error) {
	handles, err := api.enumerate()
	if err != nil {
		return nil, err
	}
	var applications []previewApplication
	rollback := func() {
		for _, application := range applications {
			application.Release()
		}
	}
	for _, handle := range handles {
		window, err := api.inspect(handle)
		if err != nil {
			rollback()
			return nil, err
		}
		if !window.eligible(api.process) {
			continue
		}
		application := &windowsPreviewApplication{api: api, window: window, marker: marker}
		if !application.sameWindow(api.state(handle)) {
			continue
		}
		if err := api.mark(handle, marker); err != nil {
			// A window may close during enumeration. A live unmarkable one
			// (including a higher-integrity app rejected by UIPI) must fail
			// before any other application is hidden.
			if !application.sameWindow(api.state(handle)) {
				continue
			}
			rollback()
			return nil, fmt.Errorf(locale.T("无法安全隐藏应用窗口 %s，请关闭该窗口或检查权限：%w"), application.Name(), err)
		}
		if application.Terminated() {
			application.Release()
			continue
		}
		applications = append(applications, application)
	}
	// Owned dialogs are separate top-level windows and are retained too.
	// Windows guarantees automatic hiding for a minimized owner, whereas
	// this temporary mode uses SW_HIDE and leaves placement unchanged.
	return applications, nil
}

func (a *windowsPreviewApplication) Name() string {
	if a.window.name != "" {
		return a.window.name
	}
	return locale.T("未命名应用")
}

func (a *windowsPreviewApplication) sameWindow(window windowsPreviewWindow) bool {
	return window.handle == a.window.handle && window.process == a.window.process && window.thread == a.window.thread
}

func (a *windowsPreviewApplication) Terminated() bool {
	return a.released || a.ignored || !a.sameWindow(a.api.state(a.window.handle)) || !a.api.marked(a.window.handle, a.marker)
}

func (a *windowsPreviewApplication) Hidden() bool {
	state := a.api.state(a.window.handle)
	// A window minimized between the snapshot and hide should be left alone.
	return !state.visible || !a.hideRequested && state.minimized
}

func (a *windowsPreviewApplication) Hide() {
	if a.Terminated() {
		return
	}
	if a.api.state(a.window.handle).minimized {
		a.ignored = true
		return
	}
	a.hideRequested = true
	a.api.showAsync(a.window.handle, windowsPreviewHide)
}

func (a *windowsPreviewApplication) Unhide() {
	if !a.hideRequested || a.Terminated() {
		return
	}
	// Always queue the inverse, even when an earlier asynchronous hide has
	// not yet become observable. SW_SHOWNA preserves size/maximized state
	// without taking focus; SW_SHOWNOACTIVATE would restore a maximized app.
	a.api.showAsync(a.window.handle, windowsPreviewShowNA)
}

func (a *windowsPreviewApplication) Release() {
	if a.released {
		return
	}
	// The unique property belongs to this session; never remove a different
	// marker from a replacement window that reused the same HWND.
	if a.api.marked(a.window.handle, a.marker) {
		a.api.unmark(a.window.handle, a.marker)
	}
	a.released = true
}
