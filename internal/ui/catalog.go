package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

const (
	legacyCardWidth       = float32(300)
	legacyCardImageHeight = float32(200)
	legacyCardBarHeight   = float32(40)
	legacyCardGap         = float32(20)
)

func hexColor(value string) kit.Color {
	v, _ := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(value), "#"), 16, 32)
	return kit.RGB(uint8(v>>16), uint8(v>>8), uint8(v))
}

func (a *App) refresh() {
	a.results.Y = 0
	a.autoPage = 0
	for _, scroll := range a.groupScroll {
		scroll.X = 0
	}
	a.controller.Refresh()
}

// legacySectionTitle follows one-title: a 40px title line, 20px vertical
// margins, and the original asymmetric cyan ornaments.
func legacySectionTitle(c *kit.Context, title string) {
	cyan := hexColor("3babde")
	kit.Row(c.Key("title")).Height(80).Shrink(0).FillWidth().Center().Children(func() {
		kit.Box(c.Key("left-lines")).Width(126).Height(14).Children(func() {
			kit.Box(c).Absolute().Left(0).Top(3).Size(126, 2).Radius(2).Background(cyan)
			kit.Box(c).Absolute().Right(0).Top(9).Size(36, 2).Radius(2).Background(cyan)
			kit.Box(c).Absolute().Right(44).Top(9).Size(20, 2).Radius(2).Background(cyan)
		})
		kit.Text(c, title).FontSize(20).Bold().Margin(0, 20).TextColor(legacyPalette(c).Text)
		kit.Box(c.Key("right-lines")).Width(120).Height(14).Children(func() {
			kit.Box(c).Absolute().Left(0).Top(3).Size(100, 2).Radius(2).Background(cyan)
			kit.Box(c).Absolute().Left(0).Top(9).Size(50, 2).Radius(2).Background(cyan)
			kit.Box(c).Absolute().Left(105).Top(3).Size(15, 2).Radius(2).Background(cyan)
		})
	})
}

func (a *App) recommendations(c *kit.Context) {
	kit.Scroll(c.Key("recommendations")).Label("推荐分组").TrackScroll(&a.recommendationScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
		kit.Column(c).FillWidth().Padding(0, 20, 20).Children(func() {
			for _, group := range a.controller.Recommendations() {
				kit.Column(c.Key(group.Key)).FillWidth().Children(func() {
					legacySectionTitle(c, group.Title)
					if group.Error != "" {
						a.errorNotice(c, group.Error, a.refresh)
						if len(group.Wallpapers) == 0 {
							return
						}
					}
					if group.Loaded && !group.Loading && len(group.Wallpapers) == 0 {
						kit.Column(c).Height(240).FillWidth().Center().Children(func() {
							kit.Text(c, "暂无推荐壁纸").FontSize(13).TextColor(legacyPalette(c).Muted)
						})
						return
					}
					scroll := a.groupScroll[group.Key]
					viewport := a.recommendationScroll.Bounds(c)
					strip := scroll.Bounds(c)
					if strip.W == 0 {
						c.Invalidate()
					}
					kit.Box(c.Key("strip-frame")).Height(240).Shrink(0).FillWidth().Children(func() {
						kit.ScrollHorizontal(c.Key("strip")).Label(group.Title + "图片列表").TrackScroll(scroll).Fill().Gap(legacyCardGap).Scrollbars(false).
							HandleInput(func(event kit.InputEvent) bool { return recommendationWheel(scroll, event) }).Children(func() {
							if group.Loading && len(group.Wallpapers) == 0 {
								for i := 0; i < 5; i++ {
									legacySkeletonCard(c.Key(i))
								}
							}
							for i, wallpaper := range group.Wallpapers {
								left := float32(i)*(legacyCardWidth+legacyCardGap) - scroll.X
								visible := strip.W > 0 && strip.Y+strip.H > viewport.Y && strip.Y < viewport.Y+viewport.H && left+legacyCardWidth > 0 && left < strip.W
								nearby := strip.W > 0 && strip.Y+strip.H > viewport.Y && strip.Y < viewport.Y+viewport.H && left+legacyCardWidth > 0 && left < strip.W+2*(legacyCardWidth+legacyCardGap)
								if nearby && !visible {
									a.prefetchThumbnail(wallpaper)
								}
								a.wallpaperCardSized(c, wallpaper, legacyCardImageHeight, visible, func() {
									a.openDetail(wallpaper, app.DetailList{Page: domain.Recommend, Recommendation: group.Key})
								})
							}
						})
					})
				})
			}
		})
	})
	// Bounds are committed geometry. A single guarded extra pass after a scroll
	// refreshes visibility without building or requesting every offscreen image.
	if a.lastImageScrollY != a.recommendationScroll.Y {
		a.lastImageScrollY = a.recommendationScroll.Y
		c.Invalidate()
	}
	for key, scroll := range a.groupScroll {
		if a.lastImageScrollX[key] != scroll.X {
			a.lastImageScrollX[key] = scroll.X
			c.Invalidate()
		}
	}
}

