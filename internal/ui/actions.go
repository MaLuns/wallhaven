package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

func (a *App) bindWallpaperAction(c *kit.Context, button kit.Element, name string, wallpaper domain.Wallpaper) {
	if a.actions == nil {
		button.Disabled(true).Description("此操作服务尚未就绪").Tooltip("此操作服务尚未就绪")
		return
	}
	if name == "favorite" {
		if a.actions.IsFavorite(wallpaper.ID) {
			button.TextColor(legacyPalette(c).Accent)
		}
		if a.library.Saving {
			button.Disabled(true).Description("正在保存收藏").Tooltip("正在保存收藏")
		} else if !a.library.Ready {
			message := "正在加载收藏"
			if a.library.Error != "" {
				message = "收藏加载失败，请在我的页面重试"
			}
			button.Disabled(true).Description(message).Tooltip(message)
		}
		button.OnClick(func() { a.actions.ToggleFavorite(wallpaper) })
		return
	}
	kind := domain.DownloadTransfer
	if name == "wallpaper" {
		kind = domain.WallpaperTransfer
	}
	if history := a.actions.TransferHistory(); !history.Ready || history.Loading {
		message := "下载历史尚未载入，请在下载页面重试"
		button.Disabled(true).Description(message).Tooltip(message)
	}
	if transfer, ok := a.actions.Transfer(kind, wallpaper.ID); ok && (transfer.Running || transfer.Queued || transfer.Stopping) {
		message := transferStatus(transfer) + "，可在下载页面取消"
		button.Disabled(true).Description(message).Tooltip(message)
	}
	button.OnClick(func() {
		if name == "wallpaper" {
			a.actions.SetWallpaper(wallpaper)
		} else {
			a.actions.Download(wallpaper)
		}
	})
}

func (a *App) emptyActionPage(c *kit.Context, title string) {
	kit.Column(c).Grow(1).FillWidth().Center().Gap(12).Children(func() {
		kit.Image(c, legacyBitmaps["empty"]).Size(270, 180).Fit(kit.Contain)
		kit.Text(c, title).FontSize(20).TextColor(legacyPalette(c).Muted)
	})
}

func (a *App) favoritesPage(c *kit.Context) {
	if a.actions == nil {
		a.emptyActionPage(c, "收藏服务尚未就绪")
		return
	}
	state := a.library
	if state.Loading && !state.Ready {
		kit.Column(c).Grow(1).FillWidth().Center().Children(func() {
			kit.Spinner(c).Label("正在加载收藏").Size(26, 26).TextColor(legacyPalette(c).Accent)
		})
		return
	}
	if state.Error != "" {
		a.errorNotice(c, state.Error, a.actions.RetryFavorites)
	}
	if len(state.Wallpapers) == 0 {
		title := "收藏夹空空如也 ~"
		if !state.Ready {
			title = "收藏加载失败"
		}
		a.emptyActionPage(c, title)
		return
	}
	windowWidth, windowHeight := c.Size()
	viewport := a.favoritesScroll.Bounds(c)
	width, height := viewport.W, viewport.H
	if width <= 0 {
		width = max(legacyCardWidth, windowWidth-20)
		c.Invalidate()
	}
	if height <= 0 {
		height = max(200, windowHeight-130)
	}
	columns := max(1, int((width+legacyCardGap)/(legacyCardWidth+legacyCardGap)))
	inset := float32(math.Mod(float64(width+legacyCardGap), float64(legacyCardWidth+legacyCardGap))) / 2
	rowHeight := legacyCardImageHeight + legacyCardBarHeight + legacyCardGap
	totalHeight := float32((len(state.Wallpapers)+columns-1)/columns) * rowHeight
	first := max(0, int(a.favoritesScroll.Y/rowHeight)-1) * columns
	last := min(len(state.Wallpapers), (int((a.favoritesScroll.Y+height)/rowHeight)+2)*columns)
	kit.Scroll(c.Key("favorite-results")).Label("收藏壁纸列表").TrackScroll(&a.favoritesScroll).Grow(1).MinHeight(0).FillWidth().Margin(10).Children(func() {
		kit.Box(c.Key("favorite-canvas")).Height(totalHeight).FillWidth().Shrink(0).Children(func() {
			for index := first; index < last; index++ {
				wallpaper := state.Wallpapers[index]
				x, y := inset+float32(index%columns)*(legacyCardWidth+legacyCardGap), float32(index/columns)*rowHeight
				visible := y+legacyCardImageHeight+legacyCardBarHeight > a.favoritesScroll.Y && y < a.favoritesScroll.Y+height
				kit.Box(c.Key(wallpaper.ID)).Absolute().Left(x).Top(y).Size(legacyCardWidth, legacyCardImageHeight+legacyCardBarHeight).Children(func() {
					a.wallpaperCardSized(c, wallpaper, legacyCardImageHeight, visible)
				})
			}
		})
	})
	if a.favoritesViewport != viewport || a.favoritesWindowWidth != windowWidth || a.favoritesWindowHeight != windowHeight || a.favoritesScrollY != a.favoritesScroll.Y {
		a.favoritesViewport, a.favoritesWindowWidth, a.favoritesWindowHeight = viewport, windowWidth, windowHeight
		a.favoritesScrollY = a.favoritesScroll.Y
		c.Invalidate()
	}
}

