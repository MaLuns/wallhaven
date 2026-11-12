package ui

import (
	"github.com/MaLuns/wallhaven-electron/internal/adapters/images"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/wallhaven"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in check exercises the real API, automatic second-page prefetch,
// native view and real thumbnail decoder without changing desktop settings.
func TestLiveCatalog(t *testing.T) {
	if os.Getenv("WALLHAVEN_LIVE_TEST") != "1" {
		t.Skip("set WALLHAVEN_LIVE_TEST=1 for live service verification")
	}
	source, err := wallhaven.New(wallhaven.Options{APIKey: os.Getenv("WALLHAVEN_API_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 100)
	redraws := make(chan struct{}, 1)
	controller := app.New(app.Options{Source: source, Prefetch: true, Dispatch: func(fn func()) { updates <- fn }})
	defer controller.Close()
	cache := images.New(images.Options{OnChange: func() {
		select {
		case redraws <- struct{}{}:
		default:
		}
	}})
	defer cache.Close()
	screen := New(controller, Options{Images: cache})
	tester := kit.NewTester(screen.View, 1200, 800)
	controller.Navigate(domain.WallpaperPage)
	tester.Frame()
	deadline := time.NewTimer(50 * time.Second)
	defer deadline.Stop()
	for {
		state := controller.SearchState()
		if state.Error != "" {
			t.Fatal(state.Error)
		}
		if state.CurrentPage >= 2 && !state.Loading && len(state.Wallpapers) > 0 {
			if thumbnail, _ := cache.Get(thumbnailURL(state.Wallpapers[0])); thumbnail != nil {
				tester.Frame()
				if _, visible := tester.Find("搜索结果"); !visible {
					t.Fatal("live results not rendered")
				}
				if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
					if err := os.MkdirAll(directory, 0755); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, "live-search.png"))
					if err != nil {
						t.Fatal(err)
					}
					encodeErr := png.Encode(file, tester.Image())
					closeErr := file.Close()
					if encodeErr != nil {
						t.Fatal(encodeErr)
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
				}
				t.Logf("Live API/native UI: page=%d total=%d shown=%d; thumbnail decoded", state.CurrentPage, state.Total, len(state.Wallpapers))
				return
			}
		}
		select {
		case fn := <-updates:
			fn()
			tester.Frame()
		case <-redraws:
			tester.Frame()
		case <-deadline.C:
			t.Fatal("live API or thumbnail verification timed out")
		}
	}
}
