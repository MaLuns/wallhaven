package ui

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/desktop"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type settingsLogin struct {
	mu      sync.Mutex
	enabled bool
	err     error
}

func (l *settingsLogin) Enabled(context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enabled, nil
}

func (l *settingsLogin) SetEnabled(_ context.Context, enabled bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.enabled = enabled
	return nil
}

type settingsDirectories struct{ opened chan string }

func (settingsDirectories) Validate(ctx context.Context, path string) error {
	return (desktop.SettingsDirectories{}).Validate(ctx, path)
}

func (d settingsDirectories) Open(_ context.Context, path string) error {
	d.opened <- path
	return nil
}

type settingsHarness struct {
	*catalogHarness
	prefs    *app.Preferences
	store    *storage.Settings
	settings *kit.Tester
	login    *settingsLogin
	opened   chan string
	path     string
}

func newSettingsHarness(t *testing.T) *settingsHarness {
	t.Helper()
	h := &settingsHarness{catalogHarness: newCatalogHarness(t), login: &settingsLogin{}, opened: make(chan string, 2), path: filepath.Join(t.TempDir(), "settings.json")}
	defaults := domain.DefaultSettings(t.TempDir())
	h.store = storage.NewSettings(h.path, defaults)
	h.prefs = app.NewPreferences(app.PreferencesOptions{Store: h.store, Defaults: defaults, Login: h.login, Directories: settingsDirectories{h.opened}, Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.prefs.Close)
	h.screen.preferences = h.prefs
	h.settings = kit.NewTester(h.screen.SettingsView, 450, 340)
	h.prefs.Load()
	h.waitSettings(t, func() bool { return h.prefs.State().Ready })
	return h
}

func (h *settingsHarness) waitSettings(t *testing.T, ready func() bool) {
	t.Helper()
	for !ready() {
		nextActionCall(t, h.updates)()
		h.tester.Frame()
		h.settings.Frame()
	}
}

func TestSettingsNativeControlsPersistModeDirectoryAndLogin(t *testing.T) {
	h := newSettingsHarness(t)
	for _, label := range []string{"应用设置", "关闭设置", "瀑布流模式", "开机启动开关", "修改目录", "打开文件位置"} {
		bounds, ok := h.settings.Find(label)
		if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 450 || bounds.Y+bounds.H > 340 {
			t.Fatalf("setting %s is outside the original window: %+v", label, bounds)
		}
	}
	openedSettings := 0
	h.screen.onOpenSettings = func() { openedSettings++ }
	click(t, h.tester, "功能菜单")
	click(t, h.tester, "前往设置")
	if openedSettings != 1 || h.controller.CurrentPage().ID != domain.Recommend {
		t.Fatal("settings menu did not open its independent window")
	}
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	h.screen.results.Y = 250
	click(t, h.settings, "瀑布流模式")
	h.waitSettings(t, func() bool { return !h.prefs.State().Saving })
	if info, _ := h.settings.Access("瀑布流模式"); info.Checked || h.screen.waterfall.mode != domain.PreviewTile || h.screen.results.Y != 0 {
		t.Fatal("saved tile mode did not update the native switch and catalog")
	}
	var complete func(string, error)
	initial := ""
	h.screen.chooseDirectory = func(path string, reply func(string, error)) { initial, complete = path, reply }
	click(t, h.settings, "修改目录")
	if initial != h.prefs.State().Values.DownloadPath || complete == nil {
		t.Fatal("directory picker did not receive the active directory")
	}
	if info, _ := h.settings.Access("修改目录"); !info.Disabled {
		t.Fatal("directory picker could be opened twice")
	}
	h.screen.RefreshSettings() // Sheet dismissal focuses the settings window.
	if h.prefs.State().Refreshing {
		t.Fatal("picker focus started a refresh that could discard its selection")
	}
	complete("", nil)
	h.settings.Frame()
	if h.prefs.State().Values.DownloadPath != initial || h.prefs.State().Saving {
		t.Fatal("canceling a picker changed its directory")
	}
	click(t, h.settings, "修改目录")
	directory := filepath.Join(t.TempDir(), "图片 & wallpapers")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	complete(directory, nil)
	h.waitSettings(t, func() bool { return !h.prefs.State().Saving })
	if h.prefs.State().Values.DownloadPath != directory || !h.settings.HasText(directory) {
		t.Fatal("selected directory was not saved and displayed")
	}
	click(t, h.settings, "打开文件位置")
	if got := nextActionCall(t, h.opened); got != directory {
		t.Fatalf("opened %q, want %q", got, directory)
	}
	h.waitSettings(t, func() bool { return !h.prefs.State().Opening })
	click(t, h.settings, "开机启动开关")
	h.waitSettings(t, func() bool { return !h.prefs.State().Saving })
	if info, _ := h.settings.Access("开机启动开关"); !info.Checked {
		t.Fatal("confirmed system login state was not reflected in its switch")
	}
	restarted, err := storage.NewSettings(h.path, domain.DefaultSettings(t.TempDir())).Load(context.Background())
	if err != nil || restarted != h.prefs.State().Values {
		t.Fatalf("restart lost settings: %+v, %v", restarted, err)
	}
	if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "settings.png"))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, h.settings.Image())
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatalf("settings preview: %v, %v", encodeErr, closeErr)
		}
	}
}

