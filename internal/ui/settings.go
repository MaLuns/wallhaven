package ui

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/locale"
	"github.com/egoist/mygo/i18n"
	kit "github.com/egoist/mygo/ui"
)

const (
	SettingsWindowWidth   = 560
	SettingsWindowHeight  = 620
	settingsLabelWidth    = float32(120)
	settingsControlHeight = float32(34)
)

// The native window and its painted frame must use the same curve. Other
// platforms retain a square frame instead of adding macOS-style corners.
func SettingsCornerRadius() int {
	if runtime.GOOS == "darwin" {
		return 16
	}
	return 0
}

// SettingsView retains the original dark frame, divider and label alignment.
// The scrollable body also supports smaller manually resized windows.
func (a *App) SettingsView(c *kit.Context) {
	_, height := c.Size()
	groupGap, pathHeight := float32(16), float32(78)
	if height < 480 {
		groupGap, pathHeight = 10, 60
	}
	theme := *kit.DarkTheme()
	theme.Background, theme.Text, theme.Accent = hexColor("201f29"), hexColor("eeeeee"), hexColor("2d8cf0")
	c.SetTheme(&theme)
	p := legacyPalette(c)
	theme.Inverse, theme.InverseText = p.Dialog, p.Text
	c.SetTheme(&theme)
	c.Root().Background(kit.Transparent)
	kit.Column(c.Key("settings")).Label(locale.T("应用设置")).Fill().Background(theme.Background).Border(4, hexColor("266294")).Radius(float32(SettingsCornerRadius())).TextColor(hexColor("c2c2c2")).Clip().DragWindow().Children(func() {
		kit.Row(c.Key("settings-header")).Label(locale.T("设置标题栏")).FillWidth().Height(42).Shrink(0).Padding(10, 20, 8).AlignItems(kit.Center).DragWindow().Children(func() {
			kit.Text(c, locale.T("设置")).FontSize(18).Bold().TextColor(hexColor("eeeeee")).Grow(1).IgnoreInput(true)
			legacyIconButton(c, "close-settings", "close", "关闭设置", 18).Size(18, 18).TextColor(hexColor("eeeeee")).OnClick(func() {
				if a.onCloseSettings != nil {
					a.onCloseSettings()
				} else {
					a.controller.Navigate(domain.Favorites)
				}
			})
		})
		kit.Box(c).Absolute().Left(124).Top(66).Bottom(24).Width(2).Radius(2).Background(hexColor("266294")).IgnoreInput(true)
		kit.Scroll(c.Key("settings-body")).Label(locale.T("设置选项")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			kit.Column(c).FillWidth().Padding(20).Gap(groupGap).Children(func() {
				a.settingsRow(c, "语言", func() { a.languageSelector(c) })
				if a.preferences == nil {
					kit.Text(c, locale.T("设置服务尚未就绪")).FontSize(14)
					return
				}
				state := a.preferences.State()
				disabled := !state.Ready || state.Busy() || a.choosingDirectory
				a.settingsRow(c, "预览模式", func() {
					kit.Row(c).Height(settingsRowHeight(c)).Gap(8).AlignItems(kit.Center).Children(func() {
						kit.Text(c, locale.T("普通模式")).FontSize(14)
						waterfall := state.Values.PreviewMode != domain.PreviewTile
						if settingsSwitch(c.Key("preview-mode"), "瀑布流模式", &waterfall, disabled).Changed() {
							mode := domain.PreviewTile
							if waterfall {
								mode = domain.PreviewWaterfall
							}
							a.preferences.SetPreviewMode(mode)
						}
						kit.Text(c, locale.T("瀑布流模式")).FontSize(14)
					})
				})
				a.settingsRow(c, "开机启动", func() {
					kit.Row(c).Height(settingsRowHeight(c)).AlignItems(kit.Center).Children(func() {
						enabled := state.Values.OpenAtLogin
						if settingsSwitch(c.Key("open-at-login"), "开机启动开关", &enabled, disabled).Changed() {
							a.preferences.SetOpenAtLogin(enabled)
						}
					})
				})
				a.settingsRow(c, "下载目录", func() {
					kit.Column(c).FillWidth().Gap(12).Children(func() {
						kit.Row(c).FillWidth().MinHeight(settingsControlHeight).AlignItems(kit.Center).Children(func() {
							kit.Text(c, locale.T("当前目录：")+state.Values.DownloadPath).Grow(1).MinWidth(0).FontSize(13).LineHeight(1.5).Wrap().MaxHeight(pathHeight).Clip().Tooltip(state.Values.DownloadPath)
						})
						kit.Row(c).FillWidth().Gap(12).Children(func() {
							settingsButton(c.Key("choose-directory"), "修改目录").Basis(0).Grow(1).MinWidth(0).Disabled(disabled).OnClick(a.chooseDownloadDirectory)
							settingsButton(c.Key("open-directory"), "打开文件位置").Basis(0).Grow(1).MinWidth(0).Disabled(disabled).OnClick(a.preferences.OpenDownloadDirectory)
						})
					})
				})
				a.settingsRow(c, "磁盘缓存", func() { a.cacheSettings(c) })
				if state.Loading {
					kit.Text(c, locale.T("正在加载设置…")).FontSize(12)
				} else if state.Saving {
					kit.Text(c, locale.T("正在保存设置…")).FontSize(12)
				}
				if state.Error != "" {
					kit.Column(c).FillWidth().Gap(8).Children(func() {
						kit.Text(c, locale.T(state.Error)).FontSize(12).LineHeight(1.4).TextColor(hexColor("ff8f8f"))
						if !state.Ready && !state.Loading {
							settingsButton(c.Key("retry-settings"), "重试加载设置").OnClick(a.preferences.Load)
						}
					})
				}
			})
		})
	})
}

