package desktop

import (
	"errors"
	"time"

	"github.com/egoist/mygo"
	kit "github.com/egoist/mygo/ui"

	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

// Only this narrow part varies by OS. The MyGO surfaces, interaction and
// lifecycle stay common; future Windows/Linux implementations replace this
// interface without introducing a second viewer or a wallpaper-setting path.
type previewPlatform interface {
	available() bool
	prepareWindow(uintptr, mygo.Rectangle) error
	hideApplications(func(error))
	restoreApplications(func(error))
}

type PreviewOptions struct {
	MainWindow     func() *mygo.Window
	SettingsWindow func() *mygo.Window
	Wallpaper      func(*kit.Context)
	Controls       func(*kit.Context)
	ControlsHeight int
	Previous       func()
	Next           func()
	RequestExit    func()
}

type Preview struct {
	options      PreviewOptions
	platform     previewPlatform
	backgrounds  []*mygo.Window
	controls     *mygo.Window
	main         *mygo.Window
	settings     *mygo.Window
	mainVisible  bool
	stopDisplays func()
	request      uint64
	shortcuts    previewShortcuts
}

var _ ports.DesktopPreview = (*Preview)(nil)

func NewPreview(options PreviewOptions) *Preview {
	after := func(delay time.Duration, fn func()) func() {
		timer := time.AfterFunc(delay, func() { mygo.App.WhenReady(fn) })
		return func() { timer.Stop() }
	}
	return &Preview{options: options, platform: newPreviewPlatform(after), shortcuts: previewShortcuts{
		register: mygo.GlobalShortcut.Register, unregister: mygo.GlobalShortcut.Unregister,
	}}
}

func (p *Preview) Available() bool { return p.platform.available() }

func (p *Preview) Open(complete func(error)) {
	if !p.Available() {
		complete(errors.New("当前平台暂不支持桌面预览"))
		return
	}
	if p.options.MainWindow == nil || p.options.Wallpaper == nil || p.options.Controls == nil || p.options.ControlsHeight <= 0 {
		complete(errors.New("桌面预览服务尚未就绪"))
		return
	}
	if p.main != nil {
		complete(errors.New("桌面预览尚未退出"))
		return
	}
	main := p.options.MainWindow()
	if main == nil || main.IsDestroyed() {
		complete(errors.New("主窗口不可用"))
		return
	}
	if main.IsFullScreen() {
		complete(errors.New("请先退出应用全屏，再使用桌面预览"))
		return
	}
	displays := mygo.Screen.Displays()
	if len(displays) == 0 {
		complete(errors.New("未找到可用屏幕"))
		return
	}
	p.request++
	request := p.request
	p.main, p.mainVisible = main, main.IsVisible()
	for _, display := range displays {
		bounds := display.Bounds
		if bounds.Width <= 0 || bounds.Height <= 0 {
			continue
		}
		window := mygo.NewWindow(mygo.WindowOptions{
			Title: "桌面壁纸预览", Width: bounds.Width, Height: bounds.Height,
			Hidden: true, Frameless: true, DisableResize: true, DisableMove: true,
			DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true, DisableShadow: true, SkipTaskbar: true,
			BackgroundColor: "#191d30", Content: kit.View(p.options.Wallpaper),
		})
		p.backgrounds = append(p.backgrounds, window)
		window.SetBounds(bounds)
		if err := p.platform.prepareWindow(window.NativeHandle(), bounds); err != nil {
			complete(err)
			return
		}
		window.OnClose(func(e *mygo.CloseEvent) { e.PreventDefault(); p.exitRequested() })
	}
	if len(p.backgrounds) == 0 {
		complete(errors.New("未找到可用屏幕"))
		return
	}
	workArea := mygo.Screen.DisplayMatching(main.Bounds()).WorkArea
	width := min(1040, workArea.Width-40)
	height := p.options.ControlsHeight
	if width < 480 || workArea.Height < height+40 {
		complete(errors.New("当前屏幕可用区域过小，无法显示桌面预览操作栏"))
		return
	}
	p.controls = mygo.NewWindow(mygo.WindowOptions{
		Title: "桌面预览", Width: width, Height: height,
		Hidden: true, Frameless: true, Panel: true, DisableResize: true, DisableMove: true,
		Transparent: true, DisableShadow: true, BackgroundColor: "#00000000", Content: kit.View(p.options.Controls),
	})
	p.controls.SetBounds(mygo.Rectangle{X: workArea.X + (workArea.Width-width)/2, Y: workArea.Y + workArea.Height - height - 20, Width: width, Height: height})
	p.controls.OnClose(func(e *mygo.CloseEvent) { e.PreventDefault(); p.exitRequested() })
	if err := p.shortcuts.start(p.options.Previous, p.options.Next, p.exitRequested); err != nil {
		complete(err)
		return
	}
	p.stopDisplays = mygo.Screen.OnDisplaysChanged(p.exitRequested)
	p.platform.hideApplications(func(err error) {
		if request != p.request {
			return
		}
		if err != nil {
			complete(err)
			return
		}
		if p.options.SettingsWindow != nil {
			settings := p.options.SettingsWindow()
			if settings != nil && !settings.IsDestroyed() && settings.IsVisible() {
				p.settings = settings
				settings.Hide()
			}
		}
		if p.mainVisible {
			main.Hide()
		}
		for _, window := range p.backgrounds {
			window.ShowInactive()
		}
		p.controls.Show()
		p.controls.Focus()
		complete(nil)
	})
}

func (p *Preview) exitRequested() {
	if p.options.RequestExit != nil {
		p.options.RequestExit()
	}
}

func (p *Preview) Close(complete func(error)) {
	p.request++ // An unfinished hide must never show its windows after exit.
	p.shortcuts.stop()
	if p.stopDisplays != nil {
		p.stopDisplays()
		p.stopDisplays = nil
	}
	if p.controls != nil {
		p.controls.Destroy()
		p.controls = nil
	}
	for _, window := range p.backgrounds {
		window.Destroy()
	}
	p.backgrounds = nil
	main, settings, visible := p.main, p.settings, p.mainVisible
	p.main, p.settings, p.mainVisible = nil, nil, false
	if settings != nil && !settings.IsDestroyed() {
		settings.ShowInactive()
	}
	if main != nil && !main.IsDestroyed() && visible {
		main.Show()
		main.Focus()
	}
	p.platform.restoreApplications(complete)
}

// Invalidate is called on the UI thread, alongside state updates. The image
// loader is shared by all backgrounds; its completion redraws every display.
func (p *Preview) Invalidate() {
	for _, window := range p.backgrounds {
		window.Invalidate()
	}
	if p.controls != nil {
		p.controls.Invalidate()
	}
}

type unsupportedPreviewPlatform struct{}

func (unsupportedPreviewPlatform) available() bool { return false }
func (unsupportedPreviewPlatform) prepareWindow(uintptr, mygo.Rectangle) error {
	return errors.New("当前平台暂不支持桌面预览")
}
func (unsupportedPreviewPlatform) hideApplications(done func(error)) {
	done(errors.New("当前平台暂不支持桌面预览"))
}
func (unsupportedPreviewPlatform) restoreApplications(done func(error)) { done(nil) }
