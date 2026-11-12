//go:build windows

package desktop

import (
	"context"
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var systemParametersInfo = windows.NewLazySystemDLL("user32.dll").NewProc("SystemParametersInfoW")

// SystemParametersInfoW acknowledges the change synchronously. UPDATEINIFILE
// persists it in the user profile and SENDCHANGE notifies desktop windows.
// https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-systemparametersinfow
func setWindowsWallpaper(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.New("壁纸文件路径无效")
	}
	if err := systemParametersInfo.Find(); err != nil {
		return actionError{"系统不提供 Windows 壁纸接口", ErrUnsupported}
	}
	const setDesktopWallpaper, updateProfile, sendChange = 0x0014, 0x0001, 0x0002
	result, _, _ := systemParametersInfo.Call(setDesktopWallpaper, 0, uintptr(unsafe.Pointer(value)), updateProfile|sendChange)
	runtime.KeepAlive(value)
	if result == 0 {
		return errors.New("Windows 未能应用桌面壁纸，请检查文件格式和系统策略")
	}
	return nil
}
