package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	kit "github.com/egoist/mygo/ui"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/desktop"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/files"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/images"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/wallhaven"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	nativeui "github.com/MaLuns/wallhaven-electron/internal/ui"
)

func main() {
	var window *mygo.Window
	var preview *app.DesktopPreview
	activationPending := false
	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		if window == nil {
			activationPending = true
			return
		}
		if preview != nil && preview.NeedsCleanup() {
			activationPending = true
			preview.Exit()
			return
		}
		app.ActivateWindow(window)
	})
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.WhenReady(func() {
		source, err := wallhaven.New(wallhaven.Options{APIKey: os.Getenv("WALLHAVEN_API_KEY")})
		if err != nil {
			log.Fatal(err)
		}
		var settingsWindow *mygo.Window
		var screen *nativeui.App
		var previewHost *desktop.Preview
		closePending, quitPending := false, false
		sessionEnding := false
		redraw := func() {
			if window != nil {
				window.Invalidate()
			}
			if settingsWindow != nil {
				settingsWindow.Invalidate()
			}
			if previewHost != nil {
				previewHost.Invalidate()
			}
		}
		dispatch := func(fn func()) {
			window.Update(func() {
				fn()
				if screen != nil {
					screen.SyncDesktopPreview()
				}
				redraw()
			})
		}
		previewHost = desktop.NewPreview(desktop.PreviewOptions{
			MainWindow: func() *mygo.Window { return window }, SettingsWindow: func() *mygo.Window { return settingsWindow },
			Wallpaper:      func(c *kit.Context) { screen.DesktopWallpaperView(c) },
			Controls:       func(c *kit.Context) { screen.DesktopControlsView(c) },
			ControlsHeight: nativeui.DesktopPreviewControlsHeight,
			Previous:       func() { screen.MoveDesktopPreview(-1) },
			Next:           func() { screen.MoveDesktopPreview(1) },
			RequestExit:    func() { preview.Exit() },
		})
		preview = app.NewDesktopPreview(previewHost, func() {
			redraw()
			if preview.State().Phase == app.DesktopPreviewRestoreFailed {
				closePending, quitPending = false, false
				return
			}
			if preview.NeedsCleanup() {
				return
			}
			if quitPending {
				quitPending = false
				mygo.App.Quit()
				return
			}
			if closePending {
				closePending = false
				window.Close()
				return
			}
			if activationPending && window != nil {
				activationPending = false
				app.ActivateWindow(window)
			}
		})
		configDir, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		downloadsDir, err := mygo.App.Path(mygo.PathDownloads)
		if err != nil {
			log.Fatal(err)
		}
		dataDir := filepath.Join(configDir, "mygo-wallhaven")
		originals := files.NewOriginalCache(64 << 20)
		actions := app.NewActions(app.ActionsOptions{
			Favorites: storage.NewFavorites(filepath.Join(dataDir, "favorites.json")),
			Transfers: storage.NewTransfers(filepath.Join(dataDir, "transfers.json")),
			Files:     files.New(files.Options{StateDir: filepath.Join(dataDir, "download-state"), Originals: originals}), Desktop: desktop.New(), Details: source,
			WallpaperDir: filepath.Join(dataDir, "wallpapers"),
			Dispatch:     dispatch,
		})
		defaults := domain.DefaultSettings(filepath.Join(downloadsDir, "MyGO Wallhaven"))
		preferences := app.NewPreferences(app.PreferencesOptions{
			Store: storage.NewSettings(filepath.Join(dataDir, "settings.json"), defaults), Defaults: defaults,
			Login: desktop.LoginItems{}, Directories: desktop.SettingsDirectories{}, Dispatch: dispatch,
			OnChange: func(values domain.Preferences) { actions.SetDownloadDirectory(values.DownloadPath) },
		})
		controller := app.New(app.Options{Source: source, Prefetch: true, Dispatch: dispatch})
		thumbnails := images.New(images.Options{OnChange: func() { dispatch(func() {}) }})
		openSettings := func() {
			if settingsWindow != nil {
				settingsWindow.Show()
				settingsWindow.Focus()
				screen.RefreshSettings()
				return
			}
			settingsWindow = mygo.NewWindow(mygo.WindowOptions{
				Title: "设置", Width: 450, Height: 340, MinWidth: 450, MinHeight: 340, MaxWidth: 450, MaxHeight: 340,
				Frameless: true, DisableResize: true, DisableMaximize: true, DisableFullScreen: true,
				CornerRadius: nativeui.SettingsCornerRadius(), Transparent: nativeui.SettingsCornerRadius() > 0,
				Content: kit.View(screen.SettingsView),
			})
			settingsWindow.OnClosed(func() { settingsWindow = nil })
			settingsWindow.OnFocus(screen.RefreshSettings)
			screen.RefreshSettings()
		}
		screen = nativeui.New(controller, nativeui.Options{
			Actions: actions, RevealFile: mygo.Shell.ShowItemInFolder, Images: thumbnails,
			FullImages: images.NewDetailLoader(images.DetailOptions{Originals: originals}), Dispatch: dispatch, Appearance: "dark",
			OnMinimize: func() { window.Minimize() }, OnClose: func() { window.Close() },
			Preferences: preferences, OnOpenSettings: openSettings,
			DesktopPreview: preview,
			OnCloseSettings: func() {
				if settingsWindow != nil {
					settingsWindow.Close()
				}
			},
			ChooseDirectory: func(current string, complete func(string, error)) {
				parent := settingsWindow
				if parent == nil {
					parent = window
				}
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: "选择下载目录", DefaultPath: current, ButtonLabel: "选择目录", Directory: true, CreateDirectories: true})
					path := ""
					if len(paths) > 0 {
						path = paths[0]
					}
					dispatch(func() { complete(path, err) })
				}()
			},
		})
		window = mygo.NewWindow(mygo.WindowOptions{
			Title:     "MyGO Wallhaven",
			Width:     1300,
			Height:    800,
			MinWidth:  800,
			MinHeight: 540,
			Frameless: true,
			StateKey:  "main",
			Content:   kit.View(screen.View),
		})
		if activationPending {
			app.ActivateWindow(window)
			activationPending = false
		}
		servicesClosed := false
		closeServices := func() {
			if servicesClosed {
				return
			}
			servicesClosed = true
			preview.Close()
			screen.Close()
			preferences.Close()
			actions.Close()
			controller.Close()
			thumbnails.Close()
		}
		window.OnClose(func(event *mygo.CloseEvent) {
			if preview.NeedsCleanup() && !sessionEnding {
				event.PreventDefault()
				closePending = true
				preview.Exit()
			}
		})
		mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
			sessionEnding = event.SessionEnding
			if preview.NeedsCleanup() {
				if !event.SessionEnding {
					event.PreventDefault()
					quitPending = true
				}
				preview.Exit()
			}
		})
		window.OnClosed(func() { closeServices(); mygo.App.Quit() })
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) { closeServices() })
		actions.Start()
		preferences.Load()
		controller.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
