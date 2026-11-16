package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	nativeupdater "github.com/egoist/mygo/plugins/updater/native"
	kit "github.com/egoist/mygo/ui"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/desktop"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/files"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/imagecache"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/images"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/wallhaven"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/locale"
	nativeui "github.com/MaLuns/wallhaven-electron/internal/ui"
)

func main() {
	mygo.Use(nativeupdater.New(updater.Options{}))
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
		offLocaleChanged := mygo.App.OnLocaleChanged(func(string) {
			if settingsWindow != nil {
				settingsWindow.SetTitle(locale.T("设置"))
			}
			redraw()
		})
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
		cacheReady := make(chan struct{})
		var cacheReadyOnce sync.Once
		finishCacheSetup := func() { cacheReadyOnce.Do(func() { close(cacheReady) }) }
		var thumbnailDisk, originalDisk *imagecache.Disk
		if cacheDir, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(cacheDir, "mygo-wallhaven", "images-v1")
			thumbnailDisk = imagecache.New(imagecache.Options{
				Directory: filepath.Join(cacheDir, "thumbnails"), MaxBytes: 256 << 20,
				MaxEntryBytes: 8 << 20, MaxEntries: 4096, Ready: cacheReady,
			})
			originalDisk = imagecache.New(imagecache.Options{
				Directory: filepath.Join(cacheDir, "originals"), MaxBytes: 1 << 30,
				MaxEntryBytes: 64 << 20, MaxEntries: 512, Ready: cacheReady,
			})
		}
		originals := files.NewOriginalCacheWithDisk(64<<20, originalDisk)
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
			Cache:    imagecache.NewManager(thumbnailDisk, originalDisk),
			OnLoaded: finishCacheSetup,
			OnChange: func(values domain.Preferences) { actions.SetDownloadDirectory(values.DownloadPath) },
		})
		controller := app.New(app.Options{Source: source, Prefetch: true, Dispatch: dispatch})
		thumbnails := images.New(images.Options{Disk: thumbnailDisk, OnChange: func() { dispatch(func() {}) }})
		openSettings := func() {
			if settingsWindow != nil {
				centerSettingsWindow(window, settingsWindow)
				settingsWindow.Show()
				settingsWindow.Focus()
				screen.RefreshSettings()
				return
			}
			created := mygo.NewWindow(settingsWindowOptions(window, screen))
			settingsWindow = created
			reportChildError := func(err error) {
				log.Printf("settings child window: %v", err)
				preferences.ReportError(err)
				created.Close()
			}
			cleanup, err := desktop.AttachSettingsChild(window, created, reportChildError)
			// Register after the binding so its closed callback first releases
			// listeners without touching an already destroyed native window.
			created.OnClosed(func() {
				cleanup()
				if settingsWindow == created {
					settingsWindow = nil
				}
			})
			if err != nil {
				reportChildError(err)
				return
			}
			created.OnReadyToShow(func() {
				centerSettingsWindow(window, created)
				created.Show()
			})
			created.OnFocus(screen.RefreshSettings)
			screen.RefreshSettings()
		}
		screen = nativeui.New(controller, nativeui.Options{
			Version: mygo.App.Version(), OnCheckForUpdates: updater.CheckForUpdates,
			Actions: actions, RevealFile: mygo.Shell.ShowItemInFolder, Images: thumbnails,
			FullImages: images.NewDetailLoader(images.DetailOptions{Originals: originals}), Dispatch: dispatch, Appearance: "dark",
			OnMinimize: func() { window.Minimize() }, OnClose: func() { window.Close() },
			Preferences: preferences, OnOpenSettings: openSettings,
			ChosenLanguage: mygo.App.ChosenLocale, OnChangeLanguage: mygo.App.SetLocale,
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
					paths, err := mygo.Dialog.Open(downloadDirectoryDialogOptions(parent, current))
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
		// Start restoring the desktop when an update finishes installing.
		updater.OnInstalled(func() {
			if !servicesClosed && preview.NeedsCleanup() {
				preview.Exit()
			}
		})
		closeServices := func() {
			if servicesClosed {
				return
			}
			servicesClosed = true
			finishCacheSetup()
			offLocaleChanged()
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

// Leave native button labels to the OS so the folder picker follows the user's
// default language, including its confirm, cancel and new-folder controls.
func downloadDirectoryDialogOptions(parent *mygo.Window, current string) mygo.OpenDialogOptions {
	return mygo.OpenDialogOptions{
		Parent: parent, Title: locale.T("选择下载目录"), DefaultPath: current,
		Directory: true, CreateDirectories: true,
	}
}

func settingsWindowOptions(parent *mygo.Window, screen *nativeui.App) mygo.WindowOptions {
	return mygo.WindowOptions{
		Parent: parent, Title: locale.T("设置"),
		Width: nativeui.SettingsWindowWidth, Height: nativeui.SettingsWindowHeight, MinWidth: 480, MinHeight: 420,
		Hidden: true, Frameless: true, DisableMaximize: true, DisableFullScreen: true,
		CornerRadius: nativeui.SettingsCornerRadius(), Transparent: nativeui.SettingsCornerRadius() > 0,
		Content: kit.View(screen.SettingsView),
	}
}

func centerSettingsWindow(parent, child *mygo.Window) {
	parentBounds, childBounds := parent.Bounds(), child.Bounds()
	child.SetPosition(
		parentBounds.X+(parentBounds.Width-childBounds.Width)/2,
		parentBounds.Y+(parentBounds.Height-childBounds.Height)/2,
	)
}