func TestSettingsShowFailuresAndAllowLoadRetry(t *testing.T) {
	h := newSettingsHarness(t)
	h.login.err = errors.New("系统拒绝设置登录项")
	click(t, h.settings, "开机启动开关")
	h.waitSettings(t, func() bool { return !h.prefs.State().Saving })
	if info, _ := h.settings.Access("开机启动开关"); info.Checked || !h.settings.HasText("系统拒绝设置登录项") {
		t.Fatal("failed system setting appeared successful")
	}
	// A damaged persisted file is visible and cannot be overwritten by switches.
	if err := os.WriteFile(h.path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	h.prefs.Close()
	h.prefs = app.NewPreferences(app.PreferencesOptions{Store: h.store, Defaults: domain.DefaultSettings(t.TempDir()), Login: h.login, Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.prefs.Close)
	h.screen.preferences = h.prefs
	h.prefs.Load()
	h.waitSettings(t, func() bool { return !h.prefs.State().Loading })
	if info, _ := h.settings.Access("瀑布流模式"); !info.Disabled || !h.settings.HasText("加载设置失败") {
		t.Fatal("failed loading left editable settings or hid its error")
	}
	if err := os.Remove(h.path); err != nil {
		t.Fatal(err)
	}
	// The error may extend the original fixed window; the body stays scrollable.
	h.settings.Scroll(400, 250, 0, 200)
	click(t, h.settings, "重试加载设置")
	h.waitSettings(t, func() bool { return h.prefs.State().Ready })
	if strings.Contains(h.prefs.State().Error, "加载设置失败") {
		t.Fatal("retry did not clear the previous load error")
	}
}

func TestPreviewModeSwitchResetsLayoutAndKeepsOriginalImageRatios(t *testing.T) {
	wallpapers := []domain.Wallpaper{{Width: 1000, Height: 2000}, {Width: 1920, Height: 1080}, {Width: 1000, Height: 1000}, {Width: 1000, Height: 1000}}
	var layout waterfallLayout
	waterfall, _ := layout.updateMode(wallpapers, 1000, 1, domain.PreviewWaterfall)
	if waterfall[0].imageHeight != 600 || waterfall[1].imageHeight != 168.75 {
		t.Fatal("waterfall mode lost the original image proportions")
	}
	tile, _ := layout.updateMode(wallpapers, 1000, 1, domain.PreviewTile)
	for i, place := range tile {
		if place.imageHeight != 200 || place.y != float32(i/3)*260 {
			t.Fatalf("tile %d: %+v", i, place)
		}
	}
	waterfall, _ = layout.updateMode(wallpapers, 1000, 1, domain.PreviewWaterfall)
	if waterfall[0].imageHeight != 600 {
		t.Fatal("switching back reused tile geometry")
	}
}
