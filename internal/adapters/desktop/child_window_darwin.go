//go:build darwin

package desktop

import (
	"errors"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

type darwinSettingsChild struct{}

// AttachSettingsChild supplements MyGO's logical Parent with an AppKit child
// attachment. It changes neither modal interaction nor the global window level.
// Call once per settings window; cleanup is safe from any goroutine. An optional
// onError receives a later show/restore attachment failure on the UI thread.
// New Content windows must use Hidden and Show from OnReadyToShow: MyGO's public
// native-window getters otherwise settle a pending initial show immediately.
func AttachSettingsChild(parent, child *mygo.Window, onError ...func(error)) (cleanup func(), err error) {
	cleanup = func() {}
	if parent == nil || child == nil || parent == child || child.Parent() != parent {
		return cleanup, errors.New(locale.T("设置窗口或主窗口不可用"))
	}
	var binding *settingsChildBinding
	var report func(error)
	if len(onError) > 0 {
		report = onError[0]
	}
	mygo.RunOnMain(func() { binding, err = bindSettingsChild(parent, child, darwinSettingsChild{}, report) })
	if err != nil {
		return cleanup, err
	}
	if binding == nil {
		return cleanup, errors.New(locale.T("设置窗口或主窗口不可用"))
	}
	return func() { mygo.RunOnMain(func() { binding.dispose(true) }) }, nil
}

func (darwinSettingsChild) attach(parent, child uintptr) error {
	parentWindow, childWindow := objc.ID(parent), objc.ID(child)
	parentSelector := objc.RegisterName("parentWindow")
	current := objc.Send[objc.ID](childWindow, parentSelector)
	if current == parentWindow {
		return nil
	}
	if current != 0 {
		return errors.New(locale.T("设置窗口已关联其他父窗口"))
	}
	// NSWindowAbove keeps this relative ordering through subsequent clicks and
	// ordering operations, without a focus callback or deferred restacking.
	objc.Send[uintptr](parentWindow, objc.RegisterName("addChildWindow:ordered:"), childWindow, int64(1))
	if objc.Send[objc.ID](childWindow, parentSelector) != parentWindow {
		return errors.New(locale.T("无法将设置窗口关联到主窗口"))
	}
	return nil
}

func (darwinSettingsChild) detach(parent, child uintptr) {
	parentWindow, childWindow := objc.ID(parent), objc.ID(child)
	if objc.Send[objc.ID](childWindow, objc.RegisterName("parentWindow")) == parentWindow {
		objc.Send[uintptr](parentWindow, objc.RegisterName("removeChildWindow:"), childWindow)
	}
}
