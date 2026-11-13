package ui

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

var navigationArtDestinations = []struct {
	key, title, label string
	id                domain.PageID
}{
	{"recommend", "推荐", "前往推荐", domain.Recommend},
	{"wallpaper", "壁纸", "前往壁纸", domain.WallpaperPage},
	{"component", "组件", "前往组件", domain.Components},
	{"my", "我的", "前往收藏", domain.Favorites},
}

func TestNavigationArtworkPreservesMinimumWindowHitTargets(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []int{1, 2} {
			t.Run(fmt.Sprintf("dark-%t-scale-%d", dark, scale), func(t *testing.T) {
				controller := app.New()
				t.Cleanup(controller.Close)
				screen := New(controller)
				t.Cleanup(screen.Close)
				tester := kit.NewTesterWith(screen.View, 800, 540, kit.TesterOptions{Dark: dark})
				tester.SetScale(float32(scale))
				controller.Navigate(domain.Components)
				tester.Frame()
				wantNav := kit.Rect{X: 0, Y: 30, W: 800, H: 60}
				if nav, ok := tester.Find("主导航"); !ok || nav != wantNav {
					t.Fatalf("navigation geometry changed: %+v want=%+v", nav, wantNav)
				}
				if bounds := tester.Image().Bounds(); bounds.Dx() != 800*scale || bounds.Dy() != 540*scale {
					t.Fatalf("native backing image has wrong scale: %v at %dx", bounds, scale)
				}
				previousRight := float32(0)
				for _, destination := range navigationArtDestinations {
					button, ok := tester.Find(destination.label)
					if !ok || button.W != 80 || button.H != 60 || button.Y != 30 || button.X < previousRight || button.X+button.W > 800 {
						t.Fatalf("%s lost its compact hit area: %+v", destination.title, button)
					}
					previousRight = button.X + button.W
					// The hit area extends below the 40px badge. A
					// click there must still switch pages, not just on its art.
					tester.ClickAt(button.X+button.W/2, button.Y+button.H-3)
					if controller.CurrentPage().ID != destination.id {
						t.Fatalf("%s lower hit area did not switch pages", destination.title)
					}
					if err := tester.Click(destination.label); err != nil {
						t.Fatal(err)
					}
					tester.Move(0, 120)
					text, ok := tester.Find(destination.title)
					if !ok || text.W <= 0 || text.H <= 0 || text.Y < 30 || text.Y+text.H > 90 {
						t.Fatalf("selected %s has no native title text: %+v", destination.title, text)
					}
					access, ok := tester.Access(destination.label)
					if !ok || access.Disabled || access.Name != destination.label || access.Bounds.W != 80 || access.Bounds.H != 60 {
						t.Fatalf("%s lost its accessible destination: %+v", destination.title, access)
					}
					badge := assertNavigationBadgePaints(t, tester, button, scale)
					if destination.id == domain.WallpaperPage {
						category := filterBounds(t, tester, "全部")
						// The former 30px gap is reduced by exactly 6px.
						if gap := category.Y - badge.Y - badge.H; gap != 24 {
							t.Fatalf("navigation/filter gap=%v, want24", gap)
						}
					}
				}
			})
		}
	}
}

