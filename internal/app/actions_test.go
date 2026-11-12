package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type favoriteWrite struct {
	wallpapers []domain.Wallpaper
	reply      chan error
}
type actionFavorites struct {
	loaded  []domain.Wallpaper
	loadErr error
	writes  chan favoriteWrite
}

func (s *actionFavorites) Load(context.Context) ([]domain.Wallpaper, error) {
	return s.loaded, s.loadErr
}
func (s *actionFavorites) Save(ctx context.Context, wallpapers []domain.Wallpaper) error {
	call := favoriteWrite{wallpapers, make(chan error, 1)}
	s.writes <- call
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type actionFileResult struct {
	path string
	err  error
}
type actionFileCall struct {
	ctx       context.Context
	wallpaper domain.Wallpaper
	directory string
	progress  func(int64, int64)
	reply     chan actionFileResult
}
type actionFiles struct{ calls chan actionFileCall }

func (f *actionFiles) Download(ctx context.Context, wallpaper domain.Wallpaper, directory string, progress func(int64, int64)) (string, error) {
	call := actionFileCall{ctx, wallpaper, directory, progress, make(chan actionFileResult, 1)}
	f.calls <- call
	// Intentionally deliver late results too, to exercise stale-completion guards.
	result := <-call.reply
	return result.path, result.err
}

type actionDesktopCall struct {
	ctx   context.Context
	path  string
	reply chan error
}
type actionDesktop struct{ calls chan actionDesktopCall }

func (d *actionDesktop) Set(ctx context.Context, path string) error {
	call := actionDesktopCall{ctx, path, make(chan error, 1)}
	d.calls <- call
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type actionHarness struct {
	a       *Actions
	updates chan func()
	store   *actionFavorites
	files   *actionFiles
	desktop *actionDesktop
}

func newActionHarness(t *testing.T) *actionHarness {
	t.Helper()
	h := &actionHarness{updates: make(chan func(), 100), store: &actionFavorites{writes: make(chan favoriteWrite, 10)}, files: &actionFiles{calls: make(chan actionFileCall, 10)}, desktop: &actionDesktop{calls: make(chan actionDesktopCall, 10)}}
	h.a = NewActions(ActionsOptions{Favorites: h.store, Files: h.files, Desktop: h.desktop, DownloadDir: "downloads", WallpaperDir: "retained-wallpapers", Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.a.Close)
	return h
}
func (h *actionHarness) until(t *testing.T, done func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case fn := <-h.updates:
			fn()
		case <-timer.C:
			t.Fatal("missing action publication")
		}
	}
}
func receiveAction[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("missing worker call")
		var zero T
		return zero
	}
}
func actionWallpaper(id string) domain.Wallpaper {
	return domain.Wallpaper{ID: id, Path: "https://images.example/" + id + ".jpg", Thumbnail: "https://thumbs.example/" + id + ".jpg"}
}

func TestDownloadDirectoryChangePreservesExistingTransfersAndWallpaperStorage(t *testing.T) {
	h := newActionHarness(t)
	h.a.Download(actionWallpaper("old"))
	old := receiveAction(t, h.files.calls)
	h.a.SetDownloadDirectory("new-downloads")
	h.a.Download(actionWallpaper("new"))
	newDownload := receiveAction(t, h.files.calls)
	h.a.SetWallpaper(actionWallpaper("desktop"))
	wallpaper := receiveAction(t, h.files.calls)
	if old.directory != "downloads" || newDownload.directory != "new-downloads" || wallpaper.directory != "retained-wallpapers" {
		t.Fatalf("destinations: old=%q new=%q wallpaper=%q", old.directory, newDownload.directory, wallpaper.directory)
	}
	old.reply <- actionFileResult{path: "downloads/old.jpg"}
	newDownload.reply <- actionFileResult{path: "new-downloads/new.jpg"}
	wallpaper.reply <- actionFileResult{path: "retained-wallpapers/desktop.jpg"}
	receiveAction(t, h.desktop.calls).reply <- nil
	h.until(t, func() bool {
		for _, transfer := range h.a.Transfers() {
			if !transfer.Completed {
				return false
			}
		}
		return true
	})
}

