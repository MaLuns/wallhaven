package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	thumbnailimages "github.com/MaLuns/wallhaven-electron/internal/adapters/images"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

var motionThumbnailColor = color.RGBA{R: 220, G: 64, B: 45, A: 255}

type delayedThumbnailHarness struct {
	*gatedListHarness
	release  func()
	changed  <-chan struct{}
	requests *atomic.Int32
}

// Exercise the real asynchronous HTTP/decode cache: the server withholds its
// PNG until release, while the UI continues to paint its loading placeholder.
func newDelayedThumbnailHarness(t *testing.T) *delayedThumbnailHarness {
	t.Helper()
	h := newGatedListHarness(t)
	bitmap := image.NewRGBA(image.Rect(0, 0, 300, 200))
	draw.Draw(bitmap, bitmap.Bounds(), image.NewUniform(motionThumbnailColor), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	ready, started, changed := make(chan struct{}), make(chan struct{}, 1), make(chan struct{}, 1)
	var requests atomic.Int32
	var release sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-ready:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(encoded.Bytes())
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	cache := thumbnailimages.New(thumbnailimages.Options{HTTPClient: server.Client(), OnChange: func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}})
	t.Cleanup(cache.Close)
	h.screen.images, h.screen.appearance = cache, "dark"
	first := h.nextPage(t)
	result := listPageResult(1, 1)
	for index := range result.Wallpapers {
		result.Wallpapers[index].Thumbnail = server.URL + "/thumbnail.png"
	}
	first.reply <- gatedListReply{result: result}
	h.applyReply(t)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("thumbnail HTTP request did not start")
	}
	return &delayedThumbnailHarness{gatedListHarness: h, release: func() { release.Do(func() { close(ready) }) }, changed: changed, requests: &requests}
}

func (h *delayedThumbnailHarness) finishDownload(t *testing.T) {
	t.Helper()
	h.release()
	select {
	case <-h.changed:
		h.tester.Frame()
	case <-time.After(3 * time.Second):
		t.Fatal("thumbnail did not finish decoding")
	}
}

func thumbnailMotionArea(t *testing.T, tester *kit.Tester) kit.Rect {
	t.Helper()
	area, ok := tester.Find("查看壁纸 page-1-0")
	if !ok {
		t.Fatal("first thumbnail is missing")
	}
	return area
}

func thumbnailMotionPixel(tester *kit.Tester, area kit.Rect, x, y int) color.RGBA {
	return tester.Image().RGBAAt(int(area.X)+x, int(area.Y)+y)
}

func TestThumbnailDelayedImageFadesInAndDoesNotReplay(t *testing.T) {
	h := newDelayedThumbnailHarness(t)
	area := thumbnailMotionArea(t, h.tester)
	before := thumbnailMotionPixel(h.tester, area, 60, 80)
	if !h.tester.HasText("图片加载中") || before == motionThumbnailColor {
		t.Fatal("withheld thumbnail did not display its loading placeholder")
	}
	h.finishDownload(t)
	if start := thumbnailMotionPixel(h.tester, area, 60, 80); start != before {
		t.Fatalf("loaded thumbnail jumped to its final image: before=%+v start=%+v", before, start)
	}
	h.tester.Advance(80 * time.Millisecond)
	middle := thumbnailMotionPixel(h.tester, area, 60, 80)
	if middle == before || middle == motionThumbnailColor || middle.R <= before.R || middle.R >= motionThumbnailColor.R {
		t.Fatalf("thumbnail did not paint a blended intermediate frame: before=%+v middle=%+v final=%+v", before, middle, motionThumbnailColor)
	}
	h.tester.Advance(140 * time.Millisecond)
	if final := thumbnailMotionPixel(h.tester, area, 60, 80); final != motionThumbnailColor {
		t.Fatalf("thumbnail did not finish fading in: %+v", final)
	}
	for range 4 {
		h.tester.Frame()
		if pixel := thumbnailMotionPixel(h.tester, area, 60, 80); pixel != motionThumbnailColor {
			t.Fatalf("static rebuild replayed the thumbnail fade: %+v", pixel)
		}
	}
	h.tester.Advance(time.Second)
	if h.tester.FramePending() || h.requests.Load() != 1 {
		t.Fatalf("settled image kept animating or downloading: pending=%v requests=%d", h.tester.FramePending(), h.requests.Load())
	}
}

