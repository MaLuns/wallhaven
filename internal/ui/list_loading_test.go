package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type gatedListReply struct {
	result domain.SearchResult
	err    error
}

type gatedListRequest struct {
	page  int
	ctx   context.Context
	reply chan gatedListReply
}

// Each page remains in flight until the test replies. This lets the view be
// exercised while loading, rather than asserting only an already-loaded list.
type gatedListSource struct{ requests chan gatedListRequest }

func (s *gatedListSource) Search(ctx context.Context, _ domain.SearchQuery, page int) (domain.SearchResult, error) {
	request := gatedListRequest{page: page, ctx: ctx, reply: make(chan gatedListReply, 1)}
	select {
	case s.requests <- request:
	case <-ctx.Done():
		return domain.SearchResult{}, ctx.Err()
	}
	select {
	case reply := <-request.reply:
		return reply.result, reply.err
	case <-ctx.Done():
		return domain.SearchResult{}, ctx.Err()
	}
}

type gatedListHarness struct {
	controller *app.Controller
	screen     *App
	tester     *kit.Tester
	source     *gatedListSource
	updates    chan func()
}

func newGatedListHarness(t *testing.T) *gatedListHarness {
	t.Helper()
	h := &gatedListHarness{source: &gatedListSource{requests: make(chan gatedListRequest, 10)}, updates: make(chan func(), 10)}
	h.controller = app.New(app.Options{Source: h.source, Dispatch: func(fn func()) { h.updates <- fn }})
	h.screen = New(h.controller, Options{Images: newCatalogImages()})
	t.Cleanup(h.controller.Close)
	t.Cleanup(h.screen.Close)
	h.controller.Navigate(domain.WallpaperPage)
	h.tester = kit.NewTester(h.screen.View, 1200, 800)
	return h
}

func (h *gatedListHarness) nextPage(t *testing.T) gatedListRequest {
	t.Helper()
	select {
	case request := <-h.source.requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("list did not request the next page")
		return gatedListRequest{}
	}
}

func (h *gatedListHarness) applyReply(t *testing.T) {
	t.Helper()
	select {
	case update := <-h.updates:
		update()
		h.tester.Frame()
	case <-time.After(3 * time.Second):
		t.Fatal("list page did not reach the UI")
	}
}

func listPageResult(page, last int) domain.SearchResult {
	result := domain.SearchResult{CurrentPage: page, LastPage: last, Total: 24 * last}
	for index := range 24 {
		result.Wallpapers = append(result.Wallpapers, domain.Wallpaper{
			ID: fmt.Sprintf("page-%d-%d", page, index), Width: 1600, Height: 900,
			Resolution: "1600x900", FileType: "image/jpeg", Thumbnail: fmt.Sprintf("thumb-%d-%d", page, index),
		})
	}
	return result
}

func scrollListToEnd(t *testing.T, h *gatedListHarness) kit.Rect {
	t.Helper()
	bounds, ok := h.tester.Find("搜索结果")
	if !ok {
		t.Fatal("missing wallpaper list")
	}
	h.tester.Scroll(bounds.X+bounds.W/2, bounds.Y+bounds.H/2, 0, 100000)
	h.tester.Frame()
	return bounds
}

func assertRemovedListFooter(t *testing.T, tester *kit.Tester) {
	t.Helper()
	if _, exists := tester.Find("加载下一页"); exists {
		t.Fatal("removed load-more button is still present")
	}
	for _, text := range []string{"加载更多", "为您筛选出", "张壁纸"} {
		if tester.HasText(text) {
			t.Fatalf("removed result footer remains: %q", text)
		}
	}
}

func TestListLoadingUsesAnimationWithoutFooterText(t *testing.T) {
	h := newGatedListHarness(t)
	first := h.nextPage(t)
	if first.page != 1 {
		t.Fatalf("first request page=%d", first.page)
	}
	assertRemovedListFooter(t, h.tester)
	if _, exists := h.tester.Find("正在加载壁纸"); !exists {
		t.Fatal("initial search does not expose its loading animation")
	}
	before := append([]byte(nil), h.tester.Image().Pix...)
	h.tester.Advance(250 * time.Millisecond)
	if bytes.Equal(before, h.tester.Image().Pix) {
		t.Fatal("loading indicator did not animate")
	}
	first.reply <- gatedListReply{result: listPageResult(1, 3)}
	h.applyReply(t)
	assertRemovedListFooter(t, h.tester)
}

