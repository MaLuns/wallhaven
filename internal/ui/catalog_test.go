package ui

import (
	"context"
	"fmt"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type requestRecord struct {
	query domain.SearchQuery
	page  int
}
type catalogSource struct{ requests chan requestRecord }

func (s *catalogSource) Search(_ context.Context, q domain.SearchQuery, page int) (domain.SearchResult, error) {
	s.requests <- requestRecord{q, page}
	if q.Query == "empty" {
		return domain.SearchResult{CurrentPage: 1, LastPage: 1}, nil
	}
	if q.Query == "error" {
		return domain.SearchResult{}, fmt.Errorf("测试网络错误")
	}
	result := domain.SearchResult{CurrentPage: page, LastPage: 2, Total: 24}
	for i := 0; i < 12; i++ {
		result.Wallpapers = append(result.Wallpapers, domain.Wallpaper{ID: fmt.Sprintf("%s-%d-%d", q.Sorting, page, i), Resolution: "2560 × 1440", FileSize: 2345678, FileType: "image/jpeg", URL: "https://wallhaven.cc/w/test", Thumbnail: fmt.Sprintf("%s/%d", q.Sorting, i)})
	}
	return result, nil
}

type catalogImages struct{ bitmap *kit.Bitmap }

func newCatalogImages() catalogImages {
	img := image.NewRGBA(image.Rect(0, 0, 480, 280))
	for y := 0; y < 280; y++ {
		for x := 0; x < 480; x++ {
			r, g, b := uint8(75+y/3), uint8(135+y/4), uint8(190)
			if y > 160+x/8 {
				r, g, b = uint8(35+y/5), uint8(90+x/8), uint8(100+y/6)
			}
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}
	return catalogImages{kit.NewBitmap(img)}
}
func (s catalogImages) Get(string) (kit.ImageSource, bool) { return s.bitmap, false }

type catalogHarness struct {
	screen     *App
	controller *app.Controller
	tester     *kit.Tester
	source     *catalogSource
	updates    chan func()
}

func newCatalogHarness(t *testing.T) *catalogHarness {
	t.Helper()
	h := &catalogHarness{source: &catalogSource{requests: make(chan requestRecord, 100)}, updates: make(chan func(), 100)}
	h.controller = app.New(app.Options{Source: h.source, Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.controller.Close)
	h.screen = New(h.controller, Options{Images: newCatalogImages()})
	h.tester = kit.NewTester(h.screen.View, 1200, 800)
	h.controller.Start()
	h.pump(t, 4)
	return h
}
func (h *catalogHarness) pump(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case fn := <-h.updates:
			fn()
			h.tester.Frame()
		case <-time.After(3 * time.Second):
			t.Fatal("missing UI update")
		}
	}
}
func click(t *testing.T, tt *kit.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	// Ordinary interaction tests inspect the completed control transition.
	// Animation-specific tests drive Click and Advance separately.
	tt.Advance(220 * time.Millisecond)
}
func lastRequest(s *catalogSource) requestRecord {
	var request requestRecord
	for {
		select {
		case request = <-s.requests:
		default:
			return request
		}
	}
}

// Bring controls in the source-style overlay into view by scrolling its own
// container, rather than sending clicks to hidden coordinates.
func scrollFilterIntoView(t *testing.T, tester *kit.Tester, label string) {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		panel, ok := tester.Find("高级筛选面板")
		if !ok {
			t.Fatal("missing advanced filter panel")
		}
		control, ok := tester.Find(label)
		if !ok {
			t.Fatalf("missing filter %s", label)
		}
		// Find reports clipped bounds. A sliver at the viewport edge is not
		// the complete 28–30px control we intend to exercise.
		if control.H >= 27.5 && control.Y >= panel.Y && control.Y+control.H <= panel.Y+panel.H {
			return
		}
		tester.Scroll(panel.X+panel.W/2, panel.Y+panel.H/2, 0, control.Y+control.H/2-panel.Y-panel.H/2)
		tester.Frame()
	}
	t.Fatalf("filter %s could not be scrolled into view", label)
}

func TestCatalogControlsAndPagination(t *testing.T) {
	h := newCatalogHarness(t)
	for _, group := range h.controller.Recommendations() {
		if len(group.Wallpapers) != 12 {
			t.Fatalf("group %s not loaded", group.Key)
		}
	}
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	click(t, h.tester, "壁纸关键词")
	h.tester.Type("cat & sea")
	h.tester.Key(0, kit.KeyEnter)
	h.pump(t, 1)
	request := lastRequest(h.source)
	if request.query.Query != "cat & sea" || request.page != 1 {
		t.Fatalf("keyword submit: %+v", request)
	}
	click(t, h.tester, "动漫")
	click(t, h.tester, "高级筛选")
	click(t, h.tester, "Sketchy")
	scrollFilterIntoView(t, h.tester, "排行榜")
	click(t, h.tester, "排行榜")
	click(t, h.tester, "搜索壁纸")
	h.pump(t, 1)
	request = lastRequest(h.source)
	if request.query.Categories != "010" || request.query.Purity != "110" || request.query.Sorting != "toplist" || request.query.TopRange != "1M" {
		t.Fatalf("filters submit: %+v", request.query)
	}
	if h.screen.advanced {
		t.Fatal("advanced filters did not collapse after submit")
	}
	results, _ := h.tester.Find("搜索结果")
	h.tester.Scroll(results.X+results.W/2, results.Y+results.H/2, 0, 5000)
	h.pump(t, 1)
	if len(h.controller.SearchState().Wallpapers) != 24 || h.controller.SearchState().HasMore {
		t.Fatalf("pagination: %+v", h.controller.SearchState())
	}
	click(t, h.tester, "前往收藏")
	click(t, h.tester, "前往壁纸")
	if len(h.controller.SearchState().Wallpapers) != 24 || h.screen.filters.keyword != "cat & sea" {
		t.Fatal("navigation lost search state")
	}
	click(t, h.tester, "高级筛选")
	scrollFilterIntoView(t, h.tester, "重置筛选")
	click(t, h.tester, "重置筛选")
	h.pump(t, 1)
	request = lastRequest(h.source)
	if request.query.Query != "" || request.query.Categories != "111" || request.query.Purity != "100" || request.query.Sorting != "date_added" {
		t.Fatalf("reset: %+v", request.query)
	}
}

