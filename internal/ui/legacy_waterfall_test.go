package ui

import (
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func TestWaterfallOriginalAspectRatioGeometry(t *testing.T) {
	wallpapers := []domain.Wallpaper{
		{Width: 1920, Height: 1080},
		{Width: 1000, Height: 2000},
		{Width: 1000, Height: 1000},
		{Width: 2560, Height: 1440},
		{Width: 1000, Height: 1000},
	}
	placements, total := legacyWaterfall(wallpapers, 1000)
	// Legacy CSS/JS: 300px width +20px gaps, remainder centered; each item
	// enters the shortest column and reserves a40px toolbar and20px gap.
	want := []wallpaperPlacement{{30, 0, 168.75}, {350, 0, 600}, {670, 0, 300}, {30, 228.75, 168.75}, {670, 360, 300}}
	for i := range want {
		if placements[i] != want[i] {
			t.Fatalf("item %d = %+v, want %+v", i, placements[i], want[i])
		}
	}
	if total != 720 {
		t.Fatalf("placeholder height = %v, want720", total)
	}
}

func TestLegacyWaterfallSmallWindowAndMissingDimensions(t *testing.T) {
	placements, total := legacyWaterfall([]domain.Wallpaper{{}, {}, {}}, 800)
	want := []wallpaperPlacement{{90, 0, 200}, {410, 0, 200}, {90, 260, 200}}
	for i := range want {
		if placements[i] != want[i] {
			t.Fatalf("item %d = %+v, want %+v", i, placements[i], want[i])
		}
	}
	if total != 520 {
		t.Fatalf("placeholder height = %v, want520", total)
	}
	if placements, height := legacyWaterfall(nil, 800); len(placements) != 0 || height != 0 {
		t.Fatalf("empty layout = %v, height %v", placements, height)
	}
}
