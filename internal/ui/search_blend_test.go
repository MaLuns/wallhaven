package ui

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	kit "github.com/egoist/mygo/ui"
)

func TestSearchFiltersBlurOnlyWhenExpanded(t *testing.T) {
	previewDir := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if previewDir != "" {
		if err := os.MkdirAll(previewDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dark := range []bool{false, true} {
		for _, width := range []int{800, 1280, 1300} {
			t.Run(fmt.Sprintf("%d/dark-%t", width, dark), func(t *testing.T) {
				h := newGatedListHarness(t)
				save := func(stage string) {
					if previewDir != "" {
						writeListPreview(t, h.tester, filepath.Join(previewDir, fmt.Sprintf("search-%s-%d-dark-%t.png", stage, width, dark)))
					}
				}
				h.tester.SetSize(width, 800)
				h.screen.appearance = "light"
				if dark {
					h.screen.appearance = "dark"
				}
				// Sharp stripes distinguish a fading blur from a fading tint:
				// the image must recover its original detail below the panel.
				stripe := color.RGBA{R: 230, G: 70, B: 40, A: 255}
				bitmap := image.NewRGBA(image.Rect(0, 0, 300, 300))
				for y := range 300 {
					for x := range 300 {
						value := stripe
						if x%16 >= 8 {
							value = color.RGBA{R: 40, G: 160, B: 230, A: 255}
						}
						bitmap.SetRGBA(x, y, value)
					}
				}
				h.screen.images = catalogImages{bitmap: kit.NewBitmap(bitmap)}
				first := h.nextPage(t)
				result := listPageResult(1, 1)
				for i := range result.Wallpapers {
					result.Wallpapers[i].Width, result.Wallpapers[i].Height = 300, 300
				}
				first.reply <- gatedListReply{result: result}
				h.applyReply(t)

				band := filterBounds(t, h.tester, "搜索筛选区")
				viewport := filterBounds(t, h.tester, "搜索结果")
				area := filterBounds(t, h.tester, "查看壁纸 page-1-0")
				if viewport.Y != band.Y+band.H || area.Y != viewport.Y {
					t.Fatalf("first row does not meet the filter edge: filters=%+v viewport=%+v image=%+v", band, viewport, area)
				}
				panel := filterBounds(t, h.tester, "搜索筛选面板")
				if viewport.X != panel.X || viewport.X+viewport.W != panel.X+panel.W {
					t.Fatalf("results extend beyond the filter width: panel=%+v viewport=%+v", panel, viewport)
				}
				x := int(area.X + 245)
				assertClear := func() {
					t.Helper()
					for _, offset := range []int{4, 12, 24} {
						if pixel := h.tester.Image().RGBAAt(x, int(area.Y)+offset); pixel != stripe {
							t.Fatalf("unscrolled first row is frosted at +%d: %+v", offset, pixel)
						}
					}
				}
				assertClear()
				save("initial")
				y := area.Y + 16
				h.tester.Scroll(float32(x), y, 0, 80)
				h.tester.Move(5, 5)
				h.tester.Advance(220 * time.Millisecond)
				if h.screen.results.Y <= 0 {
					t.Fatal("results did not scroll")
				}
				if moved := filterBounds(t, h.tester, "查看壁纸 page-1-0"); moved.Y < band.Y+band.H {
					t.Fatalf("scrolling lets images enter the collapsed filter bar: %+v", moved)
				}
				for _, offset := range []int{4, 12, 24} {
					if pixel := h.tester.Image().RGBAAt(x, int(viewport.Y)+offset); pixel != stripe {
						t.Fatalf("collapsed filters blur scrolled images at +%d: %+v", offset, pixel)
					}
				}
				save("scrolled")
				// Sample the last column, away from the custom input and reset
				// button, so control glyphs cannot be mistaken for image detail.
				contrastX := x
				for index := 1; ; index++ {
					bounds, found := h.tester.Find(fmt.Sprintf("查看壁纸 page-1-%d", index))
					if !found || bounds.Y != viewport.Y {
						break
					}
					contrastX = int(bounds.X + 150)
				}
				contrast := func(y int) int {
					left, right := h.tester.Image().RGBAAt(contrastX, y), h.tester.Image().RGBAAt(contrastX+8, y)
					dr, dg, db := int(left.R)-int(right.R), int(left.G)-int(right.G), int(left.B)-int(right.B)
					return dr*dr + dg*dg + db*db
				}
				rawContrast := contrast(int(viewport.Y) + 50)
				click(t, h.tester, "高级筛选")
				h.tester.Move(5, 5)
				panel = filterBounds(t, h.tester, "搜索筛选面板")
				if after := filterBounds(t, h.tester, "搜索结果"); after != viewport {
					t.Fatalf("expanding filters moves results: before=%+v after=%+v", viewport, after)
				}
				save("expanded")
				for _, offset := range []int{24, 8} {
					if got := contrast(int(panel.Y+panel.H) - offset); got >= rawContrast/8 {
						t.Fatalf("expanded filter bottom loses its Gaussian blur at -%d: contrast=%d raw=%d", offset, got, rawContrast)
					}
				}
				if got := contrast(int(panel.Y+panel.H) + 64); got < rawContrast*3/4 {
					t.Fatalf("blur does not fade back to image detail below the controls: contrast=%d raw=%d", got, rawContrast)
				}
				click(t, h.tester, "高级筛选")
				h.tester.Move(5, 5)
				for _, offset := range []int{4, 12, 24} {
					if pixel := h.tester.Image().RGBAAt(x, int(viewport.Y)+offset); pixel != stripe {
						t.Fatalf("collapsing filters leaves a blur at +%d: %+v", offset, pixel)
					}
				}
				h.tester.Scroll(float32(x), y, 0, -10000)
				h.tester.Move(5, 5)
				h.tester.Advance(220 * time.Millisecond)
				if h.screen.results.Y != 0 {
					t.Fatal("results did not return to the top")
				}
				assertClear()
				save("returned")
				h.tester.Press(float32(x), y)
				h.tester.Release(float32(x), y)
				if state := h.controller.DetailState(); !state.Open || state.Wallpaper.ID != "page-1-0" {
					t.Fatal("frosted edge intercepts image clicks")
				}
			})
		}
	}
}
