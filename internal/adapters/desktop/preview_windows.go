//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

var previewWinUser32 = windows.NewLazySystemDLL("user32.dll")
var (
	previewWinFindWindow     = previewWinUser32.NewProc("FindWindowExW")
	previewWinEnumWindows    = previewWinUser32.NewProc("EnumWindows")
	previewWinProcessID      = previewWinUser32.NewProc("GetWindowThreadProcessId")
	previewWinVisible        = previewWinUser32.NewProc("IsWindowVisible")
	previewWinSendTimeout    = previewWinUser32.NewProc("SendMessageTimeoutW")
	previewWinGetLong        = previewWinUser32.NewProc("GetWindowLongW")
	previewWinSetLong        = previewWinUser32.NewProc("SetWindowLongW")
	previewWinGetParent      = previewWinUser32.NewProc("GetParent")
	previewWinSetParent      = previewWinUser32.NewProc("SetParent")
	previewWinGetRect        = previewWinUser32.NewProc("GetWindowRect")
	previewWinGetClientRect  = previewWinUser32.NewProc("GetClientRect")
	previewWinClientToScreen = previewWinUser32.NewProc("ClientToScreen")
	previewWinSetPos         = previewWinUser32.NewProc("SetWindowPos")
	previewWinGetWindow      = previewWinUser32.NewProc("GetWindow")
	previewWinLayered        = previewWinUser32.NewProc("SetLayeredWindowAttributes")
	previewWinDPIContext     = previewWinUser32.NewProc("GetWindowDpiAwarenessContext")
	previewWinEqualDPI       = previewWinUser32.NewProc("AreDpiAwarenessContextsEqual")
	previewWinDPIAwareness   = previewWinUser32.NewProc("GetAwarenessFromDpiAwarenessContext")
	previewWinSetThreadDPI   = previewWinUser32.NewProc("SetThreadDpiAwarenessContext")
)

type windowsPreviewPlatform struct {
	desktop    windowsPreviewDesktop
	visibility previewVisibility
	hosts      map[uintptr]windowsPreviewHost
}

func newPreviewPlatform(after func(time.Duration, func()) func()) previewPlatform {
	return &windowsPreviewPlatform{
		hosts: make(map[uintptr]windowsPreviewHost),
		desktop: windowsPreviewDesktop{
			find: previewWindowsFind, windows: previewWindowsEnumerate,
			spawn: previewWindowsSpawn, processID: previewWindowsProcessID,
			visible: func(window uintptr) bool { r, _, _ := previewWinVisible.Call(window); return r != 0 },
			modern:  func(window uintptr) bool { return previewWindowsLong(window, -20)&0x00200000 != 0 }, // WS_EX_NOREDIRECTIONBITMAP
			sameDPI: previewWindowsSameDPI, windowRect: previewWindowsRect,
			clientRect: previewWindowsClientRect, attach: previewWindowsAttach,
		},
		visibility: previewVisibility{snapshot: snapshotWindowsPreviewApplications, after: after, now: time.Now, timeout: 4 * time.Second},
	}
}

func (*windowsPreviewPlatform) available() bool { return true }
func (p *windowsPreviewPlatform) prepareWindow(window uintptr, _ mygo.Rectangle) error {
	host, ok := p.hosts[window]
	if !ok {
		return errors.New(locale.T("桌面预览窗口不可用"))
	}
	return p.desktop.prepareAt(window, host)
}
func (p *windowsPreviewPlatform) hideApplications(done func(error)) { p.visibility.hide(done) }
func (p *windowsPreviewPlatform) restoreApplications(done func(error)) {
	clear(p.hosts)
	p.visibility.restore(done)
}

