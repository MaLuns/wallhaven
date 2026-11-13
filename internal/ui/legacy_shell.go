package ui

import (
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

const (
	navigationHeight    = float32(60)
	navigationItemWidth = float32(80)
)

func (a *App) titleBar(c *kit.Context) {
	p := legacyPalette(c)
	foreground := hexColor("161616")
	if c.Theme().Dark {
		foreground = hexColor("e4e4e4")
	}
	kit.Row(c.Key("title-bar")).Label("标题栏").FillWidth().Height(30).Shrink(0).Background(p.Header).TextColor(foreground).DragWindow().AlignItems(kit.Center).Children(func() {
		kit.Row(c.Key("brand")).Width(160).Padding(0, 10).Gap(6).AlignItems(kit.Center).Children(func() {
			kit.Image(c, legacyBitmaps["logo"]).Size(20, 20)
			kit.Text(c, "One Wallhaven").FontSize(14).Bold()
		})
		kit.Box(c).Grow(1)
		menu := legacyIconButton(c, "menu", "menu", "功能菜单", 16).Size(40, 30).OnClick(func() { a.menuOpen = !a.menuOpen })
		kit.Popover(c.Key("function-menu"), menu, &a.menuOpen, func() {
			kit.Grid(c).Width(180).Columns(3).Padding(5).Gap(10).Children(func() {
				for _, item := range []struct{ title, icon, label string }{{"下载", "download", "前往下载"}, {"皮肤", "theme", "皮肤"}, {"设置", "settings", "前往设置"}, {"日志", "log", "日志"}, {"关于", "about", "关于"}} {
					b := kit.ButtonBase(c.Key(item.title)).Label(item.label).FillWidth().Height(60).Padding(0).Background(kit.Transparent)
					if b.Hovered() {
						b.TextColor(p.Accent)
					}
					b.Children(func() {
						kit.Column(c).Fill().Center().Gap(5).Children(func() {
							appIcon(c, item.icon, 24)
							kit.Text(c, item.title).FontSize(12)
						})
					})
					b.OnClick(func() {
						a.menuOpen = false
						switch item.title {
						case "下载":
							a.controller.Navigate(domain.Downloads)
						case "设置":
							if a.onOpenSettings != nil {
								a.onOpenSettings()
							} else {
								a.controller.Navigate(domain.Settings)
							}
						default:
							a.dialogTitle = item.title
							a.dialogOpen = true
						}
					})
				}
			})
		}).Background(p.Dialog).Radius(6)
		legacyIconButton(c, "refresh", "refresh", "刷新当前页面", 16).Size(40, 30).OnClick(a.refresh)
		legacyIconButton(c, "minimize", "minimize", "最小化", 16).Size(40, 30).OnClick(func() {
			if a.onMinimize != nil {
				a.onMinimize()
			}
		})
		legacyIconButton(c, "close", "close", "关闭窗口", 16).Size(40, 30).OnClick(func() {
			if a.onClose != nil {
				a.onClose()
			}
		})
	})
}

func (a *App) navigation(c *kit.Context) {
	p := legacyPalette(c)
	current := a.controller.CurrentPage().ID
	kit.Row(c.Key("navigation")).Label("主导航").Height(navigationHeight).Shrink(0).FillWidth().Justify(kit.Center).AlignItems(kit.Center).Children(func() {
		if current == domain.Downloads || current == domain.Settings {
			legacyIconButton(c, "back", "back", "返回收藏", 20).Absolute().Left(20).Top((navigationHeight-30)/2).Size(30, 30).OnClick(func() { a.controller.Navigate(domain.Favorites) })
		}
		for _, item := range []struct {
			title, badge, label string
			id                  domain.PageID
		}{{"推荐", "recommend", "前往推荐", domain.Recommend}, {"壁纸", "wallpaper", "前往壁纸", domain.WallpaperPage}, {"组件", "component", "前往组件", domain.Components}, {"我的", "my", "前往收藏", domain.Favorites}} {
			selected := current == item.id || item.id == domain.Favorites && (current == domain.Downloads || current == domain.Settings)
			b := kit.ButtonBase(c.Key(string(item.id))).Label(item.label).Tooltip(item.title).Size(navigationItemWidth, navigationHeight).Margin(0, 20, 0, 0).Padding(0).Background(kit.Transparent)
			if b.Hovered() {
				b.TextColor(p.Accent)
			}
			b.Children(func() {
				if selected {
					navigationBadge(c, item.badge, item.title)
				} else {
					kit.Text(c, item.title).FontSize(14).FontWeight(550).IgnoreInput(true)
				}
			})
			b.OnClick(func() { a.controller.Navigate(item.id) })
		}
	})
}

func (a *App) dialog(c *kit.Context) {
	p := legacyPalette(c)
	windowWidth, windowHeight := c.Size()
	kit.DialogBase(c.Key("legacy-dialog"), &a.dialogOpen, func(backdrop, panel kit.Element) {
		backdrop.Background(kit.RGBA(0, 0, 0, .71))
		width := float32(400)
		if a.dialogTitle == "关于" {
			width = 500
		}
		panel.Label(a.dialogTitle+"对话框").Absolute().Top(windowHeight*.25).Left((windowWidth-width)/2).Width(width).MaxWidthPercent(90).Padding(12, 20, 20).Background(p.Dialog).Border(2, hexColor("3da7fe")).Radius(16).Clip().Children(func() {
			kit.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(6).Background(hexColor("3da7fe"))
			kit.Column(c).FillWidth().Gap(18).Children(func() {
				kit.Row(c).FillWidth().AlignItems(kit.Center).Children(func() {
					kit.Text(c, a.dialogTitle).FontSize(16).Grow(1)
					legacyIconButton(c, "close-dialog", "close", "关闭弹窗").OnClick(func() { a.dialogOpen = false })
				})
				switch a.dialogTitle {
				case "皮肤":
					kit.Row(c).FillWidth().Justify(kit.Center).Gap(30).Padding(20, 0).Children(func() {
						for _, item := range []struct{ name, title string }{{"light", "经典白"}, {"dark", "炫酷黑"}} {
							kit.ButtonBase(c.Key(item.name)).Label(item.title).Padding(0).Width(132).Background(kit.Transparent).Children(func() {
								kit.Column(c).Gap(10).AlignItems(kit.Center).Children(func() {
									selected := a.appearance == item.name || a.appearance == "" && c.Theme().Dark == (item.name == "dark")
									preview := kit.Box(c).Size(136, 78).Radius(16).Border(2, kit.Transparent).Clip()
									if selected {
										preview.Border(2, hexColor("3da7fe"))
									}
									preview.Children(func() {
										kit.Image(c, legacyBitmaps[item.name]).Size(132, 74)
										if selected {
											kit.Box(c).Absolute().Right(0).Bottom(0).Size(32, 32).Center().Background(p.Accent).TextColor(kit.RGB(255, 255, 255)).IgnoreInput(true).Children(func() { appIcon(c, "check", 18) })
										}
									})
									kit.Text(c, item.title).FontSize(14)
								})
							}).OnClick(func() { a.appearance = item.name })
						}
					})
				case "关于":
					kit.Text(c, "《One Wallhaven》是一个由个人开发的桌面壁纸类程序。本程序图片内容来源于 wallhaven.cc，图片版权为原作者所有。").FontSize(14).LineHeight(1.8)
				case "日志":
					kit.Scroll(c).Height(300).FillWidth().Children(func() {
						kit.Column(c).Gap(16).Children(func() {
							for _, log := range legacyLogs {
								kit.Text(c, log.Title+"  "+log.Time).FontSize(16).Bold()
								for _, line := range log.Children {
									kit.Text(c, "· "+line).FontSize(14)
								}
							}
						})
					})
				}
			})
		})
	})
}
