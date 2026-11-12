package app

import (
	"context"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

type ActionsOptions struct {
	Favorites                 ports.FavoritesStore
	Transfers                 ports.TransferStore
	Files                     ports.OriginalFiles
	Desktop                   ports.DesktopWallpaper
	Details                   ports.WallpaperDetails
	DownloadDir, WallpaperDir string
	Dispatch                  func(func())
}

type LibraryState struct {
	Loading, Saving, Ready bool
	Error                  string
	Wallpapers             []domain.Wallpaper
}

type NoticeState struct {
	Message  string
	Error    bool
	Sequence uint64
}

type transferKey struct {
	kind domain.TransferKind
	id   string
}

type transferJob struct {
	state      domain.Transfer
	generation uint64
	cancel     context.CancelCauseFunc
	stop       string
}

// Actions, like Controller, is confined to the UI thread. Background file and
// system operations publish immutable results only through Dispatch.
type Actions struct {
	options           ActionsOptions
	ctx               context.Context
	cancel            context.CancelFunc
	closed, started   bool
	library           LibraryState
	favorites         map[string]bool
	libraryGeneration uint64
	libraryRevision   uint64
	jobs              map[transferKey]*transferJob
	order             []transferKey
	slots             chan struct{}
	notice            NoticeState
	history           TransferHistoryState
	historyPending    bool
}

func NewActions(options ActionsOptions) *Actions {
	ctx, cancel := context.WithCancel(context.Background())
	return &Actions{options: options, ctx: ctx, cancel: cancel, favorites: map[string]bool{}, jobs: map[transferKey]*transferJob{}, slots: make(chan struct{}, 3), history: TransferHistoryState{Ready: options.Transfers == nil}}
}

func (a *Actions) Start() {
	if a.closed || a.started {
		return
	}
	a.started = true
	a.loadFavorites()
	a.loadTransfers()
}

func (a *Actions) Close() {
	if a.closed {
		return
	}
	a.closed = true
	a.cancel()
	a.libraryGeneration++
	a.library.Loading, a.library.Saving = false, false
	a.libraryRevision++
	a.closeTransfers()
}

func (a *Actions) Library() LibraryState {
	state := a.library
	state.Wallpapers = cloneWallpapers(state.Wallpapers)
	return state
}

func (a *Actions) IsFavorite(id string) bool { return a.favorites[id] }
func (a *Actions) LibraryRevision() uint64   { return a.libraryRevision }
func (a *Actions) Notice() NoticeState       { return a.notice }
func (a *Actions) ClearNotice()              { a.notice.Message = "" }

func (a *Actions) notify(message string, failed bool) {
	a.notice.Sequence++
	a.notice.Message, a.notice.Error = message, failed
}

func (a *Actions) RetryFavorites() {
	if !a.closed && !a.library.Loading && !a.library.Saving {
		a.loadFavorites()
	}
}

func (a *Actions) loadFavorites() {
	a.libraryRevision++
	if a.options.Favorites == nil || a.options.Dispatch == nil {
		a.library.Error = "收藏存储服务尚未就绪"
		return
	}
	a.libraryGeneration++
	generation := a.libraryGeneration
	a.library.Loading, a.library.Ready, a.library.Error = true, false, ""
	ctx, store, dispatch := a.ctx, a.options.Favorites, a.options.Dispatch
	go func() {
		wallpapers, err := store.Load(ctx)
		wallpapers = uniqueWallpapers(nil, wallpapers)
		dispatch(func() {
			if a.closed || ctx.Err() != nil || generation != a.libraryGeneration {
				return
			}
			a.libraryRevision++
			a.library.Loading = false
			if err != nil {
				a.library.Error = "读取收藏失败：" + displayError(err)
				return
			}
			a.library.Ready, a.library.Error = true, ""
			a.library.Wallpapers = wallpapers
			a.indexFavorites()
		})
	}()
}

func (a *Actions) indexFavorites() {
	a.favorites = make(map[string]bool, len(a.library.Wallpapers))
	for _, wallpaper := range a.library.Wallpapers {
		a.favorites[wallpaper.ID] = true
	}
}

func (a *Actions) ToggleFavorite(wallpaper domain.Wallpaper) {
	if a.closed {
		return
	}
	if !a.library.Ready || a.options.Favorites == nil || a.options.Dispatch == nil {
		a.notify("收藏尚未载入，请在我的页面重试", true)
		return
	}
	if a.library.Saving {
		return
	}
	if wallpaper.ID == "" {
		a.notify("壁纸编号无效", true)
		return
	}
	removing := a.IsFavorite(wallpaper.ID)
	var next []domain.Wallpaper
	if removing {
		for _, existing := range a.library.Wallpapers {
			if existing.ID != wallpaper.ID {
				next = append(next, existing)
			}
		}
	} else {
		next = append([]domain.Wallpaper{wallpaper}, a.library.Wallpapers...)
	}
	a.library.Saving, a.library.Error = true, ""
	a.libraryRevision++
	ctx, store, dispatch := a.ctx, a.options.Favorites, a.options.Dispatch
	generation := a.libraryGeneration
	// The store receives its own slice; neither worker nor adapter owns UI data.
	save := cloneWallpapers(next)
	go func() {
		err := store.Save(ctx, save)
		dispatch(func() {
			if a.closed || ctx.Err() != nil || generation != a.libraryGeneration {
				return
			}
			a.libraryRevision++
			a.library.Saving = false
			if err != nil {
				a.library.Error = "保存收藏失败：" + displayError(err)
				a.notify(a.library.Error, true)
				return
			}
			a.library.Wallpapers = next
			a.indexFavorites()
			message := "已收藏壁纸"
			if removing {
				message = "已取消收藏"
			}
			a.notify(message, false)
		})
	}()
}
