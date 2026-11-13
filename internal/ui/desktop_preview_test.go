package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type desktopPreviewHost struct {
	opens, closes         int
	openError, closeError error
}

func (*desktopPreviewHost) Available() bool          { return true }
func (h *desktopPreviewHost) Open(done func(error))  { h.opens++; done(h.openError) }
func (h *desktopPreviewHost) Close(done func(error)) { h.closes++; done(h.closeError) }

func attachDesktopPreview(screen *App) *desktopPreviewHost {
	host := &desktopPreviewHost{}
	screen.desktopPreview = app.NewDesktopPreview(host, nil)
	return host
}

func TestDesktopPreviewStripMouseWheelKeysAndExitPreserveDetail(t *testing.T) {
	h, loader := newViewerHarness(t)
	host := attachDesktopPreview(h.screen)
	wallpapers := h.controller.Recommendations()[0].Wallpapers
	click(t, h.tester, "查看壁纸 "+wallpapers[0].ID)
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	first := nextOriginal(t, loader)
	first.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	click(t, h.tester, "桌面预览")
	if host.opens != 1 || h.screen.desktopPreview.State().Phase != app.DesktopPreviewActive {
		t.Fatal("toolbar did not enter desktop preview")
	}
	controls := kit.NewTester(h.screen.DesktopControlsView, 800, DesktopPreviewControlsHeight)
	for _, label := range []string{"桌面预览壁纸列表", "上一张桌面预览", "下一张桌面预览", "退出桌面预览"} {
		bounds, ok := controls.Find(label)
		if !ok || bounds.Y < 0 || bounds.Y+bounds.H > DesktopPreviewControlsHeight || bounds.X < 0 || bounds.X+bounds.W > 800 {
			t.Fatalf("control %s does not fit: %+v", label, bounds)
		}
	}
	if button, ok := controls.Access("设为壁纸"); !ok || !button.Disabled {
		t.Fatal("wallpaper action is enabled without its services")
	}
	click(t, controls, "桌面预览 "+wallpapers[2].ID)
	if h.controller.DetailState().Wallpaper.ID != wallpapers[2].ID {
		t.Fatal("thumbnail did not select its wallpaper")
	}
	if first.ctx.Err() == nil {
		t.Fatal("switching did not cancel the previous original")
	}
	strip, _ := controls.Find("桌面预览壁纸列表")
	controls.Scroll(strip.X+40, strip.Y+40, 0, 100)
	if h.controller.DetailState().Wallpaper.ID != wallpapers[3].ID {
		t.Fatal("mouse wheel did not switch the desktop preview")
	}
	controls.Key(0, kit.KeyLeft)
	if h.controller.DetailState().Wallpaper.ID != wallpapers[2].ID {
		t.Fatal("left arrow did not switch the desktop preview")
	}
	controls.Key(0, kit.KeyRight)
	if h.controller.DetailState().Wallpaper.ID != wallpapers[3].ID {
		t.Fatal("right arrow did not switch the desktop preview")
	}
	controls.Key(0, kit.KeyEscape)
	if host.closes != 1 || h.screen.desktopPreview.NeedsCleanup() {
		t.Fatal("Escape did not restore the desktop session")
	}
	h.tester.Frame()
	if state := h.controller.DetailState(); !state.Open || state.Wallpaper.ID != wallpapers[3].ID {
		t.Fatal("exit lost the selected in-app detail")
	}
}

func TestDesktopPreviewDoesNotSubmitWallpaperOrDownloadsUntilClicked(t *testing.T) {
	h := newActionsHarness(t)
	h.ready(t)
	attachDesktopPreview(h.screen)
	wallpapers := h.controller.Recommendations()[0].Wallpapers
	click(t, h.tester, "查看壁纸 "+wallpapers[0].ID)
	click(t, h.tester, "桌面预览")
	controls := kit.NewTester(h.screen.DesktopControlsView, 800, DesktopPreviewControlsHeight)
	click(t, controls, "桌面预览 "+wallpapers[1].ID)
	button, exists := controls.Access("设为壁纸")
	close, _ := controls.Find("退出桌面预览")
	if !exists || button.Disabled || button.Bounds.X+button.Bounds.W > close.X || button.Bounds.Y != close.Y {
		t.Fatalf("wallpaper action is not available immediately left of close: %+v", button)
	}
	click(t, controls, "退出桌面预览")
	if len(h.files.calls) != 0 || len(h.desktop.calls) != 0 {
		t.Fatal("preview submitted a file or system wallpaper operation")
	}
}