func TestSearchEmptyErrorAndScrollLoading(t *testing.T) {
	h := newCatalogHarness(t)
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	// Scrolling through the real virtual grid reaches its pagination boundary.
	bounds, ok := h.tester.Find("搜索结果")
	if !ok {
		t.Fatal("missing result grid")
	}
	h.tester.Scroll(bounds.X+bounds.W/2, bounds.Y+bounds.H/2, 0, 5000)
	h.pump(t, 1)
	if h.controller.SearchState().CurrentPage != 2 {
		t.Fatal("scroll did not fetch next page")
	}
	for _, query := range []string{"empty", "error"} {
		h.screen.filters.keyword = query
		h.screen.submitSearch()
		h.pump(t, 1)
		want := "没有找到符合条件的壁纸"
		if query == "error" {
			want = "壁纸加载失败"
		}
		if !h.tester.HasText(want) {
			t.Fatalf("missing %s: %q", want, h.tester.Texts())
		}
	}
	click(t, h.tester, "重试")
	h.pump(t, 1)
	if !h.tester.HasText("测试网络错误") {
		t.Fatal("retry must preserve query/error")
	}
}

func TestAdvancedResolutionRatioAndColorControls(t *testing.T) {
	h := newCatalogHarness(t)
	click(t, h.tester, "前往壁纸")
	h.pump(t, 1)
	click(t, h.tester, "高级筛选")
	scrollFilterIntoView(t, h.tester, "升序")
	click(t, h.tester, "升序")
	scrollFilterIntoView(t, h.tester, "自定义分辨率")
	click(t, h.tester, "自定义分辨率")
	h.tester.Type("3840x2160")
	scrollFilterIntoView(t, h.tester, "相等")
	click(t, h.tester, "相等")
	panel, ok := h.tester.Find("高级筛选面板")
	if !ok {
		t.Fatal("missing filter panel")
	}
	_ = panel
	scrollFilterIntoView(t, h.tester, "1920x1080")
	click(t, h.tester, "1920x1080")
	scrollFilterIntoView(t, h.tester, "16:9")
	click(t, h.tester, "16:9")
	scrollFilterIntoView(t, h.tester, "颜色 #0066cc")
	click(t, h.tester, "颜色 #0066cc")
	click(t, h.tester, "搜索壁纸")
	h.pump(t, 1)
	q := lastRequest(h.source).query
	if q.Order != "asc" || q.Color != "0066cc" || len(q.Ratios) != 1 || q.Ratios[0] != "16x9" || len(q.Resolutions) != 2 || q.Resolutions[0] != "1920x1080" || q.Resolutions[1] != "3840x2160" {
		t.Fatalf("advanced controls: %+v", q)
	}
}

type countingImages struct {
	images catalogImages
	seen   map[string]bool
}

func (c *countingImages) Get(url string) (kit.ImageSource, bool) {
	c.seen[url] = true
	return c.images.Get(url)
}
func TestRecommendationThumbnailsFollowVisibleRegion(t *testing.T) {
	h := newCatalogHarness(t)
	images := &countingImages{images: newCatalogImages(), seen: map[string]bool{}}
	h.screen.images = images
	h.tester.Frame()
	if len(images.seen) == 0 || len(images.seen) >= 48 || images.seen["views/0"] {
		t.Fatalf("offscreen recommendation thumbnails requested: %v", images.seen)
	}
	viewport, _ := h.tester.Find("推荐分组")
	h.tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 3000)
	h.tester.Frame()
	if !images.seen["views/0"] {
		t.Fatal("newly visible group was not loaded")
	}
	strip, ok := h.tester.Find("查看榜图片列表")
	if !ok {
		t.Fatal("missing horizontal recommendation list")
	}
	h.tester.Scroll(strip.X+strip.W/2, strip.Y+strip.H/2, 400, 0)
	h.tester.Frame()
	if !images.seen["views/4"] {
		t.Fatal("newly visible horizontal cards were not loaded")
	}
}

func TestCatalogPreviews(t *testing.T) {
	directory := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if directory == "" {
		t.Skip("preview export not requested")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	h := newCatalogHarness(t)
	save := func(name string) {
		t.Helper()
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
	}
	for _, dark := range []bool{false, true} {
		h.tester.SetDark(dark)
		h.controller.Navigate(domain.Recommend)
		h.tester.Frame()
		save(fmt.Sprintf("recommend-dark-%t.png", dark))
		click(t, h.tester, "前往壁纸")
		if !h.controller.SearchState().Loaded {
			h.pump(t, 1)
		}
		save(fmt.Sprintf("search-dark-%t.png", dark))
		h.screen.advanced = true
		h.tester.SetSize(800, 540)
		h.tester.Frame()
		save(fmt.Sprintf("filters-min-dark-%t.png", dark))
		h.screen.advanced = false
		h.tester.SetSize(1200, 800)
	}
}