func (a *App) downloadsPage(c *kit.Context) {
	if a.actions == nil {
		a.emptyActionPage(c, "下载服务尚未就绪")
		return
	}
	history := a.actions.TransferHistory()
	if history.Loading && !history.Ready {
		kit.Column(c).Grow(1).FillWidth().Center().Children(func() {
			kit.Spinner(c).Label("正在加载下载历史").Size(26, 26).TextColor(legacyPalette(c).Accent)
		})
		return
	}
	if history.Error != "" {
		a.errorNotice(c, history.Error, a.actions.RetryTransfers)
	}
	if !history.Ready {
		a.emptyActionPage(c, "下载历史尚未载入")
		return
	}
	transfers := a.actions.Transfers()
	kit.Scroll(c.Key("downloads")).Label("下载任务列表").TrackScroll(&a.downloadsScroll).Grow(1).FillWidth().MinHeight(0).Children(func() {
		kit.Column(c).FillWidth().Padding(20).Gap(30).Children(func() {
			for _, completed := range []bool{false, true} {
				count := 0
				for _, transfer := range transfers {
					if (transfer.Completed && !transfer.Running) == completed {
						count++
					}
				}
				if completed && count == 0 {
					continue
				}
				kit.Column(c.Key(completed)).FillWidth().Gap(20).Children(func() {
					title := "下载中"
					if completed {
						title = "已完成"
					}
					kit.Row(c).FillWidth().Gap(20).AlignItems(kit.Center).Children(func() {
						kit.Text(c, title).FontSize(20).Bold().Shrink(0)
						if completed {
							kit.Box(c).Grow(1)
							canClear := false
							for _, transfer := range transfers {
								canClear = canClear || transfer.Completed && !transfer.Running && !transfer.Stopping
							}
							legacyChip(c, "clear-completed", "清空已完成记录", false).Label("清空已完成记录").MinWidth(120).TextColor(legacyPalette(c).Accent).Disabled(!canClear).Description("仅清除记录，保留已下载文件").Tooltip("仅清除记录，保留已下载文件").OnClick(a.actions.ClearCompletedTransfers)
						} else {
							a.downloadDirectory(c)
						}
					})
					if !completed && a.preferences != nil && a.preferences.State().Error != "" {
						kit.Row(c.Key("directory-error")).FillWidth().Gap(10).AlignItems(kit.Center).Children(func() {
							state := a.preferences.State()
							kit.Text(c, state.Error).FontSize(12).TextColor(c.Theme().Danger).Grow(1).MinWidth(0)
							if !state.Ready {
								legacyChip(c, "retry-directory", "重试下载目录", false).Disabled(state.Busy()).OnClick(a.preferences.Load)
							}
						})
					}
					if count == 0 {
						kit.Column(c.Key("empty-downloads")).FillWidth().Height(240).Center().Gap(12).Children(func() {
							kit.Image(c, legacyBitmaps["empty"]).Size(270, 180).Fit(kit.Contain)
							kit.Text(c, "任务队列已经空空如也 ~").FontSize(20).TextColor(legacyPalette(c).Muted)
						})
					}
					kit.Column(c).FillWidth().Gap(30).Children(func() {
						for _, transfer := range transfers {
							if (transfer.Completed && !transfer.Running) == completed {
								a.transferRow(c, transfer)
							}
						}
					})
				})
			}
		})
	})
}

