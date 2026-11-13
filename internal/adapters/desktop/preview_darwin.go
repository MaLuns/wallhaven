//go:build darwin

package desktop

import (
	"errors"
	"structs"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

var previewSelectors = struct {
	workspace, running, count, at, current, equal, policy, hidden, terminated, name, utf8, retain, release, hide, unhide, newPool, drain, level, behavior, ignoresMouse, excluded objc.SEL
}{
	objc.RegisterName("sharedWorkspace"), objc.RegisterName("runningApplications"), objc.RegisterName("count"), objc.RegisterName("objectAtIndex:"),
	objc.RegisterName("currentApplication"), objc.RegisterName("isEqual:"), objc.RegisterName("activationPolicy"),
	objc.RegisterName("isHidden"), objc.RegisterName("isTerminated"), objc.RegisterName("localizedName"), objc.RegisterName("UTF8String"),
	objc.RegisterName("retain"), objc.RegisterName("release"), objc.RegisterName("hide"), objc.RegisterName("unhide"),
	objc.RegisterName("new"), objc.RegisterName("drain"), objc.RegisterName("setLevel:"), objc.RegisterName("setCollectionBehavior:"),
	objc.RegisterName("setIgnoresMouseEvents:"), objc.RegisterName("setExcludedFromWindowsMenu:"),
}

var previewCG struct {
	once  sync.Once
	err   error
	level func(int32) int32
}

type darwinPreviewPlatform struct{ visibility previewVisibility }

func newPreviewPlatform(after func(time.Duration, func()) func()) previewPlatform {
	return &darwinPreviewPlatform{visibility: previewVisibility{snapshot: snapshotPreviewApplications, after: after, now: time.Now, timeout: 4 * time.Second}}
}

func (*darwinPreviewPlatform) available() bool                        { return true }
func (p *darwinPreviewPlatform) hideApplications(done func(error))    { p.visibility.hide(done) }
func (p *darwinPreviewPlatform) restoreApplications(done func(error)) { p.visibility.restore(done) }

func (*darwinPreviewPlatform) prepareWindow(handle uintptr, bounds mygo.Rectangle) error {
	if handle == 0 {
		return errors.New("桌面预览窗口不可用")
	}
	previewCG.once.Do(func() {
		library, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			previewCG.err = err
			return
		}
		symbol, err := purego.Dlsym(library, "CGWindowLevelForKey")
		if err != nil {
			previewCG.err = err
			return
		}
		purego.RegisterFunc(&previewCG.level, symbol)
	})
	if previewCG.err != nil {
		return errors.New("无法读取系统桌面窗口层级")
	}
	// CGWindowLevelKey.desktopWindow=2, desktopIconWindow=18. Resolve the
	// values at runtime: never hard-code negative WindowServer layer values.
	level, icons := previewCG.level(2)+1, previewCG.level(18)
	if level >= icons {
		return errors.New("系统未提供壁纸与桌面图标之间的窗口层级")
	}
	window := objc.ID(handle)
	// MyGO's Frameless keeps NSWindowStyleMaskTitled and hides its chrome.
	// A desktop surface needs a genuinely borderless native window; otherwise
	// AppKit shifts it below the menu bar and rounds its screen-sized corners.
	objc.Send[uintptr](window, objc.RegisterName("setStyleMask:"), uint64(0))
	objc.Send[uintptr](window, previewSelectors.level, int64(level))
	// canJoinAllSpaces | stationary | ignoresCycle: like the desktop, not a
	// normal app window. Mouse events pass through to Finder's desktop icons.
	objc.Send[uintptr](window, previewSelectors.behavior, uint64(1|1<<4|1<<6))
	objc.Send[uintptr](window, previewSelectors.ignoresMouse, true)
	objc.Send[uintptr](window, previewSelectors.excluded, true)
	// Reposition after removing the titled style, using the full screen
	// rather than its work area, including the area behind the menu bar.
	screens := objc.Send[objc.ID](objc.ID(objc.GetClass("NSScreen")), objc.RegisterName("screens"))
	if objc.Send[uint64](screens, previewSelectors.count) == 0 {
		return errors.New("未找到可用屏幕")
	}
	primary := objc.Send[objc.ID](screens, previewSelectors.at, uint64(0))
	frame := objc.Send[previewScreenRect](primary, objc.RegisterName("frame"))
	origin := previewScreenPoint{X: float64(bounds.X), Y: frame.Height - float64(bounds.Y+bounds.Height)}
	objc.Send[uintptr](window, objc.RegisterName("setFrameOrigin:"), origin)
	return nil
}

type previewScreenPoint struct {
	_    structs.HostLayout
	X, Y float64
}
type previewScreenRect struct {
	_                   structs.HostLayout
	X, Y, Width, Height float64
}

type darwinPreviewApplication struct {
	id   objc.ID
	name string
}

func (a *darwinPreviewApplication) Name() string { return a.name }
func (a *darwinPreviewApplication) Hidden() bool {
	return objc.Send[bool](a.id, previewSelectors.hidden)
}
func (a *darwinPreviewApplication) Terminated() bool {
	return objc.Send[bool](a.id, previewSelectors.terminated)
}
func (a *darwinPreviewApplication) Hide()    { objc.Send[bool](a.id, previewSelectors.hide) }
func (a *darwinPreviewApplication) Unhide()  { objc.Send[bool](a.id, previewSelectors.unhide) }
func (a *darwinPreviewApplication) Release() { objc.Send[uintptr](a.id, previewSelectors.release) }

func snapshotPreviewApplications() ([]previewApplication, error) {
	workspaceClass, applicationClass := objc.GetClass("NSWorkspace"), objc.GetClass("NSRunningApplication")
	if workspaceClass == 0 || applicationClass == 0 {
		return nil, errors.New("系统应用管理接口不可用")
	}
	pool := objc.Send[objc.ID](objc.ID(objc.GetClass("NSAutoreleasePool")), previewSelectors.newPool)
	defer objc.Send[uintptr](pool, previewSelectors.drain)
	workspace := objc.Send[objc.ID](objc.ID(workspaceClass), previewSelectors.workspace)
	current := objc.Send[objc.ID](objc.ID(applicationClass), previewSelectors.current)
	applications := objc.Send[objc.ID](workspace, previewSelectors.running)
	if applications == 0 {
		return nil, errors.New("无法读取正在运行的应用")
	}
	var result []previewApplication
	for i, count := uint64(0), objc.Send[uint64](applications, previewSelectors.count); i < count; i++ {
		application := objc.Send[objc.ID](applications, previewSelectors.at, i)
		// Only user-facing regular applications; never hide Dock, menu-bar
		// agents, background services, or this application's own preview panel.
		if objc.Send[bool](application, previewSelectors.equal, current) || objc.Send[int64](application, previewSelectors.policy) != 0 {
			continue
		}
		name := objc.Send[objc.ID](application, previewSelectors.name)
		label := "未命名应用"
		if name != 0 {
			label = objc.Send[string](name, previewSelectors.utf8)
		}
		objc.Send[objc.ID](application, previewSelectors.retain)
		result = append(result, &darwinPreviewApplication{id: application, name: label})
	}
	return result, nil
}