// Convert ordinary vertical mouse wheels only. Touchpads and modified input
// keep their native axes; at either end the outer page can take the wheel.
func recommendationWheel(scroll *kit.ScrollState, event kit.InputEvent) bool {
	if event.Kind != kit.InputScroll || event.Precise || event.Mods != 0 || event.DX != 0 || event.DY == 0 {
		return false
	}
	next := max(float32(0), min(scroll.MaxX, scroll.X+event.DY))
	if next == scroll.X {
		return false
	}
	scroll.X = next
	return true
}

func legacySkeletonCard(c *kit.Context) {
	p := legacyPalette(c)
	kit.Column(c).Width(300).Height(240).Shrink(0).Radius(8).Clip().Children(func() {
		kit.Box(c).FillWidth().Height(200).Background(p.Search)
		kit.Box(c).FillWidth().Height(40).Background(p.Card)
	})
}

func (a *App) errorNotice(c *kit.Context, message string, retry func()) {
	p := legacyPalette(c)
	kit.Row(c.Key("error")).FillWidth().Padding(10, 20).Gap(8).AlignItems(kit.Center).Children(func() {
		kit.Text(c, message).FontSize(13).TextColor(c.Theme().Danger).Grow(1).MinWidth(0)
		legacyChip(c, "retry", "重试", false).TextColor(p.Accent).OnClick(retry)
	})
}

func (a *App) wallpaperCard(c *kit.Context, wallpaper domain.Wallpaper, visible ...bool) {
	load := len(visible) == 0 || visible[0]
	a.wallpaperCardSized(c, wallpaper, legacyCardImageHeight, load)
}

func (a *App) wallpaperCardSized(c *kit.Context, wallpaper domain.Wallpaper, imageHeight float32, visible bool, open ...func()) {
	p := legacyPalette(c)
	card := kit.Column(c.Key(wallpaper.ID)).Label("壁纸 " + wallpaper.ID).Width(legacyCardWidth).Height(imageHeight + legacyCardBarHeight).Shrink(0).Radius(8).Clip().
		Transition(kit.ElementTransition{Duration: 140 * time.Millisecond, Shadows: true})
	if card.Hovered() {
		card.Shadow(4, 4, 11, -4, kit.RGBA(0, 0, 0, .5))
	}
	card.Children(func() {
		area := kit.Box(c.Key("thumbnail")).Label("查看壁纸 " + wallpaper.ID).FillWidth().Height(imageHeight).Shrink(0).Background(p.Search).Clip()
		hovered := area.Hovered()
		onOpen := func() { a.openDetail(wallpaper) }
		if len(open) > 0 {
			onOpen = open[0]
		}
		area.OnClick(onOpen)
		area.Children(func() {
			var source kit.ImageSource
			loading := false
			if a.images != nil && visible {
				source, loading = a.images.Get(a.cardThumbnailURL(wallpaper))
			}
			waited := kit.Local(area, "thumbnail-waited", func() bool { return false })
			if !visible {
				*waited = false
			} else if loading {
				*waited = true
			}
			if source != nil {
				duration := time.Duration(-1)
				if *waited {
					duration = 160 * time.Millisecond
				}
				fit := kit.Contain
				if a.useSmallCardThumbnails() {
					fit = kit.Cover
				}
				kit.Image(c.Key("loaded-thumbnail"), source).Fill().Fit(fit).
					Transition(kit.ElementTransition{Duration: duration, Enter: &kit.Motion{}})
			} else {
				label := "图片暂不可用"
				if loading {
					label = "图片加载中…"
				}
				kit.Column(c).Fill().Center().Children(func() { kit.Text(c, label).FontSize(12).TextColor(p.Muted) })
			}
			if hovered {
				kit.Row(c.Key("image-info")).Absolute().Top(0).Left(0).Right(0).Height(30).Padding(0, 10).AlignItems(kit.Center).Background(p.Card.Alpha(.8)).ZIndex(1).
					Transition(kit.ElementTransition{Duration: 120 * time.Millisecond, Enter: &kit.Motion{Y: -4}, Exit: &kit.Motion{Y: -4}}).Children(func() {
					kit.Text(c, fileSize(wallpaper.FileSize)).FontSize(12).TextColor(p.Muted)
					kit.Text(c, wallpaper.Resolution).FontSize(12).TextColor(p.Muted).Grow(1).TextAlign(kit.Center)
					kit.Text(c, wallpaper.FileType).FontSize(12).TextColor(p.Muted)
				})
			}
		})
		kit.Row(c.Key("actions")).FillWidth().Height(40).Shrink(0).Padding(0, 10).AlignItems(kit.Center).Background(p.Card).Children(func() {
			for i, action := range []struct{ name, label string }{{"favorite", "收藏"}, {"wallpaper", "设为壁纸"}, {"download", "下载"}} {
				if i > 0 {
					kit.Box(c).Grow(1)
				}
				label := action.label + " " + wallpaper.ID
				if action.name == "favorite" && a.actions != nil && a.actions.IsFavorite(wallpaper.ID) {
					label = "取消收藏 " + wallpaper.ID
				}
				a.bindWallpaperAction(c, legacyIconButton(c, action.name, action.name, label).Size(24, 30).TextColor(p.Muted), action.name, wallpaper)
			}
		})
	})
}