func (a *App) downloadDirectory(c *kit.Context) {
	if a.preferences == nil {
		kit.Box(c).Grow(1)
		return
	}
	state := a.preferences.State()
	path := state.Values.DownloadPath
	if !state.Ready {
		path = "下载目录尚未载入"
		if state.Loading {
			path = "正在加载下载目录…"
		}
	}
	kit.Text(c, "当前目录："+path).Tooltip(path).FontSize(12).TextColor(legacyPalette(c).Muted).Grow(1).MinWidth(0).MaxLines(1)
	legacyChip(c, "choose-directory", "修改下载目录", false).Label("修改下载目录").MinWidth(100).Shrink(0).
		TextColor(legacyPalette(c).Accent).Disabled(!state.Ready || state.Busy() || a.choosingDirectory).
		OnClick(a.chooseDownloadDirectory)
}

func transferStatus(transfer domain.Transfer) string {
	if transfer.Stopping {
		return "正在停止…"
	}
	if transfer.Paused {
		if transfer.Total > 0 {
			return fmt.Sprintf("已暂停 · %.0f%%", float64(transfer.Received)/float64(transfer.Total)*100)
		}
		return "已暂停"
	}
	if transfer.Queued {
		return "等待中"
	}
	if transfer.Running {
		if transfer.Kind == domain.WallpaperTransfer && transfer.Total > 0 && transfer.Received >= transfer.Total {
			return "正在设置桌面壁纸"
		}
		if transfer.Total > 0 {
			return fmt.Sprintf("已下载 %.0f%%", float64(transfer.Received)/float64(transfer.Total)*100)
		}
		return "已下载 " + fileSize(transfer.Received)
	}
	if transfer.Canceled {
		return "已取消"
	}
	if transfer.Error != "" {
		return "失败：" + transfer.Error
	}
	if transfer.Kind == domain.WallpaperTransfer {
		return "已设置为桌面壁纸"
	}
	return "下载完成"
}

func (a *App) transferRow(c *kit.Context, transfer domain.Transfer) {
	p, wallpaper := legacyPalette(c), transfer.Wallpaper
	key := string(transfer.Kind) + "-" + wallpaper.ID
	kit.Row(c.Key(key)).Label("任务 " + key).FillWidth().Height(80).Shrink(0).Radius(3).Clip().Background(p.Search).Children(func() {
		kit.Box(c).Size(120, 80).Shrink(0).Background(p.Card).Children(func() {
			if a.images != nil {
				if source, _ := a.images.Get(wallpaper.Thumbnail); source != nil {
					kit.Image(c, source).Fill().Fit(kit.Cover)
				}
			}
		})
		kit.Column(c).Grow(1).MinWidth(0).FillHeight().Padding(8, 14).Gap(3).Children(func() {
			kind := "下载"
			if transfer.Kind == domain.WallpaperTransfer {
				kind = "设为壁纸"
			}
			kit.Text(c, kind+" · "+wallpaper.Resolution+" · "+fileSize(wallpaper.FileSize)).FontSize(13).MaxLines(1)
			status := transferStatus(transfer)
			if transfer.Completed && transfer.Path != "" {
				status += " · " + transfer.Path
			}
			kit.Text(c, status).Tooltip(status).FontSize(12).TextColor(p.Muted).MaxLines(1)
			if transfer.Completed && !transfer.CompletedAt.IsZero() {
				kit.Text(c, "完成于 "+transfer.CompletedAt.Local().Format("2006-01-02 15:04:05")).FontSize(11).TextColor(p.Muted).MaxLines(1)
			} else if !transfer.Queued {
				amount := "已下载：" + transferSize(transfer.Received)
				if transfer.Total > 0 {
					amount += " / " + transferSize(transfer.Total)
				}
				if transfer.Running && !transfer.Stopping {
					amount += " · 速度：" + transferSpeed(transfer.BytesPerSecond)
				}
				kit.Text(c, amount).Tooltip(amount).FontSize(11).TextColor(p.Muted).MaxLines(1)
			}
			if transfer.Running || transfer.Stopping || transfer.Paused && transfer.Total > 0 {
				progress := float64(-1)
				if transfer.Total > 0 {
					progress = min(1, float64(transfer.Received)/float64(transfer.Total))
				}
				kit.Progress(c, progress).Label("进度 " + key).Absolute().Left(20).Right(20).Bottom(3).Height(3).TextColor(p.Accent)
			}
		})
		kit.Row(c).AlignItems(kit.Center).Height(80).Shrink(0).Padding(0, 8).Children(func() {
			history := a.actions.TransferHistory()
			if transfer.Stopping {
				legacyChip(c, "stopping", "停止中", false).Label("停止中 " + key).Disabled(true)
			} else if transfer.Running || transfer.Queued {
				if !transfer.Queued {
					legacyChip(c, "pause", "暂停", false).Label("暂停 " + key).Disabled(!history.Ready).OnClick(func() { a.actions.PauseTransfer(transfer.Kind, wallpaper.ID) })
				}
				legacyChip(c, "cancel", "取消", false).Label("取消 " + key).Disabled(!history.Ready).OnClick(func() { a.actions.CancelTransfer(transfer.Kind, wallpaper.ID) })
			} else if transfer.Paused {
				legacyChip(c, "resume", "继续", false).Label("继续 " + key).Disabled(!history.Ready).OnClick(func() { a.actions.ResumeTransfer(transfer.Kind, wallpaper.ID) })
				legacyChip(c, "cancel", "取消", false).Label("取消 " + key).Disabled(!history.Ready).OnClick(func() { a.actions.CancelTransfer(transfer.Kind, wallpaper.ID) })
			} else if transfer.Error != "" || transfer.Canceled {
				legacyChip(c, "retry", "重试", false).Label("重试 " + key).Disabled(!history.Ready).OnClick(func() { a.actions.ResumeTransfer(transfer.Kind, wallpaper.ID) })
			} else if transfer.Completed && transfer.Path != "" {
				legacyChip(c, "reveal", "打开位置", false).Label("打开位置 " + key).Disabled(a.revealFile == nil).OnClick(func() {
					if a.revealFile != nil {
						a.revealFile(transfer.Path)
					}
				})
			}
			if !transfer.Running && !transfer.Queued && !transfer.Stopping {
				legacyIconButton(c, "remove-record", "close", "删除记录 "+key, 16).Size(30, 30).Disabled(!history.Ready).Description("仅删除记录，保留已下载文件").Tooltip("仅删除记录，保留已下载文件").OnClick(func() { a.actions.RemoveTransfer(transfer.Kind, wallpaper.ID) })
			}
		})
	})
}