func TestFavoritesCommitOnlyAfterSuccessfulPersistence(t *testing.T) {
	h := newActionHarness(t)
	h.a.Start()
	h.until(t, func() bool { return h.a.Library().Ready })
	w := actionWallpaper("abc123")
	h.a.ToggleFavorite(w)
	write := receiveAction(t, h.store.writes)
	if len(write.wallpapers) != 1 || write.wallpapers[0] != w || h.a.IsFavorite(w.ID) || !h.a.Library().Saving {
		t.Fatal("favorite changed before durable save")
	}
	write.reply <- errors.New("磁盘写入失败")
	h.until(t, func() bool { return !h.a.Library().Saving })
	if h.a.IsFavorite(w.ID) || !h.a.Notice().Error {
		t.Fatal("failed save claimed success")
	}
	h.a.ToggleFavorite(w)
	receiveAction(t, h.store.writes).reply <- nil
	h.until(t, func() bool { return h.a.IsFavorite(w.ID) })
	snapshot := h.a.Library()
	snapshot.Wallpapers[0].ID = "mutated"
	if h.a.Library().Wallpapers[0].ID != w.ID {
		t.Fatal("library getter leaked mutable slice")
	}
	h.a.ToggleFavorite(w)
	remove := receiveAction(t, h.store.writes)
	if len(remove.wallpapers) != 0 {
		t.Fatal("unfavorite did not persist removal")
	}
	remove.reply <- nil
	h.until(t, func() bool { return !h.a.IsFavorite(w.ID) })
}

func TestFavoriteLoadFailureCannotOverwriteStore(t *testing.T) {
	h := newActionHarness(t)
	h.store.loadErr = errors.New("损坏的收藏文件")
	h.a.Start()
	h.until(t, func() bool { return !h.a.Library().Loading })
	h.a.ToggleFavorite(actionWallpaper("abc123"))
	if h.a.Library().Ready || h.a.Library().Error == "" || len(h.store.writes) != 0 {
		t.Fatal("load failure allowed overwriting favorites")
	}
	// Modify only after the completed load publication has synchronized the worker.
	h.store.loadErr = nil
	h.store.loaded = []domain.Wallpaper{actionWallpaper("abc123"), actionWallpaper("abc123")}
	h.a.RetryFavorites()
	h.until(t, func() bool { return h.a.Library().Ready })
	if len(h.a.Library().Wallpapers) != 1 || !h.a.IsFavorite("abc123") {
		t.Fatal("retry did not reload and deduplicate favorites")
	}
}

func TestTransferProgressCompletionAndDesktopFailure(t *testing.T) {
	h := newActionHarness(t)
	w := actionWallpaper("abc123")
	h.a.Download(w)
	h.a.Download(w)
	call := receiveAction(t, h.files.calls)
	if call.directory != "downloads" || len(h.files.calls) != 0 {
		t.Fatal("download duplicated or used wrong directory")
	}
	call.progress(40, 100)
	h.until(t, func() bool { state, _ := h.a.Transfer(domain.DownloadTransfer, w.ID); return state.Received == 40 })
	call.reply <- actionFileResult{path: "downloads/wallhaven-abc123.jpg"}
	h.until(t, func() bool { state, _ := h.a.Transfer(domain.DownloadTransfer, w.ID); return state.Completed })
	state, _ := h.a.Transfer(domain.DownloadTransfer, w.ID)
	if state.Running || state.Path == "" || h.a.Notice().Error {
		t.Fatal("completion was lost after worker cleanup")
	}
	h.a.SetWallpaper(w)
	wallpaper := receiveAction(t, h.files.calls)
	if wallpaper.directory != "retained-wallpapers" {
		t.Fatal("wallpaper not retained outside temporary files")
	}
	wallpaper.reply <- actionFileResult{path: "retained-wallpapers/abc123.jpg"}
	set := receiveAction(t, h.desktop.calls)
	if set.path != "retained-wallpapers/abc123.jpg" {
		t.Fatal("desktop did not receive actual local original")
	}
	state, _ = h.a.Transfer(domain.WallpaperTransfer, w.ID)
	if state.Completed {
		t.Fatal("claimed wallpaper success before platform acknowledged")
	}
	set.reply <- errors.New("系统拒绝设置壁纸")
	h.until(t, func() bool { state, _ := h.a.Transfer(domain.WallpaperTransfer, w.ID); return !state.Running })
	state, _ = h.a.Transfer(domain.WallpaperTransfer, w.ID)
	if state.Completed || state.Error == "" || !h.a.Notice().Error {
		t.Fatal("platform failure claimed success")
	}
}

