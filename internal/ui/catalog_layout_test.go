package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func layoutWallpapers(count int) []domain.Wallpaper {
	wallpapers := make([]domain.Wallpaper, count)
	for i := range wallpapers {
		wallpapers[i] = domain.Wallpaper{ID: fmt.Sprintf("p%05d", i), Width: 1200 + i%4*200, Height: 700 + i%7*300}
	}
	return wallpapers
}

func assertLegacyLayout(t *testing.T, layout *waterfallLayout, wallpapers []domain.Wallpaper, width float32, generation uint64) []wallpaperPlacement {
	t.Helper()
	got, height := layout.update(wallpapers, width, generation)
	want, wantHeight := legacyWaterfall(wallpapers, width)
	if !slices.Equal(got, want) || height != wantHeight {
		t.Fatalf("cached layout differs from source geometry: got %d items/height%f want %d/height%f", len(got), height, len(want), wantHeight)
	}
	return got
}

func TestWaterfallCacheAppendResetAndReuse(t *testing.T) {
	wallpapers := layoutWallpapers(200)
	var layout waterfallLayout
	first := assertLegacyLayout(t, &layout, wallpapers[:24], 1280, 7)
	prefix := append([]wallpaperPlacement(nil), first...)
	unchanged := assertLegacyLayout(t, &layout, wallpapers[:24], 1280, 7)
	if &unchanged[0] != &first[0] {
		t.Fatal("unchanged thumbnail redraw allocated another placement array")
	}
	all := assertLegacyLayout(t, &layout, wallpapers, 1280, 7)
	if !slices.Equal(all[:len(prefix)], prefix) {
		t.Fatal("appending a page moved existing wallpaper positions")
	}
	// A different query can have the same count and width but different heights.
	replacement := layoutWallpapers(200)
	replacement[0].Height = 5000
	assertLegacyLayout(t, &layout, replacement, 1280, 8)
	if layout.placements[0].imageHeight == prefix[0].imageHeight {
		t.Fatal("new query reused previous wallpaper dimensions")
	}
	// Same column count still requires a new centering offset.
	oldX := layout.placements[0].x
	assertLegacyLayout(t, &layout, replacement, 1290, 8)
	if layout.placements[0].x != oldX+5 {
		t.Fatal("width-only centering change was ignored")
	}
	assertLegacyLayout(t, &layout, replacement[:8], 1290, 8)
	assertLegacyLayout(t, &layout, nil, 800, 9)
}

func TestWaterfallVisibleRangeRetainsTallOverlaps(t *testing.T) {
	wallpapers := layoutWallpapers(300)
	wallpapers[0].Width, wallpapers[0].Height = 100, 10000
	var layout waterfallLayout
	placements, _ := layout.update(wallpapers, 1280, 1)
	for _, window := range [][2]float32{{0, 800}, {5000, 5800}, {15000, 15800}, {35000, 36000}} {
		start, end := layout.visibleRange(window[0], window[1])
		if start < 0 || start > end || end > len(placements) {
			t.Fatalf("invalid visible range %d..%d", start, end)
		}
		for i, placement := range placements {
			intersects := placement.y+placement.imageHeight+legacyCardBarHeight >= window[0] && placement.y <= window[1]
			if intersects && (i < start || i >= end) {
				t.Fatalf("window%v lost overlapping item%d at%+v in range%d..%d", window, i, placement, start, end)
			}
		}
	}
	start, end := layout.visibleRange(15000, 15800)
	if start != 0 || end < 50 {
		t.Fatal("fixture must retain the very tall first item across many expired short items")
	}
	// Binary-search bounds are deliberately conservative: renderers must still
	// test each candidate's exact intersection before building or prefetching it.
	visible := 0
	for _, placement := range placements[start:end] {
		if placement.y+placement.imageHeight+legacyCardBarHeight >= 15000 && placement.y <= 15800 {
			visible++
		}
	}
	if visible >= end-start {
		t.Fatal("fixture did not expose conservative-range false positives")
	}
}

type layoutSearchReply struct {
	result domain.SearchResult
	err    error
}
type layoutSearchCall struct {
	ctx   context.Context
	query domain.SearchQuery
	page  int
	reply chan layoutSearchReply
}
type layoutSearchSource struct{ calls chan layoutSearchCall }

func (s *layoutSearchSource) Search(ctx context.Context, query domain.SearchQuery, page int) (domain.SearchResult, error) {
	call := layoutSearchCall{ctx: ctx, query: query, page: page, reply: make(chan layoutSearchReply, 1)}
	s.calls <- call
	select {
	case reply := <-call.reply:
		return reply.result, reply.err
	case <-ctx.Done():
		return domain.SearchResult{}, ctx.Err()
	}
}

