package ui

import (
	"math"
	"sort"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

// UI-owned snapshots retain the controller's isolation guarantee while avoiding
// another whole-result copy for every thumbnail notification or animation frame.
func (a *App) searchSnapshot() app.SearchState {
	revision := a.controller.SearchRevision()
	if !a.catalogReady || revision != a.catalogRevision {
		a.catalogSnapshot = a.controller.SearchState()
		a.catalogRevision = revision
		a.catalogReady = true
	}
	return a.catalogSnapshot
}

type waterfallLayout struct {
	generation    uint64
	width         float32
	mode          domain.PreviewMode
	ready         bool
	placements    []wallpaperPlacement
	columnHeights []float32
	maxBottom     []float32
	totalHeight   float32
}

// An immutable prefix is reused on append. A new query or width (including a
// centering-only width change) starts a fresh layout.
func (layout *waterfallLayout) update(wallpapers []domain.Wallpaper, width float32, generation uint64) ([]wallpaperPlacement, float32) {
	return layout.updateMode(wallpapers, width, generation, domain.PreviewWaterfall)
}

func (layout *waterfallLayout) updateMode(wallpapers []domain.Wallpaper, width float32, generation uint64, mode domain.PreviewMode) ([]wallpaperPlacement, float32) {
	columns := max(1, int((width+legacyCardGap)/(legacyCardWidth+legacyCardGap)))
	if !layout.ready || layout.generation != generation || layout.width != width || layout.mode != mode || len(wallpapers) < len(layout.placements) {
		*layout = waterfallLayout{generation: generation, width: width, mode: mode, ready: true, columnHeights: make([]float32, columns)}
	}
	inset := float32(math.Mod(float64(width+legacyCardGap), float64(legacyCardWidth+legacyCardGap))) / 2
	for i := len(layout.placements); i < len(wallpapers); i++ {
		wallpaper := wallpapers[i]
		column := 0
		for candidate := 1; candidate < columns; candidate++ {
			if layout.columnHeights[candidate] < layout.columnHeights[column] {
				column = candidate
			}
		}
		height := legacyCardImageHeight
		if mode != domain.PreviewTile && wallpaper.Width > 0 && wallpaper.Height > 0 {
			height = legacyCardWidth * float32(wallpaper.Height) / float32(wallpaper.Width)
		}
		placement := wallpaperPlacement{inset + float32(column)*(legacyCardWidth+legacyCardGap), layout.columnHeights[column], height}
		layout.placements = append(layout.placements, placement)
		bottom := placement.y + height + legacyCardBarHeight
		if len(layout.maxBottom) > 0 {
			bottom = max(bottom, layout.maxBottom[len(layout.maxBottom)-1])
		}
		layout.maxBottom = append(layout.maxBottom, bottom)
		layout.columnHeights[column] += height + legacyCardBarHeight + legacyCardGap
		layout.totalHeight = max(layout.totalHeight, layout.columnHeights[column])
	}
	return layout.placements, layout.totalHeight
}

// Placement tops and prefix maximum bottoms are monotone. Binary searches keep
// frame preparation proportional to the nearby items without losing tall cards.
func (layout *waterfallLayout) visibleRange(top, bottom float32) (int, int) {
	start := sort.Search(len(layout.maxBottom), func(i int) bool { return layout.maxBottom[i] >= top })
	end := sort.Search(len(layout.placements), func(i int) bool { return layout.placements[i].y > bottom })
	return start, max(start, end)
}

type thumbnailPrefetcher interface{ Prefetch(string) }

func thumbnailURL(wallpaper domain.Wallpaper) string {
	// The original thumbnail preserves the full image. The small variant can
	// already be cropped by the server, which no client-side fit can undo.
	if wallpaper.OriginalThumbnail != "" {
		return wallpaper.OriginalThumbnail
	}
	return wallpaper.Thumbnail
}

func (a *App) cardThumbnailURL(wallpaper domain.Wallpaper) string {
	if a.useSmallCardThumbnails() {
		return wallpaper.Thumbnail
	}
	return thumbnailURL(wallpaper)
}

func (a *App) useSmallCardThumbnails() bool {
	page := a.controller.CurrentPage().ID
	return page == domain.Recommend || page == domain.WallpaperPage && a.previewMode() == domain.PreviewTile
}

func (a *App) prefetchThumbnail(wallpaper domain.Wallpaper) {
	if prefetcher, ok := a.images.(thumbnailPrefetcher); ok {
		prefetcher.Prefetch(a.cardThumbnailURL(wallpaper))
	}
}
