package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

var aspectTop = color.RGBA{R: 230, G: 70, B: 40, A: 255}
var aspectBottom = color.RGBA{R: 40, G: 190, B: 80, A: 255}

func aspectBitmap(width, height int) *kit.Bitmap {
	bitmap := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(bitmap, bitmap.Bounds(), image.NewUniform(color.RGBA{R: 40, G: 80, B: 220, A: 255}), image.Point{}, draw.Src)
	draw.Draw(bitmap, image.Rect(0, 0, width, height/4), image.NewUniform(aspectTop), image.Point{}, draw.Src)
	draw.Draw(bitmap, image.Rect(0, height*3/4, width, height), image.NewUniform(aspectBottom), image.Point{}, draw.Src)
	return kit.NewBitmap(bitmap)
}

type aspectThumbnails struct {
	bitmaps             map[string]*kit.Bitmap
	visible, prefetched map[string]bool
}

func (images *aspectThumbnails) Get(url string) (kit.ImageSource, bool) {
	images.visible[url] = true
	if bitmap := images.bitmaps[url]; bitmap != nil {
		return bitmap, false
	}
	return nil, false
}

func (images *aspectThumbnails) Prefetch(url string) { images.prefetched[url] = true }

func TestRecommendationUsesSmallForFixedCardsAndPrefetch(t *testing.T) {
	controller := app.New()
	t.Cleanup(controller.Close)
	images := &aspectThumbnails{
		bitmaps: map[string]*kit.Bitmap{"small": aspectBitmap(300, 200), "original": aspectBitmap(300, 100)},
		visible: map[string]bool{}, prefetched: map[string]bool{},
	}
	screen := New(controller, Options{Images: images})
	wallpaper := domain.Wallpaper{ID: "recommendation", Width: 2400, Height: 800, Thumbnail: "small", OriginalThumbnail: "original"}
	tester := kit.NewTester(func(c *kit.Context) {
		kit.Column(c).Fill().Children(func() { screen.wallpaperCard(c, wallpaper) })
	}, 300, 240)
	screen.prefetchThumbnail(wallpaper)
	if !images.visible["small"] || !images.prefetched["small"] || images.visible["original"] || images.prefetched["original"] {
		t.Fatal("fixed recommendation card or its prefetch switched away from small")
	}
	area, _ := tester.Find("查看壁纸 recommendation")
	if area.W != 300 || area.H != 200 {
		t.Fatalf("homepage card changed its fixed size: %+v", area)
	}
	if top, bottom := tester.Image().RGBAAt(150, 10), tester.Image().RGBAAt(150, 190); top != aspectTop || bottom != aspectBottom {
		t.Fatalf("homepage small thumbnail no longer fills its fixed card: top=%+v bottom=%+v", top, bottom)
	}
}

func TestWaterfallOriginalThumbnailsKeepFullImageAndVirtualLoading(t *testing.T) {
	h := newGatedListHarness(t)
	images := &aspectThumbnails{bitmaps: map[string]*kit.Bitmap{}, visible: map[string]bool{}, prefetched: map[string]bool{}}
	h.screen.images = images
	shapes := [][2]int{{2400, 800}, {900, 1200}, {1600, 900}, {1000, 1000}}
	bitmaps := []*kit.Bitmap{aspectBitmap(300, 100), aspectBitmap(90, 120), aspectBitmap(160, 90), aspectBitmap(100, 100)}
	result := domain.SearchResult{CurrentPage: 1, LastPage: 1, Total: 1200}
	for i := range result.Total {
		shape := shapes[i%len(shapes)]
		wallpaper := domain.Wallpaper{ID: fmt.Sprint(i), Width: shape[0], Height: shape[1], Thumbnail: fmt.Sprintf("small/%d", i), OriginalThumbnail: fmt.Sprintf("original/%d", i)}
		result.Wallpapers = append(result.Wallpapers, wallpaper)
		images.bitmaps[wallpaper.OriginalThumbnail] = bitmaps[i%len(shapes)]
	}
	first := h.nextPage(t)
	first.reply <- gatedListReply{result: result}
	h.applyReply(t)
	for i, wantHeight := range []float32{100, 400, 168.75} {
		bounds, found := h.tester.Find("查看壁纸 " + fmt.Sprint(i))
		if !found || bounds.W != 300 || bounds.H != wantHeight {
			t.Fatalf("original-aspect image %d bounds=%+v want height=%f", i, bounds, wantHeight)
		}
		for _, sample := range []struct {
			y    float32
			want color.RGBA
		}{{bounds.Y + 10, aspectTop}, {bounds.Y + bounds.H - 10, aspectBottom}} {
			if got := h.tester.Image().RGBAAt(int(bounds.X+150), int(sample.y)); got != sample.want {
				t.Fatalf("image %d lost its top or bottom edge: got=%+v want=%+v", i, got, sample.want)
			}
		}
	}
	assertVisible := func() {
		t.Helper()
		if len(images.visible) == 0 || len(images.visible) > 40 || images.visible["original/1199"] || images.prefetched["original/1199"] {
			t.Fatalf("virtual list requested distant images: visible=%d prefetched=%d", len(images.visible), len(images.prefetched))
		}
		for url := range images.visible {
			if _, original := images.bitmaps[url]; !original {
				t.Fatalf("list requested a cropped thumbnail: %s", url)
			}
		}
	}
	assertVisible()
	clear(images.visible)
	clear(images.prefetched)
	viewport, _ := h.tester.Find("搜索结果")
	h.tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 10000)
	h.tester.Frame()
	assertVisible()
	if _, stillMounted := h.tester.Find("查看壁纸 0"); stillMounted || images.visible["original/0"] {
		t.Fatal("scrolling retained a distant card or its visible-image request")
	}
}

func TestDetailPreviewKeepsPortraitAspectUntilOriginalArrives(t *testing.T) {
	controller := app.New()
	t.Cleanup(controller.Close)
	images := &aspectThumbnails{bitmaps: map[string]*kit.Bitmap{"original": aspectBitmap(90, 120)}, visible: map[string]bool{}}
	screen := New(controller, Options{Images: images})
	screen.viewer.zoom = 1
	wallpaper := domain.Wallpaper{ID: "portrait", Width: 900, Height: 1200, Thumbnail: "cropped-small", OriginalThumbnail: "original"}
	tester := kit.NewTester(func(c *kit.Context) {
		kit.Box(c).Fill().Background(kit.RGB(0, 0, 0)).Children(func() { screen.detailPicture(c, wallpaper, 700, 700) })
	}, 700, 700)
	checkEdges := func() {
		t.Helper()
		if top, bottom := tester.Image().RGBAAt(350, 150), tester.Image().RGBAAt(350, 550); top != aspectTop || bottom != aspectBottom {
			t.Fatalf("portrait preview lost full-image content: top=%+v bottom=%+v", top, bottom)
		}
	}
	checkEdges()
	if images.visible["cropped-small"] || !images.visible["original"] {
		t.Fatal("viewer requested the cropped thumbnail")
	}
	fit := screen.viewer.fit
	screen.viewer.image = aspectBitmap(900, 1200)
	tester.Frame()
	tester.Advance(220 * time.Millisecond)
	checkEdges()
	if screen.viewer.fit != fit {
		t.Fatalf("original changed the fitted preview size: %f -> %f", fit, screen.viewer.fit)
	}
}
