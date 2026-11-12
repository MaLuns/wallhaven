package ui

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type viewerDetailSource struct{}

func (viewerDetailSource) Detail(_ context.Context, id string) (domain.WallpaperDetail, error) {
	return domain.WallpaperDetail{
		Wallpaper: domain.Wallpaper{ID: id, Path: "https://images.example/" + id + ".png", URL: "https://wallhaven.cc/w/" + id, Width: 480, Height: 280, Resolution: "480x280", FileType: "image/png", FileSize: 500000, Category: "general", Purity: "sfw"},
		Views:     123, Favorites: 45, Uploader: "测试作者", CreatedAt: "2026-11-12", Tags: []domain.WallpaperTag{{Name: "自然"}, {Name: "风景"}}, Colors: []string{"#0066cc", "77cc33"},
	}, nil
}

type originalReply struct {
	image *kit.Bitmap
	err   error
}
type originalCall struct {
	ctx      context.Context
	reply    chan originalReply
	url      string
	progress func(int64, int64)
}
type viewerImageSource struct{ calls chan originalCall }

func (s *viewerImageSource) Load(ctx context.Context, url string, progress func(int64, int64)) (*kit.Bitmap, error) {
	call := originalCall{ctx: ctx, url: url, reply: make(chan originalReply, 1), progress: progress}
	s.calls <- call
	select {
	case response := <-call.reply:
		return response.image, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newViewerHarness(t *testing.T) (*catalogHarness, *viewerImageSource) {
	t.Helper()
	h := &catalogHarness{source: &catalogSource{requests: make(chan requestRecord, 100)}, updates: make(chan func(), 100)}
	h.controller = app.New(app.Options{Source: h.source, Details: viewerDetailSource{}, Dispatch: func(fn func()) { h.updates <- fn }})
	loader := &viewerImageSource{calls: make(chan originalCall, 10)}
	h.screen = New(h.controller, Options{Images: newCatalogImages(), FullImages: loader, Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.controller.Close)
	t.Cleanup(h.screen.Close)
	h.tester = kit.NewTester(h.screen.View, 1200, 800)
	h.controller.Start()
	h.pump(t, 4)
	return h, loader
}
func waitViewer(t *testing.T, h *catalogHarness, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for !predicate() {
		select {
		case fn := <-h.updates:
			fn()
			h.tester.Frame()
		case <-deadline.C:
			t.Fatalf("viewer update timed out: %+v", h.controller.DetailState())
		}
	}
}
func nextOriginal(t *testing.T, loader *viewerImageSource) originalCall {
	t.Helper()
	select {
	case call := <-loader.calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("missing original request")
		return originalCall{}
	}
}

func TestInAppDetailOriginalZoomPanAndClose(t *testing.T) {
	h, loader := newViewerHarness(t)
	first := h.controller.Recommendations()[0].Wallpapers[0]
	click(t, h.tester, "查看壁纸 "+first.ID)
	waitViewer(t, h, func() bool { return !h.controller.DetailState().Loading && h.screen.viewer.url != "" })
	call := nextOriginal(t, loader)
	call.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	if len(h.tester.OpenedURLs()) != 0 {
		t.Fatal("card click opened an external page")
	}
	if _, shown := h.tester.Find("图片详细信息"); shown || h.screen.viewer.showInfo {
		t.Fatal("detail information was shown before clicking the information button")
	}
	click(t, h.tester, "图片信息")
	if !h.tester.HasText("测试作者") || !h.tester.HasText("标签：自然、风景") || !h.tester.HasText("查看 123 · 收藏 45") {
		t.Fatalf("detail metadata missing: %q", h.tester.Texts())
	}
	colorBounds, ok := h.tester.Find("主色 #0066cc")
	if !ok {
		t.Fatal("detail color palette missing")
	}
	color := h.tester.Image().RGBAAt(int(colorBounds.X+colorBounds.W/2), int(colorBounds.Y+colorBounds.H/2))
	if color.R != 0 || color.G != 0x66 || color.B != 0xcc {
		t.Fatalf("API color with # prefix was rendered incorrectly: %+v", color)
	}
	canvas, _ := h.tester.Find("壁纸大图")
	x, y := canvas.X+canvas.W/2, canvas.Y+canvas.H/2
	h.tester.Scroll(x, y, 0, -240)
	if h.screen.viewer.zoom <= 1 {
		t.Fatal("wheel did not zoom image")
	}
	h.tester.Press(x, y)
	h.tester.Move(x+80, y+45)
	h.tester.Release(x+80, y+45)
	if h.screen.viewer.panX != 80 || h.screen.viewer.panY != 45 {
		t.Fatalf("drag did not pan image: %f,%f", h.screen.viewer.panX, h.screen.viewer.panY)
	}
	click(t, h.tester, "还原图片")
	if h.screen.viewer.zoom != 1 || h.screen.viewer.panX != 0 || h.screen.viewer.panY != 0 {
		t.Fatal("reset did not restore fitted image")
	}
	h.tester.Pinch(x, y, 1.5)
	if h.screen.viewer.zoom <= 1 {
		t.Fatal("pinch did not zoom")
	}
	h.tester.Key(0, kit.KeyEscape)
	if h.controller.DetailState().Open || h.screen.viewer.image != nil {
		t.Fatal("Esc did not close/release viewer")
	}
	if h.controller.Recommendations()[0].Wallpapers[0].ID != first.ID {
		t.Fatal("closing detail changed recommendation state")
	}
}

func TestOriginalProgressTracksBytesAndCompletes(t *testing.T) {
	h, loader := newViewerHarness(t)
	h.screen.openDetail(h.controller.Recommendations()[0].Wallpapers[0])
	h.tester.Frame()
	waitViewer(t, h, func() bool { return !h.controller.DetailState().Loading && h.screen.viewer.url != "" })
	call := nextOriginal(t, loader)
	assertProgress := func(want float64) {
		t.Helper()
		bar, shown := h.tester.Access("原图加载中")
		if !shown || bar.Now != want || bar.Bounds.W != 70 || bar.Bounds.H != 3 {
			t.Fatalf("original progress want=%v got=%+v shown=%v", want, bar, shown)
		}
	}
	assertProgress(0)
	call.progress(25, 100)
	h.pump(t, 1)
	assertProgress(.25)
	// Missing Content-Length falls back to Wallhaven's file-size metadata.
	call.progress(250000, 0)
	h.pump(t, 1)
	assertProgress(.5)
	h.tester.Advance(time.Second)
	assertProgress(.5)
	call.progress(100, 100)
	h.pump(t, 1)
	assertProgress(.99)
	if !h.screen.viewer.loading {
		t.Fatal("transfer marked the original ready before decoding")
	}
	call.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	assertProgress(1)
	h.tester.Advance(200 * time.Millisecond)
	if _, shown := h.tester.Find("原图加载中"); shown {
		t.Fatal("completed progress bar did not hide")
	}
}

func TestDetailInformationDefaultsHiddenOnEveryOpen(t *testing.T) {
	h, _ := newViewerHarness(t)
	wallpapers := h.controller.Recommendations()[0].Wallpapers
	for i, wallpaper := range []domain.Wallpaper{wallpapers[0], wallpapers[0], wallpapers[1]} {
		if i == 1 {
			click(t, h.tester, "关闭详情")
		}
		h.screen.openDetail(wallpaper)
		h.tester.Frame()
		if _, shown := h.tester.Find("图片详细信息"); shown || h.screen.viewer.showInfo {
			t.Fatalf("open %d showed information by default", i)
		}
		click(t, h.tester, "图片信息")
		if _, shown := h.tester.Find("图片详细信息"); !shown || !h.screen.viewer.showInfo {
			t.Fatalf("open %d could not expand information", i)
		}
		click(t, h.tester, "图片信息")
		if _, shown := h.tester.Find("图片详细信息"); shown || h.screen.viewer.showInfo {
			t.Fatalf("open %d could not hide information", i)
		}
		// Leave it expanded to verify opening again resets the preference.
		click(t, h.tester, "图片信息")
	}
}

func TestOriginalRetryAndObsoleteRequestCancellation(t *testing.T) {
	h, loader := newViewerHarness(t)
	first := h.controller.Recommendations()[0].Wallpapers[0]
	h.screen.openDetail(first)
	h.tester.Frame()
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	call := nextOriginal(t, loader)
	call.progress(40, 100)
	waitViewer(t, h, func() bool { return h.screen.viewer.progress == .4 })
	call.reply <- originalReply{err: errors.New("原图暂时无法下载")}
	waitViewer(t, h, func() bool { return h.screen.viewer.error != "" })
	click(t, h.tester, "重试原图")
	retry := nextOriginal(t, loader)
	if retry.url != call.url {
		t.Fatal("retry changed original URL")
	}
	if h.screen.viewer.progress != 0 {
		t.Fatal("retry retained progress from the failed request")
	}
	retry.progress(80, 100)
	h.screen.closeDetail()
	h.tester.Frame()
	select {
	case <-retry.ctx.Done():
	default:
		t.Fatal("close did not cancel original transfer")
	}
	// Reopen the same ID while the old callback is queued. It must not restore
	// the closed image or overwrite the new selection.
	h.screen.openDetail(first)
	h.tester.Frame()
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	same := nextOriginal(t, loader)
	if h.screen.viewer.progress != 0 {
		t.Fatal("queued progress restored a closed request")
	}
	same.progress(70, 100)
	other := h.controller.Recommendations()[0].Wallpapers[1]
	h.screen.openDetail(other)
	h.tester.Frame()
	waitViewer(t, h, func() bool { return h.screen.viewer.id == other.ID && h.screen.viewer.url != "" })
	current := nextOriginal(t, loader)
	if h.screen.viewer.progress != 0 {
		t.Fatal("queued progress was applied to a different wallpaper")
	}
	select {
	case <-same.ctx.Done():
	default:
		t.Fatal("new selection did not cancel previous original")
	}
	current.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	if h.screen.viewer.id != other.ID || h.controller.DetailState().Wallpaper.ID != other.ID {
		t.Fatal("obsolete original restored old selection")
	}
	click(t, h.tester, "关闭详情")
	if h.controller.DetailState().Open {
		t.Fatal("close button did not close")
	}
}

func TestSearchDetailPreservesFiltersAndResults(t *testing.T) {
	h, loader := newViewerHarness(t)
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	click(t, h.tester, "壁纸关键词")
	h.tester.Type("mountains")
	h.tester.Key(0, kit.KeyEnter)
	h.pump(t, 1)
	before := h.controller.SearchState()
	scroll := h.screen.results.Y
	click(t, h.tester, "查看壁纸 "+before.Wallpapers[0].ID)
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	request := nextOriginal(t, loader)
	request.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	click(t, h.tester, "关闭详情")
	after := h.controller.SearchState()
	if h.screen.filters.keyword != "mountains" || after.Query.Query != before.Query.Query || len(after.Wallpapers) != len(before.Wallpapers) || h.screen.results.Y != scroll {
		t.Fatal("closing detail lost search filters, results or scroll")
	}
}

func TestDetailNavigationButtonsAndArrowKeys(t *testing.T) {
	h, loader := newViewerHarness(t)
	wallpapers := h.controller.Recommendations()[0].Wallpapers
	click(t, h.tester, "查看壁纸 "+wallpapers[0].ID)
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	first := nextOriginal(t, loader)
	first.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	if access, ok := h.tester.Access("上一张"); !ok || !access.Disabled {
		t.Fatalf("first image previous button is not disabled: %+v", access)
	}
	click(t, h.tester, "上一张")
	if state := h.controller.DetailState(); state.Wallpaper.ID != wallpapers[0].ID || state.Navigation.HasPrevious {
		t.Fatal("previous button changed the first image")
	}
	canvas, _ := h.tester.Find("壁纸大图")
	h.tester.Scroll(canvas.X+canvas.W/2, canvas.Y+canvas.H/2, 0, -240)
	click(t, h.tester, "图片信息")
	click(t, h.tester, "下一张")
	waitViewer(t, h, func() bool { return h.screen.viewer.id == wallpapers[1].ID && h.screen.viewer.url != "" })
	_ = nextOriginal(t, loader)
	if first.ctx.Err() != context.Canceled || h.screen.viewer.zoom != 1 || h.screen.viewer.panX != 0 || h.screen.viewer.panY != 0 || h.screen.viewer.showInfo {
		t.Fatal("next image did not cancel the old original and reset presentation")
	}
	h.tester.Key(0, kit.KeyLeft)
	waitViewer(t, h, func() bool { return h.screen.viewer.id == wallpapers[0].ID && h.screen.viewer.url != "" })
	_ = nextOriginal(t, loader)
	// The toolbar has focus after a click; arrows must still navigate.
	click(t, h.tester, "图片信息")
	h.tester.Key(0, kit.KeyRight)
	waitViewer(t, h, func() bool { return h.screen.viewer.id == wallpapers[1].ID && h.screen.viewer.url != "" })
	_ = nextOriginal(t, loader)
	h.tester.Key(kit.Shift, kit.KeyRight)
	if h.controller.DetailState().Wallpaper.ID != wallpapers[1].ID {
		t.Fatal("modified arrow key changed selection")
	}
	click(t, h.tester, "上一张")
	if h.controller.DetailState().Wallpaper.ID != wallpapers[0].ID {
		t.Fatal("previous button did not follow the opening list")
	}
	click(t, h.tester, "关闭详情")
	h.tester.Key(0, kit.KeyRight)
	if h.controller.DetailState().Open {
		t.Fatal("arrow key reopened the closed viewer")
	}
}

func TestSearchDetailNavigationLoadsNextPageIntoOpeningList(t *testing.T) {
	h, _ := newViewerHarness(t)
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	before := h.controller.SearchState()
	last := before.Wallpapers[len(before.Wallpapers)-1]
	h.screen.results.Y = 100
	h.screen.openDetail(last)
	h.tester.Frame()
	click(t, h.tester, "下一张")
	waitViewer(t, h, func() bool { return h.controller.SearchState().CurrentPage == 2 })
	state := h.controller.SearchState()
	want := state.Wallpapers[len(before.Wallpapers)].ID
	waitViewer(t, h, func() bool { return h.screen.viewer.id == want })
	if state.Query.Query != before.Query.Query || state.Query.Sorting != before.Query.Sorting || len(state.Wallpapers) != len(before.Wallpapers)*2 || h.screen.results.Y != 100 {
		t.Fatal("detail continuation lost source list, filters or scroll position")
	}
	h.tester.Key(0, kit.KeyLeft)
	if h.controller.DetailState().Wallpaper.ID != last.ID {
		t.Fatal("previous could not cross back over the page boundary")
	}
	for range len(before.Wallpapers) {
		h.tester.Key(0, kit.KeyRight)
	}
	if h.controller.DetailState().Wallpaper.ID != state.Wallpapers[len(state.Wallpapers)-1].ID {
		t.Fatal("keyboard did not reach the final image")
	}
	if access, ok := h.tester.Access("下一张"); !ok || !access.Disabled {
		t.Fatalf("final image next button is not disabled: %+v", access)
	}
	click(t, h.tester, "下一张")
	if detail := h.controller.DetailState(); detail.Wallpaper.ID != state.Wallpapers[len(state.Wallpapers)-1].ID || detail.Navigation.HasNext {
		t.Fatal("next button changed the final image")
	}
}

func TestFavoriteDetailNavigationUsesFavoriteOrder(t *testing.T) {
	h := newActionsHarness(t)
	wallpapers := []domain.Wallpaper{{ID: "local-c"}, {ID: "local-a"}, {ID: "local-b"}}
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{wallpapers: wallpapers}
	h.wait(t, func() bool { return h.actions.Library().Ready })
	click(t, h.tester, "前往收藏")
	wallpapers = h.actions.Library().Wallpapers
	click(t, h.tester, "查看壁纸 "+wallpapers[1].ID)
	click(t, h.tester, "下一张")
	if state := h.controller.DetailState(); state.Wallpaper.ID != wallpapers[2].ID || state.Navigation.HasNext {
		t.Fatalf("favorite next did not follow its source list: %+v", state)
	}
	h.tester.Key(0, kit.KeyLeft)
	h.tester.Key(0, kit.KeyLeft)
	if state := h.controller.DetailState(); state.Wallpaper.ID != wallpapers[0].ID || state.Navigation.HasPrevious {
		t.Fatalf("favorite previous did not follow its source list: %+v", state)
	}
}

func TestDetailToolbarFitsMinimumWindow(t *testing.T) {
	h, _ := newViewerHarness(t)
	h.tester.SetSize(800, 540)
	click(t, h.tester, "查看壁纸 "+h.controller.Recommendations()[0].Wallpapers[0].ID)
	for _, label := range []string{"关闭详情", "上一张", "下一张", "还原图片", "图片信息", "下载原图", "设为壁纸原图", "收藏原图"} {
		bounds, found := h.tester.Find(label)
		if !found || bounds.W != 36 || bounds.H != 36 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 800 || bounds.Y+bounds.H > 540 {
			t.Fatalf("toolbar action %s is clipped at minimum size: %+v", label, bounds)
		}
	}
}

func TestDetailPreview(t *testing.T) {
	directory := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if directory == "" {
		t.Skip("preview export not requested")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	h, loader := newViewerHarness(t)
	click(t, h.tester, "查看壁纸 "+h.controller.Recommendations()[0].Wallpapers[0].ID)
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	h.tester.SetDark(true)
	call := nextOriginal(t, loader)
	call.progress(50, 100)
	waitViewer(t, h, func() bool { return h.screen.viewer.progress == .5 })
	h.tester.Advance(450 * time.Millisecond)
	writeListPreview(t, h.tester, filepath.Join(directory, "detail-loading-dark.png"))
	call.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	for _, dark := range []bool{false, true} {
		h.tester.SetDark(dark)
		name := "detail-light.png"
		if dark {
			name = "detail-dark.png"
		}
		file, err := os.Create(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, h.tester.Image())
		closeErr := file.Close()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		info, _ := h.tester.Find("图片信息")
		h.tester.Move(info.X+info.W/2, info.Y+info.H/2)
		h.tester.Advance(650 * time.Millisecond)
		writeListPreview(t, h.tester, filepath.Join(directory, "tooltip-"+name))
		h.tester.Move(0, 0)
		click(t, h.tester, "图片信息")
		h.tester.Advance(200 * time.Millisecond)
		writeListPreview(t, h.tester, filepath.Join(directory, "information-"+name))
		click(t, h.tester, "图片信息")
		h.tester.Advance(140 * time.Millisecond)
	}
	h.tester.SetSize(800, 540)
	click(t, h.tester, "图片信息")
	h.tester.Advance(200 * time.Millisecond)
	writeListPreview(t, h.tester, filepath.Join(directory, "detail-small-dark.png"))
}
