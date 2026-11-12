package ui

import (
	"context"
	"fmt"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type benchmarkCatalogSource struct{ wallpapers []domain.Wallpaper }

func (s benchmarkCatalogSource) Search(_ context.Context, _ domain.SearchQuery, page int) (domain.SearchResult, error) {
	return domain.SearchResult{Wallpapers: s.wallpapers, CurrentPage: page, LastPage: page, Total: len(s.wallpapers)}, nil
}

func benchmarkController(b *testing.B, count int) *app.Controller {
	b.Helper()
	wallpapers := make([]domain.Wallpaper, count)
	for i := range wallpapers {
		wallpapers[i] = domain.Wallpaper{ID: fmt.Sprintf("w%05d", i), Width: 1920, Height: 1080 + i%3*600, Resolution: "1920x1080", FileType: "image/jpeg", FileSize: 3 << 20}
	}
	updates := make(chan func(), 1)
	controller := app.New(app.Options{Source: benchmarkCatalogSource{wallpapers}, Dispatch: func(fn func()) { updates <- fn }})
	controller.Navigate(domain.WallpaperPage)
	(<-updates)()
	b.Cleanup(controller.Close)
	return controller
}

var benchmarkLayoutSink []wallpaperPlacement
var benchmarkHeightSink float32
var benchmarkSnapshotSink app.SearchState

// This measures an uncached reference: a defensive snapshot copy plus a fresh
// complete layout. BenchmarkCatalogFrame measures the current rendering path.
func BenchmarkCatalogPreparation(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			controller := benchmarkController(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				state := controller.SearchState()
				benchmarkLayoutSink, benchmarkHeightSink = legacyWaterfall(state.Wallpapers, 1280)
			}
		})
	}
}

func BenchmarkCatalogSnapshot(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			controller := benchmarkController(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkSnapshotSink = controller.SearchState()
			}
		})
	}
}

// Repeated frames model redraws from thumbnail arrivals without altering the
// result set. Native controls, text and drawing remain part of the measurement.
func BenchmarkCatalogFrame(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			controller := benchmarkController(b, count)
			screen := New(controller)
			tester := kit.NewTester(screen.View, 1300, 800)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				tester.Frame()
			}
		})
	}
}
