package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type desktopViewer struct {
	scroll      kit.ScrollState
	selected    string
	lastScroll  float32
	wheel       float32
	lastWheel   time.Time
	session     uint64
	autoPage    int
	wallpaperID string // Last image explicitly submitted in this preview session.
}

const (
	desktopControlsInset         = 12
	desktopControlsGap           = 14
	desktopToolbarHeight         = 44
	desktopToolbarWidth          = 400
	desktopThumbnailWidth        = 168
	desktopThumbnailHeight       = 112
	desktopThumbnailGap          = 16
	desktopStripHeight           = desktopThumbnailHeight + 16
	desktopStripFadeWidth        = 48
	DesktopPreviewControlsHeight = 2*desktopControlsInset + desktopToolbarHeight + desktopControlsGap + desktopStripHeight
)

func (a *App) enterDesktopPreview() {
	if a.closed || a.desktopPreview == nil || !a.controller.DetailState().Open {
		return
	}
	a.desktopViewer = desktopViewer{session: a.desktopViewer.session + 1}
	a.desktopPreview.Enter()
}

// Called after controller updates, outside view construction. A replaced query
// or closed detail also exits the desktop session and restores its applications.
func (a *App) SyncDesktopPreview() {
	if a.desktopPreview == nil || a.controller.DetailState().Open {
		return
	}
	phase := a.desktopPreview.State().Phase
	if phase == app.DesktopPreviewEntering || phase == app.DesktopPreviewActive {
		a.desktopPreview.Exit()
	}
}

func (a *App) exitDesktopPreview() {
	if a.desktopPreview != nil {
		a.desktopPreview.Exit()
	}
}

func (a *App) redrawDesktopPreview() {
	if a.dispatch != nil {
		a.dispatch(func() {})
	}
}

// MoveDesktopPreview is also used by the session's temporary system shortcuts,
// so switching remains available when the desktop or another app has focus.
func (a *App) MoveDesktopPreview(direction int) {
	if a.desktopPreview == nil || a.desktopPreview.State().Phase != app.DesktopPreviewActive {
		return
	}
	if direction > 0 {
		a.controller.NextDetail()
	} else {
		a.controller.PreviousDetail()
	}
	a.redrawDesktopPreview()
}

func (a *App) desktopWheel(event kit.InputEvent) bool {
	if event.Kind != kit.InputScroll || event.DY == 0 || math.Abs(float64(event.DX)) > math.Abs(float64(event.DY)) {
		return false
	}
	if event.Momentum {
		return true
	}
	if event.DY*a.desktopViewer.wheel < 0 {
		a.desktopViewer.wheel = 0
	}
	a.desktopViewer.wheel += event.DY
	threshold := float32(1)
	if event.Precise {
		threshold = 60
	}
	if math.Abs(float64(a.desktopViewer.wheel)) >= float64(threshold) && event.Time.Sub(a.desktopViewer.lastWheel) >= 140*time.Millisecond {
		direction := 1
		if a.desktopViewer.wheel < 0 {
			direction = -1
		}
		a.desktopViewer.wheel, a.desktopViewer.lastWheel = 0, event.Time
		a.MoveDesktopPreview(direction)
	}
	return true
}

// DesktopWallpaperView is shared by every display. It uses the same original
// loader as in-app detail, but fits the image as a wallpaper independently of
// the detail viewer's zoom and pan. It never calls a wallpaper-setting action.
func (a *App) DesktopWallpaperView(c *kit.Context) {
	a.applyLegacyTheme(c)
	state := a.controller.DetailState()
	a.syncDetailViewer(state)
	kit.Box(c.Key("desktop-wallpaper")).Fill().Clip().Background(hexColor("191d30")).IgnoreInput(true).Children(func() {
		if !state.Open {
			return
		}
		frame := kit.Box(c.Key(struct {
			id      string
			request uint64
		}{state.Wallpaper.ID, a.viewer.request})).Fill()
		ready := float32(0)
		if a.viewer.image != nil {
			ready = 1
		}
		fade := frame.AnimateTo("original-ready", ready, kit.Animation{Duration: 180 * time.Millisecond, Ease: kit.EaseOut, NonMotion: true})
		frame.Children(func() {
			if a.images != nil && fade < 1 {
				if image, _ := a.images.Get(thumbnailURL(state.Wallpaper)); image != nil {
					kit.Image(c.Key("thumbnail"), image).Absolute().Fill().Fit(kit.Cover).IgnoreInput(true)
				}
			}
			if a.viewer.image != nil {
				kit.Image(c.Key("original"), a.viewer.image).Absolute().Fill().Fit(kit.Cover).Opacity(fade).IgnoreInput(true)
			}
		})
	})
}