func (p *windowsPreviewPlatform) newBackground(options mygo.WindowOptions) (window *mygo.Window, err error) {
	mygo.RunOnMain(func() {
		host, hostErr := p.desktop.host()
		if hostErr != nil {
			err = hostErr
			return
		}
		if previewWinDPIContext.Find() != nil || previewWinDPIAwareness.Find() != nil || previewWinSetThreadDPI.Find() != nil {
			err = errors.New(locale.T("Windows 桌面与应用的 DPI 模式不兼容，无法安全开启桌面预览"))
			return
		}
		context, _, _ := previewWinDPIContext.Call(host.parent)
		awareness, _, _ := previewWinDPIAwareness.Call(context)
		if context == 0 || awareness != 2 { // DPI_AWARENESS_PER_MONITOR_AWARE
			err = errors.New(locale.T("Windows 桌面与应用的 DPI 模式不兼容，无法安全开启桌面预览"))
			return
		}
		// Scope this window's DPI context on MyGO's UI thread. The process
		// and controls keep their original awareness, including when Explorer
		// uses PMv1 and MyGO uses PMv2. prepareAt verifies exact compatibility.
		previous, _, nativeErr := previewWinSetThreadDPI.Call(context)
		if previous == 0 {
			err = previewWindowsError("SetThreadDpiAwarenessContext", nativeErr)
			return
		}
		defer previewWinSetThreadDPI.Call(previous)
		window = mygo.NewWindow(options)
		if !window.IsDestroyed() {
			handle := window.NativeHandle()
			p.hosts[handle] = host
			window.OnClosed(func() { delete(p.hosts, handle) })
		}
	})
	if err == nil && (window == nil || window.IsDestroyed()) {
		err = errors.New(locale.T("桌面预览窗口不可用"))
	}
	return window, err
}

func (p *windowsPreviewPlatform) showBackground(window *mygo.Window) error {
	window.ShowInactive()
	host, ok := p.hosts[window.NativeHandle()]
	if !ok {
		return errors.New(locale.T("桌面预览窗口不可用"))
	}
	return previewWindowsOrder(window.NativeHandle(), host)
}

func previewWindowsError(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return fmt.Errorf("%s (%s)", locale.T("Windows 桌面预览窗口操作失败"), operation)
	}
	return fmt.Errorf("%s (%s): %w", locale.T("Windows 桌面预览窗口操作失败"), operation, err)
}

func previewWindowsFind(parent, after uintptr, class string) uintptr {
	value, _ := windows.UTF16PtrFromString(class)
	r, _, _ := previewWinFindWindow.Call(parent, after, uintptr(unsafe.Pointer(value)), 0)
	runtime.KeepAlive(value)
	return r
}

var previewWindowsEnumCallback = syscall.NewCallback(func(window, data uintptr) uintptr {
	result := (*[]uintptr)(unsafe.Pointer(data))
	*result = append(*result, window)
	return 1
})

func previewWindowsEnumerate() ([]uintptr, error) {
	var result []uintptr
	r, _, err := previewWinEnumWindows.Call(previewWindowsEnumCallback, uintptr(unsafe.Pointer(&result)))
	if r == 0 {
		return nil, previewWindowsError("EnumWindows", err)
	}
	return result, nil
}

