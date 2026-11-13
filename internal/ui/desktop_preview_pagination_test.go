package ui

import (
	"errors"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type desktopPagingHarness struct {
	*gatedListHarness
	controls *kit.Tester
}

func newDesktopPagingHarness(t *testing.T) *desktopPagingHarness {
	t.Helper()
	h := newGatedListHarness(t)
	first := h.nextPage(t)
	first.reply <- gatedListReply{result: listPageResult(1, 3)}
	h.applyReply(t)
	attachDesktopPreview(h.screen)
	h.screen.dispatch = func(fn func()) { h.updates <- fn }
	click(t, h.tester, "查看壁纸 page-1-0")
	click(t, h.tester, "桌面预览")
	return &desktopPagingHarness{gatedListHarness: h, controls: kit.NewTester(h.screen.DesktopControlsView, 800, DesktopPreviewControlsHeight)}
}

func (h *desktopPagingHarness) scrollEnd(t *testing.T) {
	t.Helper()
	strip, found := h.controls.Find("桌面预览壁纸列表")
	if !found {
		t.Fatal("missing desktop preview strip")
	}
	h.controls.Scroll(strip.X+strip.W/2, strip.Y+strip.H/2, 100000, 0)
	h.controls.Frame()
}

func (h *desktopPagingHarness) update(t *testing.T) {
	t.Helper()
	select {
	case fn := <-h.updates:
		fn()
		h.tester.Frame()
		h.controls.Frame()
	case <-time.After(3 * time.Second):
		t.Fatal("desktop pagination update was not dispatched")
	}
}

func (h *desktopPagingHarness) assertNoRequests(t *testing.T) {
	t.Helper()
	for range 5 {
		h.controls.Frame()
	}
	if len(h.source.requests) != 0 || len(h.updates) != 0 {
		t.Fatal("preview queued an unexpected or duplicate pagination request")
	}
}

func TestDesktopPreviewScrollEndLoadsOnceAndPreservesSelectionAndScroll(t *testing.T) {
	h := newDesktopPagingHarness(t)
	h.assertNoRequests(t)
	h.scrollEnd(t)
	for range 5 {
		h.controls.Frame()
	}
	if len(h.updates) != 1 || len(h.source.requests) != 0 {
		t.Fatal("rebuilds queued duplicate requests or started IO during view construction")
	}
	h.update(t)
	second := h.nextPage(t)
	if second.page != 2 {
		t.Fatalf("next page=%d", second.page)
	}
	for range 5 {
		h.scrollEnd(t)
	}
	h.assertNoRequests(t)
	if status, ok := h.controls.Access("桌面预览状态"); !ok || status.Description != "正在加载下一页…" {
		t.Fatalf("loading is hidden unless the last image is selected: %+v", status)
	}
	if h.controller.DetailState().Navigation.Waiting {
		t.Fatal("scroll pagination queued an image change")
	}
	scroll := h.screen.desktopViewer.scroll.X
	second.reply <- gatedListReply{result: listPageResult(2, 3)}
	h.update(t)
	if h.screen.desktopViewer.scroll.X != scroll || h.controller.DetailState().Wallpaper.ID != "page-1-0" {
		t.Fatal("appending a page changed the selected image or scroll position")
	}
	if list, _ := h.controller.DetailWallpapers(); len(list) != 48 {
		t.Fatal("next page was not appended to the opening list")
	}
	h.assertNoRequests(t) // The old MaxX must not immediately trigger page 3.
	h.scrollEnd(t)
	h.update(t)
	third := h.nextPage(t)
	if third.page != 3 {
		t.Fatalf("final page=%d", third.page)
	}
	third.reply <- gatedListReply{result: listPageResult(3, 3)}
	h.update(t)
	h.scrollEnd(t)
	h.assertNoRequests(t)
	if h.controller.DetailPaginationState().HasMore {
		t.Fatal("last page still offers continuation")
	}
}

func TestDesktopPreviewScrollFailureStopsAutomaticRetryAndRetriesSamePage(t *testing.T) {
	h := newDesktopPagingHarness(t)
	h.scrollEnd(t)
	h.update(t)
	second := h.nextPage(t)
	second.reply <- gatedListReply{err: errors.New("page unavailable")}
	h.update(t)
	for range 5 {
		h.scrollEnd(t)
	}
	h.assertNoRequests(t)
	if status, ok := h.controls.Access("桌面预览状态"); !ok || status.Description != "下一页加载失败" {
		t.Fatalf("pagination error was not shown: %+v", status)
	}
	click(t, h.controls, "重试下一页")
	retry := h.nextPage(t)
	if retry.page != second.page {
		t.Fatal("retry skipped the failed page")
	}
	h.update(t) // The explicit action's redraw.
	retry.reply <- gatedListReply{result: listPageResult(2, 3)}
	h.update(t)
	h.assertNoRequests(t)
	if h.controller.DetailState().Wallpaper.ID != "page-1-0" || h.controller.DetailPaginationState().Error != "" {
		t.Fatal("retry changed selection or retained the page error")
	}
}

func TestDesktopPreviewScrollSharesAnAlreadyStartedSourceRequest(t *testing.T) {
	h := newDesktopPagingHarness(t)
	h.scrollEnd(t)
	h.controller.LoadMore() // Another pagination entry wins before the queued UI callback.
	second := h.nextPage(t)
	h.update(t)
	h.assertNoRequests(t)
	second.reply <- gatedListReply{result: listPageResult(2, 3)}
	h.update(t)
	h.assertNoRequests(t)
}

func TestDesktopPreviewQueuedPaginationDoesNotRunAfterReopening(t *testing.T) {
	h := newDesktopPagingHarness(t)
	h.scrollEnd(t)
	local := domain.Wallpaper{ID: "local"}
	h.screen.openDetail(local, app.DetailList{Page: domain.Favorites, Wallpapers: []domain.Wallpaper{local}})
	h.screen.enterDesktopPreview()
	h.update(t) // A scroll callback queued by the old preview.
	h.assertNoRequests(t)
	if h.controller.DetailState().Wallpaper.ID != "local" {
		t.Fatal("old scroll callback changed the reopened preview")
	}
}
