package ui

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/images"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/wallhaven"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

func TestLiveDetailViewer(t *testing.T) {
	if os.Getenv("WALLHAVEN_LIVE_TEST") != "1" {
		t.Skip("set WALLHAVEN_LIVE_TEST=1 for real detail/original verification")
	}
	source, err := wallhaven.New(wallhaven.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := source.Search(ctx, domain.DefaultSearchQuery(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wallpapers) == 0 {
		t.Fatal("no live wallpaper available")
	}
	updates := make(chan func(), 50)
	controller := app.New(app.Options{Source: source, Dispatch: func(fn func()) { updates <- fn }})
	defer controller.Close()
	screen := New(controller, Options{FullImages: images.NewDetailLoader(images.DetailOptions{}), Dispatch: func(fn func()) { updates <- fn }, Appearance: "dark"})
	defer screen.Close()
	tester := kit.NewTester(screen.View, 1300, 800)
	screen.openDetail(result.Wallpapers[0])
	tester.Frame()
	for screen.viewer.image == nil || controller.DetailState().Loading {
		if screen.viewer.error != "" {
			t.Fatal(screen.viewer.error)
		}
		if state := controller.DetailState(); !state.Loading && state.Error != "" {
			t.Fatal(state.Error)
		}
		select {
		case fn := <-updates:
			fn()
			tester.Frame()
		case <-ctx.Done():
			t.Fatal("live detail/original timed out")
		}
	}
	state := controller.DetailState()
	width, height := kit.ImageSize(screen.viewer.image)
	if int(width) != state.Wallpaper.Width || int(height) != state.Wallpaper.Height {
		t.Fatalf("viewer did not decode original dimensions: %fx%f metadata=%dx%d", width, height, state.Wallpaper.Width, state.Wallpaper.Height)
	}
	if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "live-detail.png"))
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
	t.Logf("Live in-app detail: id=%s original=%dx%d tags=%d uploader=%s", state.Wallpaper.ID, int(width), int(height), len(state.Detail.Tags), state.Detail.Uploader)
	click(t, tester, "关闭详情")
	if controller.DetailState().Open {
		t.Fatal("live detail did not close")
	}
}