func assertNavigationBadgePaints(t *testing.T, tester *kit.Tester, button kit.Rect, scale int) kit.Rect {
	t.Helper()
	blue, white := 0, 0
	bitmap := tester.Image()
	minX, minY := bitmap.Bounds().Max.X, bitmap.Bounds().Max.Y
	maxX, maxY := 0, 0
	// Sample the selected badge's region rather than its path commands. This
	// detects missing/clipped artwork and text at either backing scale.
	for y := int(button.Y) * scale; y < int(button.Y+button.H)*scale; y++ {
		for x := int(button.X-8) * scale; x < int(button.X+button.W+8)*scale; x++ {
			pixel := bitmap.RGBAAt(x, y)
			if pixel.B > 210 && pixel.G > 100 && pixel.G < 220 && pixel.R < 150 {
				if x < int(button.X)*scale || x >= int(button.X+80)*scale || y < int(button.Y+10)*scale || y >= int(button.Y+50)*scale {
					t.Fatalf("selected artwork exceeds its compact 80x40 bounds at (%d,%d)", x, y)
				}
				blue++
				minX, minY = min(minX, x), min(minY, y)
				maxX, maxY = max(maxX, x+1), max(maxY, y+1)
			}
			if pixel.R > 235 && pixel.G > 235 && pixel.B > 235 {
				white++
			}
		}
	}
	if blue < 1500*scale*scale || white < 50*scale*scale {
		t.Fatalf("selected badge lost visible blue artwork/white text: blue=%d white=%d scale=%d", blue, white, scale)
	}
	return kit.Rect{X: float32(minX) / float32(scale), Y: float32(minY) / float32(scale), W: float32(maxX-minX) / float32(scale), H: float32(maxY-minY) / float32(scale)}
}

func TestNavigationBadgeTextAndScale(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tester := newNavigationBadgeGallery(dark)
		for _, scale := range []int{1, 2} {
			tester.SetScale(float32(scale))
			if bounds := tester.Image().Bounds(); bounds.Dx() != 480*scale || bounds.Dy() != 88*scale {
				t.Fatalf("badge gallery backing image has wrong scale: %v", bounds)
			}
			for _, destination := range navigationArtDestinations {
				text, ok := tester.AccessByRole(kit.RoleText, destination.title)
				if !ok || text.Bounds.W < 20 || text.Bounds.H < 10 {
					t.Fatalf("%s is not exposed as native title text at %dx: %+v", destination.title, scale, text)
				}
			}
		}
	}
}

func newNavigationBadgeGallery(dark bool) *kit.Tester {
	return kit.NewTesterWith(func(c *kit.Context) {
		theme := kit.LightTheme()
		if dark {
			theme = kit.DarkTheme()
		}
		c.SetTheme(theme)
		kit.Row(c).Fill().Padding(20).Gap(16).AlignItems(kit.Center).Background(theme.Background).Children(func() {
			for _, destination := range navigationArtDestinations {
				navigationBadge(c, destination.key, destination.title)
			}
		})
	}, 480, 88, kit.TesterOptions{Dark: dark})
}

func TestNavigationArtworkPreview(t *testing.T) {
	dir := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set WALLHAVEN_PREVIEW_DIR to export native navigation previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		controller := app.New()
		t.Cleanup(controller.Close)
		screen := New(controller)
		t.Cleanup(screen.Close)
		tester := kit.NewTesterWith(screen.View, 800, 540, kit.TesterOptions{Dark: dark})
		for _, scale := range []int{1, 2} {
			tester.SetScale(float32(scale))
			for _, destination := range navigationArtDestinations {
				if err := tester.Click(destination.label); err != nil {
					t.Fatal(err)
				}
				tester.Move(0, 120)
				strip := image.NewRGBA(image.Rect(0, 0, 800*scale, 60*scale))
				draw.Draw(strip, strip.Bounds(), tester.Image(), image.Pt(0, 30*scale), draw.Src)
				writeNavigationArtwork(t, strip, filepath.Join(dir, fmt.Sprintf("navigation-%s-dark-%t-%dx.png", destination.key, dark, scale)))
			}
		}
		gallery := newNavigationBadgeGallery(dark)
		for _, scale := range []int{1, 2} {
			gallery.SetScale(float32(scale))
			writeNavigationArtwork(t, gallery.Image(), filepath.Join(dir, fmt.Sprintf("badges-dark-%t-%dx.png", dark, scale)))
		}
	}
}

func writeNavigationArtwork(t *testing.T, bitmap image.Image, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, bitmap); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log(path)
}