func cacheAmount(bytes int64) string {
	if bytes >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
	}
	if bytes >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
	}
	if bytes >= 1<<10 {
		return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d B", bytes)
}

func cacheCapacity(mib int64) string {
	if mib >= 1024 && mib%1024 == 0 {
		return fmt.Sprintf("%d GiB", mib/1024)
	}
	return fmt.Sprintf("%d MiB", mib)
}

func (a *App) cacheSettings(c *kit.Context) {
	state := a.preferences.State()
	disabled := !state.Ready || state.Busy() || a.choosingDirectory
	_, height := c.Size()
	gap := float32(12)
	if height < 480 {
		gap = 8
	}
	kit.Column(c).FillWidth().Gap(gap).Children(func() {
		thumbnailUsage, originalUsage := locale.T("缩略图上限"), locale.T("原图上限")
		if state.CacheReady {
			thumbnailUsage = fmt.Sprintf(locale.T("缩略图：%s / %s"), cacheAmount(state.Cache.ThumbnailBytes), cacheCapacity(state.Values.ThumbnailCacheMiB))
			originalUsage = fmt.Sprintf(locale.T("原图：%s / %s"), cacheAmount(state.Cache.OriginalBytes), cacheCapacity(state.Values.OriginalCacheMiB))
		} else if !state.CacheBusy {
			kit.Text(c, locale.T("缓存占用尚未读取")).FontSize(12)
		}
		if state.CacheBusy {
			kit.Text(c, locale.T("正在处理缓存…")).FontSize(12)
		}
		cacheLimitSelector(c.Key("thumbnail-cache-limit"), "缩略图缓存上限", thumbnailUsage, &a.thumbnailCacheMiB, state.Values.ThumbnailCacheMiB,
			[]int64{64, 128, 256, 512, 1024, 2048, 4096}, disabled, func(value int64) {
				a.preferences.SetCacheLimits(value, state.Values.OriginalCacheMiB)
			})
		cacheLimitSelector(c.Key("original-cache-limit"), "原图缓存上限", originalUsage, &a.originalCacheMiB, state.Values.OriginalCacheMiB,
			[]int64{64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384}, disabled, func(value int64) {
				a.preferences.SetCacheLimits(state.Values.ThumbnailCacheMiB, value)
			})
		kit.Row(c).FillWidth().Gap(12).Margin(4, 0, 0).Children(func() {
			settingsButton(c.Key("clear-disk-cache"), "清理磁盘缓存").Basis(0).Grow(1).MinWidth(0).Disabled(disabled).OnClick(a.preferences.ClearCache)
			settingsButton(c.Key("refresh-cache"), "刷新").Basis(0).Grow(1).MinWidth(0).Label(locale.T("刷新缓存占用")).Disabled(disabled).OnClick(a.preferences.RefreshCache)
		})
		kit.Text(c, locale.T("仅清理缓存，已下载文件不受影响")).FontSize(11).LineHeight(1.5).Wrap().TextColor(hexColor("969696"))
		if state.CacheError != "" {
			kit.Text(c, locale.T(state.CacheError)).FontSize(12).Wrap().TextColor(hexColor("ff8f8f"))
		}
	})
}