func (a *App) DesktopControlsView(c *kit.Context) {
	a.applyLegacyTheme(c)
	c.Root().Background(kit.Transparent)
	state := a.controller.DetailState()
	a.syncDetailViewer(state)
	wallpapers, selected := a.controller.DetailWallpapers()
	pagination := a.controller.DetailPaginationState()
	width, _ := c.Size()
	available := max(0, width-2*desktopControlsInset)
	contentWidth := float32(16)
	contentWidth += float32(len(wallpapers)) * (desktopThumbnailWidth + desktopThumbnailGap)
	if len(wallpapers) > 0 {
		contentWidth -= desktopThumbnailGap
	}
	stripWidth := min(contentWidth, available)
	remaining := contentWidth - a.desktopViewer.scroll.X - stripWidth
	selectionChanged := a.desktopViewer.selected != state.Wallpaper.ID
	root := kit.Column(c.Key("desktop-preview-controls")).Label("桌面预览操作栏").Fill().Padding(desktopControlsInset).Gap(desktopControlsGap).AlignItems(kit.Center).
		Background(kit.Transparent).TextColor(hexColor("ffffff")).Focusable().AutoFocus().HandleInput(a.desktopWheel).
		OnShortcut(0, kit.KeyEscape, a.exitDesktopPreview).
		OnShortcut(0, kit.KeyLeft, func() { a.MoveDesktopPreview(-1) }).
		OnShortcut(0, kit.KeyRight, func() { a.MoveDesktopPreview(1) })
	root.Children(func() {
		kit.Row(c.Key("toolbar")).Label("桌面预览工具栏").Width(min(available, desktopToolbarWidth)).Height(desktopToolbarHeight).Shrink(0).Padding(6, 10).Gap(8).AlignItems(kit.Center).
			Radius(desktopToolbarHeight/2).Background(hexColor("191d30").Alpha(.9)).Border(1, kit.RGBA(255, 255, 255, .14)).Shadow(0, 4, 14, 0, kit.RGBA(0, 0, 0, .25)).Children(func() {
			desktopPreviewButton(c, "left", "left", "上一张桌面预览").Disabled(!state.Navigation.HasPrevious).OnClick(func() { a.MoveDesktopPreview(-1) })
			kit.Textf(c, "%d / %d", max(0, selected+1), len(wallpapers)).Label("桌面预览序号").FontSize(13).Width(64).TextAlign(kit.Center).TextColor(kit.RGBA(255, 255, 255, .9)).Shrink(0)
			desktopPreviewButton(c, "right", "right", "下一张桌面预览").Disabled(!state.Navigation.HasNext || state.Navigation.Waiting).OnClick(func() { a.MoveDesktopPreview(1) })
			kit.Box(c.Key("navigation-divider")).Size(1, 16).Margin(0, 4).Shrink(0).Background(kit.RGBA(255, 255, 255, .18))
			message := "← → 切图 · Esc 退出"
			if a.viewer.loading {
				message = fmt.Sprintf("原图加载中 %.0f%%", a.viewer.progress*100)
			}
			if a.viewer.error != "" {
				message = a.viewer.error
			}
			if pagination.Loading {
				message = "正在加载下一页…"
			}
			if pagination.Error != "" {
				message = "下一页加载失败"
			}
			if a.actions != nil && a.desktopViewer.wallpaperID != "" && a.desktopViewer.wallpaperID == state.Wallpaper.ID {
				if transfer, ok := a.actions.Transfer(domain.WallpaperTransfer, state.Wallpaper.ID); ok {
					message = transferStatus(transfer)
				} else if notice := a.actions.Notice(); notice.Message != "" {
					message = notice.Message
				}
			}
			kit.Text(c, message).Label("桌面预览状态").Tooltip(message).FontSize(12).TextColor(kit.RGBA(255, 255, 255, .65)).Grow(1).MinWidth(0).SingleLine().Clip()
			if a.viewer.error != "" {
				desktopPreviewButton(c, "retry-original", "refresh", "重试原图").OnClick(func() { a.loadOriginal(state.Wallpaper.Path); a.redrawDesktopPreview() })
			}
			if pagination.Error != "" {
				desktopPreviewButton(c, "retry-page", "refresh", "重试下一页").OnClick(func() { a.controller.LoadMoreDetail(); a.redrawDesktopPreview() })
			}
			kit.Box(c.Key("exit-divider")).Size(1, 16).Margin(0, 4).Shrink(0).Background(kit.RGBA(255, 255, 255, .18))
			setWallpaper := desktopPreviewButton(c, "set-wallpaper", "wallpaper", "设为壁纸").Disabled(!state.Open).OnClick(func() {
				a.desktopViewer.wallpaperID = state.Wallpaper.ID
				a.redrawDesktopPreview()
			})
			a.bindWallpaperAction(c, setWallpaper, "wallpaper", state.Wallpaper)
			desktopPreviewButton(c, "close", "close", "退出桌面预览").OnClick(a.exitDesktopPreview)
		})
		bounds := a.desktopViewer.scroll.Bounds(c)
		fade := min(desktopStripFadeWidth/max(stripWidth, 1), .25)
		opaque := kit.RGB(255, 255, 255)
		kit.ScrollHorizontal(c.Key("wallpapers")).Label("桌面预览壁纸列表").TrackScroll(&a.desktopViewer.scroll).Width(stripWidth).Height(desktopStripHeight).Shrink(0).
			Padding(0, 8).Gap(desktopThumbnailGap).AlignItems(kit.Center).Background(kit.Transparent).Scrollbars(false).HandleInput(a.desktopWheel).
			MaskLinearGradient(kit.LinearGradient{Angle: 90, Stops: []kit.GradientStop{
				{Color: opaque.Alpha(1 - min(max(a.desktopViewer.scroll.X, 0)/desktopStripFadeWidth, 1)), At: 0},
				{Color: opaque, At: fade},
				{Color: opaque, At: 1 - fade},
				{Color: opaque.Alpha(1 - min(max(remaining, 0)/desktopStripFadeWidth, 1)), At: 1},
			}}).Children(func() {
			left := float32(8) - a.desktopViewer.scroll.X
			for i, wallpaper := range wallpapers {
				visible := bounds.W == 0 && i < 10 || left+desktopThumbnailWidth >= 0 && left < bounds.W
				a.desktopThumbnail(c, wallpaper, visible, i == selected, selectionChanged)
				left += desktopThumbnailWidth + desktopThumbnailGap
			}
		})
	})
	a.desktopViewer.selected = state.Wallpaper.ID
	if a.desktopViewer.lastScroll != a.desktopViewer.scroll.X {
		a.desktopViewer.lastScroll = a.desktopViewer.scroll.X
		c.Invalidate()
	}
	// Use the current list width, not the previous layout's MaxX. An appended
	// page must move the end away without scrolling or immediately fetching
	// another page from stale geometry.
	if a.desktopViewer.scroll.X > 0 && remaining <= desktopThumbnailGap {
		a.requestDesktopPreviewPage(pagination)
	}
}

