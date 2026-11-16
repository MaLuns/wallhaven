package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/locale"
	kit "github.com/egoist/mygo/ui"
)

type settingsCacheManager struct {
	mu           sync.Mutex
	usage        domain.CacheUsage
	limits       [2]int64
	usageCalls   int
	clearCalls   int
	clearError   error
	clearStarted chan struct{}
	clearRelease chan struct{}
	usageStarted chan struct{}
	usageRelease chan struct{}
	limitStarted chan [2]int64
	limitRelease chan struct{}
}

func (m *settingsCacheManager) Usage(ctx context.Context) (domain.CacheUsage, error) {
	m.mu.Lock()
	m.usageCalls++
	started, release := m.usageStarted, m.usageRelease
	m.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		case <-ctx.Done():
			return domain.CacheUsage{}, ctx.Err()
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return domain.CacheUsage{}, ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage, nil
}

func (m *settingsCacheManager) SetLimits(ctx context.Context, thumbnail, original int64) error {
	m.mu.Lock()
	started, release := m.limitStarted, m.limitRelease
	m.mu.Unlock()
	if started != nil {
		select {
		case started <- [2]int64{thumbnail, original}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	m.limits = [2]int64{thumbnail, original}
	m.mu.Unlock()
	return nil
}

func (m *settingsCacheManager) Clear(ctx context.Context) error {
	m.mu.Lock()
	m.clearCalls++
	started, release := m.clearStarted, m.clearRelease
	m.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clearError != nil {
		return m.clearError
	}
	m.usage = domain.CacheUsage{}
	return nil
}

func newCacheSettingsHarness(t *testing.T) (*settingsHarness, *settingsCacheManager) {
	t.Helper()
	return newCacheSettingsHarnessSized(t, 450, 340, true)
}

func newCacheSettingsHarnessSized(t *testing.T, width, height int, scroll bool) (*settingsHarness, *settingsCacheManager) {
	t.Helper()
	h := newSettingsHarness(t)
	h.settings.SetSize(width, height)
	defaults := h.prefs.State().Values
	h.prefs.Close()
	manager := &settingsCacheManager{usage: domain.CacheUsage{ThumbnailBytes: 12 << 20, OriginalBytes: 320 << 20, ThumbnailEntries: 3, OriginalEntries: 5}}
	h.prefs = app.NewPreferences(app.PreferencesOptions{
		Store: h.store, Defaults: defaults, Login: h.login, Cache: manager,
		Directories: settingsDirectories{h.opened}, Dispatch: func(fn func()) { h.updates <- fn },
	})
	t.Cleanup(h.prefs.Close)
	h.screen.preferences = h.prefs
	h.prefs.Load()
	h.waitSettings(t, func() bool { return h.prefs.State().Ready && !h.prefs.State().Loading })
	if scroll {
		showCacheSettings(t, h)
	}
	return h, manager
}

func showCacheSettings(t *testing.T, h *settingsHarness) {
	t.Helper()
	h.settings.Frame()
	h.settings.Scroll(400, 250, 0, 10000)
	h.settings.Frame()
}

func requireCacheControlsDisabled(t *testing.T, h *settingsHarness, disabled bool) {
	t.Helper()
	for _, source := range []string{"缩略图缓存上限", "原图缓存上限", "清理磁盘缓存", "刷新缓存占用"} {
		label := locale.T(source)
		info, ok := h.settings.Access(label)
		if !ok || info.Disabled != disabled {
			t.Fatalf("cache control %q disabled=%v, want %v (found %v)", label, info.Disabled, disabled, ok)
		}
	}
}

func TestSettingsCacheLimitsPersistAndLeaveOtherSettingsIntact(t *testing.T) {
	h, manager := newCacheSettingsHarness(t)
	before := h.prefs.State().Values
	if !h.prefs.State().CacheReady || h.prefs.State().Cache.ThumbnailBytes != 12<<20 || h.prefs.State().Cache.OriginalBytes != 320<<20 {
		t.Fatalf("cache usage did not reach settings: %+v", h.prefs.State())
	}
	requireSettingsControlBounds(t, h.settings, "缩略图缓存上限", "原图缓存上限", "清理磁盘缓存", "刷新缓存占用")
	if !h.settings.HasText("缩略图：12.0 MiB / 256 MiB") || !h.settings.HasText("原图：320.0 MiB / 1 GiB") {
		t.Fatalf("per-type cache usage was not displayed: %q", h.settings.Texts())
	}
	if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		writeListPreview(t, h.settings, filepath.Join(directory, "settings-cache-chinese.png"))
	}
	requireCacheControlsDisabled(t, h, false)
	manager.mu.Lock()
	manager.limitStarted = make(chan [2]int64, 1)
	manager.limitRelease = make(chan struct{})
	started, release := manager.limitStarted, manager.limitRelease
	manager.mu.Unlock()
	click(t, h.settings, "缩略图缓存上限")
	click(t, h.settings, "128 MiB")
	if applied := nextActionCall(t, started); applied != [2]int64{128 << 20, 1024 << 20} {
		t.Fatalf("thumbnail selector applied unexpected cache limits: %v", applied)
	}
	if !h.prefs.State().Busy() {
		t.Fatal("cache limit update did not expose its in-flight state")
	}
	h.settings.Frame()
	requireCacheControlsDisabled(t, h, true)
	close(release)
	h.waitSettings(t, func() bool { return !h.prefs.State().Busy() })
	want := before
	want.ThumbnailCacheMiB = 128
	if h.prefs.State().Values != want || h.prefs.State().Error != "" || h.prefs.State().CacheError != "" {
		t.Fatalf("cache limit update changed other settings or failed: %+v", h.prefs.State())
	}
	requireCacheControlsDisabled(t, h, false)
	manager.mu.Lock()
	manager.limitStarted, manager.limitRelease = make(chan [2]int64, 1), nil
	originalStarted := manager.limitStarted
	manager.mu.Unlock()
	click(t, h.settings, "原图缓存上限")
	click(t, h.settings, "2 GiB")
	if applied := nextActionCall(t, originalStarted); applied != [2]int64{128 << 20, 2 << 30} {
		t.Fatalf("original selector changed the thumbnail limit: %v", applied)
	}
	h.waitSettings(t, func() bool { return !h.prefs.State().Busy() })
	want.OriginalCacheMiB = 2048
	if h.prefs.State().Values != want {
		t.Fatalf("original limit update changed other settings: %+v", h.prefs.State().Values)
	}
	restored, err := h.store.Load(context.Background())
	if err != nil || restored != want {
		t.Fatalf("cache limits did not survive a settings reload: %+v, %v", restored, err)
	}
	requireCacheControlsDisabled(t, h, false)
}

func TestSettingsCacheClearIsSingleFlightAndShowsFreshUsage(t *testing.T) {
	h, manager := newCacheSettingsHarness(t)
	manager.mu.Lock()
	manager.clearStarted, manager.clearRelease = make(chan struct{}, 1), make(chan struct{})
	started, release := manager.clearStarted, manager.clearRelease
	usageCalls := manager.usageCalls
	manager.mu.Unlock()
	click(t, h.settings, "清理磁盘缓存")
	nextActionCall(t, started)
	if !h.prefs.State().CacheBusy {
		t.Fatal("clearing cache did not expose its loading state")
	}
	requireCacheControlsDisabled(t, h, true)
	// Repeated clicks and view rebuilds cannot submit a second clear.
	_ = h.settings.Click("清理磁盘缓存")
	for range 3 {
		h.settings.Frame()
	}
	manager.mu.Lock()
	clears := manager.clearCalls
	manager.mu.Unlock()
	if clears != 1 {
		t.Fatalf("one clear interaction submitted %d cache operations", clears)
	}
	close(release)
	h.waitSettings(t, func() bool { return !h.prefs.State().CacheBusy })
	if !h.prefs.State().CacheReady || h.prefs.State().Cache != (domain.CacheUsage{}) || h.prefs.State().CacheError != "" {
		t.Fatalf("successful clear did not display actual empty usage: %+v", h.prefs.State())
	}
	manager.mu.Lock()
	refreshed := manager.usageCalls > usageCalls
	manager.mu.Unlock()
	if !refreshed {
		t.Fatal("clear guessed usage instead of reading the cache manager")
	}
	requireCacheControlsDisabled(t, h, false)
}

func TestSettingsCacheFailureCanRetryAndRefreshInEnglish(t *testing.T) {
	useEnglishLocale(t)
	h, manager := newCacheSettingsHarness(t)
	if locale.T("磁盘缓存") == "磁盘缓存" || locale.T("清理磁盘缓存") == "清理磁盘缓存" {
		t.Fatal("cache controls are not translated in English")
	}
	requireSettingsControlBounds(t, h.settings, locale.T("缩略图缓存上限"), locale.T("原图缓存上限"), locale.T("清理磁盘缓存"), locale.T("刷新缓存占用"))
	if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		writeListPreview(t, h.settings, filepath.Join(directory, "settings-cache-english.png"))
	}
	manager.mu.Lock()
	manager.clearError = errors.New("test cache cleanup failed")
	manager.mu.Unlock()
	click(t, h.settings, locale.T("清理磁盘缓存"))
	h.waitSettings(t, func() bool { return !h.prefs.State().CacheBusy })
	showCacheSettings(t, h)
	if !strings.Contains(h.prefs.State().CacheError, "test cache cleanup failed") || !h.settings.HasText("test cache cleanup failed") {
		t.Fatalf("failed cache clear was not visible: state=%+v texts=%q", h.prefs.State(), h.settings.Texts())
	}
	if h.prefs.State().Cache.ThumbnailBytes != 12<<20 || h.prefs.State().Cache.OriginalBytes != 320<<20 {
		t.Fatal("failed clear falsely displayed empty usage")
	}
	requireCacheControlsDisabled(t, h, false)
	manager.mu.Lock()
	manager.clearError = nil
	manager.usage = domain.CacheUsage{ThumbnailBytes: 2 << 20, OriginalBytes: 4 << 20, ThumbnailEntries: 1, OriginalEntries: 1}
	manager.usageStarted, manager.usageRelease = make(chan struct{}, 1), make(chan struct{})
	started, release := manager.usageStarted, manager.usageRelease
	manager.mu.Unlock()
	click(t, h.settings, locale.T("刷新缓存占用"))
	nextActionCall(t, started)
	requireCacheControlsDisabled(t, h, true)
	close(release)
	h.waitSettings(t, func() bool { return !h.prefs.State().CacheBusy })
	if h.prefs.State().CacheError != "" || h.prefs.State().Cache.ThumbnailBytes != 2<<20 || h.prefs.State().Cache.OriginalBytes != 4<<20 {
		t.Fatalf("refresh did not recover the cache controls: %+v", h.prefs.State())
	}
	// The previous error row may have changed the scroll extent.
	showCacheSettings(t, h)
	click(t, h.settings, locale.T("清理磁盘缓存"))
	h.waitSettings(t, func() bool { return !h.prefs.State().CacheBusy })
	if h.prefs.State().Cache != (domain.CacheUsage{}) || h.prefs.State().CacheError != "" {
		t.Fatal("retrying a failed clear did not recover")
	}
}

