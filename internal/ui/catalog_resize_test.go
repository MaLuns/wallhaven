package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	kit "github.com/egoist/mygo/ui"
)

func TestListResizeReflowsAndLoadsVisibleThumbnails(t *testing.T) {
	h := newGatedListHarness(t)
	images := &countingImages{images: newCatalogImages(), seen: map[string]bool{}}
	h.screen.images = images
	first := h.nextPage(t)
	first.reply <- gatedListReply{result: listPageResult(1, 1)}
	h.applyReply(t)

	// The source layout has fixed 300px cards, 20px gaps and centered columns.
	// Read the settled resize directly: no wheel input or extra Frame should
	// be necessary to correct a layout or display newly visible thumbnails.
	for _, size := range []struct {
		width, height, columns, visible int
		firstX                          float32
	}{
		{1200, 800, 3, 9, 140},
		{800, 540, 2, 4, 100},
		{1300, 800, 4, 12, 30},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			clear(images.seen)
			h.tester.SetSize(size.width, size.height)
			viewport, ok := h.tester.Find("搜索结果")
			wantViewport := kit.Rect{X: 10, Y: 200, W: float32(size.width - 10), H: float32(size.height - 210)}
			if !ok || viewport != wantViewport {
				t.Fatalf("resized list viewport=%+v want=%+v", viewport, wantViewport)
			}
			for index := range size.visible {
				want := kit.Rect{X: size.firstX + float32(index%size.columns)*320, Y: 200 + float32(index/size.columns)*228.75, W: 300, H: 168.75}
				want.H = min(want.H, viewport.Y+viewport.H-want.Y)
				got, ok := h.tester.Find(fmt.Sprintf("查看壁纸 page-1-%d", index))
				if !ok || got != want {
					t.Fatalf("resized image %d=%+v want=%+v", index, got, want)
				}
				if !images.seen[fmt.Sprintf("thumb-1-%d", index)] {
					t.Fatalf("visible image %d was not requested during resize", index)
				}
				// A request alone does not prove that the final frame paints the
				// image. Sample inside every visible image, including partial rows.
				pixel := h.tester.Image().RGBAAt(int(got.X+150), int(got.Y+20))
				if pixel.B != 190 || pixel.G < 135 {
					t.Fatalf("visible image %d still paints a placeholder: %+v", index, pixel)
				}
			}
			if images.seen["thumb-1-23"] {
				t.Fatal("resize requested a far offscreen thumbnail")
			}
		})
	}
}

// WALLHAVEN_PREVIEW_DIR is opt-in so normal tests do not write screenshots.
func TestListLoadingPreview(t *testing.T) {
	dir := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set WALLHAVEN_PREVIEW_DIR to export native list previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newGatedListHarness(t)
	first := h.nextPage(t)
	writeListPreview(t, h.tester, filepath.Join(dir, "list-first-loading.png"))
	first.reply <- gatedListReply{result: listPageResult(1, 3)}
	h.applyReply(t)
	writeListPreview(t, h.tester, filepath.Join(dir, "list-first-page.png"))
	scrollListToEnd(t, h)
	h.nextPage(t)
	scrollListToEnd(t, h)
	if _, ok := h.tester.Find("正在加载下一页"); !ok {
		t.Fatal("tail preview does not contain the loading animation")
	}
	writeListPreview(t, h.tester, filepath.Join(dir, "list-tail-loading.png"))
}

func writeListPreview(t *testing.T, tester *kit.Tester, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, tester.Image()); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log(path)
}