func TestListTailLoadingIsSingleFlightAndPreservesScroll(t *testing.T) {
	h := newGatedListHarness(t)
	first := h.nextPage(t)
	first.reply <- gatedListReply{result: listPageResult(1, 3)}
	h.applyReply(t)
	assertRemovedListFooter(t, h.tester)
	scrollListToEnd(t, h)
	second := h.nextPage(t)
	if second.page != 2 {
		t.Fatalf("next request page=%d", second.page)
	}
	for range 5 {
		h.tester.Frame()
	}
	if len(h.source.requests) != 0 {
		t.Fatal("rebuilding a loading list queued duplicate page requests")
	}
	// The loading element belongs to the scrollable tail, not a screen-fixed
	// footer. Move to the new end after that tail has been inserted.
	viewport := scrollListToEnd(t, h)
	spinner, exists := h.tester.Find("正在加载下一页")
	if !exists || spinner.Y+spinner.H < viewport.Y || spinner.Y > viewport.Y+viewport.H {
		t.Fatalf("missing visible in-list tail animation: spinner=%+v viewport=%+v", spinner, viewport)
	}
	h.tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -200)
	h.tester.Frame()
	if moved, exists := h.tester.Find("正在加载下一页"); exists && moved.Y < spinner.Y+100 {
		t.Fatalf("loading indicator stayed screen-fixed instead of moving with the list: before=%+v after=%+v", spinner, moved)
	}
	scrollListToEnd(t, h)
	scroll := h.screen.results.Y
	second.reply <- gatedListReply{result: listPageResult(2, 3)}
	h.applyReply(t)
	if h.screen.results.Y != scroll {
		t.Fatalf("appending a page moved the reading position: %f -> %f", scroll, h.screen.results.Y)
	}
	if _, exists := h.tester.Find("正在加载下一页"); exists {
		t.Fatal("tail animation remains after the page finished")
	}
	if h.controller.SearchState().Loading {
		t.Fatal("a page far beyond the expanded list was prefetched immediately")
	}
	scrollListToEnd(t, h)
	third := h.nextPage(t)
	if third.page != 3 {
		t.Fatalf("final request page=%d", third.page)
	}
	third.reply <- gatedListReply{result: listPageResult(3, 3)}
	h.applyReply(t)
	scrollListToEnd(t, h)
	if h.controller.SearchState().Loading || h.controller.SearchState().HasMore || len(h.source.requests) != 0 {
		t.Fatal("completed list kept fetching past its last page")
	}
	if _, exists := h.tester.Find("正在加载下一页"); exists {
		t.Fatal("completed list retained a tail loading animation")
	}
	assertRemovedListFooter(t, h.tester)
}

func TestListPageFailureStopsAnimationAndRetriesSamePage(t *testing.T) {
	h := newGatedListHarness(t)
	first := h.nextPage(t)
	first.reply <- gatedListReply{result: listPageResult(1, 3)}
	h.applyReply(t)
	scrollListToEnd(t, h)
	second := h.nextPage(t)
	second.reply <- gatedListReply{err: errors.New("测试分页连接失败")}
	h.applyReply(t)
	for range 5 {
		h.tester.Frame()
	}
	if h.controller.SearchState().Loading || len(h.source.requests) != 0 {
		t.Fatal("failed page automatically entered a retry loop")
	}
	if _, exists := h.tester.Find("正在加载下一页"); exists {
		t.Fatal("failed page retained a loading animation")
	}
	if len(h.controller.SearchState().Wallpapers) != 24 {
		t.Fatal("failed append discarded loaded images")
	}
	click(t, h.tester, "重试")
	retry := h.nextPage(t)
	if retry.page != second.page {
		t.Fatalf("retry skipped the failed page: %d -> %d", second.page, retry.page)
	}
	retry.reply <- gatedListReply{result: listPageResult(2, 3)}
	h.applyReply(t)
	if h.controller.SearchState().Error != "" || len(h.controller.SearchState().Wallpapers) != 48 {
		t.Fatal("successful retry did not append the original failed page")
	}
}
