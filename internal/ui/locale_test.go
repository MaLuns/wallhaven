package ui

import (
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/egoist/mygo/i18n"
	kit "github.com/egoist/mygo/ui"
)

func useEnglishLocale(t *testing.T) {
	t.Helper()
	previous := i18n.Locale()
	i18n.SetLocaleFunc(func() string { return "en" })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return previous }) })
}

func TestEnglishNavigationAndSettingsDirectoryActions(t *testing.T) {
	useEnglishLocale(t)
	h := newSettingsHarness(t)
	h.tester.SetSize(800, 540)
	for _, label := range []string{"Go to featured", "Go to wallpapers", "Go to widgets", "Go to favorites", "Menu"} {
		bounds, ok := h.tester.Find(label)
		if !ok || bounds.X < 0 || bounds.X+bounds.W > 800 || bounds.Y < 0 || bounds.Y+bounds.H > 540 {
			t.Fatalf("English navigation %q is outside the minimum window: %+v", label, bounds)
		}
	}
	if !h.tester.HasText("Featured") {
		t.Fatalf("selected navigation did not show English text: %q", h.tester.Texts())
	}
	openedSettings := 0
	h.screen.onOpenSettings = func() { openedSettings++ }
	click(t, h.tester, "Menu")
	click(t, h.tester, "Open settings")
	if openedSettings != 1 || h.controller.CurrentPage().ID != domain.Recommend {
		t.Fatal("English settings menu did not invoke the settings action")
	}
	for _, label := range []string{"App settings", "Close settings", "Waterfall", "Start at login switch", "Change folder", "Show folder"} {
		bounds, ok := h.settings.Find(label)
		if !ok || bounds.X < 0 || bounds.X+bounds.W > 450 || bounds.Y < 0 || bounds.Y+bounds.H > 340 {
			t.Fatalf("English setting %q is outside the original window: %+v", label, bounds)
		}
	}
	if !h.settings.HasText("Settings") || !h.settings.HasText("Grid") || !h.settings.HasText("Current folder: "+h.prefs.State().Values.DownloadPath) {
		t.Fatalf("English settings text missing: %q", h.settings.Texts())
	}
	var picked string
	h.screen.chooseDirectory = func(current string, complete func(string, error)) {
		picked = current
		complete("", nil)
	}
	click(t, h.settings, "Change folder")
	want := h.prefs.State().Values.DownloadPath
	if picked != want || h.prefs.State().Saving {
		t.Fatal("English folder picker changed the current path when canceled")
	}
	click(t, h.settings, "Show folder")
	if got := nextActionCall(t, h.opened); got != want {
		t.Fatalf("English folder action opened %q, want %q", got, want)
	}
	h.waitSettings(t, func() bool { return !h.prefs.State().Opening })
	click(t, h.settings, "Waterfall")
	h.waitSettings(t, func() bool { return !h.prefs.State().Saving })
	if h.prefs.State().Values.PreviewMode != domain.PreviewTile {
		t.Fatal("English view mode control changed its stored meaning")
	}
}

func TestLocaleChangeRefreshesRecommendationHeaders(t *testing.T) {
	useEnglishLocale(t)
	h := newSettingsHarness(t)
	if !h.tester.HasText("Recently added") {
		t.Fatal("English recommendation header is missing")
	}
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	h.tester.Frame()
	if !h.tester.HasText("最近更新") || h.tester.HasText("Recently added") {
		t.Fatalf("recommendation header kept the previous language: %q", h.tester.Texts())
	}
}

func TestEnglishFiltersPreserveWallhavenRequestValues(t *testing.T) {
	useEnglishLocale(t)
	h := newCatalogHarness(t)
	click(t, h.tester, "Go to wallpapers")
	h.pump(t, 1)
	click(t, h.tester, "Anime")
	click(t, h.tester, "Advanced filters")
	for _, label := range []string{"Asc", "Toplist", "1 week", "Landscape", "2560x1440"} {
		click(t, h.tester, label)
	}
	click(t, h.tester, "Wallpaper keywords")
	h.tester.Type("海 & sky")
	h.tester.Key(0, kit.KeyEnter)
	h.pump(t, 1)
	request := lastRequest(h.source)
	q := request.query
	if q.Query != "海 & sky" || q.Categories != "010" || q.Sorting != "toplist" || q.Order != "asc" || q.TopRange != "1w" || q.Purity != "100" || q.AtLeast != "2560x1440" || len(q.Ratios) != 1 || q.Ratios[0] != "landscape" || request.page != 1 {
		t.Fatalf("translated controls altered Wallhaven request values: %+v", request)
	}
	if h.screen.advanced {
		t.Fatal("successful English search did not close the advanced panel")
	}
}

func TestEnglishDetailKeepsWallpaperMetadata(t *testing.T) {
	useEnglishLocale(t)
	h, loader := newViewerHarness(t)
	h.screen.openDetail(h.controller.Recommendations()[0].Wallpapers[0])
	h.tester.Frame()
	waitViewer(t, h, func() bool { return !h.controller.DetailState().Loading && h.screen.viewer.url != "" })
	call := nextOriginal(t, loader)
	call.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	click(t, h.tester, "Image info")
	for _, text := range []string{"Views 123 · Favorites 45", "Uploader: 测试作者", "Tags: 自然, 风景", "General  SFW"} {
		if !h.tester.HasText(text) {
			t.Fatalf("English detail missing %q: %q", text, h.tester.Texts())
		}
	}
	if _, ok := h.tester.Find("Color #0066cc"); !ok {
		t.Fatal("English color accessibility label is missing")
	}
	click(t, h.tester, "Close details")
	if h.controller.DetailState().Open {
		t.Fatal("English close control did not close details")
	}
}

func TestEnglishDownloadStatuses(t *testing.T) {
	useEnglishLocale(t)
	for _, test := range []struct {
		transfer domain.Transfer
		want     string
	}{
		{domain.Transfer{Queued: true}, "Queued"},
		{domain.Transfer{Running: true, Received: 25, Total: 100}, "Downloaded 25%"},
		{domain.Transfer{Paused: true, Received: 50, Total: 100}, "Paused · 50%"},
		{domain.Transfer{Canceled: true}, "Canceled"},
		{domain.Transfer{Completed: true}, "Download complete"},
		{domain.Transfer{Kind: domain.WallpaperTransfer, Completed: true}, "Desktop wallpaper set"},
	} {
		if got := transferStatus(test.transfer); got != test.want {
			t.Errorf("English transfer status = %q, want %q", got, test.want)
		}
	}
	controller := app.New()
	t.Cleanup(controller.Close)
	screen := New(controller)
	controller.Navigate(domain.Downloads)
	tester := kit.NewTester(screen.View, 800, 540)
	if !tester.HasText("Download service is not ready") {
		t.Fatalf("English download empty state missing: %q", tester.Texts())
	}
}