func (a *App) requestDesktopPreviewPage(pagination app.DetailPaginationState) {
	if a.closed || a.dispatch == nil || a.desktopPreview == nil || a.desktopPreview.State().Phase != app.DesktopPreviewActive ||
		pagination.CurrentPage <= 0 || !pagination.HasMore || pagination.Loading || pagination.Error != "" || a.desktopViewer.autoPage == pagination.CurrentPage {
		return
	}
	a.desktopViewer.autoPage = pagination.CurrentPage
	session := a.desktopViewer.session
	// Queue once outside view construction, and recheck after any intervening
	// prefetch, exit or new session before asking the controller for that page.
	a.dispatch(func() {
		if a.closed || a.desktopViewer.session != session || a.desktopPreview.State().Phase != app.DesktopPreviewActive {
			return
		}
		current := a.controller.DetailPaginationState()
		if current.CurrentPage == pagination.CurrentPage && current.HasMore && !current.Loading && current.Error == "" {
			a.controller.LoadMoreDetail()
		}
	})
}

func desktopPreviewButton(c *kit.Context, key, icon, label string) kit.Element {
	button := kit.ButtonBase(c.Key(key)).Label(label).Description(label).Tooltip(label).Size(32, 32).Shrink(0).Padding(0).Radius(16).Center().FocusRing(false).
		Background(kit.Transparent).TextColor(hexColor("ffffff"))
	if button.Hovered() {
		button.Background(kit.RGBA(255, 255, 255, .13))
	}
	button.Children(func() { appIcon(c, icon, 18) })
	return button
}