func TestThumbnailHoverFadesAndHasNoDetailTooltip(t *testing.T) {
	h := newDelayedThumbnailHarness(t)
	h.finishDownload(t)
	h.tester.Advance(220 * time.Millisecond)
	area := thumbnailMotionArea(t, h.tester)
	access, ok := h.tester.Access("查看壁纸 page-1-0")
	if !ok || strings.Contains(access.Description, "查看壁纸详情") {
		t.Fatalf("thumbnail lost its accessible name or retained its tooltip: %+v", access)
	}
	before := thumbnailMotionPixel(h.tester, area, 60, 15)
	h.tester.Move(area.X+200, area.Y+100)
	if start := thumbnailMotionPixel(h.tester, area, 60, 15); start != before {
		t.Fatalf("hover information appeared without its fade: before=%+v start=%+v", before, start)
	}
	h.tester.Advance(60 * time.Millisecond)
	middle := thumbnailMotionPixel(h.tester, area, 60, 15)
	h.tester.Advance(160 * time.Millisecond)
	shown := thumbnailMotionPixel(h.tester, area, 60, 15)
	if middle == before || middle == shown || shown == before {
		t.Fatalf("hover information lacks intermediate opacity: before=%+v middle=%+v shown=%+v", before, middle, shown)
	}
	h.tester.Advance(time.Second)
	if h.tester.HasText("查看壁纸详情") {
		t.Fatal("hovering still opens the removed detail tooltip")
	}
	h.tester.Move(5, 5)
	h.tester.Advance(60 * time.Millisecond)
	leaving := thumbnailMotionPixel(h.tester, area, 60, 15)
	if leaving == before || leaving == shown {
		t.Fatalf("hover information disappeared without its fade: shown=%+v leaving=%+v before=%+v", shown, leaving, before)
	}
	h.tester.Advance(160 * time.Millisecond)
	if after := thumbnailMotionPixel(h.tester, area, 60, 15); after != before {
		t.Fatalf("hover information remained after pointer exit: %+v", after)
	}
	// Re-entering uses the same image; only the information overlay appears.
	h.tester.Move(area.X+200, area.Y+100)
	h.tester.Advance(220 * time.Millisecond)
	if pixel := thumbnailMotionPixel(h.tester, area, 60, 80); pixel != motionThumbnailColor {
		t.Fatalf("hover re-entry restarted the image fade: %+v", pixel)
	}
}

func TestThumbnailCachedImagesAppearImmediatelyOnResizeAndReturn(t *testing.T) {
	h := newDelayedThumbnailHarness(t)
	h.finishDownload(t)
	h.tester.Advance(220 * time.Millisecond)
	for _, size := range []struct {
		width, height, visible int
	}{
		{800, 540, 4},
		{1300, 800, 12},
	} {
		h.tester.SetSize(size.width, size.height)
		for index := range size.visible {
			area, ok := h.tester.Find("查看壁纸 " + h.controller.SearchState().Wallpapers[index].ID)
			if !ok {
				t.Fatalf("resizing to %dx%d lost visible image %d", size.width, size.height, index)
			}
			if pixel := thumbnailMotionPixel(h.tester, area, 60, 20); pixel != motionThumbnailColor {
				t.Fatalf("cached image %d was hidden by a new fade at %dx%d: %+v", index, size.width, size.height, pixel)
			}
		}
	}
	if err := h.tester.Click("前往组件"); err != nil {
		t.Fatal(err)
	}
	if err := h.tester.Click("前往壁纸"); err != nil {
		t.Fatal(err)
	}
	area := thumbnailMotionArea(t, h.tester)
	if pixel := thumbnailMotionPixel(h.tester, area, 60, 80); pixel != motionThumbnailColor {
		t.Fatalf("returning to the page hid a cached thumbnail: %+v", pixel)
	}
	if h.requests.Load() != 1 {
		t.Fatalf("cached lifecycle requested the thumbnail again: %d requests", h.requests.Load())
	}
}