func TestDesktopPreviewWallpaperActionUsesClickedImageAndSharesTransferGuards(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint("failure=", failed), func(t *testing.T) {
			h := newActionsHarness(t)
			h.ready(t)
			host := attachDesktopPreview(h.screen)
			wallpapers := h.controller.Recommendations()[0].Wallpapers
			click(t, h.tester, "查看壁纸 "+wallpapers[0].ID)
			click(t, h.tester, "桌面预览")
			controls := kit.NewTester(h.screen.DesktopControlsView, 800, DesktopPreviewControlsHeight)
			selected := wallpapers[1]
			click(t, controls, "桌面预览 "+selected.ID)
			click(t, controls, "设为壁纸")
			call := nextActionCall(t, h.files.calls)
			if call.wallpaper.ID != selected.ID || call.directory != h.wallpaperDir {
				t.Fatalf("wallpaper action used the wrong selection or directory: %+v", call)
			}
			button, ok := controls.Access("设为壁纸")
			if !ok || !button.Disabled {
				t.Fatal("running wallpaper task can be submitted again")
			}
			for range 3 {
				controls.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
				controls.Frame()
			}
			if len(h.files.calls) != 0 {
				t.Fatal("repeated clicks submitted duplicate wallpaper tasks")
			}
			click(t, controls, "桌面预览 "+wallpapers[2].ID)
			path := filepath.Join(h.wallpaperDir, selected.ID+".png")
			call.reply <- actionFileReply{path: path}
			desktop := nextActionCall(t, h.desktop.calls)
			if desktop.path != path {
				t.Fatal("switching preview changed an already submitted wallpaper")
			}
			message := "已设置为桌面壁纸"
			if failed {
				desktop.reply <- errors.New("系统拒绝设置壁纸")
				message = "失败：系统拒绝设置壁纸"
			} else {
				desktop.reply <- nil
			}
			h.wait(t, func() bool {
				transfer := h.transfer(domain.WallpaperTransfer, selected.ID)
				return !transfer.Running && (transfer.Completed || transfer.Error != "")
			})
			controls.Frame()
			if status, _ := controls.Access("桌面预览状态"); status.Description == message {
				t.Fatal("a previous image's wallpaper result was shown for the new selection")
			}
			click(t, controls, "桌面预览 "+selected.ID)
			if status, ok := controls.Access("桌面预览状态"); !ok || status.Description != message {
				t.Fatalf("wallpaper result is hidden in desktop preview: %+v", status)
			}
			if host.closes != 0 || h.screen.desktopPreview.State().Phase != app.DesktopPreviewActive || len(h.actions.Transfers()) != 1 || h.actions.Transfers()[0].Kind != domain.WallpaperTransfer {
				t.Fatal("setting wallpaper exited preview or submitted a user download")
			}
			if failed {
				click(t, controls, "设为壁纸")
				retry := nextActionCall(t, h.files.calls)
				if retry.wallpaper.ID != selected.ID {
					t.Fatal("retry used a different image")
				}
				retry.reply <- actionFileReply{path: path}
				nextActionCall(t, h.desktop.calls).reply <- nil
				h.wait(t, func() bool { return h.transfer(domain.WallpaperTransfer, selected.ID).Completed })
				controls.Frame()
				if status, _ := controls.Access("桌面预览状态"); status.Description != "已设置为桌面壁纸" {
					t.Fatal("wallpaper retry did not show success")
				}
			}
		})
	}
}

func TestDesktopPreviewFailureShowsRollbackAndRestoreRetry(t *testing.T) {
	h, _ := newViewerHarness(t)
	host := attachDesktopPreview(h.screen)
	host.openError, host.closeError = errors.New("进入失败"), errors.New("窗口恢复失败")
	click(t, h.tester, "查看壁纸 "+h.controller.Recommendations()[0].Wallpapers[0].ID)
	click(t, h.tester, "桌面预览")
	if host.closes != 1 || !h.tester.HasText("窗口恢复失败") {
		t.Fatal("partial entry was not rolled back or failure was hidden")
	}
	host.closeError = nil
	click(t, h.tester, "重试恢复窗口")
	if host.closes != 2 || h.screen.desktopPreview.NeedsCleanup() {
		t.Fatal("restore retry did not clear the failed session")
	}
}

