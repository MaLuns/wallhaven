//go:build windows

package desktop

import (
	"crypto/rand"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

var windowsPreviewAppsUser32 = windows.NewLazySystemDLL("user32.dll")

var (
	windowsPreviewAppsIsWindow         = windowsPreviewAppsUser32.NewProc("IsWindow")
	windowsPreviewAppsIsWindowVisible  = windowsPreviewAppsUser32.NewProc("IsWindowVisible")
	windowsPreviewAppsIsIconic         = windowsPreviewAppsUser32.NewProc("IsIconic")
	windowsPreviewAppsGetWindowLong    = windowsPreviewAppsUser32.NewProc("GetWindowLongW")
	windowsPreviewAppsGetClassName     = windowsPreviewAppsUser32.NewProc("GetClassNameW")
	windowsPreviewAppsGetWindowText    = windowsPreviewAppsUser32.NewProc("GetWindowTextW")
	windowsPreviewAppsGetShellWindow   = windowsPreviewAppsUser32.NewProc("GetShellWindow")
	windowsPreviewAppsGetDesktopWindow = windowsPreviewAppsUser32.NewProc("GetDesktopWindow")
	windowsPreviewAppsSetProp          = windowsPreviewAppsUser32.NewProc("SetPropW")
	windowsPreviewAppsGetProp          = windowsPreviewAppsUser32.NewProc("GetPropW")
	windowsPreviewAppsRemoveProp       = windowsPreviewAppsUser32.NewProc("RemovePropW")
	windowsPreviewAppsShowWindowAsync  = windowsPreviewAppsUser32.NewProc("ShowWindowAsync")
)

func snapshotWindowsPreviewApplications() ([]previewApplication, error) {
	api := &windowsPreviewAppsAPI{process: windows.GetCurrentProcessId()}
	api.enumerate = func() ([]uintptr, error) {
		handles, err := enumerateWindowsPreviewHandles()
		if err != nil {
			if err == windows.ERROR_SUCCESS {
				// Match the fallback in windows.EnumWindows when the call sets no error.
				err = syscall.EINVAL
			}
			return nil, fmt.Errorf("%s: %w", locale.T("无法读取正在运行的应用"), err)
		}
		return handles, nil
	}
	api.state = windowsPreviewAppState
	api.inspect = func(handle uintptr) (windowsPreviewWindow, error) {
		window := windowsPreviewAppState(handle)
		// Read the style DWORDs using GetWindowLongW on both 32/64 bit;
		// these indexes contain flags, never pointers.
		const styleIndex, exStyleIndex = ^uintptr(15), ^uintptr(19) // GWL_STYLE=-16, GWL_EXSTYLE=-20
		style, _, _ := windowsPreviewAppsGetWindowLong.Call(handle, styleIndex)
		exStyle, _, _ := windowsPreviewAppsGetWindowLong.Call(handle, exStyleIndex)
		window.style, window.exStyle = uint32(style), uint32(exStyle)
		shell, _, _ := windowsPreviewAppsGetShellWindow.Call()
		desktop, _, _ := windowsPreviewAppsGetDesktopWindow.Call()
		window.shell = handle == shell || handle == desktop
		if !window.eligible(api.process) {
			return window, nil
		}
		var class [256]uint16
		count, _, err := windowsPreviewAppsGetClassName.Call(handle, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
		if count == 0 {
			if windowsPreviewAppState(handle).handle == 0 {
				return windowsPreviewWindow{}, nil
			}
			return windowsPreviewWindow{}, fmt.Errorf(locale.T("无法读取应用窗口状态：%w"), err)
		}
		window.class = windows.UTF16ToString(class[:])
		if !window.eligible(api.process) {
			return window, nil
		}
		// Windows on other virtual desktops can remain WS_VISIBLE. Do not
		// hide them or change the state they will have when switched to.
		const dwmwaCloaked = 14
		var cloaked uint32
		if err := windows.DwmGetWindowAttribute(windows.HWND(handle), dwmwaCloaked, unsafe.Pointer(&cloaked), uint32(unsafe.Sizeof(cloaked))); err != nil {
			if windowsPreviewAppState(handle).handle == 0 {
				return windowsPreviewWindow{}, nil
			}
			return windowsPreviewWindow{}, fmt.Errorf(locale.T("无法读取应用窗口状态：%w"), err)
		}
		window.cloaked = cloaked != 0
		var title [512]uint16
		windowsPreviewAppsGetWindowText.Call(handle, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		window.name = windows.UTF16ToString(title[:])
		return window, nil
	}
	// Each snapshot uses its own property name, rather than only a PID/TID:
	// HWNDs and both identifiers may be reused during a preview session.
	// The value is an integer tag, not a Go pointer or an owned OS handle.
	// https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-setpropw
	api.mark = func(handle uintptr, marker string) error {
		key, err := windows.UTF16PtrFromString(marker)
		if err != nil {
			return err
		}
		result, _, err := windowsPreviewAppsSetProp.Call(handle, uintptr(unsafe.Pointer(key)), 1)
		runtime.KeepAlive(key)
		if result == 0 {
			return err
		}
		return nil
	}
	api.marked = func(handle uintptr, marker string) bool {
		key, _ := windows.UTF16PtrFromString(marker)
		result, _, _ := windowsPreviewAppsGetProp.Call(handle, uintptr(unsafe.Pointer(key)))
		runtime.KeepAlive(key)
		return result == 1
	}
	api.unmark = func(handle uintptr, marker string) {
		key, _ := windows.UTF16PtrFromString(marker)
		windowsPreviewAppsRemoveProp.Call(handle, uintptr(unsafe.Pointer(key)))
		runtime.KeepAlive(key)
	}
	// ShowWindowAsync posts to the target UI queue instead of waiting for
	// another application's window procedure on our UI thread.
	// https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-showwindowasync
	api.showAsync = func(handle uintptr, command int) {
		windowsPreviewAppsShowWindowAsync.Call(handle, uintptr(command))
	}
	return snapshotWindowsPreviewApps(api, "MyGOWallhaven.DesktopPreview."+rand.Text())
}

func windowsPreviewAppState(handle uintptr) windowsPreviewWindow {
	exists, _, _ := windowsPreviewAppsIsWindow.Call(handle)
	if exists == 0 {
		return windowsPreviewWindow{}
	}
	var process uint32
	thread, err := windows.GetWindowThreadProcessId(windows.HWND(handle), &process)
	if err != nil || thread == 0 || process == 0 {
		return windowsPreviewWindow{}
	}
	visible, _, _ := windowsPreviewAppsIsWindowVisible.Call(handle)
	minimized, _, _ := windowsPreviewAppsIsIconic.Call(handle)
	return windowsPreviewWindow{handle: handle, process: process, thread: thread, visible: visible != 0, minimized: minimized != 0}
}
