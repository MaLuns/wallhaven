package ui

import (
	"errors"
	"runtime"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

// The native window and its painted frame must use the same curve. Other
// platforms retain a square frame instead of adding macOS-style corners.
func SettingsCornerRadius() int {
	if runtime.GOOS == "darwin" {
		return 16
	}
	return 0
}

// SettingsView retains public/config.html's fixed 450×340 window, dark
// background, blue frame/divider, 100px labels and the three original options.
func (a *App) SettingsView(c *kit.Context) {
	theme := *kit.DarkTheme()
	theme.Background, theme.Text, theme.Accent = hexColor("201f29"), hexColor("eeeeee"), hexColor("2d8cf0")
	c.SetTheme(&theme)
	p := legacyPalette(c)
	theme.Inverse, theme.InverseText = p.Dialog, p.Text
	c.SetTheme(&theme)
	c.Root().Background(kit.Transparent)
	kit.Column(c.Key("settings")).Label("应用设置").Fill().Background(theme.Background).Border(4, hexColor("266294")).Radius(float32(SettingsCornerRadius())).TextColor(hexColor("c2c2c2")).Clip().Children(func() {
		kit.Row(c.Key("settings-header")).Label("设置标题栏").FillWidth().Height(42).Shrink(0).Padding(10, 20, 8).AlignItems(kit.Center).DragWindow().Children(func() {
			kit.Text(c, "设置").FontSize(18).Bold().TextColor(hexColor("eeeeee")).Grow(1)
			legacyIconButton(c, "close-settings", "close", "关闭设置", 18).Size(18, 18).TextColor(hexColor("eeeeee")).OnClick(func() {
				if a.onCloseSettings != nil {
					a.onCloseSettings()
				} else {
					a.controller.Navigate(domain.Favorites)
				}
			})
		})
		kit.Box(c).Absolute().Left(100).Top(66).Bottom(24).Width(2).Radius(2).Background(hexColor("266294")).IgnoreInput(true)
		kit.Scroll(c.Key("settings-body")).Label("设置选项").Grow(1).MinHeight(0).FillWidth().Children(func() {
			kit.Column(c).FillWidth().Padding(40, 20, 20).Gap(20).Children(func() {
				if a.preferences == nil {
					kit.Text(c, "设置服务尚未就绪").FontSize(14)
					return
				}
				state := a.preferences.State()
				disabled := !state.Ready || state.Busy() || a.choosingDirectory
				a.settingsRow(c, "预览模式", func() {
					kit.Row(c).Gap(5).AlignItems(kit.Center).Children(func() {
						kit.Text(c, "普通模式").FontSize(14)
						waterfall := state.Values.PreviewMode != domain.PreviewTile
						if settingsSwitch(c.Key("preview-mode"), "瀑布流模式", &waterfall, disabled).Changed() {
							mode := domain.PreviewTile
							if waterfall {
								mode = domain.PreviewWaterfall
							}
							a.preferences.SetPreviewMode(mode)
						}
						kit.Text(c, "瀑布流模式").FontSize(14)
					})
				})
				a.settingsRow(c, "开机启动", func() {
					enabled := state.Values.OpenAtLogin
					if settingsSwitch(c.Key("open-at-login"), "开机启动开关", &enabled, disabled).Changed() {
						a.preferences.SetOpenAtLogin(enabled)
					}
				})
				a.settingsRow(c, "下载目录", func() {
					kit.Column(c).FillWidth().Gap(20).Children(func() {
						kit.Text(c, "当前目录："+state.Values.DownloadPath).FontSize(14).LineHeight(1.4).MaxHeight(80).Clip().Tooltip(state.Values.DownloadPath)
						kit.Row(c).Gap(20).Children(func() {
							settingsButton(c.Key("choose-directory"), "修改目录").Disabled(disabled).OnClick(a.chooseDownloadDirectory)
							settingsButton(c.Key("open-directory"), "打开文件位置").Disabled(disabled).OnClick(a.preferences.OpenDownloadDirectory)
						})
					})
				})
				if state.Loading {
					kit.Text(c, "正在加载设置…").FontSize(12)
				} else if state.Saving {
					kit.Text(c, "正在保存设置…").FontSize(12)
				}
				if state.Error != "" {
					kit.Column(c).FillWidth().Gap(8).Children(func() {
						kit.Text(c, state.Error).FontSize(12).LineHeight(1.4).TextColor(hexColor("ff8f8f"))
						if !state.Ready && !state.Loading {
							settingsButton(c.Key("retry-settings"), "重试加载设置").OnClick(a.preferences.Load)
						}
					})
				}
			})
		})
	})
}

func (a *App) settingsRow(c *kit.Context, label string, value func()) {
	kit.Row(c.Key(label)).FillWidth().AlignItems(kit.Start).Children(func() {
		kit.Text(c, label).Width(100).Shrink(0).FontSize(14).TextColor(hexColor("969696")).Margin(2, 0, 0)
		kit.Box(c).Grow(1).MinWidth(0).Children(value)
	})
}

func settingsSwitch(c *kit.Context, label string, value *bool, disabled bool) kit.Element {
	color := hexColor("b6bbc6")
	left := float32(2)
	if *value {
		color, left = hexColor("2d8cf0"), 20
	}
	switcher := kit.SwitchBase(c, value).Label(label).Size(40, 22).Radius(11).Background(color).Disabled(disabled)
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
	button := kit.ButtonBase(c).Label(title).Padding(6, 16).Height(34).Radius(6).Border(1, hexColor("eeeeee")).Background(kit.Transparent).TextColor(hexColor("c2c2c2"))
	if button.Hovered() {
		button.Border(1, hexColor("4d98e7")).TextColor(hexColor("4d98e7"))
	}
	button.Children(func() { kit.Text(c, title).FontSize(14) })
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
		a.preferences.ReportError(errors.New("目录选择器尚未就绪"))
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
