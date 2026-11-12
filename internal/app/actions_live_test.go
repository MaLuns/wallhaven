package app

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/files"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/wallhaven"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

// This opt-in check writes only inside t.TempDir and never calls Desktop.Set.
func TestLiveDownloadOriginal(t *testing.T) {
	if os.Getenv("WALLHAVEN_LIVE_TEST") != "1" {
		t.Skip("set WALLHAVEN_LIVE_TEST=1 to download a public original into a temporary directory")
	}
	source, err := wallhaven.New(wallhaven.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	detail, err := source.Detail(ctx, "7jm1qy")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	updates := make(chan func(), 100)
	a := NewActions(ActionsOptions{Files: files.New(files.Options{StateDir: filepath.Join(directory, "state")}), DownloadDir: directory, Dispatch: func(fn func()) { updates <- fn }})
	defer a.Close()
	a.Download(detail.Wallpaper)
	var state domain.Transfer
	for {
		select {
		case update := <-updates:
			update()
			state, _ = a.Transfer(domain.DownloadTransfer, detail.Wallpaper.ID)
			if !state.Running {
				goto finished
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
finished:
	if !state.Completed || state.Error != "" {
		t.Fatalf("download failed: %+v", state)
	}
	if filepath.Dir(state.Path) != directory {
		t.Fatalf("download escaped temporary directory: %s", state.Path)
	}
	file, err := os.Open(state.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(file)
	if err != nil || config.Width != detail.Wallpaper.Width || config.Height != detail.Wallpaper.Height {
		t.Fatalf("download did not preserve original: dimensions=%+v metadata=%dx%d error=%v", config, detail.Wallpaper.Width, detail.Wallpaper.Height, err)
	}
	info, err := file.Stat()
	if err != nil || info.Size() != detail.Wallpaper.FileSize {
		t.Fatalf("download size differs from metadata: info=%v error=%v", info, err)
	}
	t.Logf("Downloaded public original %s: %dx%d %s, %d bytes; saved only inside temporary test directory", detail.Wallpaper.ID, config.Width, config.Height, format, info.Size())
}
