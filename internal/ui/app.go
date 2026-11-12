// Package ui builds the native wallpaper interface.
package ui

import (
	"time"

	kit "github.com/egoist/mygo/ui"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type App struct {
	controller            *app.Controller
	images                ThumbnailSource
	filters               searchFilters
	advanced              bool
	grid                  kit.GridState
	results               kit.ScrollState
	recommendationScroll  kit.ScrollState
	lastImageScrollY      float32
	lastImageScrollX      map[string]float32
	groupScroll           map[string]*kit.ScrollState
	autoPage              int
	catalogViewport       kit.Rect
	catalogScrollY        float32
	catalogWindowWidth    float32
	catalogWindowHeight   float32
	appearance            string
	menuOpen              bool
	dialogOpen            bool
	dialogTitle           string
	onMinimize, onClose   func()
	fullImages            DetailImageSource
	dispatch              func(func())
	viewer                detailViewer
	closed                bool
	catalogSnapshot       app.SearchState
	catalogRevision       uint64
	catalogReady          bool
	waterfall             waterfallLayout
	actions               *app.Actions
	revealFile            func(string)
	library               app.LibraryState
	libraryRevision       uint64
	libraryCached         bool
	favoritesScroll       kit.ScrollState
	favoritesViewport     kit.Rect
	favoritesWindowWidth  float32
	favoritesWindowHeight float32
	favoritesScrollY      float32
	downloadsScroll       kit.ScrollState
	noticeSequence        uint64
	noticeUntil           time.Time
	preferences           *app.Preferences
	onOpenSettings        func()
	onCloseSettings       func()
	chooseDirectory       func(string, func(string, error))
	choosingDirectory     bool
}

type ThumbnailSource interface {
	Get(string) (kit.ImageSource, bool)
}

type Options struct {
	Images              ThumbnailSource
	Appearance          string
	OnMinimize, OnClose func()
	FullImages          DetailImageSource
	Dispatch            func(func())
	Actions             *app.Actions
	RevealFile          func(string)
	Preferences         *app.Preferences
	OnOpenSettings      func()
	OnCloseSettings     func()
	ChooseDirectory     func(string, func(string, error))
}

func New(controller *app.Controller, options ...Options) *App {
	loadLegacyAssets()
	a := &App{controller: controller, filters: defaultFilters(), groupScroll: map[string]*kit.ScrollState{}, lastImageScrollX: map[string]float32{}}
	if len(options) > 0 {
		a.images = options[0].Images
		a.appearance = options[0].Appearance
		a.onMinimize = options[0].OnMinimize
		a.onClose = options[0].OnClose
		a.fullImages = options[0].FullImages
		a.dispatch = options[0].Dispatch
		a.actions = options[0].Actions
		a.revealFile = options[0].RevealFile
		a.preferences = options[0].Preferences
		a.onOpenSettings = options[0].OnOpenSettings
		a.onCloseSettings = options[0].OnCloseSettings
		a.chooseDirectory = options[0].ChooseDirectory
	}
	for _, key := range []string{"date_added", "hot", "favorites", "views"} {
		a.groupScroll[key] = &kit.ScrollState{}
	}
	return a
}

func (a *App) View(c *kit.Context) {
	a.applyLegacyTheme(c)
	if a.actions != nil {
		revision := a.actions.LibraryRevision()
		if !a.libraryCached || a.libraryRevision != revision {
			a.library, a.libraryRevision, a.libraryCached = a.actions.Library(), revision, true
		}
	}
	p := legacyPalette(c)
	page := a.controller.CurrentPage()
	shell := kit.Column(c.Key("shell")).Fill().Background(p.Canvas).TextColor(p.Text)
	if a.controller.DetailState().Open {
		shell.Blur(12)
	}
	shell.Children(func() {
		a.titleBar(c)
		a.navigation(c)
		kit.Column(c.Key("page-" + string(page.ID))).Grow(1).MinWidth(0).MinHeight(0).FillWidth().Children(func() {
			switch page.ID {
			case domain.Recommend:
				a.recommendations(c)
			case domain.WallpaperPage:
				a.searchPage(c)
			case domain.Favorites:
				a.favoritesPage(c)
			case domain.Downloads:
				a.downloadsPage(c)
			case domain.Settings:
				kit.Box(c.Key("settings-page")).Fill().Center().Children(func() {
					kit.Box(c).Size(450, 340).Children(func() { a.SettingsView(c) })
				})
			default:
				kit.Column(c.Key("pending")).Grow(1).FillWidth().Center().Gap(12).Children(func() {
					kit.Image(c, legacyBitmaps["empty"]).Size(270, 180).Fit(kit.Contain)
					kit.Text(c, "功能开发中 ~").FontSize(20).TextColor(p.Muted)
				})
			}
		})
	})
	a.dialog(c)
	a.detailView(c)
	if !a.controller.DetailState().Open {
		a.actionNotice(c)
	}
}