func TestThumbnailMotionKeepsDetailClickAndReturnsFromOffscreen(t *testing.T) {
	h := newDelayedThumbnailHarness(t)
	h.finishDownload(t)
	h.tester.Advance(220 * time.Millisecond)
	if err := h.tester.Click("查看壁纸 page-1-0"); err != nil {
		t.Fatal(err)
	}
	if !h.controller.DetailState().Open || h.controller.DetailState().Wallpaper.ID != "page-1-0" {
		t.Fatal("animated thumbnail no longer opens its own detail")
	}
	h.tester.Advance(220 * time.Millisecond)
	if _, ok := h.tester.Find("壁纸详情"); !ok {
		t.Fatal("thumbnail click did not show the native detail viewer")
	}
	if err := h.tester.Click("关闭详情"); err != nil {
		t.Fatal(err)
	}
	h.tester.Advance(220 * time.Millisecond)
	viewport, ok := h.tester.Find("搜索结果")
	if !ok {
		t.Fatal("closing detail did not return to the list")
	}
	h.tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 100000)
	h.tester.Advance(220 * time.Millisecond)
	if _, ok := h.tester.Find("查看壁纸 page-1-0"); ok {
		t.Fatal("test did not move the first thumbnail out of the virtualized list")
	}
	h.tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -100000)
	h.tester.Advance(220 * time.Millisecond)
	area := thumbnailMotionArea(t, h.tester)
	if pixel := thumbnailMotionPixel(h.tester, area, 60, 80); pixel != motionThumbnailColor {
		t.Fatalf("cached thumbnail did not return after scrolling into view: %+v", pixel)
	}
	if err := h.tester.Click("前往组件"); err != nil {
		t.Fatal(err)
	}
	h.tester.Advance(220 * time.Millisecond)
	if err := h.tester.Click("前往壁纸"); err != nil {
		t.Fatal(err)
	}
	h.tester.Advance(220 * time.Millisecond)
	area = thumbnailMotionArea(t, h.tester)
	if pixel := thumbnailMotionPixel(h.tester, area, 60, 80); pixel != motionThumbnailColor || h.requests.Load() != 1 {
		t.Fatalf("thumbnail did not survive page return: pixel=%+v requests=%d", pixel, h.requests.Load())
	}
}

func TestThumbnailURLPreservesOriginalAspectAcrossShapes(t *testing.T) {
	wallpaper := domain.Wallpaper{Thumbnail: "small", OriginalThumbnail: "original"}
	for _, sample := range []struct {
		name          string
		width, height int
	}{
		{"landscape", 1920, 1080},
		{"near-three-by-two", 4096, 2731},
		{"ultrawide", 3840, 1080},
		{"square", 1000, 1000},
		{"portrait", 900, 1200},
	} {
		t.Run(sample.name, func(t *testing.T) {
			wallpaper.Width, wallpaper.Height = sample.width, sample.height
			if got := thumbnailURL(wallpaper); got != "original" {
				t.Fatalf("%dx%d used cropped thumbnail: %s", sample.width, sample.height, got)
			}
		})
	}
	wallpaper.OriginalThumbnail = ""
	if got := thumbnailURL(wallpaper); got != "small" {
		t.Fatalf("missing original thumbnail did not use its available preview: %s", got)
	}
}