func (a *App) desktopThumbnail(c *kit.Context, wallpaper domain.Wallpaper, visible, selected, reveal bool) {
	button := kit.ButtonBase(c.Key(wallpaper.ID)).Label("桌面预览 "+wallpaper.ID).Description(wallpaper.Resolution).Size(desktopThumbnailWidth, desktopThumbnailHeight).Shrink(0).
		Padding(0).Radius(12).Background(kit.RGBA(17, 20, 32, .3)).FocusRing(false).Clip().
		OnClick(func() { a.controller.SelectDetail(wallpaper.ID); a.redrawDesktopPreview() })
	selection := float32(0)
	if selected {
		selection = 1
	}
	selection = button.AnimateTo("selection", selection, kit.Animation{Duration: 180 * time.Millisecond, Ease: kit.EaseOut})
	button.Border(2, kit.RGBA(255, 255, 255, .18+.78*selection)).Top(-4*selection).
		Shadow(0, 4, 10, 0, kit.RGBA(0, 0, 0, .3*selection))
	emphasis := float32(.82)
	if selected || button.Hovered() {
		emphasis = 1
	}
	button.Opacity(button.AnimateTo("emphasis", emphasis, kit.Animation{Duration: 140 * time.Millisecond, Ease: kit.EaseOut}))
	if selected && reveal {
		animation := kit.Animation{Duration: 220 * time.Millisecond, Ease: kit.EaseOut}
		button.ScrollIntoView(kit.ScrollIntoViewOptions{Animation: &animation})
	}
	button.Children(func() {
		var image kit.ImageSource
		if visible && a.images != nil && wallpaper.Thumbnail != "" {
			image, _ = a.images.Get(wallpaper.Thumbnail)
		}
		if image != nil {
			kit.Image(c, image).Fill().Fit(kit.Cover).IgnoreInput(true)
		} else {
			kit.Text(c, wallpaper.ID).FontSize(12).TextColor(kit.RGBA(255, 255, 255, .7)).IgnoreInput(true)
		}
	})
}

func (a *App) desktopPreviewStatus(c *kit.Context) {
	if a.desktopPreview == nil {
		return
	}
	state := a.desktopPreview.State()
	message := state.Error
	label := ""
	switch state.Phase {
	case app.DesktopPreviewEntering:
		message, label = "正在进入桌面预览…", "取消桌面预览"
	case app.DesktopPreviewExiting:
		message = "正在恢复其他应用…"
	case app.DesktopPreviewRestoreFailed:
		label = "重试恢复窗口"
	}
	if message == "" || state.Phase == app.DesktopPreviewActive {
		return
	}
	p := legacyPalette(c)
	kit.Row(c.Key("desktop-preview-status")).Label("桌面预览提示").Absolute().Top(116).Left(20).Right(80).Gap(10).Padding(8, 12).Radius(8).Background(p.Dialog).TextColor(p.Text).AlignItems(kit.Center).Children(func() {
		kit.Text(c, message).FontSize(13).Grow(1).MinWidth(0).Wrap()
		if label != "" {
			legacyChip(c, "restore", label, false).OnClick(a.exitDesktopPreview)
		}
	})
}