func cacheLimitSelector(c *kit.Context, label, usage string, selected *int64, current int64, values []int64, disabled bool, onChange func(int64)) {
	*selected = current
	_, windowHeight := c.Size()
	menuHeight := min(float32(224), float32(len(values))*28, max(float32(84), windowHeight*.55))
	rowHeight := float32(40)
	if windowHeight < 480 {
		rowHeight = settingsControlHeight
	}
	kit.Row(c).FillWidth().MinHeight(rowHeight).AlignItems(kit.Center).Gap(12).Children(func() {
		kit.Text(c, usage).Grow(1).MinWidth(0).FontSize(12).LineHeight(1.5).Wrap()
		selector := kit.SelectBase(c.Key("select"), selected)
		selector.Trigger.Label(locale.T(label)).Value(cacheCapacity(*selected)).Disabled(disabled).
			Width(136).Shrink(0).Height(settingsControlHeight).Padding(4, 10).Radius(6).Border(1, hexColor("eeeeee")).Background(kit.Transparent).TextColor(hexColor("c2c2c2")).Children(func() {
			kit.Row(c).FillWidth().Gap(8).AlignItems(kit.Center).Children(func() {
				kit.Text(c, cacheCapacity(*selected)).FontSize(13).Grow(1).SingleLine().IgnoreInput(true)
				appIcon(c, "chevron-down", 14)
			})
		})
		selector.Popup(func(panel kit.Element) {
			panel.Padding(4).Radius(6).Background(hexColor("201f29")).Border(1, hexColor("266294")).TextColor(hexColor("c2c2c2"))
			kit.Scroll(c.Key("limit-options")).FillWidth().Height(menuHeight).Children(func() {
				for _, value := range values {
					item := selector.Item(value).Label(cacheCapacity(value)).Height(28).Shrink(0).Padding(5, 10).Radius(4)
					if item.Highlighted() {
						item.Background(hexColor("266294")).TextColor(hexColor("eeeeee"))
					}
					item.Children(func() { kit.Text(c, cacheCapacity(value)).FontSize(13).IgnoreInput(true) })
				}
			})
		})
		if selector.Trigger.Changed() {
			onChange(*selected)
		}
	})
}

func (a *App) languageSelector(c *kit.Context) {
	if a.chosenLanguage != nil {
		chosen := a.chosenLanguage()
		a.language = ""
		if chosen != "" {
			a.language = i18n.Match([]string{chosen}, []string{"zh-Hans", "en"})
		}
	}
	selector := kit.SelectBase(c.Key("language"), &a.language)
	options := []struct{ tag, title string }{
		{"", locale.T("跟随系统")}, {"zh-Hans", "简体中文"}, {"en", "English"},
	}
	title := options[0].title
	for _, option := range options {
		if option.tag == a.language {
			title = option.title
			break
		}
	}
	selector.Trigger.Label(locale.T("界面语言")).Value(title).Disabled(a.onChangeLanguage == nil).
		FillWidth().Height(settingsRowHeight(c)).Padding(4, 10).Radius(6).Border(1, hexColor("eeeeee")).Background(kit.Transparent).TextColor(hexColor("c2c2c2")).
		Children(func() {
			kit.Row(c).FillWidth().AlignItems(kit.Center).Gap(8).Children(func() {
				kit.Text(c, title).FontSize(14).Grow(1).SingleLine().IgnoreInput(true)
				appIcon(c, "chevron-down", 14)
			})
		})
	if selector.Trigger.Hovered() {
		selector.Trigger.Border(1, hexColor("4d98e7")).TextColor(hexColor("4d98e7"))
	}
	selector.Popup(func(panel kit.Element) {
		panel.Padding(4).Radius(6).Background(hexColor("201f29")).Border(1, hexColor("266294")).TextColor(hexColor("c2c2c2"))
		for _, option := range options {
			item := selector.Item(option.tag).Label(option.title).Padding(6, 10).Radius(4)
			if item.Highlighted() {
				item.Background(hexColor("266294")).TextColor(hexColor("eeeeee"))
			}
			item.Children(func() { kit.Text(c, option.title).FontSize(14).IgnoreInput(true) })
		}
	})
	// Select input is applied after its items exist in this build pass.
	if selector.Trigger.Changed() && a.onChangeLanguage != nil {
		a.onChangeLanguage(a.language)
	}
}

