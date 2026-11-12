package ui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type DetailImageSource interface {
	Load(context.Context, string, func(received, total int64)) (*kit.Bitmap, error)
}

type detailViewer struct {
	id, url       string
	image         kit.ImageSource
	loading       bool
	progress      float64
	error         string
	cancel        context.CancelFunc
	request       uint64
	zoom, fit     float64
	panX, panY    float32
	dragging      bool
	lastX, lastY  float32
	showInfo      bool
	width, height float32
}

func (a *App) openDetail(w domain.Wallpaper, source ...app.DetailList) {
	list := app.DetailList{Page: a.controller.CurrentPage().ID}
	if list.Page == domain.Favorites {
		list.Wallpapers = a.library.Wallpapers
	}
	if len(source) > 0 {
		list = source[0]
	}
	a.closeDetail()
	a.menuOpen = false
	a.dialogOpen = false
	a.advanced = false
	a.controller.OpenDetail(w, list)
}

func (a *App) closeDetail() {
	if a.viewer.cancel != nil {
		a.viewer.cancel()
	}
	request := a.viewer.request + 1
	a.viewer = detailViewer{request: request}
	a.controller.CloseDetail()
}

func (a *App) Close() { a.closed = true; a.closeDetail() }

func (a *App) loadOriginal(rawURL string) {
	if a.viewer.cancel != nil {
		a.viewer.cancel()
	}
	a.viewer.url = rawURL
	a.viewer.request++
	a.viewer.error = ""
	a.viewer.loading = false
	a.viewer.progress = 0
	if rawURL == "" {
		a.viewer.error = "暂未获取到原图地址"
		return
	}
	if a.fullImages == nil || a.dispatch == nil {
		a.viewer.error = "原图加载服务尚未就绪"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.viewer.cancel = cancel
	a.viewer.loading = true
	request, id := a.viewer.request, a.viewer.id
	loader, dispatch := a.fullImages, a.dispatch
	go func() {
		bitmap, err := loader.Load(ctx, rawURL, func(received, total int64) {
			if ctx.Err() != nil {
				return
			}
			dispatch(func() {
				state := a.controller.DetailState()
				if a.closed || ctx.Err() != nil || !state.Open || state.Wallpaper.ID != id || a.viewer.request != request || !a.viewer.loading {
					return
				}
				if total <= 0 {
					total = state.Wallpaper.FileSize
				}
				if total > 0 {
					// Reserve completion for a successfully decoded original.
					a.viewer.progress = max(a.viewer.progress, min(.99, float64(max(int64(0), received))/float64(total)))
				}
			})
		})
		dispatch(func() {
			state := a.controller.DetailState()
			if a.closed || ctx.Err() != nil || !state.Open || state.Wallpaper.ID != id || a.viewer.request != request {
				return
			}
			a.viewer.loading = false
			if err != nil {
				a.viewer.error = err.Error()
				return
			}
			if bitmap == nil {
				a.viewer.error = "原图解码失败"
				return
			}
			a.viewer.image = bitmap
			a.viewer.progress = 1
		})
	}()
}

func (a *App) detailView(c *kit.Context) {
	state := a.controller.DetailState()
	if !state.Open {
		if a.viewer.id != "" {
			if a.viewer.cancel != nil {
				a.viewer.cancel()
			}
			a.viewer = detailViewer{request: a.viewer.request + 1}
		}
		return
	}
	if a.viewer.id != state.Wallpaper.ID {
		if a.viewer.cancel != nil {
			a.viewer.cancel()
		}
		a.viewer = detailViewer{id: state.Wallpaper.ID, zoom: 1, request: a.viewer.request + 1}
		a.loadOriginal(state.Wallpaper.Path)
	} else if state.Wallpaper.Path != "" && a.viewer.url != state.Wallpaper.Path {
		a.loadOriginal(state.Wallpaper.Path)
	}
	width, height := c.Size()
	a.viewer.width, a.viewer.height = width, height
	opened := true
	kit.DialogBase(c.Key("wallpaper-detail"), &opened, func(backdrop, panel kit.Element) {
		panel.OnShortcut(0, kit.KeyLeft, a.controller.PreviousDetail).
			OnShortcut(0, kit.KeyRight, a.controller.NextDetail)
		backdrop.Background(kit.RGBA(0, 0, 0, .71))
		panel.Label("壁纸详情").Absolute().Left(0).Top(0).Size(width, height).Clip().Children(func() {
			canvas := kit.Box(c.Key("detail-canvas")).Label("壁纸大图").Fill().Clip().Focusable().HandleInput(a.viewerInput)
			canvas.Children(func() { a.detailPicture(c, state.Wallpaper, width, height) })
			a.detailImageStatus(c)
			a.detailNavigationStatus(c, state.Navigation)
			a.detailToolbar(c, width, height)
			if a.viewer.showInfo {
				a.detailInformation(c, state, height)
			}
			a.detailNavigation(c, state.Navigation, width)
			a.actionNotice(c)
		})
	})
	if !opened {
		a.closeDetail()
	}
}

func (a *App) detailPicture(c *kit.Context, w domain.Wallpaper, width, height float32) {
	container := kit.Box(c.Key(struct {
		wallpaper string
		request   uint64
	}{w.ID, a.viewer.request})).Absolute().Fill().IgnoreInput(true)
	ready := float32(0)
	if a.viewer.image != nil {
		ready = 1
	}
	fade := container.AnimateTo("original-ready", ready, kit.Animation{Duration: 180 * time.Millisecond, Ease: kit.EaseOut, NonMotion: true})
	var preview kit.ImageSource
	if a.images != nil && (a.viewer.image == nil || fade < 1) {
		preview, _ = a.images.Get(thumbnailURL(w))
	}
	imageWidth, imageHeight := float32(w.Width), float32(w.Height)
	// Keep the frame and zoom percentage tied to the original's metadata,
	// independently of the decoded thumbnail and the crossfade's progress.
	if (imageWidth <= 0 || imageHeight <= 0) && a.viewer.image != nil {
		imageWidth, imageHeight = kit.ImageSize(a.viewer.image)
	} else if (imageWidth <= 0 || imageHeight <= 0) && preview != nil {
		imageWidth, imageHeight = kit.ImageSize(preview)
	}
	if imageWidth <= 0 || imageHeight <= 0 {
		imageWidth, imageHeight = 1200, 800
	}
	a.viewer.fit = math.Min(1, math.Min(float64(max(100, width-200)/imageWidth), float64(max(100, height-200)/imageHeight)))
	drawWidth, drawHeight := imageWidth*float32(a.viewer.fit*a.viewer.zoom), imageHeight*float32(a.viewer.fit*a.viewer.zoom)
	container.Children(func() {
		// Keep the preview underneath until the original is opaque, so a
		// successful download never replaces it with a dark frame.
		if preview != nil {
			kit.Image(c.Key("preview"), preview).Absolute().Left((width-drawWidth)/2+a.viewer.panX).Top((height-drawHeight)/2+a.viewer.panY).Size(drawWidth, drawHeight).Fit(kit.Contain).IgnoreInput(true)
		}
		if a.viewer.image != nil {
			kit.Image(c.Key("original"), a.viewer.image).Absolute().Left((width-drawWidth)/2+a.viewer.panX).Top((height-drawHeight)/2+a.viewer.panY).Size(drawWidth, drawHeight).Fit(kit.Contain).Opacity(fade).IgnoreInput(true)
		}
	})
}

// Status actions are siblings of the gesture canvas, so its pointer capture
// cannot swallow clicks on the retry control.
func (a *App) detailImageStatus(c *kit.Context) {
	if a.viewer.error == "" {
		status := kit.Column(c.Key(struct {
			name    string
			request uint64
		}{"original-status", a.viewer.request})).Absolute().Top(30).Left(20).Right(20).AlignItems(kit.Center)
		complete := float32(0)
		if !a.viewer.loading {
			complete = 1
		}
		// Briefly show the full bar while the decoded original fades in.
		finished := status.AnimateTo("complete", complete, kit.Animation{Duration: 180 * time.Millisecond, NonMotion: true})
		if a.viewer.loading || (a.viewer.progress == 1 && finished < 1) {
			status.Children(func() {
				kit.Progress(c.Key("original-progress"), a.viewer.progress).Label("原图加载中").Width(70).MaxWidthPercent(65).Height(3).Radius(1.5).
					Background(hexColor("414145")).TextColor(hexColor("3b82f6"))
			})
		}
	} else if a.viewer.error != "" {
		kit.Column(c.Key("original-status")).Absolute().Top(24).Left(20).Right(90).AlignItems(kit.Center).Gap(8).Children(func() {
			kit.Text(c, a.viewer.error).FontSize(14).TextColor(hexColor("ffffff")).Background(kit.RGBA(0, 0, 0, .6)).Padding(6, 12).Radius(4)
			legacyChip(c, "retry-original", "重试原图", false).Background(legacyPalette(c).Dialog).OnClick(func() { a.loadOriginal(a.controller.DetailState().Wallpaper.Path) })
		})
	}
}

func (a *App) detailNavigationStatus(c *kit.Context, navigation app.DetailNavigation) {
	if !navigation.Loading && navigation.Error == "" {
		return
	}
	kit.Column(c.Key("detail-navigation-status")).Absolute().Left(20).Right(90).Bottom(88).AlignItems(kit.Center).Gap(8).Children(func() {
		message := "正在加载下一页…"
		if navigation.Error != "" {
			message = "下一页加载失败：" + navigation.Error
		}
		kit.Text(c, message).FontSize(14).TextColor(hexColor("ffffff")).Background(kit.RGBA(0, 0, 0, .6)).Padding(6, 12).Radius(4)
		if navigation.Error != "" {
			legacyChip(c, "retry-detail-page", "重试下一页", false).Background(legacyPalette(c).Dialog).OnClick(a.controller.NextDetail)
		}
	})
}

func (a *App) zoomAt(factor float64, x, y float32) {
	old := a.viewer.zoom
	if old <= 0 {
		old = 1
	}
	next := min(max(old*factor, 1), 32)
	ratio := float32(next / old)
	ox, oy := x-a.viewer.width/2, y-a.viewer.height/2
	a.viewer.panX = ox - (ox-a.viewer.panX)*ratio
	a.viewer.panY = oy - (oy-a.viewer.panY)*ratio
	a.viewer.zoom = next
}

func (a *App) viewerInput(event kit.InputEvent) bool {
	switch event.Kind {
	case kit.InputScroll:
		a.zoomAt(math.Exp(-float64(event.DY)*.002), event.X, event.Y)
		return true
	case kit.InputZoom:
		if event.Scale > 0 {
			a.zoomAt(float64(event.Scale), event.X, event.Y)
		}
		return true
	case kit.InputPointerDown:
		if event.Button != 0 {
			return false
		}
		a.viewer.dragging = true
		a.viewer.lastX, a.viewer.lastY = event.X, event.Y
		return true
	case kit.InputPointerMove:
		if !a.viewer.dragging {
			return false
		}
		a.viewer.panX += event.X - a.viewer.lastX
		a.viewer.panY += event.Y - a.viewer.lastY
		a.viewer.lastX, a.viewer.lastY = event.X, event.Y
		return true
	case kit.InputPointerUp:
		a.viewer.dragging = false
		return true
	}
	return false
}

const (
	detailButtonSize = float32(36)
	detailButtonGap  = float32(8)
)

func detailIconButton(c *kit.Context, name, label string, tipSide kit.Anchor) kit.Element {
	buttonBackground := hexColor("40a8fe")
	if c.Theme().Dark {
		buttonBackground = hexColor("163758")
	}
	button := kit.ButtonBase(c.Key(name)).Label(label).Description(label).Size(detailButtonSize, detailButtonSize).Padding(0).Center().Radius(detailButtonSize / 2).Background(buttonBackground).TextColor(hexColor("ffffff"))
	button.Children(func() { appIcon(c, name, 18) })
	kit.TooltipBase(c.Key(name+"-tooltip"), button, func(tip kit.Element) {
		p := legacyPalette(c)
		tip.Label("操作提示 "+label).Padding(6, 10).Radius(6).Background(p.Dialog).TextColor(p.Text).Border(1, p.Border.Alpha(.5)).Shadow(0, 2, 8, 0, kit.RGBA(0, 0, 0, .2))
		if tipSide == kit.AnchorLeft {
			tip.AttachTo(button, kit.AnchorLeft, kit.AnchorRight).Margin(0, 8, 0, 0)
		} else {
			tip.AttachTo(button, kit.AnchorTop, kit.AnchorBottom).Margin(0, 0, 8, 0)
		}
		tip.Children(func() { kit.Text(c, label).FontSize(13) })
	})
	return button
}

func (a *App) detailNavigation(c *kit.Context, navigation app.DetailNavigation, width float32) {
	navigationWidth := 2*detailButtonSize + 2*detailButtonGap + 64
	kit.Row(c.Key("detail-navigation")).Absolute().Left((width-navigationWidth)/2).Bottom(20).Size(navigationWidth, detailButtonSize).Gap(detailButtonGap).AlignItems(kit.Center).Children(func() {
		detailIconButton(c, "left", "上一张", kit.AnchorTop).Disabled(!navigation.HasPrevious).OnClick(a.controller.PreviousDetail)
		kit.Textf(c, "%.0f%%", a.viewer.fit*a.viewer.zoom*100).Label("图片缩放比例").Width(64).TextAlign(kit.Center).FontSize(18).TextColor(hexColor("cacaca")).IgnoreInput(true)
		detailIconButton(c, "right", "下一张", kit.AnchorTop).Disabled(!navigation.HasNext || navigation.Waiting).OnClick(a.controller.NextDetail)
	})
}

func (a *App) detailToolbar(c *kit.Context, width, height float32) {
	toolbarHeight := 6*detailButtonSize + 5*detailButtonGap
	kit.Column(c.Key("detail-toolbar")).Absolute().Right(20).Top(max(10, (height-toolbarHeight)/2)).Gap(detailButtonGap).Width(detailButtonSize).Children(func() {
		for _, action := range []struct {
			name, label string
			run         func()
		}{
			{"close", "关闭详情", a.closeDetail},
			{"reset", "还原图片", func() { a.viewer.zoom = 1; a.viewer.panX = 0; a.viewer.panY = 0 }},
			{"about", "图片信息", func() { a.viewer.showInfo = !a.viewer.showInfo }},
		} {
			detailIconButton(c, action.name, action.label, kit.AnchorLeft).OnClick(action.run)
		}
		for _, action := range []struct{ name, label string }{{"download", "下载"}, {"wallpaper", "设为壁纸"}, {"favorite", "收藏"}} {
			wallpaper := a.controller.DetailState().Wallpaper
			label := action.label + "原图"
			if action.name == "favorite" && a.actions != nil && a.actions.IsFavorite(wallpaper.ID) {
				label = "取消收藏原图"
			}
			a.bindWallpaperAction(c, detailIconButton(c, action.name, label, kit.AnchorLeft), action.name, wallpaper)
		}
	})
}

func (a *App) detailInformation(c *kit.Context, state app.DetailState, height float32) {
	p := legacyPalette(c)
	w := state.Wallpaper
	detail := state.Detail
	kit.Scroll(c.Key("detail-information")).Label("图片详细信息").Absolute().Left(20).Bottom(50).MaxWidth(320).MaxWidthPercent(65).MaxHeight(min(250, height*.45)).Padding(12).Radius(8).
		Draw(frostedBackground(p.Dialog.Alpha(.55), 8)).
		Transition(kit.ElementTransition{Duration: 160 * time.Millisecond, ExitDuration: 120 * time.Millisecond, Enter: &kit.Motion{}, Exit: &kit.Motion{}}).Children(func() {
		kit.Column(c).Gap(7).Children(func() {
			kit.Text(c, "壁纸 "+w.ID).FontSize(16).Bold().TextColor(p.Text)
			kit.Text(c, w.Resolution+" · "+fileSize(w.FileSize)+" · "+w.FileType).FontSize(12).TextColor(p.Text)
			category := map[string]string{"general": "通用", "anime": "动漫", "people": "人物"}[w.Category]
			if category == "" {
				category = w.Category
			}
			kit.Text(c, strings.TrimSpace(category+"  "+strings.ToUpper(w.Purity))).FontSize(12).TextColor(p.Text)
			if state.Loading {
				kit.Text(c, "详情加载中…").FontSize(12).TextColor(p.Text)
			} else if state.Error != "" {
				kit.Text(c, state.Error).FontSize(12).TextColor(p.Text)
				legacyChip(c, "retry-detail", "重试详情", false).OnClick(a.controller.RetryDetail)
			} else {
				kit.Text(c, fmt.Sprintf("查看 %d · 收藏 %d", detail.Views, detail.Favorites)).FontSize(12).TextColor(p.Text)
				if detail.Uploader != "" {
					kit.Text(c, "上传者："+detail.Uploader).FontSize(12).TextColor(p.Text)
				}
				if detail.CreatedAt != "" {
					kit.Text(c, "上传于 "+detail.CreatedAt).FontSize(12).TextColor(p.Text)
				}
				if len(detail.Tags) > 0 {
					names := make([]string, len(detail.Tags))
					for i, tag := range detail.Tags {
						names[i] = tag.Name
					}
					kit.Text(c, "标签："+strings.Join(names, "、")).FontSize(12).TextColor(p.Text).Wrap()
				}
				if len(detail.Colors) > 0 {
					kit.Row(c).Gap(4).Children(func() {
						for i, color := range detail.Colors[:min(12, len(detail.Colors))] {
							kit.Box(c.Key(i)).Label("主色 #"+strings.TrimPrefix(color, "#")).Size(16, 16).Radius(3).Background(hexColor(color))
						}
					})
				}
			}
		})
	})
}
