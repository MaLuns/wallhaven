// Package desktop applies a downloaded image using the platform desktop API.
package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
	"unicode/utf8"

	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

var ErrUnsupported = errors.New("当前平台暂不支持设置桌面壁纸")

type Runner func(context.Context, string, ...string) ([]byte, error)

type Options struct {
	Runner     Runner
	Platform   string
	Timeout    time.Duration
	Getenv     func(string) string
	LookPath   func(string) (string, error)
	WindowsSet func(context.Context, string) error
}

type Wallpaper struct {
	run      Runner
	platform string
	timeout  time.Duration
	getenv   func(string) string
	lookPath func(string) (string, error)
	windows  func(context.Context, string) error
}

var _ ports.DesktopWallpaper = (*Wallpaper)(nil)

func New() *Wallpaper { return NewWithOptions(Options{}) }

// NewWithOptions permits platform dependencies to be replaced for tests. New
// selects the native platform and never invokes a command through a shell.
func NewWithOptions(options Options) *Wallpaper {
	if options.Runner == nil {
		options.Runner = func(ctx context.Context, program string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, program, args...).CombinedOutput()
		}
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.Timeout <= 0 {
		options.Timeout = 15 * time.Second
	}
	if options.Getenv == nil {
		options.Getenv = os.Getenv
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.WindowsSet == nil {
		options.WindowsSet = setWindowsWallpaper
	}
	return &Wallpaper{run: options.Runner, platform: options.Platform, timeout: options.Timeout, getenv: options.Getenv, lookPath: options.LookPath, windows: options.WindowsSet}
}

// The fixed JXA script calls AppKit on osascript's main thread; argv is data,
// never source. NSScreen.screens covers the attached screens, not every Space.
// API contract: https://developer.apple.com/documentation/appkit/nsworkspace/setdesktopimageurl(_:for:options:)?language=objc
const wallpaperScript = `ObjC.import("AppKit");
function run(argv) {
    if (argv.length !== 1) throw new Error("缺少壁纸文件");
    if (!$.NSThread.isMainThread) throw new Error("设置壁纸必须在主线程执行");
    var url = $.NSURL.fileURLWithPath(argv[0]);
    var screens = $.NSScreen.screens;
    var count = Number(screens.count);
    if (count === 0) throw new Error("未找到可用屏幕");
    var workspace = $.NSWorkspace.sharedWorkspace;
    for (var i = 0; i < count; i++) {
        var screen = screens.objectAtIndex(i);
        var options = workspace.desktopImageOptionsForScreen(screen);
        if (!options) options = $.NSDictionary.dictionary;
        var error = Ref();
        if (!workspace.setDesktopImageURLForScreenOptionsError(url, screen, options, error)) {
            throw new Error("无法设置第 " + (i + 1) + " 个屏幕的壁纸");
        }
    }
}`

func (w *Wallpaper) Set(ctx context.Context, path string) error {
	if w.platform != "darwin" && w.platform != "windows" && w.platform != "linux" {
		return ErrUnsupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return desktopContextError(err)
	}
	if path == "" || !utf8.ValidString(path) {
		return errors.New("请选择已下载的壁纸文件")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return errors.New("壁纸文件路径无效")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("壁纸文件不存在或无法读取")
	}
	switch w.platform {
	case "darwin":
		_, err = w.run(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", wallpaperScript, absolute)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("系统未能设置桌面壁纸，请重试")
		}
	case "windows":
		err = w.windows(ctx, absolute)
	case "linux":
		err = w.setLinuxWallpaper(ctx, absolute)
	}
	if ctx.Err() != nil {
		return desktopContextError(ctx.Err())
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return desktopContextError(err)
		}
		return err
	}
	return nil
}

type actionError struct {
	message string
	cause   error
}

func (e actionError) Error() string { return e.message }
func (e actionError) Unwrap() error { return e.cause }

func desktopContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return actionError{"设置桌面壁纸超时，请重试", context.DeadlineExceeded}
	}
	return actionError{"设置桌面壁纸已取消", context.Canceled}
}