func requireCacheControlBounds(t *testing.T, tester *kit.Tester, width, height int) {
	t.Helper()
	for _, source := range []string{"缩略图缓存上限", "原图缓存上限", "清理磁盘缓存", "刷新缓存占用"} {
		label := locale.T(source)
		bounds, ok := tester.Find(label)
		if !ok || bounds.W <= 0 || bounds.H < 30 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > float32(height) {
			t.Fatalf("cache control %q is not fully inside %dx%d: %+v", label, width, height, bounds)
		}
	}
}

func requireCachePopupBounds(t *testing.T, tester *kit.Tester, width, height int) kit.Rect {
	t.Helper()
	popup, ok := tester.AccessByRole(kit.RoleListBox, "")
	bounds := popup.Bounds
	if !ok || popup.Offscreen || bounds.W <= 0 || bounds.H <= 0 || bounds.H > 234 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > float32(height) {
		t.Fatalf("cache popup is not bounded inside %dx%d: found=%v %+v", width, height, ok, popup)
	}
	return bounds
}

func requireVisibleCacheOption(t *testing.T, tester *kit.Tester, label string, popup kit.Rect) {
	t.Helper()
	option, ok := tester.AccessByRole(kit.RoleOption, label)
	bounds := option.Bounds
	if !ok || option.Offscreen || bounds.W <= 0 || bounds.H < 28 || bounds.X < popup.X || bounds.Y < popup.Y || bounds.X+bounds.W > popup.X+popup.W || bounds.Y+bounds.H > popup.Y+popup.H {
		t.Fatalf("cache option %q is not fully visible inside popup %+v: found=%v %+v", label, popup, ok, option)
	}
}

