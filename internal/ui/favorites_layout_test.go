package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

func TestFavoritesFollowSavedPreviewModeAndThumbnailShape(t *testing.T) {
	h := newActionsHarness(t)
	images := &aspectThumbnails{bitmaps: map[string]*kit.Bitmap{}, visible: map[string]bool{}, prefetched: map[string]bool{}}
	shapes := [][2]int{{1000, 2000}, {2400, 800}, {1000, 1000}}
	originals := []*kit.Bitmap{aspectBitmap(100, 200), aspectBitmap(300, 100), aspectBitmap(100, 100)}
	small := aspectBitmap(300, 200)
	wallpapers := make([]domain.Wallpaper, 1200)
	for i := range wallpapers {
		shape := shapes[i%len(shapes)]
		wallpapers[i] = domain.Wallpaper{ID: fmt.Sprint(i), Width: shape[0], Height: shape[1], Thumbnail: fmt.Sprintf("small/%d", i), OriginalThumbnail: fmt.Sprintf("original/%d", i)}
		images.bitmaps[wallpapers[i].Thumbnail] = small
		images.bitmaps[wallpapers[i].OriginalThumbnail] = originals[i%len(originals)]
	}
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{wallpapers: wallpapers}
	h.wait(t, func() bool { return h.actions.Library().Ready })
	defaults := domain.DefaultSettings(t.TempDir())
	prefs := app.NewPreferences(app.PreferencesOptions{
		Store: storage.NewSettings(filepath.Join(t.TempDir(), "settings.json"), defaults), Defaults: defaults,
		Dispatch: func(fn func()) { h.updates <- fn },
	})
	t.Cleanup(prefs.Close)
	h.screen.preferences = prefs
	prefs.Load()
	h.wait(t, func() bool { return prefs.State().Ready })
	settings := kit.NewTester(h.screen.SettingsView, 450, 340)
	click(t, h.tester, "前往收藏")
	h.tester.SetSize(1280, 800)
	h.screen.images = images

	for step, mode := range []domain.PreviewMode{domain.PreviewWaterfall, domain.PreviewTile, domain.PreviewWaterfall} {
		if step > 0 {
			click(t, settings, "瀑布流模式")
			h.wait(t, func() bool { return !prefs.State().Saving })
			settings.Frame()
		}
		if state := prefs.State(); state.Error != "" || state.Values.PreviewMode != mode {
			t.Fatalf("preview mode was not saved: %+v", state)
		}
		clear(images.visible)
		clear(images.prefetched)
		h.tester.Frame()
		viewport, found := h.tester.Find("收藏壁纸列表")
		if !found || viewport.X != 10 || viewport.W != 1260 {
			t.Fatalf("%s favorites lost the side margins: %+v", mode, viewport)
		}
		left, _ := h.tester.Find("查看壁纸 0")
		right, _ := h.tester.Find("查看壁纸 3")
		if left.X != 10 || right.X+right.W != 1270 || right.W != 300 {
			t.Fatalf("%s favorite columns are shifted or clipped: left=%+v right=%+v", mode, left, right)
		}
		if h.screen.favoritesScroll.Y != 0 {
			t.Fatal("switching preview mode did not return favorites to the top")
		}
		prefix := "original/"
		if mode == domain.PreviewTile {
			prefix = "small/"
		}
		for i, height := range []float32{600, 100, 300} {
			if mode == domain.PreviewTile {
				height = 200
			}
			bounds, found := h.tester.Find("查看壁纸 " + fmt.Sprint(i))
			if !found || bounds.W != 300 || bounds.H != height {
				t.Fatalf("%s favorite %d bounds=%+v, want 300x%f", mode, i, bounds, height)
			}
			if !images.visible[prefix+fmt.Sprint(i)] {
				t.Fatalf("%s favorite %d did not request its matching thumbnail", mode, i)
			}
			for _, x := range []float32{10, 150, 290} {
				if top, bottom := h.tester.Image().RGBAAt(int(bounds.X+x), int(bounds.Y+10)), h.tester.Image().RGBAAt(int(bounds.X+x), int(bounds.Y+bounds.H-10)); top != aspectTop || bottom != aspectBottom {
					t.Fatalf("%s favorite %d left blank space or cropped the full image: top=%+v bottom=%+v", mode, i, top, bottom)
				}
			}
		}
		checkRequests := func() {
			t.Helper()
			if len(images.visible) == 0 || len(images.visible) > 40 || len(images.prefetched) == 0 || images.visible[prefix+"1199"] || images.prefetched[prefix+"1199"] {
				t.Fatalf("favorites did not limit loading to nearby images: visible=%d prefetched=%d", len(images.visible), len(images.prefetched))
			}
			for _, requests := range []map[string]bool{images.visible, images.prefetched} {
				for url := range requests {
					if !strings.HasPrefix(url, prefix) {
						t.Fatalf("%s favorites requested the wrong thumbnail: %s", mode, url)
					}
				}
			}
		}
		checkRequests()
		clear(images.visible)
		clear(images.prefetched)
		h.tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 10000)
		h.tester.Frame()
		checkRequests()
		if _, mounted := h.tester.Find("查看壁纸 0"); mounted || images.visible[prefix+"0"] {
			t.Fatal("scrolling favorites retained the distant first card")
		}
	}
}

func TestFavoritesReflowAfterLibraryReload(t *testing.T) {
	h := newActionsHarness(t)
	wallpapers := []domain.Wallpaper{{ID: "portrait", Width: 1000, Height: 2000}, {ID: "landscape", Width: 2400, Height: 800}}
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{wallpapers: wallpapers}
	h.wait(t, func() bool { return h.actions.Library().Ready })
	click(t, h.tester, "前往收藏")
	if bounds, _ := h.tester.Find("查看壁纸 portrait"); bounds.H != 600 {
		t.Fatalf("favorite did not use its portrait proportions: %+v", bounds)
	}
	// Reloading can change order and dimensions without changing the count.
	h.actions.RetryFavorites()
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{wallpapers: []domain.Wallpaper{wallpapers[1], wallpapers[0]}}
	h.wait(t, func() bool { return h.actions.Library().Ready && !h.actions.Library().Loading })
	for id, height := range map[string]float32{"landscape": 100, "portrait": 600} {
		if bounds, found := h.tester.Find("查看壁纸 " + id); !found || bounds.H != height {
			t.Fatalf("reloaded favorite %s reused the previous layout: %+v", id, bounds)
		}
	}
}
