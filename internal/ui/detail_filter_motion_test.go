package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

func TestAdvancedFiltersAnimateHeightWithoutMovingResults(t *testing.T) {
	controller := app.New(app.Options{})
	t.Cleanup(controller.Close)
	screen := New(controller)
	underneath := kit.RGB(180, 15, 20)
	tester := kit.NewTesterWith(func(c *kit.Context) {
		screen.applyLegacyTheme(c)
		kit.Column(c).Fill().Children(func() {
			screen.searchControls(c)
			kit.Box(c).Label("underlying results").Grow(1).FillWidth().Background(underneath)
		})
	}, 1200, 800, kit.TesterOptions{})
	before := filterBounds(t, tester, "underlying results")
	if err := tester.Click("高级筛选"); err != nil {
		t.Fatal(err)
	}
	initial := filterBounds(t, tester, "高级筛选面板")
	tester.Advance(90 * time.Millisecond)
	middle := filterBounds(t, tester, "高级筛选面板")
	tester.Advance(110 * time.Millisecond)
	finished := filterBounds(t, tester, "高级筛选面板")
	if !(initial.H < middle.H && middle.H < finished.H) {
		t.Fatalf("filter expansion did not progress through an intermediate height: %v, %v, %v", initial.H, middle.H, finished.H)
	}
	if after := filterBounds(t, tester, "underlying results"); after != before {
		t.Fatalf("filter expansion changed the result viewport: before=%+v after=%+v", before, after)
	}
	// At 1200px the responsive options fit without the former empty rows.
	if finished.H != 410 {
		t.Fatalf("compact filter target height changed: %+v", finished)
	}
	if err := tester.Click("高级筛选"); err != nil {
		t.Fatal(err)
	}
	// The collapsing overlay still covers this point at the beginning and
	// halfway through, then reveals the unchanged results underneath.
	start := tester.Image().RGBAAt(30, 100)
	tester.Advance(90 * time.Millisecond)
	mid := tester.Image().RGBAAt(30, 100)
	tester.Advance(110 * time.Millisecond)
	end := tester.Image().RGBAAt(30, 100)
	if start != mid || end.R != 180 || end.G != 15 || end.B != 20 || start == end {
		t.Fatalf("filter collapse did not retain its intermediate overlay: start=%+v middle=%+v end=%+v", start, mid, end)
	}
	if _, exists := tester.Find("高级筛选面板"); exists {
		t.Fatal("collapsed advanced controls remained interactive")
	}
}

func TestDetailInformationFadesAndStopsTakingInputOnHide(t *testing.T) {
	controller := app.New(app.Options{})
	t.Cleanup(controller.Close)
	screen := New(controller)
	state := app.DetailState{Wallpaper: domain.Wallpaper{ID: "motion", Resolution: "800x600"}}
	shown := false
	tester := kit.NewTesterWith(func(c *kit.Context) {
		screen.applyLegacyTheme(c)
		kit.Box(c).Fill().Background(kit.RGB(0, 0, 0)).Children(func() {
			if shown {
				screen.detailInformation(c, state, 400)
			}
		})
	}, 500, 400, kit.TesterOptions{})
	background := tester.Image().RGBAAt(24, 250)
	shown = true
	tester.Frame()
	start := tester.Image().RGBAAt(24, 250)
	tester.Advance(80 * time.Millisecond)
	middle := tester.Image().RGBAAt(24, 250)
	tester.Advance(100 * time.Millisecond)
	finished := tester.Image().RGBAAt(24, 250)
	if start != background || middle == background || middle == finished || finished == background {
		t.Fatalf("information panel did not fade in: background=%+v start=%+v middle=%+v finished=%+v", background, start, middle, finished)
	}
	shown = false
	tester.Frame()
	if _, exists := tester.Find("图片详细信息"); exists {
		t.Fatal("hidden information panel still took input during its exit")
	}
	tester.Advance(60 * time.Millisecond)
	leaving := tester.Image().RGBAAt(24, 250)
	tester.Advance(80 * time.Millisecond)
	if leaving == background || leaving == finished || tester.Image().RGBAAt(24, 250) != background {
		t.Fatalf("information panel did not finish fading out: leaving=%+v", leaving)
	}
}

func solidMotionBitmap(value color.RGBA) *kit.Bitmap {
	bitmap := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := range 40 {
		for x := range 40 {
			bitmap.SetRGBA(x, y, value)
		}
	}
	return kit.NewBitmap(bitmap)
}

func TestDetailOriginalCrossfadesWithoutDelayingPan(t *testing.T) {
	controller := app.New(app.Options{})
	t.Cleanup(controller.Close)
	screen := New(controller)
	red := solidMotionBitmap(color.RGBA{R: 255, A: 255})
	blue := solidMotionBitmap(color.RGBA{B: 255, A: 255})
	screen.images = catalogImages{bitmap: red}
	screen.viewer.zoom = 1
	wallpaper := domain.Wallpaper{ID: "motion", Width: 40, Height: 40, Thumbnail: "preview"}
	tester := kit.NewTesterWith(func(c *kit.Context) {
		kit.Box(c).Fill().Background(kit.RGB(0, 0, 0)).Children(func() { screen.detailPicture(c, wallpaper, 400, 400) })
	}, 400, 400, kit.TesterOptions{})
	if got := tester.Image().RGBAAt(200, 200); got.R != 255 || got.B != 0 {
		t.Fatalf("preview did not appear immediately: %+v", got)
	}
	screen.viewer.image = blue
	tester.Frame()
	start := tester.Image().RGBAAt(200, 200)
	tester.Advance(90 * time.Millisecond)
	middle := tester.Image().RGBAAt(200, 200)
	tester.Advance(110 * time.Millisecond)
	finished := tester.Image().RGBAAt(200, 200)
	if start.R != 255 || start.B != 0 || middle.R == 0 || middle.B == 0 || finished.R != 0 || finished.B != 255 {
		t.Fatalf("preview-to-original crossfade failed: start=%+v middle=%+v finished=%+v", start, middle, finished)
	}
	screen.viewer.panX = 50
	tester.Frame()
	if old, moved := tester.Image().RGBAAt(200, 200), tester.Image().RGBAAt(250, 200); old.B != 0 || moved.B != 255 {
		t.Fatalf("fade added an unwanted pan animation: old=%+v moved=%+v", old, moved)
	}
}
