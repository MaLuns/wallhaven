package ui

import (
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	kit "github.com/egoist/mygo/ui"
)

// The title bar and cards retain their original dimensions. The navigation
// is compacted at the user's request, reducing the gap to the filters by 6px.
func TestLegacyShellAndCardGeometry(t *testing.T) {
	h := newCatalogHarness(t)
	h.tester.SetSize(1300, 800)
	for label, want := range map[string]kit.Rect{
		"标题栏":                 {X: 0, Y: 0, W: 1300, H: 30},
		"主导航":                 {X: 0, Y: 30, W: 1300, H: 60},
		"功能菜单":                {X: 1140, Y: 0, W: 40, H: 30},
		"壁纸 date_added-1-0":   {X: 20, Y: 170, W: 300, H: 240},
		"查看壁纸 date_added-1-0": {X: 20, Y: 170, W: 300, H: 200},
	} {
		got, ok := h.tester.Find(label)
		if !ok || got != want {
			t.Fatalf("%s bounds=%+v want=%+v", label, got, want)
		}
	}
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	before, _ := h.tester.Find("搜索结果")
	band, _ := h.tester.Find("搜索筛选区")
	// Images scroll below the collapsed bar; only expanded filters overlay
	// them. The former empty strip below the controls stays removed.
	if band.Y != 90 || band.H != 58 || before.Y != 148 {
		t.Fatalf("search band: %+v", band)
	}
	click(t, h.tester, "高级筛选")
	after, _ := h.tester.Find("搜索结果")
	if before != after {
		t.Fatalf("expanded filters reflowed results: before=%+v after=%+v", before, after)
	}
	if got, _ := h.tester.Find("动漫"); got.W != 60 || got.H != 30 {
		t.Fatalf("source category chip dimensions: %+v", got)
	}
}

func TestLegacyMenuThemeAndWindowActions(t *testing.T) {
	minimized, closed := false, false
	screen := New(app.New(), Options{Appearance: "dark", OnMinimize: func() { minimized = true }, OnClose: func() { closed = true }})
	tester := kit.NewTester(screen.View, 1300, 800)
	color := tester.Image().RGBAAt(0, 120)
	if color.R != 0x19 || color.G != 0x1d || color.B != 0x30 {
		t.Fatalf("dark canvas=%+v", color)
	}
	click(t, tester, "功能菜单")
	click(t, tester, "皮肤")
	panel, ok := tester.Find("皮肤对话框")
	if !ok || panel.Y != 200 || panel.W != 400 {
		t.Fatalf("source skin dialog: %+v", panel)
	}
	click(t, tester, "经典白")
	if screen.appearance != "light" {
		t.Fatal("skin selection did not apply")
	}
	tester.Key(0, kit.KeyEscape)
	if screen.dialogOpen {
		t.Fatal("Esc did not close skin dialog")
	}
	color = tester.Image().RGBAAt(0, 120)
	if color.R != 255 || color.G != 255 || color.B != 255 {
		t.Fatalf("light canvas=%+v", color)
	}
	click(t, tester, "最小化")
	click(t, tester, "关闭窗口")
	if !minimized || !closed {
		t.Fatal("title bar controls were not connected")
	}
}

func TestRecommendationImagesReturnAfterNavigation(t *testing.T) {
	h := newCatalogHarness(t)
	images := &countingImages{images: newCatalogImages(), seen: map[string]bool{}}
	h.screen.images = images
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	clear(images.seen)
	click(t, h.tester, "前往推荐")
	if !images.seen["date_added/0"] {
		t.Fatal("returning to recommendations did not load visible images")
	}
	if h.tester.FramePending() {
		t.Fatal("settled UI kept requesting frames")
	}
}