func TestCanceledTransferCannotOverwriteRetryOrSetDesktop(t *testing.T) {
	h := newActionHarness(t)
	w := actionWallpaper("abc123")
	h.a.SetWallpaper(w)
	old := receiveAction(t, h.files.calls)
	h.a.CancelTransfer(domain.WallpaperTransfer, w.ID)
	if old.ctx.Err() == nil {
		t.Fatal("cancel did not reach transfer context")
	}
	h.a.SetWallpaper(w)
	current := receiveAction(t, h.files.calls)
	old.progress(100, 100)
	old.reply <- actionFileResult{path: "obsolete.jpg"}
	current.reply <- actionFileResult{path: "current.jpg"}
	set := receiveAction(t, h.desktop.calls)
	if set.path != "current.jpg" {
		t.Fatal("canceled transfer changed desktop")
	}
	set.reply <- nil
	h.until(t, func() bool { state, _ := h.a.Transfer(domain.WallpaperTransfer, w.ID); return state.Completed })
	state, _ := h.a.Transfer(domain.WallpaperTransfer, w.ID)
	if state.Path != "current.jpg" || state.Canceled || len(h.desktop.calls) != 0 {
		t.Fatal("stale transfer overwrote retry")
	}
}

func TestCloseCancelsActiveAndRejectsLateCompletions(t *testing.T) {
	h := newActionHarness(t)
	w := actionWallpaper("abc123")
	h.a.Download(w)
	call := receiveAction(t, h.files.calls)
	h.a.Close()
	call.reply <- actionFileResult{path: "late.jpg"}
	if call.ctx.Err() == nil {
		t.Fatal("close did not cancel worker")
	}
	// Both a queued-start and completion publication may arrive after close.
	for range 2 {
		receiveAction(t, h.updates)()
	}
	state, _ := h.a.Transfer(domain.DownloadTransfer, w.ID)
	if state.Completed || state.Running || state.Path != "" {
		t.Fatal("closed action accepted a late result")
	}
}

func TestTransferQueueIsBoundedAndCanceledWorkDoesNotStart(t *testing.T) {
	h := newActionHarness(t)
	for _, id := range []string{"abc001", "abc002", "abc003"} {
		h.a.Download(actionWallpaper(id))
	}
	active := make([]actionFileCall, 3)
	for index := range active {
		active[index] = receiveAction(t, h.files.calls)
	}
	h.a.Download(actionWallpaper("abc004"))
	state, _ := h.a.Transfer(domain.DownloadTransfer, "abc004")
	if !state.Queued || !state.Running {
		t.Fatal("fourth transfer did not wait for a slot")
	}
	select {
	case <-h.files.calls:
		t.Fatal("transfer limit exceeded")
	case <-time.After(25 * time.Millisecond):
	}
	h.a.CancelTransfer(domain.DownloadTransfer, "abc004")
	for _, call := range active {
		call.reply <- actionFileResult{path: "completed.jpg"}
	}
	h.until(t, func() bool {
		completed := 0
		for _, state := range h.a.Transfers() {
			if state.Completed {
				completed++
			}
		}
		return completed == 3
	})
	select {
	case <-h.files.calls:
		t.Fatal("canceled queued task started after a slot became free")
	case <-time.After(25 * time.Millisecond):
	}
}
