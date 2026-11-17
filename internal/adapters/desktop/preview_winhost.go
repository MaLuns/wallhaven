package desktop

import (
	"errors"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

// Explorer's desktop hierarchy is not a public API. Probe its shape rather
// than guessing from a Windows version, and fail before hiding other apps.
// Keep the selection and pixel bounds checks injectable on every test host.
type windowsPreviewRect struct{ left, top, right, bottom int32 }

type windowsPreviewHost struct {
	parent, after uintptr
}

type windowsPreviewDesktop struct {
	find       func(parent, after uintptr, class string) uintptr
	windows    func() ([]uintptr, error)
	spawn      func(uintptr) error
	processID  func(uintptr) uint32
	visible    func(uintptr) bool
	modern     func(uintptr) bool
	sameDPI    func(window, parent uintptr) bool
	windowRect func(uintptr) (windowsPreviewRect, error)
	clientRect func(uintptr) (windowsPreviewRect, error)
	attach     func(window uintptr, host windowsPreviewHost, screen windowsPreviewRect) error
}

func (d *windowsPreviewDesktop) host() (windowsPreviewHost, error) {
	unsupported := errors.New(locale.T("未找到可用的 Windows 桌面预览层级，请检查资源管理器是否正常运行"))
	progman := d.find(0, 0, "Progman")
	pid := d.processID(progman)
	if progman == 0 || pid == 0 || !d.visible(progman) {
		return windowsPreviewHost{}, unsupported
	}
	if err := d.spawn(progman); err != nil {
		return windowsPreviewHost{}, err
	}
	if d.modern(progman) {
		icons := d.find(progman, 0, "SHELLDLL_DefView")
		// On the newer composition desktop, the preview is a Progman child
		// between its icon view and its wallpaper WorkerW. Never reorder or
		// hide Explorer's own windows to manufacture a compatible hierarchy.
		wallpaper := d.find(progman, icons, "WorkerW")
		if icons != 0 && wallpaper != 0 && d.processID(icons) == pid && d.processID(wallpaper) == pid && d.visible(wallpaper) {
			return windowsPreviewHost{parent: progman, after: icons}, nil
		}
		return windowsPreviewHost{}, unsupported
	}
	windows, err := d.windows()
	if err != nil {
		return windowsPreviewHost{}, err
	}
	for _, window := range windows {
		icons := d.find(window, 0, "SHELLDLL_DefView")
		if d.processID(window) != pid || icons == 0 || d.processID(icons) != pid {
			continue
		}
		worker := d.find(0, window, "WorkerW")
		if worker != 0 && d.processID(worker) == pid && d.visible(worker) && d.find(worker, 0, "SHELLDLL_DefView") == 0 {
			return windowsPreviewHost{parent: worker}, nil
		}
	}
	return windowsPreviewHost{}, unsupported
}

func (d *windowsPreviewDesktop) prepare(window uintptr) error {
	if window == 0 {
		return errors.New(locale.T("桌面预览窗口不可用"))
	}
	host, err := d.host()
	if err != nil {
		return err
	}
	return d.prepareAt(window, host)
}

func (d *windowsPreviewDesktop) prepareAt(window uintptr, host windowsPreviewHost) error {
	// Cross-process SetParent with different DPI contexts resets the child's
	// process awareness. Refuse that operation instead of affecting the app.
	if !d.sameDPI(window, host.parent) {
		return errors.New(locale.T("Windows 桌面与应用的 DPI 模式不兼容，无法安全开启桌面预览"))
	}
	screen, err := d.windowRect(window)
	if err != nil {
		return err
	}
	parent, err := d.clientRect(host.parent)
	if err != nil {
		return err
	}
	if screen.right <= screen.left || screen.bottom <= screen.top || screen.left < parent.left || screen.top < parent.top || screen.right > parent.right || screen.bottom > parent.bottom {
		return errors.New(locale.T("Windows 桌面预览层级未覆盖当前屏幕"))
	}
	return d.attach(window, host, screen)
}
