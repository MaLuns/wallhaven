package ui

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	kit "github.com/egoist/mygo/ui"
)

func filterBounds(t *testing.T, tester *kit.Tester, label string) kit.Rect {
	t.Helper()
	bounds, ok := tester.Find(label)
	if !ok {
		t.Fatalf("missing filter %q", label)
	}
	return bounds
}

func TestFilterLabelsAndControlsAlign(t *testing.T) {
	for _, width := range []int{800, 1300, 1600} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			h := newCatalogHarness(t)
			h.tester.SetSize(width, 1000)
			click(t, h.tester, "前往壁纸")
			h.pump(t, 1)
			last := filterBounds(t, h.tester, "人物")
			input := filterBounds(t, h.tester, "壁纸关键词")
			if last.X+last.W > input.X || math.Abs(float64(last.Y+last.H/2-input.Y-input.H/2)) > .6 {
				t.Fatalf("category/search row misaligned: category=%+v input=%+v", last, input)
			}
			before := filterBounds(t, h.tester, "搜索结果")
			click(t, h.tester, "高级筛选")
			if after := filterBounds(t, h.tester, "搜索结果"); after != before {
				t.Fatalf("expanded overlay moved the results: before=%+v after=%+v", before, after)
			}
			for _, pair := range [][2]string{{"分类", "全部"}, {"场景", "SFW"}, {"分辨率", "至少"}, {"比例", "所有横屏"}, {"颜色", "颜色 #660000"}, {"排序", "降序"}} {
				label := filterBounds(t, h.tester, pair[0])
				value := filterBounds(t, h.tester, pair[1])
				if math.Abs(float64(label.Y+label.H/2-value.Y-value.H/2)) > .6 {
					t.Fatalf("%s baseline not centered: label=%+v value=%+v", pair[0], label, value)
				}
			}
			first := filterBounds(t, h.tester, "全部")
			for _, name := range []string{"SFW", "至少", "2560x1080", "所有横屏", "颜色 #660000", "降序", "自定义分辨率"} {
				bounds := filterBounds(t, h.tester, name)
				if bounds.X != first.X {
					t.Fatalf("%s left edge=%f want=%f", name, bounds.X, first.X)
				}
			}
		})
	}
}

func TestResponsiveFilterPresetsUseAvailableWidth(t *testing.T) {
	for _, size := range []struct{ width, resolutionRows, ratioRows, colorRows int }{
		{800, 4, 2, 3}, {1300, 3, 1, 2}, {1600, 2, 1, 1},
	} {
		t.Run(fmt.Sprint(size.width), func(t *testing.T) {
			h := newCatalogHarness(t)
			h.tester.SetSize(size.width, 1000)
			click(t, h.tester, "前往壁纸")
			h.pump(t, 1)
			click(t, h.tester, "高级筛选")
			panel := filterBounds(t, h.tester, "高级筛选面板")
			for _, group := range []struct {
				labels []string
				rows   int
			}{
				{append([]string(nil), resolutions...), size.resolutionRows},
				{filterRatioLabels(), size.ratioRows},
				{filterColorLabels(), size.colorRows},
			} {
				rows := map[float32]bool{}
				var rectangles []kit.Rect
				for _, label := range group.labels {
					bounds := filterBounds(t, h.tester, label)
					if bounds.X < panel.X || bounds.X+bounds.W > panel.X+panel.W || bounds.Y < panel.Y || bounds.Y+bounds.H > panel.Y+panel.H {
						t.Fatalf("%s outside the expanded panel: %+v panel=%+v", label, bounds, panel)
					}
					for _, other := range rectangles {
						if bounds.X < other.X+other.W && bounds.X+bounds.W > other.X && bounds.Y < other.Y+other.H && bounds.Y+bounds.H > other.Y {
							t.Fatalf("filter controls overlap: %s %+v previous=%+v", label, bounds, other)
						}
					}
					rows[bounds.Y] = true
					rectangles = append(rectangles, bounds)
				}
				if len(rows) != group.rows {
					t.Fatalf("%s occupies %d rows at width%d, want%d", group.labels[0], len(rows), size.width, group.rows)
				}
			}
			last := filterBounds(t, h.tester, "重置筛选")
			if panel.Y+panel.H-last.Y-last.H > 2 {
				t.Fatalf("expanded panel retains unused space below its last control: panel=%+v last=%+v", panel, last)
			}
		})
	}
}

func filterRatioLabels() []string {
	var labels []string
	for _, value := range ratios {
		labels = append(labels, value.title)
	}
	return labels
}

func filterColorLabels() []string {
	var labels []string
	for _, value := range colors {
		labels = append(labels, "颜色 #"+value)
	}
	return append(labels, "不限颜色")
}

// Find reports the clipped rectangle of partially visible controls. Keep
// scrolling until the entire hit area is visible, rather than accepting a
// one-pixel strip at a viewport edge as an interactable option.
func revealCompactFilter(t *testing.T, tester *kit.Tester, label string) {
	t.Helper()
	for range 12 {
		panel := filterBounds(t, tester, "高级筛选面板")
		control := filterBounds(t, tester, label)
		if control.H >= 28 && control.Y >= panel.Y && control.Y+control.H <= panel.Y+panel.H {
			return
		}
		delta := control.Y + 30 - panel.Y - panel.H + 20
		if control.Y <= panel.Y+1 {
			delta = min(float32(-60), control.Y-panel.Y-20)
		} else if control.H < 28 {
			delta = 60
		}
		tester.Scroll(panel.X+panel.W/2, panel.Y+panel.H/2, 0, delta)
		tester.Frame()
	}
	t.Fatalf("filter %s could not be fully scrolled into view", label)
}

