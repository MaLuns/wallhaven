package ui

import (
	"fmt"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	kit "github.com/egoist/mygo/ui"
)

var iconNames = []string{"minimize", "refresh", "wallpaper", "back", "download", "search", "filter-up", "filter-down", "about", "settings", "log", "theme", "menu", "close", "favorite", "reset", "check", "clear", "chevron-up", "chevron-down", "left", "right"}

func TestFunctionalIconsRenderCenteredInInheritedColor(t *testing.T) {
	for _, size := range []float32{16, 18, 22, 24} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ink := color.RGBA{R: 30, G: 150, B: 230, A: 255}
			tester := kit.NewTester(func(c *kit.Context) {
				kit.Row(c).Fill().AlignItems(kit.Start).Background(kit.RGB(0, 0, 0)).TextColor(kit.RGB(ink.R, ink.G, ink.B)).Children(func() {
					for _, name := range iconNames {
						kit.Box(c.Key(name)).Size(40, 40).Center().Children(func() { appIcon(c, name, size).Label("icon " + name) })
					}
				})
			}, len(iconNames)*40, 40)
			for i, name := range iconNames {
				if appIcons[name] == nil {
					t.Fatalf("functional icon %q has no vector", name)
				}
				bounds, exists := tester.Find("icon " + name)
				if !exists || bounds.W != size || bounds.H != size || math.Abs(float64(bounds.X+size/2-float32(i*40+20))) > .01 || math.Abs(float64(bounds.Y+size/2-20)) > .01 {
					t.Fatalf("icon %s is not centered on its canvas: %+v", name, bounds)
				}
				pixels := 0
				for y := range 40 {
					for x := i * 40; x < (i+1)*40; x++ {
						pixel := tester.Image().RGBAAt(x, y)
						if pixel.B > 20 {
							pixels++
							// Strong strokes retain their inherited blue hue;
							// faint antialiased edge pixels can round channels.
							if pixel.B <= pixel.G || pixel.G <= pixel.R {
								t.Fatalf("icon %s lost inherited color: %+v", name, pixel)
							}
						}
					}
				}
				if pixels < 6 {
					t.Fatalf("icon %s rendered no visible stroke at %vpx", name, size)
				}
			}
		})
	}
}

func TestFunctionalIconsStayOutlinedAndButtonsTakeClicks(t *testing.T) {
	clicked := 0
	tester := kit.NewTester(func(c *kit.Context) {
		kit.Row(c).Fill().AlignItems(kit.Start).Background(kit.RGB(255, 255, 255)).Children(func() {
			legacyIconButton(c, "favorite", "favorite", "favorite test", 24).Size(40, 40).TextColor(kit.RGB(0, 0, 0)).OnClick(func() { clicked++ })
			legacyIconButton(c, "search", "search", "search test").Size(40, 40).TextColor(kit.RGB(0, 0, 0)).OnClick(func() { clicked++ })
		})
	}, 80, 40)
	if pixel := tester.Image().RGBAAt(20, 20); pixel.R != 255 || pixel.G != 255 || pixel.B != 255 {
		t.Fatalf("favorite reverted to a filled star: %+v", pixel)
	}
	for _, label := range []string{"favorite test", "search test"} {
		bounds, exists := tester.Find(label)
		if !exists || bounds.W != 40 || bounds.H != 40 {
			t.Fatalf("icon changed button hit target: %s %+v", label, bounds)
		}
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if clicked != 2 {
		t.Fatal("decorative icons intercepted button clicks")
	}
}

func TestIconNativePreview(t *testing.T) {
	directory := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if directory == "" {
		t.Skip("preview export not requested")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		tester := kit.NewTesterWith(func(c *kit.Context) {
			theme := kit.LightTheme()
			if dark {
				theme = kit.DarkTheme()
			}
			c.SetTheme(theme)
			kit.Column(c).Fill().Padding(20).Gap(18).Background(theme.Background).TextColor(theme.Text).Children(func() {
				kit.Text(c, "One Wallhaven · native line icons").FontSize(18).Bold()
				kit.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
					for _, name := range iconNames {
						kit.Column(c.Key(name)).Width(108).Height(90).Center().Gap(12).Children(func() {
							appIcon(c, name, 24)
							kit.Text(c, name).FontSize(11).TextColor(theme.TextMuted)
						})
					}
				})
			})
		}, 880, 540, kit.TesterOptions{Dark: dark})
		path := filepath.Join(directory, fmt.Sprintf("icons-dark-%t.png", dark))
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, tester.Image()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