func settingsRowHeight(c *kit.Context) float32 {
	if _, height := c.Size(); height < 480 {
		return 32
	}
	return settingsControlHeight
}

func (a *App) settingsRow(c *kit.Context, label string, value func()) {
	kit.Row(c.Key(label)).FillWidth().AlignItems(kit.Start).Children(func() {
		kit.Row(c).Width(settingsLabelWidth).Height(settingsRowHeight(c)).Shrink(0).Padding(0, 30, 0, 0).AlignItems(kit.Center).Children(func() {
			kit.Text(c, locale.T(label)).FillWidth().TextAlign(kit.End).FontSize(13).TextColor(hexColor("969696")).Wrap()
		})
		kit.Box(c).Grow(1).MinWidth(0).Children(value)
	})
}

func settingsSwitch(c *kit.Context, label string, value *bool, disabled bool) kit.Element {
	color := hexColor("b6bbc6")
	left := float32(2)
	if *value {
		color, left = hexColor("2d8cf0"), 20
	}
	switcher := kit.SwitchBase(c, value).Label(locale.T(label)).Size(40, 22).Radius(11).Background(color).Disabled(disabled)
	if disabled {
		switcher.Opacity(.5)
	}
	switcher.Children(func() {
		kit.Box(c).Absolute().Left(left).Top(2).Size(18, 18).Radius(9).Background(hexColor("ffffff")).IgnoreInput(true).
			Transition(kit.ElementTransition{Duration: 140 * time.Millisecond})
	})
	return switcher
}

func settingsButton(c *kit.Context, title string) kit.Element {
	title = locale.T(title)
	button := kit.ButtonBase(c).Label(title).Padding(6, 10).Height(settingsControlHeight).Center().Radius(6).Border(1, hexColor("eeeeee")).Background(kit.Transparent).TextColor(hexColor("c2c2c2"))
	if button.Hovered() {
		button.Border(1, hexColor("4d98e7")).TextColor(hexColor("4d98e7"))
	}
	button.Children(func() { kit.Text(c, title).FontSize(13).SingleLine().IgnoreInput(true) })
	return button
}

func (a *App) chooseDownloadDirectory() {
	if a.preferences == nil || a.choosingDirectory {
		return
	}
	state := a.preferences.State()
	if !state.Ready || state.Busy() {
		return
	}
	if a.chooseDirectory == nil {
		a.preferences.ReportError(errors.New(locale.T("目录选择器尚未就绪")))
		return
	}
	a.choosingDirectory = true
	a.chooseDirectory(state.Values.DownloadPath, func(path string, err error) {
		if a.closed {
			return
		}
		a.choosingDirectory = false
		if err != nil {
			a.preferences.ReportError(err)
		} else if path != "" {
			a.preferences.SetDownloadDirectory(path)
		}
	})
}

func (a *App) previewMode() domain.PreviewMode {
	if a.preferences != nil && a.preferences.State().Values.PreviewMode == domain.PreviewTile {
		return domain.PreviewTile
	}
	return domain.PreviewWaterfall
}

// A native picker also changes window focus. Do not start a refresh that would
// temporarily block saving the directory just returned by that picker.
func (a *App) RefreshSettings() {
	if a.preferences == nil || a.choosingDirectory {
		return
	}
	if !a.preferences.State().Ready {
		a.preferences.Load()
	} else {
		a.preferences.RefreshLogin()
	}
}