func TestAllFilterOptionsRemainReachableInMinimumWindow(t *testing.T) {
	h := newCatalogHarness(t)
	h.tester.SetSize(800, 540)
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	click(t, h.tester, "高级筛选")
	panel := filterBounds(t, h.tester, "高级筛选面板")
	if panel.Y+panel.H > 540-20 {
		t.Fatalf("minimum-window overlay extends below the screen: %+v", panel)
	}
	for _, label := range append(append(append([]string{}, resolutions...), filterRatioLabels()...), filterColorLabels()...) {
		revealCompactFilter(t, h.tester, label)
		bounds := filterBounds(t, h.tester, label)
		if bounds.W <= 0 || bounds.H < 28 {
			t.Fatalf("%s has no usable hit area: %+v", label, bounds)
		}
	}
	for _, label := range filterColorLabels() {
		revealCompactFilter(t, h.tester, label)
		click(t, h.tester, label)
		access, ok := h.tester.Access(label)
		if !ok || !access.Checked {
			t.Fatalf("selected color is not exposed: %s %+v", label, access)
		}
	}
	revealCompactFilter(t, h.tester, "排行榜")
	click(t, h.tester, "排行榜")
	for _, label := range []string{"近一天", "近三天", "近一周", "近一月", "近三月", "近半年", "近一年", "自定义分辨率", "重置筛选"} {
		revealCompactFilter(t, h.tester, label)
	}
}

func TestSelectedFilterSwatchesKeepTheirFillAndReadableMark(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, value := range []string{"ffffff", "ffff00", "660000", ""} {
			t.Run(fmt.Sprintf("dark-%t-%s", dark, value), func(t *testing.T) {
				controller := app.New()
				t.Cleanup(controller.Close)
				screen := New(controller)
				screen.filters.color = value
				tester := kit.NewTesterWith(func(c *kit.Context) {
					screen.applyLegacyTheme(c)
					kit.Row(c).Fill().Padding(20).Background(legacyPalette(c).Search).Children(func() { screen.filterSwatch(c, value) })
				}, 160, 80, kit.TesterOptions{Dark: dark})
				label := "颜色 #" + value
				if value == "" {
					label = "不限颜色"
				}
				bounds := filterBounds(t, tester, label)
				fill := tester.Image().RGBAAt(int(bounds.X+5), int(bounds.Y+5))
				wantFill := map[string]color.RGBA{
					"ffffff": {255, 255, 255, 255}, "ffff00": {255, 255, 0, 255}, "660000": {102, 0, 0, 255}, "": {255, 255, 255, 255},
				}[value]
				if fill != wantFill {
					t.Fatalf("selected swatch lost its original fill: %+v want=%+v", fill, wantFill)
				}
				border := tester.Image().RGBAAt(int(bounds.X+1), int(bounds.Y+14))
				if border.B < 200 || border.G < 120 || border.R > 100 {
					t.Fatalf("selected swatch lacks its blue border: %+v", border)
				}
				markPixels := 0
				for y := int(bounds.Y + 4); y < int(bounds.Y+bounds.H-4); y++ {
					for x := int(bounds.X + 4); x < int(bounds.X+bounds.W-4); x++ {
						pixel := tester.Image().RGBAAt(x, y)
						// SVG strokes are antialiased at 15px. Count a visible
						// contrast from the fill, including their blended edges.
						contrasts := pixel.R < 200 && pixel.G < 200 && pixel.B < 220
						if value == "660000" {
							contrasts = pixel.R > 160 && pixel.G > 80 && pixel.B > 80
						} else if value == "" {
							contrasts = pixel.R > 180 && pixel.G < 100 && pixel.B < 100
						}
						if contrasts {
							markPixels++
						}
					}
				}
				if markPixels < 6 {
					t.Fatalf("selected swatch mark does not contrast with its fill: %d pixels", markPixels)
				}
			})
		}
	}
}

func TestCompactFilterPreview(t *testing.T) {
	dir := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set WALLHAVEN_PREVIEW_DIR to export native filter previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{800, 1300, 1600} {
		for _, dark := range []bool{false, true} {
			h := newCatalogHarness(t)
			h.tester.SetDark(dark)
			height := 900
			if width == 800 {
				height = 540
			}
			h.tester.SetSize(width, height)
			click(t, h.tester, "前往壁纸")
			h.pump(t, 1)
			click(t, h.tester, "高级筛选")
			click(t, h.tester, "1920x1080")
			h.tester.Advance(220 * time.Millisecond)
			writeListPreview(t, h.tester, filepath.Join(dir, fmt.Sprintf("filters-%d-dark-%t.png", width, dark)))
			if width == 1300 {
				for _, color := range []string{"ffffff", "660000"} {
					click(t, h.tester, "颜色 #"+color)
					writeListPreview(t, h.tester, filepath.Join(dir, fmt.Sprintf("filters-%d-dark-%t-selected-%s.png", width, dark, color)))
				}
				h.tester.Move(0, 0)
				resolution := filterBounds(t, h.tester, "3440x1440")
				h.tester.Move(resolution.X+resolution.W/2, resolution.Y+resolution.H/2)
				h.tester.Advance(650 * time.Millisecond)
				writeListPreview(t, h.tester, filepath.Join(dir, fmt.Sprintf("tooltip-filters-dark-%t.png", dark)))
			}
		}
	}
}
