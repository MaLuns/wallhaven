package desktop

import (
	"errors"

	"github.com/egoist/mygo"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

// settingsChildWindow is confined to the UI thread, including event callbacks.
type settingsChildWindow interface {
	NativeHandle() uintptr
	IsVisible() bool
	OnShow(func()) func()
	OnHide(func()) func()
	OnClose(func(*mygo.CloseEvent)) func()
	OnClosed(func()) func()
	OnRestore(func()) func()
	OnLeaveFullScreen(func()) func()
}

type settingsChildNative interface {
	attach(parent, child uintptr) error
	detach(parent, child uintptr)
}

type settingsChildBinding struct {
	parent, child settingsChildWindow
	native        settingsChildNative
	intendedShown bool
	closing       bool
	disposed      bool
	listeners     []func()
	onError       func(error)
}

func bindSettingsChild(parent, child settingsChildWindow, native settingsChildNative, onError func(error)) (*settingsChildBinding, error) {
	if parent.NativeHandle() == 0 || child.NativeHandle() == 0 || parent.NativeHandle() == child.NativeHandle() {
		return nil, errors.New(locale.T("设置窗口或主窗口不可用"))
	}
	binding := &settingsChildBinding{parent: parent, child: child, native: native, intendedShown: child.IsVisible(), onError: onError}
	// A new Content window must be created Hidden until OnReadyToShow. Its
	// OnShow then supplies the first attachment without exposing an empty frame.
	if binding.intendedShown {
		if err := binding.attach(); err != nil {
			return nil, err
		}
	}
	binding.listeners = []func(){
		child.OnShow(func() {
			if binding.disposed {
				return
			}
			binding.intendedShown = true
			binding.restore()
		}),
		child.OnHide(func() {
			if binding.disposed {
				return
			}
			binding.intendedShown = false
			binding.detach()
		}),
		child.OnClosed(func() { binding.dispose(false) }),
		parent.OnShow(binding.restore),
		parent.OnRestore(binding.restore),
		parent.OnLeaveFullScreen(binding.restore),
		parent.OnClose(func(event *mygo.CloseEvent) {
			if !binding.disposed && !event.DefaultPrevented() {
				binding.closing = true
				binding.detach()
			}
		}),
		parent.OnClosed(func() { binding.dispose(false) }),
	}
	return binding, nil
}

func (b *settingsChildBinding) attach() error {
	parent, child := b.parent.NativeHandle(), b.child.NativeHandle()
	if parent == 0 || child == 0 {
		return errors.New(locale.T("设置窗口或主窗口不可用"))
	}
	return b.native.attach(parent, child)
}

func (b *settingsChildBinding) restore() {
	if b.disposed || b.closing || !b.intendedShown || !b.parent.IsVisible() {
		return
	}
	// MyGO hides logical children with raw orderOut, which removes AppKit's
	// attachment without emitting the child's public OnHide. Reattach according
	// to explicit show/hide intent, even before MyGO's deferred restore runs.
	if err := b.attach(); err != nil && b.onError != nil {
		b.onError(err)
	}
}

func (b *settingsChildBinding) detach() {
	parent, child := b.parent.NativeHandle(), b.child.NativeHandle()
	if parent != 0 && child != 0 {
		b.native.detach(parent, child)
	}
}

func (b *settingsChildBinding) dispose(detach bool) {
	if b.disposed {
		return
	}
	b.disposed = true
	if detach {
		b.detach()
	}
	for _, off := range b.listeners {
		off()
	}
	b.listeners = nil
}