func fileSize(bytes int64) string {
	if bytes >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	}
	return fmt.Sprintf("%d KB", bytes/1024)
}

type wallpaperPlacement struct{ x, y, imageHeight float32 }

// legacyWaterfall keeps img-list's 300px columns, shortest-column packing,
// 20px gap and centered remainder, with full original-aspect-ratio images.
func legacyWaterfall(wallpapers []domain.Wallpaper, width float32) ([]wallpaperPlacement, float32) {
	var layout waterfallLayout
	return layout.update(wallpapers, width, 0)
}

func (a *App) searchPage(c *kit.Context) {
	a.searchControls(c)
	state := a.searchSnapshot()
	if state.Error != "" {
		a.errorNotice(c, state.Error, func() {
			if state.CurrentPage > 0 {
				a.controller.LoadMore()
			} else {
				a.controller.Search(state.Query)
			}
		})
	}
	if len(state.Wallpapers) == 0 {
		kit.Column(c.Key("search-empty")).Grow(1).FillWidth().Center().Gap(10).Children(func() {
			if state.Loading {
				kit.Spinner(c.Key("first-loading")).Label("正在加载壁纸").Size(26, 26).TextColor(legacyPalette(c).Accent)
				return
			}
			title := "搜索喜欢的壁纸"
			if state.Error != "" {
				title = "壁纸加载失败"
			} else if state.Loaded {
				title = "没有找到符合条件的壁纸"
			}
			kit.Text(c, title).FontSize(18).TextColor(legacyPalette(c).Muted)
		})
		return
	}
	windowWidth, windowHeight := c.Size()
	viewport := a.results.Bounds(c)
	width, height := viewport.W, viewport.H
	if width <= 0 {
		width = max(legacyCardWidth, windowWidth-2*searchPanelSideInset)
		c.Invalidate()
	}
	if height <= 0 {
		height = max(200, windowHeight-30-navigationHeight-compactSearchPanelHeight-10)
	}
	mode := a.previewMode()
	if a.waterfall.ready && a.waterfall.mode != mode {
		a.results.Y, a.autoPage = 0, 0
	}
	placements, totalHeight := a.waterfall.updateMode(state.Wallpapers, width, state.Generation, mode)
	tailHeight := float32(0)
	// Reserving the tail before a request keeps the loader visible at the end
	// without moving the reader when its animation starts or disappears.
	if state.HasMore || state.Loading {
		tailHeight = 52
	}
	first, last := a.waterfall.visibleRange(a.results.Y-height*.5, a.results.Y+height*2)
	// Stretch deducts both margins; a 100% width would overflow the right edge.
	kit.Scroll(c.Key("search-results")).Label("搜索结果").TrackScroll(&a.results).Grow(1).MinHeight(0).AlignSelf(kit.Stretch).Margin(0, searchPanelSideInset, 10).Children(func() {
		kit.Box(c.Key("waterfall-canvas")).Height(totalHeight + tailHeight).FillWidth().Shrink(0).Children(func() {
			for i := first; i < last; i++ {
				place := placements[i]
				if place.y+place.imageHeight+legacyCardBarHeight < a.results.Y-height*.5 || place.y > a.results.Y+height*2 {
					continue
				}
				visible := place.y+place.imageHeight+legacyCardBarHeight > a.results.Y && place.y < a.results.Y+height
				if !visible {
					a.prefetchThumbnail(state.Wallpapers[i])
				}
				kit.Box(c.Key(state.Wallpapers[i].ID)).Absolute().Left(place.x).Top(place.y).Width(legacyCardWidth).Height(place.imageHeight + legacyCardBarHeight).Children(func() { a.wallpaperCardSized(c, state.Wallpapers[i], place.imageHeight, visible) })
			}
			if state.Loading {
				kit.Column(c.Key("tail-loading")).Absolute().Left(0).Right(0).Top(totalHeight).Height(tailHeight).Center().Children(func() {
					kit.Spinner(c).Label("正在加载下一页").Size(24, 24).TextColor(legacyPalette(c).Accent)
				})
			}
		})
	})
	if a.catalogViewport != viewport || a.catalogScrollY != a.results.Y || a.catalogWindowWidth != windowWidth || a.catalogWindowHeight != windowHeight {
		a.catalogViewport = viewport
		a.catalogScrollY = a.results.Y
		a.catalogWindowWidth = windowWidth
		a.catalogWindowHeight = windowHeight
		c.Invalidate()
	}
	// Calculate remaining space from the new layout, not last frame's MaxY:
	// an appended page must not accidentally trigger another request at once.
	remaining := totalHeight + tailHeight - a.results.Y - height
	lookahead := max(float32(1000), height*1.5)
	if state.HasMore && !state.Loading && state.Error == "" && a.results.Y > 0 && remaining <= lookahead && a.autoPage != state.CurrentPage && !a.controller.DetailState().Open {
		a.autoPage = state.CurrentPage
		a.controller.LoadMore()
		c.Invalidate()
	}
}