func transferSize(bytes int64) string {
	if bytes < 1<<10 {
		return fmt.Sprintf("%d B", max(int64(0), bytes))
	}
	return fileSize(bytes)
}

func transferSpeed(value float64) string {
	if value >= 1<<20 {
		return fmt.Sprintf("%.1f MB/s", value/(1<<20))
	}
	if value >= 1<<10 {
		return fmt.Sprintf("%.1f KB/s", value/(1<<10))
	}
	return fmt.Sprintf("%.0f B/s", value)
}

func (a *App) actionNotice(c *kit.Context) {
	if a.actions == nil {
		return
	}
	notice := a.actions.Notice()
	if notice.Message == "" {
		return
	}
	if a.noticeSequence != notice.Sequence {
		a.noticeSequence, a.noticeUntil = notice.Sequence, c.Now().Add(5*time.Second)
	}
	if !c.Now().Before(a.noticeUntil) {
		a.actions.ClearNotice()
		return
	}
	c.After(a.noticeUntil.Sub(c.Now()))
	windowWidth, _ := c.Size()
	width := min(float32(600), windowWidth-40)
	p := legacyPalette(c)
	border := p.Accent
	if notice.Error {
		border = c.Theme().Danger
	}
	kit.Row(c.Key("action-notice")).Label("操作提示").Absolute().ZIndex(100).Left((windowWidth-width)/2).Bottom(20).Width(width).Padding(10, 12).Gap(8).AlignItems(kit.Center).Radius(6).Background(p.Dialog).Border(1, border).
		Transition(kit.ElementTransition{Duration: 140 * time.Millisecond, Enter: &kit.Motion{Y: 6}, Exit: &kit.Motion{Y: 6}}).Children(func() {
		kit.Text(c, notice.Message).FontSize(13).TextColor(p.Text).Grow(1).MinWidth(0).MaxLines(2)
		legacyIconButton(c, "dismiss", "close", "关闭操作提示").Size(24, 24).OnClick(a.actions.ClearNotice)
	})
}
