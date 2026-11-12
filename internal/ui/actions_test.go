package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type favoriteLoadReply struct {
	wallpapers []domain.Wallpaper
	err        error
}
type favoriteSaveCall struct {
	wallpapers []domain.Wallpaper
	reply      chan error
}
type actionFavoriteStore struct {
	loads chan chan favoriteLoadReply
	saves chan favoriteSaveCall
}

func (s *actionFavoriteStore) Load(ctx context.Context) ([]domain.Wallpaper, error) {
	reply := make(chan favoriteLoadReply, 1)
	select {
	case s.loads <- reply:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-reply:
		return result.wallpapers, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *actionFavoriteStore) Save(ctx context.Context, wallpapers []domain.Wallpaper) error {
	call := favoriteSaveCall{wallpapers: wallpapers, reply: make(chan error, 1)}
	select {
	case s.saves <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type actionFileReply struct {
	path string
	err  error
}
type actionFileCall struct {
	wallpaper domain.Wallpaper
	directory string
	progress  chan [2]int64
	reply     chan actionFileReply
}
type actionFiles struct{ calls chan actionFileCall }

func (s *actionFiles) Download(ctx context.Context, wallpaper domain.Wallpaper, directory string, progress func(int64, int64)) (string, error) {
	call := actionFileCall{wallpaper: wallpaper, directory: directory, progress: make(chan [2]int64, 10), reply: make(chan actionFileReply, 1)}
	select {
	case s.calls <- call:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	for {
		select {
		case update := <-call.progress:
			progress(update[0], update[1])
		case result := <-call.reply:
			return result.path, result.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

type desktopActionCall struct {
	path  string
	reply chan error
}
type actionDesktop struct{ calls chan desktopActionCall }

func (s *actionDesktop) Set(ctx context.Context, path string) error {
	call := desktopActionCall{path: path, reply: make(chan error, 1)}
	select {
	case s.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type actionsHarness struct {
	*catalogHarness
	actions                   *app.Actions
	store                     *actionFavoriteStore
	files                     *actionFiles
	desktop                   *actionDesktop
	downloadDir, wallpaperDir string
	revealed                  string
}

func newActionsHarness(t *testing.T) *actionsHarness {
	t.Helper()
	h := &actionsHarness{
		catalogHarness: newCatalogHarness(t),
		store:          &actionFavoriteStore{loads: make(chan chan favoriteLoadReply, 10), saves: make(chan favoriteSaveCall, 10)},
		files:          &actionFiles{calls: make(chan actionFileCall, 10)},
		desktop:        &actionDesktop{calls: make(chan desktopActionCall, 10)},
		downloadDir:    filepath.Join(t.TempDir(), "Downloads"),
		wallpaperDir:   filepath.Join(t.TempDir(), "wallpapers"),
	}
	h.actions = app.NewActions(app.ActionsOptions{Favorites: h.store, Files: h.files, Desktop: h.desktop, Details: viewerDetailSource{}, DownloadDir: h.downloadDir, WallpaperDir: h.wallpaperDir, Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.actions.Close)
	h.screen.actions = h.actions
	h.screen.revealFile = func(path string) { h.revealed = path }
	h.actions.Start()
	h.tester.Frame()
	return h
}

func nextActionCall[T any](t *testing.T, calls <-chan T) T {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("native action did not reach its service")
		var zero T
		return zero
	}
}

func (h *actionsHarness) wait(t *testing.T, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !ready() {
		select {
		case update := <-h.updates:
			update()
			h.tester.Frame()
		case <-timer.C:
			t.Fatal("action did not publish its result to the native UI")
		}
	}
}

func (h *actionsHarness) ready(t *testing.T) {
	t.Helper()
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{}
	h.wait(t, func() bool { return h.actions.Library().Ready })
}

func (h *actionsHarness) transfer(kind domain.TransferKind, id string) domain.Transfer {
	transfer, _ := h.actions.Transfer(kind, id)
	return transfer
}

func TestFavoriteActionsPersistBeforeUpdatingCardsAndMyPage(t *testing.T) {
	h := newActionsHarness(t)
	wallpaper := h.controller.Recommendations()[0].Wallpapers[0]
	label := "收藏 " + wallpaper.ID
	if access, ok := h.tester.Access(label); !ok || !access.Disabled {
		t.Fatal("favorite action was enabled before its library loaded")
	}
	h.ready(t)
	click(t, h.tester, label)
	save := nextActionCall(t, h.store.saves)
	if h.actions.IsFavorite(wallpaper.ID) || len(save.wallpapers) != 1 || save.wallpapers[0].ID != wallpaper.ID {
		t.Fatal("favorite changed before its persistence succeeded")
	}
	if access, _ := h.tester.Access(label); !access.Disabled {
		t.Fatal("saving favorite can be submitted repeatedly")
	}
	save.reply <- nil
	h.wait(t, func() bool { return h.actions.IsFavorite(wallpaper.ID) })
	click(t, h.tester, "前往收藏")
	if _, ok := h.tester.Find("查看壁纸 " + wallpaper.ID); !ok {
		t.Fatal("saved favorite is missing from the My page")
	}
	click(t, h.tester, "取消收藏 "+wallpaper.ID)
	nextActionCall(t, h.store.saves).reply <- errors.New("磁盘写入失败")
	h.wait(t, func() bool { return !h.actions.Library().Saving })
	if !h.actions.IsFavorite(wallpaper.ID) || !h.tester.HasText("保存收藏失败") {
		t.Fatal("failed persistence removed the saved favorite or hid its failure")
	}
	click(t, h.tester, "取消收藏 "+wallpaper.ID)
	nextActionCall(t, h.store.saves).reply <- nil
	h.wait(t, func() bool { return !h.actions.IsFavorite(wallpaper.ID) })
	if !h.tester.HasText("收藏夹空空如也") {
		t.Fatal("removing the last favorite did not restore the source empty state")
	}
}

func TestFavoritesReadFailureCanRetryFromMyPage(t *testing.T) {
	h := newActionsHarness(t)
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{err: errors.New("收藏文件暂时无法读取")}
	h.wait(t, func() bool { return !h.actions.Library().Loading })
	click(t, h.tester, "前往收藏")
	if !h.tester.HasText("读取收藏失败") {
		t.Fatal("favorite read failure is missing")
	}
	click(t, h.tester, "重试")
	if _, ok := h.tester.Find("正在加载收藏"); !ok {
		t.Fatal("retry did not show its loading animation")
	}
	wallpaper := h.controller.Recommendations()[0].Wallpapers[0]
	nextActionCall(t, h.store.loads) <- favoriteLoadReply{wallpapers: []domain.Wallpaper{wallpaper}}
	h.wait(t, func() bool { return h.actions.Library().Ready })
	if _, ok := h.tester.Find("取消收藏 " + wallpaper.ID); !ok {
		t.Fatal("retry did not restore the persisted favorite")
	}
}

func TestDownloadActionsProgressCancelRetryAndReveal(t *testing.T) {
	h := newActionsHarness(t)
	h.ready(t)
	wallpaper := h.controller.Recommendations()[0].Wallpapers[0]
	click(t, h.tester, "下载 "+wallpaper.ID)
	call := nextActionCall(t, h.files.calls)
	if call.directory != h.downloadDir || call.wallpaper.ID != wallpaper.ID || call.wallpaper.Path == "" {
		t.Fatalf("card download selected the wrong original/directory: %+v", call)
	}
	if access, _ := h.tester.Access("下载 " + wallpaper.ID); !access.Disabled {
		t.Fatal("running card download remained enabled")
	}
	click(t, h.tester, "功能菜单")
	click(t, h.tester, "前往下载")
	call.progress <- [2]int64{64, 128}
	h.wait(t, func() bool { return h.transfer(domain.DownloadTransfer, wallpaper.ID).Received == 64 })
	key := "download-" + wallpaper.ID
	progress, ok := h.tester.AccessByRole(kit.RoleProgress, "进度 "+key)
	if !ok || progress.Now != .5 || !h.tester.HasText("已下载 50%") {
		t.Fatalf("download progress is not visible/accessibile: %+v", progress)
	}
	click(t, h.tester, "取消 "+key)
	if !h.transfer(domain.DownloadTransfer, wallpaper.ID).Canceled || !h.tester.HasText("已取消") {
		t.Fatal("cancel did not update the task")
	}
	click(t, h.tester, "重试 "+key)
	nextActionCall(t, h.files.calls).reply <- actionFileReply{err: errors.New("网络中断")}
	h.wait(t, func() bool { return h.transfer(domain.DownloadTransfer, wallpaper.ID).Error != "" })
	if !h.tester.HasText("网络中断") {
		t.Fatal("download error is not visible")
	}
	click(t, h.tester, "重试 "+key)
	path := filepath.Join(h.downloadDir, wallpaper.ID+".png")
	nextActionCall(t, h.files.calls).reply <- actionFileReply{path: path}
	h.wait(t, func() bool { return h.transfer(domain.DownloadTransfer, wallpaper.ID).Completed })
	if !h.tester.HasText("已完成") || !h.tester.HasText("下载完成") {
		t.Fatal("finished download did not move to the completed section")
	}
	click(t, h.tester, "打开位置 "+key)
	if h.revealed != path {
		t.Fatalf("reveal selected %q, want %q", h.revealed, path)
	}
}

func TestDetailActionsShowClickableFailuresAndSetWallpaper(t *testing.T) {
	h := newActionsHarness(t)
	h.ready(t)
	wallpaper := h.controller.Recommendations()[0].Wallpapers[0]
	click(t, h.tester, "查看壁纸 "+wallpaper.ID)
	click(t, h.tester, "收藏原图")
	nextActionCall(t, h.store.saves).reply <- errors.New("收藏写入失败")
	h.wait(t, func() bool { return !h.actions.Library().Saving })
	h.tester.Advance(220 * time.Millisecond)
	notice, ok := h.tester.Find("操作提示")
	if !ok || !h.tester.HasText("收藏写入失败") {
		t.Fatal("detail action failure is hidden")
	}
	if pixel := h.tester.Image().RGBAAt(int(notice.X+8), int(notice.Y+15)); pixel.R < 200 || pixel.G < 200 || pixel.B < 200 {
		t.Fatalf("detail notice paints beneath the modal: %+v", pixel)
	}
	click(t, h.tester, "关闭操作提示")
	if h.actions.Notice().Message != "" {
		t.Fatal("detail notice close cannot receive pointer input")
	}
	click(t, h.tester, "下载原图")
	nextActionCall(t, h.files.calls).reply <- actionFileReply{err: errors.New("原图下载失败")}
	h.wait(t, func() bool { return h.transfer(domain.DownloadTransfer, wallpaper.ID).Error != "" })
	h.tester.Advance(220 * time.Millisecond)
	click(t, h.tester, "关闭操作提示")
	click(t, h.tester, "设为壁纸原图")
	call := nextActionCall(t, h.files.calls)
	if call.directory != h.wallpaperDir || strings.HasPrefix(call.directory, h.downloadDir) {
		t.Fatalf("wallpaper task used the user download directory: %q", call.directory)
	}
	path := filepath.Join(h.wallpaperDir, wallpaper.ID+".png")
	call.reply <- actionFileReply{path: path}
	desktop := nextActionCall(t, h.desktop.calls)
	if desktop.path != path {
		t.Fatalf("desktop service received %q, want %q", desktop.path, path)
	}
	desktop.reply <- nil
	h.wait(t, func() bool { return h.transfer(domain.WallpaperTransfer, wallpaper.ID).Completed })
	h.tester.Advance(220 * time.Millisecond)
	if !h.tester.HasText("已设置为桌面壁纸") {
		t.Fatal("successful wallpaper action did not display its result")
	}
	click(t, h.tester, "关闭操作提示")
}

func TestCardWallpaperActionCanRetryDesktopFailure(t *testing.T) {
	h := newActionsHarness(t)
	h.ready(t)
	wallpaper := h.controller.Recommendations()[0].Wallpapers[0]
	click(t, h.tester, "设为壁纸 "+wallpaper.ID)
	call := nextActionCall(t, h.files.calls)
	if call.directory != h.wallpaperDir {
		t.Fatalf("card wallpaper action chose %q", call.directory)
	}
	if access, _ := h.tester.Access("设为壁纸 " + wallpaper.ID); !access.Disabled {
		t.Fatal("running wallpaper action can be submitted again")
	}
	path := filepath.Join(h.wallpaperDir, wallpaper.ID+".png")
	call.reply <- actionFileReply{path: path}
	nextActionCall(t, h.desktop.calls).reply <- errors.New("系统拒绝设置壁纸")
	h.wait(t, func() bool { return h.transfer(domain.WallpaperTransfer, wallpaper.ID).Error != "" })
	click(t, h.tester, "功能菜单")
	click(t, h.tester, "前往下载")
	if !h.tester.HasText("系统拒绝设置壁纸") {
		t.Fatal("desktop error is missing from its task")
	}
	click(t, h.tester, "重试 wallpaper-"+wallpaper.ID)
	nextActionCall(t, h.files.calls).reply <- actionFileReply{path: path}
	nextActionCall(t, h.desktop.calls).reply <- nil
	h.wait(t, func() bool { return h.transfer(domain.WallpaperTransfer, wallpaper.ID).Completed })
	if !h.tester.HasText("已设置为桌面壁纸") {
		t.Fatal("wallpaper retry did not display success")
	}
}

func TestWallpaperActionsWithoutServicesStayDisabled(t *testing.T) {
	h := newCatalogHarness(t)
	wallpaper := h.controller.Recommendations()[0].Wallpapers[0]
	for _, name := range []string{"收藏", "下载", "设为壁纸"} {
		access, ok := h.tester.Access(name + " " + wallpaper.ID)
		if !ok || !access.Disabled || !strings.Contains(access.Description, "服务尚未就绪") {
			t.Fatalf("missing service does not explain its disabled action: %+v", access)
		}
	}
}

func TestActionsPreview(t *testing.T) {
	dir := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set WALLHAVEN_PREVIEW_DIR to export native action previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newActionsHarness(t)
	h.ready(t)
	h.screen.appearance = "dark"
	h.tester.Frame()
	wallpapers := h.controller.Recommendations()[0].Wallpapers
	click(t, h.tester, "收藏 "+wallpapers[0].ID)
	nextActionCall(t, h.store.saves).reply <- nil
	h.wait(t, func() bool { return h.actions.IsFavorite(wallpapers[0].ID) })
	h.actions.ClearNotice()
	click(t, h.tester, "前往收藏")
	writeListPreview(t, h.tester, filepath.Join(dir, "favorites-dark.png"))
	click(t, h.tester, "下载 "+wallpapers[0].ID)
	active := nextActionCall(t, h.files.calls)
	active.progress <- [2]int64{40, 100}
	h.wait(t, func() bool { return h.transfer(domain.DownloadTransfer, wallpapers[0].ID).Received == 40 })
	h.actions.Download(wallpapers[1])
	nextActionCall(t, h.files.calls).reply <- actionFileReply{path: filepath.Join(h.downloadDir, "completed.png")}
	h.wait(t, func() bool { return h.transfer(domain.DownloadTransfer, wallpapers[1].ID).Completed })
	h.actions.ClearNotice()
	click(t, h.tester, "功能菜单")
	click(t, h.tester, "前往下载")
	writeListPreview(t, h.tester, filepath.Join(dir, "downloads-dark.png"))
	h.tester.SetSize(800, 540)
	writeListPreview(t, h.tester, filepath.Join(dir, "downloads-minimum-dark.png"))
}