func TestSettingsCacheDefaultWindowShowsControlsWithoutScrolling(t *testing.T) {
	for _, language := range []string{"zh-Hans", "en"} {
		t.Run(language, func(t *testing.T) {
			if language == "en" {
				useEnglishLocale(t)
			}
			h, _ := newCacheSettingsHarnessSized(t, SettingsWindowWidth, SettingsWindowHeight, false)
			requireCacheControlBounds(t, h.settings, SettingsWindowWidth, SettingsWindowHeight)
			// Controls at the top and bottom must coexist in the first frame;
			// this harness never scrolls its settings view.
			for _, source := range []string{"界面语言", "瀑布流模式", "修改目录", "打开文件位置"} {
				if bounds, ok := h.settings.Find(locale.T(source)); !ok || bounds.H <= 0 || bounds.Y < 0 || bounds.Y+bounds.H > float32(SettingsWindowHeight) {
					t.Fatalf("default settings window lost %q: %+v", locale.T(source), bounds)
				}
			}
			if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
				writeListPreview(t, h.settings, filepath.Join(directory, "settings-cache-default-"+language+".png"))
			}
		})
	}
}

func TestSettingsCacheOriginalPopupScrollsToBothCapacityExtremes(t *testing.T) {
	useEnglishLocale(t)
	for _, size := range []struct {
		width, height int
		scroll        bool
	}{
		{560, 620, false},
		{480, 420, true},
		{450, 340, true},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			h, manager := newCacheSettingsHarnessSized(t, size.width, size.height, size.scroll)
			requireCacheControlBounds(t, h.settings, size.width, size.height)
			click(t, h.settings, locale.T("原图缓存上限"))
			popup := requireCachePopupBounds(t, h.settings, size.width, size.height)
			h.settings.Scroll(popup.X+popup.W/2, popup.Y+popup.H/2, 0, 10000)
			h.settings.Frame()
			popup = requireCachePopupBounds(t, h.settings, size.width, size.height)
			requireVisibleCacheOption(t, h.settings, "16 GiB", popup)
			// Keyboard navigation must scroll the option list, independently
			// from the settings body, before Enter commits the highlighted cap.
			h.settings.Key(0, kit.KeyHome)
			popup = requireCachePopupBounds(t, h.settings, size.width, size.height)
			requireVisibleCacheOption(t, h.settings, "64 MiB", popup)
			h.settings.Key(0, kit.KeyEnd)
			popup = requireCachePopupBounds(t, h.settings, size.width, size.height)
			requireVisibleCacheOption(t, h.settings, "16 GiB", popup)
			if h.prefs.State().Values.OriginalCacheMiB != 1024 {
				t.Fatal("moving through cache options saved a capacity before Enter")
			}
			if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
				writeListPreview(t, h.settings, filepath.Join(directory, fmt.Sprintf("settings-cache-popup-english-%dx%d.png", size.width, size.height)))
			}
			h.settings.Key(0, kit.KeyEnter)
			h.waitSettings(t, func() bool { return !h.prefs.State().Busy() })
			if h.prefs.State().Values.OriginalCacheMiB != 16384 || h.prefs.State().Values.ThumbnailCacheMiB != 256 {
				t.Fatalf("End/Enter did not select the original-cache maximum: %+v", h.prefs.State().Values)
			}
			manager.mu.Lock()
			applied := manager.limits
			manager.mu.Unlock()
			if applied != [2]int64{256 << 20, 16 << 30} {
				t.Fatalf("maximum capacity did not reach the manager: %v", applied)
			}
			if _, open := h.settings.AccessByRole(kit.RoleListBox, ""); open {
				t.Fatal("committing a capacity left the options popup open")
			}
			click(t, h.settings, locale.T("原图缓存上限"))
			h.settings.Key(0, kit.KeyHome)
			popup = requireCachePopupBounds(t, h.settings, size.width, size.height)
			requireVisibleCacheOption(t, h.settings, "64 MiB", popup)
			h.settings.Key(0, kit.KeyEnter)
			h.waitSettings(t, func() bool { return !h.prefs.State().Busy() })
			restored, err := h.store.Load(context.Background())
			if err != nil || restored.OriginalCacheMiB != 64 || restored.ThumbnailCacheMiB != 256 {
				t.Fatalf("Home/Enter did not persist the minimum original capacity: %+v, %v", restored, err)
			}
		})
	}
}