func nextLayoutSearch(t *testing.T, source *layoutSearchSource) layoutSearchCall {
	t.Helper()
	select {
	case call := <-source.calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("missing search request")
		return layoutSearchCall{}
	}
}
func applyLayoutUpdate(t *testing.T, updates <-chan func()) {
	t.Helper()
	select {
	case update := <-updates:
		update()
	case <-time.After(3 * time.Second):
		t.Fatal("missing search state publication")
	}
}

func TestSearchSnapshotCacheTracksLoadingFailureQueryAndRefresh(t *testing.T) {
	source := &layoutSearchSource{calls: make(chan layoutSearchCall, 10)}
	updates := make(chan func(), 10)
	controller := app.New(app.Options{Source: source, Dispatch: func(update func()) { updates <- update }})
	t.Cleanup(controller.Close)
	screen := &App{controller: controller}
	controller.Navigate(domain.WallpaperPage)
	loading := screen.searchSnapshot()
	if !loading.Loading || len(loading.Wallpapers) != 0 {
		t.Fatalf("initial snapshot did not show loading: %+v", loading)
	}
	first := nextLayoutSearch(t, source)
	first.reply <- layoutSearchReply{result: domain.SearchResult{Wallpapers: layoutWallpapers(2), CurrentPage: 1, LastPage: 3, Total: 6}}
	applyLayoutUpdate(t, updates)
	loaded := screen.searchSnapshot()
	if loaded.Loading || len(loaded.Wallpapers) != 2 {
		t.Fatalf("loaded state was stale: %+v", loaded)
	}
	reused := screen.searchSnapshot()
	if &reused.Wallpapers[0] != &loaded.Wallpapers[0] {
		t.Fatal("unchanged revision copied the wallpaper snapshot")
	}
	public := controller.SearchState()
	public.Wallpapers[0].ID = "edited"
	if screen.searchSnapshot().Wallpapers[0].ID == "edited" {
		t.Fatal("external snapshot mutated the UI cache")
	}
	controller.LoadMore()
	if state := screen.searchSnapshot(); !state.Loading || len(state.Wallpapers) != 2 {
		t.Fatal("page loading state was not published while preserving old results")
	}
	pageTwo := nextLayoutSearch(t, source)
	pageTwo.reply <- layoutSearchReply{err: errors.New("page failed")}
	applyLayoutUpdate(t, updates)
	if state := screen.searchSnapshot(); state.Loading || state.Error != "page failed" || len(state.Wallpapers) != 2 {
		t.Fatalf("failed page state was stale: %+v", state)
	}
	controller.LoadMore()
	if state := screen.searchSnapshot(); !state.Loading || state.Error != "" {
		t.Fatal("retry did not clear cached error")
	}
	retry := nextLayoutSearch(t, source)
	newPage := layoutWallpapers(2)
	newPage[0].ID, newPage[1].ID = "new001", "new002"
	retry.reply <- layoutSearchReply{result: domain.SearchResult{Wallpapers: newPage, CurrentPage: 2, LastPage: 3, Total: 6}}
	applyLayoutUpdate(t, updates)
	if state := screen.searchSnapshot(); len(state.Wallpapers) != 4 || state.Generation != loaded.Generation {
		t.Fatal("appended page lost generation or used stale data")
	}
	query := domain.DefaultSearchQuery()
	query.Query = "replacement"
	controller.Search(query)
	if state := screen.searchSnapshot(); !state.Loading || len(state.Wallpapers) != 0 || state.Generation == loaded.Generation {
		t.Fatal("new query retained cached old results")
	}
	replacement := nextLayoutSearch(t, source)
	replacement.reply <- layoutSearchReply{result: domain.SearchResult{Wallpapers: newPage, CurrentPage: 1, LastPage: 1, Total: 2}}
	applyLayoutUpdate(t, updates)
	replaced := screen.searchSnapshot()
	controller.Refresh()
	if state := screen.searchSnapshot(); !state.Loading || len(state.Wallpapers) != 0 || state.Generation == replaced.Generation {
		t.Fatal("refresh reused stale cached results")
	}
	refresh := nextLayoutSearch(t, source)
	controller.Close()
	if screen.searchSnapshot().Loading {
		t.Fatal("closed controller left cached loading state active")
	}
	select {
	case <-refresh.ctx.Done():
	default:
		t.Fatal("refresh work was not canceled")
	}
}