func previewWindowsProcessID(window uintptr) uint32 {
	var pid uint32
	previewWinProcessID.Call(window, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// Explorer does not publish a desktop-surface API. This bounded request and
// both hierarchy probes follow Lively's SetupDesktopLayer implementation:
// https://github.com/lively-community/lively/blob/core-separation/src/Lively/Lively/Core/WinDesktopCore.cs
func previewWindowsSpawn(progman uintptr) error {
	var result uintptr
	const smtoAbortIfHung, smtoErrorOnExit = 2, 0x20
	r, _, err := previewWinSendTimeout.Call(progman, 0x052c, 0xd, 1, smtoAbortIfHung|smtoErrorOnExit, 1000, uintptr(unsafe.Pointer(&result)))
	if r == 0 {
		return previewWindowsError("SendMessageTimeoutW", err)
	}
	return nil
}

func previewWindowsLong(window uintptr, index int32) uint32 {
	r, _, _ := previewWinGetLong.Call(window, uintptr(index))
	return uint32(r)
}

func previewWindowsSetLong(window uintptr, index int32, value uint32) error {
	_, _, err := previewWinSetLong.Call(window, uintptr(index), uintptr(value))
	// A zero previous value is also success; validate the requested bits.
	if previewWindowsLong(window, index) != value {
		return previewWindowsError("SetWindowLongW", err)
	}
	return nil
}

func previewWindowsSameDPI(window, parent uintptr) bool {
	if previewWinDPIContext.Find() != nil || previewWinEqualDPI.Find() != nil {
		return false
	}
	childContext, _, _ := previewWinDPIContext.Call(window)
	parentContext, _, _ := previewWinDPIContext.Call(parent)
	if childContext == 0 || parentContext == 0 {
		return false
	}
	r, _, _ := previewWinEqualDPI.Call(childContext, parentContext)
	return r != 0
}

func previewWindowsRect(window uintptr) (windowsPreviewRect, error) {
	var rect windowsPreviewRect
	r, _, err := previewWinGetRect.Call(window, uintptr(unsafe.Pointer(&rect)))
	if r == 0 {
		return rect, previewWindowsError("GetWindowRect", err)
	}
	return rect, nil
}

type previewWindowsPoint struct{ x, y int32 }

func previewWindowsClientRect(window uintptr) (windowsPreviewRect, error) {
	var rect windowsPreviewRect
	r, _, err := previewWinGetClientRect.Call(window, uintptr(unsafe.Pointer(&rect)))
	if r == 0 {
		return rect, previewWindowsError("GetClientRect", err)
	}
	origin := previewWindowsPoint{rect.left, rect.top}
	r, _, err = previewWinClientToScreen.Call(window, uintptr(unsafe.Pointer(&origin)))
	if r == 0 {
		return rect, previewWindowsError("ClientToScreen", err)
	}
	return windowsPreviewRect{origin.x, origin.y, origin.x + rect.right - rect.left, origin.y + rect.bottom - rect.top}, nil
}

func previewWindowsAttach(window uintptr, host windowsPreviewHost, screen windowsPreviewRect) error {
	const wsChild, wsPopup, wsCaption, wsThickFrame = 0x40000000, 0x80000000, 0x00c00000, 0x00040000
	const wsExNoActivate, wsExToolWindow, wsExAppWindow, wsExLayered = 0x08000000, 0x80, 0x40000, 0x80000
	style := previewWindowsLong(window, -16)&^(wsPopup|wsCaption|wsThickFrame) | wsChild
	if err := previewWindowsSetLong(window, -16, style); err != nil {
		return err
	}
	ex := previewWindowsLong(window, -20)&^wsExAppWindow | wsExNoActivate | wsExToolWindow
	if host.after != 0 {
		ex |= wsExLayered
	}
	if err := previewWindowsSetLong(window, -20, ex); err != nil {
		return err
	}
	if host.after != 0 {
		r, _, err := previewWinLayered.Call(window, 0, 255, 2) // LWA_ALPHA, fully opaque
		if r == 0 {
			return previewWindowsError("SetLayeredWindowAttributes", err)
		}
	}
	_, _, err := previewWinSetParent.Call(window, host.parent)
	parent, _, _ := previewWinGetParent.Call(window)
	if parent != host.parent {
		return previewWindowsError("SetParent", err)
	}
	// GetWindowRect above reads the physical screen rectangle after MyGO has
	// resolved its DIP bounds. Convert to the parent's client coordinates;
	// never scale the virtual desktop origin or a negative screen coordinate.
	client, err := previewWindowsClientRect(host.parent)
	if err != nil {
		return err
	}
	const noActivate, frameChanged, noOwnerZOrder = 0x10, 0x20, 0x200
	r, _, err := previewWinSetPos.Call(window, host.after, uintptr(screen.left-client.left), uintptr(screen.top-client.top), uintptr(screen.right-screen.left), uintptr(screen.bottom-screen.top), noActivate|frameChanged|noOwnerZOrder)
	if r == 0 {
		return previewWindowsError("SetWindowPos", err)
	}
	actual, err := previewWindowsRect(window)
	if err != nil {
		return err
	}
	if actual != screen {
		return errors.New(locale.T("Windows 桌面预览窗口位置校验失败"))
	}
	return previewWindowsOrder(window, host)
}

func previewWindowsOrder(window uintptr, host windowsPreviewHost) error {
	parent, _, _ := previewWinGetParent.Call(window)
	if parent != host.parent {
		return errors.New(locale.T("Windows 桌面预览窗口层级校验失败"))
	}
	const noSize, noMove, noActivate, noOwnerZOrder = 1, 2, 0x10, 0x200
	r, _, err := previewWinSetPos.Call(window, host.after, 0, 0, 0, 0, noSize|noMove|noActivate|noOwnerZOrder)
	if r == 0 {
		return previewWindowsError("SetWindowPos", err)
	}
	if host.after != 0 {
		previous, _, _ := previewWinGetWindow.Call(window, 3) // GW_HWNDPREV
		if previous != host.after {
			return errors.New(locale.T("Windows 桌面预览窗口层级校验失败"))
		}
	}
	return nil
}