func TestDesktopPreviewWallpaperAndControlsPreview(t *testing.T) {
	directory := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if directory == "" {
		t.Skip("preview export not requested")
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	h, loader := newViewerHarness(t)
	attachDesktopPreview(h.screen)
	click(t, h.tester, "查看壁纸 "+h.controller.Recommendations()[0].Wallpapers[0].ID)
	waitViewer(t, h, func() bool { return h.screen.viewer.url != "" })
	call := nextOriginal(t, loader)
	call.reply <- originalReply{image: newCatalogImages().bitmap}
	waitViewer(t, h, func() bool { return h.screen.viewer.image != nil })
	click(t, h.tester, "桌面预览")
	for _, dark := range []bool{true, false} {
		h.screen.appearance = "dark"
		name := "dark"
		if !dark {
			h.screen.appearance, name = "light", "light"
		}
		controls := kit.NewTester(h.screen.DesktopControlsView, 800, DesktopPreviewControlsHeight)
		controls.Advance(time.Second)
		writeListPreview(t, controls, filepath.Join(directory, "desktop-controls-"+name+".png"))
	}
	wallpaper := kit.NewTester(h.screen.DesktopWallpaperView, 1280, 720)
	wallpaper.Advance(time.Second)
	writeListPreview(t, wallpaper, filepath.Join(directory, "desktop-wallpaper.png"))
	h.screen.appearance = "dark"
	composite := kit.NewTester(func(c *kit.Context) {
		h.screen.DesktopWallpaperView(c)
		kit.Box(c.Key("floating-controls")).Absolute().Left(0).Right(0).Bottom(20).Height(DesktopPreviewControlsHeight).
			Children(func() { h.screen.DesktopControlsView(c) })
	}, 1280, 800)
	composite.Advance(time.Second)
	writeListPreview(t, composite, filepath.Join(directory, "desktop-floating-composite.png"))
}

func TestDesktopPreviewTransparentBackgroundAndSeparateToolbarAboveStrip(t *testing.T) {
	h, _ := newViewerHarness(t)
	attachDesktopPreview(h.screen)
	wallpapers := h.controller.Recommendations()[0].Wallpapers
	click(t, h.tester, "查看壁纸 "+wallpapers[0].ID)
	click(t, h.tester, "桌面预览")
	for _, width := range []int{480, 800, 1040} {
		controls := kit.NewTester(h.screen.DesktopControlsView, width, DesktopPreviewControlsHeight)
		toolbar, barOK := controls.Find("桌面预览工具栏")
		strip, stripOK := controls.Find("桌面预览壁纸列表")
		if !barOK || !stripOK || toolbar.Y+toolbar.H >= strip.Y || toolbar.W > desktopToolbarWidth || toolbar.X < 0 || toolbar.X+toolbar.W > float32(width) {
			t.Fatalf("toolbar and strip are not separated at %dpx: toolbar=%+v strip=%+v", width, toolbar, strip)
		}
		if pixel := controls.Image().RGBAAt(1, 1); pixel.A != 0 {
			t.Fatalf("outer background is opaque: %+v", pixel)
		}
		// The capsule's soft shadow may cross the gap; a solid panel may not.
		if pixel := controls.Image().RGBAAt(width/2, int(strip.Y)-3); pixel.A > 64 {
			t.Fatalf("gap between the two rows is opaque: %+v", pixel)
		}
	}
}

type desktopSmallImages struct {
	bitmap catalogImages
	urls   []string
}

func (s *desktopSmallImages) Get(url string) (kit.ImageSource, bool) {
	s.urls = append(s.urls, url)
	return s.bitmap.Get(url)
}

func TestDesktopPreviewThumbnailsUseFixedSizeAndOnlySmallSources(t *testing.T) {
	h, _ := newViewerHarness(t)
	attachDesktopPreview(h.screen)
	wallpapers := []domain.Wallpaper{
		{ID: "portrait", Width: 1000, Height: 2400, Thumbnail: "small/portrait", OriginalThumbnail: "orig/portrait"},
		{ID: "wide", Width: 4000, Height: 1200, Thumbnail: "small/wide", OriginalThumbnail: "orig/wide"},
	}
	h.screen.openDetail(wallpapers[0], app.DetailList{Page: domain.Favorites, Wallpapers: wallpapers})
	h.tester.Frame()
	h.screen.enterDesktopPreview()
	source := &desktopSmallImages{bitmap: newCatalogImages()}
	h.screen.images = source
	controls := kit.NewTester(h.screen.DesktopControlsView, 800, DesktopPreviewControlsHeight)
	for _, wallpaper := range wallpapers {
		bounds, found := controls.Find("桌面预览 " + wallpaper.ID)
		if !found || bounds.W != desktopThumbnailWidth || bounds.H != desktopThumbnailHeight {
			t.Fatalf("%s thumbnail does not use the fixed small frame: %+v", wallpaper.ID, bounds)
		}
	}
	seen := map[string]bool{}
	for _, url := range source.urls {
		if url != "small/portrait" && url != "small/wide" {
			t.Fatalf("strip requested a non-small image: %s", url)
		}
		seen[url] = true
	}
	if !seen["small/portrait"] || !seen["small/wide"] {
		t.Fatal("visible small thumbnails were not loaded")
	}
}
