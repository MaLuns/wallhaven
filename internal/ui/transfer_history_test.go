package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type historyUIReply struct {
	records []domain.Transfer
	err     error
}
type historyUIStore struct {
	loads   chan chan historyUIReply
	mu      sync.Mutex
	records []domain.Transfer
	saveErr error
}

func (s *historyUIStore) Load(ctx context.Context) ([]domain.Transfer, error) {
	reply := make(chan historyUIReply, 1)
	select {
	case s.loads <- reply:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case value := <-reply:
		return value.records, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *historyUIStore) Save(ctx context.Context, records []domain.Transfer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.records = append([]domain.Transfer(nil), records...)
	return nil
}

type resumableUICall struct {
	directory, key     string
	progress           chan [2]int64
	reply              chan actionFileReply
	stopped, allowStop chan struct{}
}
type resumableUIFiles struct {
	calls     chan resumableUICall
	shutdown  chan struct{}
	discarded chan string
}

func (s *resumableUIFiles) Download(ctx context.Context, w domain.Wallpaper, directory string, p func(int64, int64)) (string, error) {
	return s.DownloadResumable(ctx, w, directory, "", p)
}
func (s *resumableUIFiles) DownloadResumable(ctx context.Context, _ domain.Wallpaper, directory, key string, p func(int64, int64)) (string, error) {
	call := resumableUICall{directory: directory, key: key, progress: make(chan [2]int64, 5), reply: make(chan actionFileReply, 1), stopped: make(chan struct{}), allowStop: make(chan struct{})}
	select {
	case s.calls <- call:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	for {
		select {
		case value := <-call.progress:
			p(value[0], value[1])
		case value := <-call.reply:
			return value.path, value.err
		case <-ctx.Done():
			close(call.stopped)
			select {
			case <-call.allowStop:
			case <-s.shutdown:
			}
			return "", ctx.Err()
		}
	}
}
func (s *resumableUIFiles) Discard(ctx context.Context, key string) error {
	select {
	case s.discarded <- key:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type historyUIHarness struct {
	*catalogHarness
	actions   *app.Actions
	store     *historyUIStore
	files     *resumableUIFiles
	directory string
}

func newHistoryUIHarness(t *testing.T) *historyUIHarness {
	t.Helper()
	h := &historyUIHarness{catalogHarness: newCatalogHarness(t), store: &historyUIStore{loads: make(chan chan historyUIReply, 10)}, files: &resumableUIFiles{calls: make(chan resumableUICall, 10), shutdown: make(chan struct{}), discarded: make(chan string, 20)}, directory: t.TempDir()}
	h.actions = app.NewActions(app.ActionsOptions{Transfers: h.store, Files: h.files, Details: viewerDetailSource{}, DownloadDir: h.directory, WallpaperDir: t.TempDir(), Dispatch: func(fn func()) { h.updates <- fn }})
	t.Cleanup(h.actions.Close)
	t.Cleanup(func() { close(h.files.shutdown) })
	h.screen.actions = h.actions
	h.actions.Start()
	h.tester.Frame()
	return h
}
func (h *historyUIHarness) wait(t *testing.T, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !condition() {
		select {
		case update := <-h.updates:
			update()
			h.tester.Frame()
		case <-timer.C:
			t.Fatalf("history UI state timed out: %+v", h.actions.TransferHistory())
		}
	}
}
func (h *historyUIHarness) load(t *testing.T, records []domain.Transfer) {
	t.Helper()
	nextActionCall(t, h.store.loads) <- historyUIReply{records: records}
	h.wait(t, func() bool { return h.actions.TransferHistory().Ready && !h.actions.TransferHistory().Saving })
}
func (h *historyUIHarness) transfer(id string) domain.Transfer {
	value, _ := h.actions.Transfer(domain.DownloadTransfer, id)
	return value
}
func (h *historyUIHarness) openDownloads(t *testing.T) {
	t.Helper()
	click(t, h.tester, "功能菜单")
	click(t, h.tester, "前往下载")
}

func (h *historyUIHarness) attachPreferences(t *testing.T) *storage.Settings {
	t.Helper()
	defaults := domain.DefaultSettings(h.directory)
	store := storage.NewSettings(filepath.Join(t.TempDir(), "settings.json"), defaults)
	prefs := app.NewPreferences(app.PreferencesOptions{
		Store: store, Defaults: defaults, Directories: settingsDirectories{},
		Dispatch: func(fn func()) { h.updates <- fn },
		OnChange: func(values domain.Preferences) { h.actions.SetDownloadDirectory(values.DownloadPath) },
	})
	t.Cleanup(prefs.Close)
	h.screen.preferences = prefs
	prefs.Load()
	h.wait(t, func() bool { return prefs.State().Ready })
	return store
}

func TestTransferHistoryDirectoryEntryWorksWhenQueueEmptyAndPreservesActiveTarget(t *testing.T) {
	h := newHistoryUIHarness(t)
	h.load(t, nil)
	store := h.attachPreferences(t)
	h.tester.SetSize(800, 540)
	h.openDownloads(t)
	if !h.tester.HasText("任务队列已经空空如也") || !h.tester.HasText(h.directory) {
		t.Fatal("empty queue hides the current download directory")
	}
	if bounds, ok := h.tester.Find("修改下载目录"); !ok || bounds.X < 0 || bounds.X+bounds.W > 800 || bounds.Y+bounds.H > 540 {
		t.Fatalf("directory shortcut outside the minimum window: %+v", bounds)
	}
	var current string
	var complete func(string, error)
	h.screen.chooseDirectory = func(path string, reply func(string, error)) { current, complete = path, reply }
	click(t, h.tester, "修改下载目录")
	if current != h.directory || complete == nil {
		t.Fatal("download page did not invoke the native picker with the current directory")
	}
	if info, _ := h.tester.Access("修改下载目录"); !info.Disabled {
		t.Fatal("directory picker can be opened twice")
	}
	complete("", nil)
	h.tester.Frame()
	if h.screen.preferences.State().Values.DownloadPath != h.directory || h.screen.preferences.State().Saving {
		t.Fatal("canceling the picker changed the directory")
	}
	walls := h.controller.Recommendations()[0].Wallpapers
	h.actions.Download(walls[0])
	old := nextActionCall(t, h.files.calls)
	h.tester.Frame()
	click(t, h.tester, "修改下载目录")
	directory := t.TempDir()
	complete(directory, nil)
	h.wait(t, func() bool { return !h.screen.preferences.State().Saving })
	if !h.tester.HasText(directory) || h.screen.preferences.State().Values.DownloadPath != directory {
		t.Fatal("confirmed directory was not displayed")
	}
	saved, err := store.Load(context.Background())
	if err != nil || saved.DownloadPath != directory {
		t.Fatalf("directory selection was not persisted: %+v, %v", saved, err)
	}
	h.actions.Download(walls[1])
	newCall := nextActionCall(t, h.files.calls)
	if old.directory != h.directory || newCall.directory != directory {
		t.Fatalf("directory change affected the wrong tasks: old=%q new=%q", old.directory, newCall.directory)
	}
}

func TestTransferHistoryDirectoryFailuresAreVisibleAndRecoverable(t *testing.T) {
	h := newHistoryUIHarness(t)
	h.load(t, nil)
	h.attachPreferences(t)
	h.openDownloads(t)
	h.screen.chooseDirectory = func(_ string, complete func(string, error)) {
		complete("", errors.New("目录选择暂时失败"))
	}
	click(t, h.tester, "修改下载目录")
	if !h.tester.HasText("目录选择暂时失败") || h.screen.choosingDirectory {
		t.Fatal("picker failure is hidden or prevents another attempt")
	}
	h.screen.chooseDirectory = func(_ string, complete func(string, error)) {
		complete(filepath.Join(h.directory, "missing"), nil)
	}
	click(t, h.tester, "修改下载目录")
	h.wait(t, func() bool { return !h.screen.preferences.State().Saving })
	if h.screen.preferences.State().Values.DownloadPath != h.directory || h.screen.preferences.State().Error == "" || !h.tester.HasText(h.screen.preferences.State().Error) {
		t.Fatal("invalid directory replaced the current target or hid its error")
	}
}

func TestTransferHistoryLoadBlocksFileActionsAndCanRetry(t *testing.T) {
	h := newHistoryUIHarness(t)
	id := h.controller.Recommendations()[0].Wallpapers[0].ID
	for _, name := range []string{"下载", "设为壁纸"} {
		access, ok := h.tester.Access(name + " " + id)
		if !ok || !access.Disabled {
			t.Fatalf("file action enabled before history load: %+v", access)
		}
	}
	h.openDownloads(t)
	if _, ok := h.tester.Find("正在加载下载历史"); !ok {
		t.Fatal("history loading animation missing")
	}
	nextActionCall(t, h.store.loads) <- historyUIReply{err: errors.New("历史文件无法读取")}
	h.wait(t, func() bool { return !h.actions.TransferHistory().Loading })
	if !h.tester.HasText("历史文件无法读取") {
		t.Fatal("load failure hidden")
	}
	click(t, h.tester, "重试")
	h.load(t, nil)
	if !h.tester.HasText("任务队列已经空空如也") {
		t.Fatal("history retry did not load empty queue")
	}
}

func TestTransferHistoryPauseContinueStopGuardAndCompletion(t *testing.T) {
	h := newHistoryUIHarness(t)
	h.load(t, nil)
	w := h.controller.Recommendations()[0].Wallpapers[0]
	key := "download-" + w.ID
	click(t, h.tester, "下载 "+w.ID)
	call := nextActionCall(t, h.files.calls)
	call.progress <- [2]int64{64, 128}
	h.wait(t, func() bool { return h.transfer(w.ID).Received == 64 })
	h.openDownloads(t)
	if !h.tester.HasText("速度：") || !h.tester.HasText("已下载：64 B / 128 B") {
		t.Fatal("running task does not show speed and cumulative bytes")
	}
	click(t, h.tester, "暂停 "+key)
	select {
	case <-call.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("pause did not stop downloader")
	}
	if access, ok := h.tester.Access("停止中 " + key); !ok || !access.Disabled {
		t.Fatalf("stopping guard missing: %+v", access)
	}
	if _, ok := h.tester.Find("继续 " + key); ok {
		t.Fatal("resume enabled before stopped worker acknowledged")
	}
	close(call.allowStop)
	h.wait(t, func() bool {
		return h.transfer(w.ID).Paused && !h.transfer(w.ID).Stopping && !h.actions.TransferHistory().Saving
	})
	if !h.tester.HasText("已暂停") || !h.tester.HasText("已下载：64 B / 128 B") || h.tester.HasText("速度：") {
		t.Fatal("pause result does not preserve bytes or stop showing speed")
	}
	h.actions.SetDownloadDirectory(t.TempDir())
	click(t, h.tester, "继续 "+key)
	resumed := nextActionCall(t, h.files.calls)
	if resumed.key != call.key || resumed.directory != call.directory {
		t.Fatalf("resume lost checkpoint/directory: before=%+v after=%+v", call, resumed)
	}
	path := filepath.Join(h.directory, "keep.png")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	resumed.reply <- actionFileReply{path: path}
	h.wait(t, func() bool { return h.transfer(w.ID).Completed && !h.actions.TransferHistory().Saving })
	if !h.tester.HasText("完成于 ") {
		t.Fatal("completion time missing")
	}
	click(t, h.tester, "删除记录 "+key)
	h.wait(t, func() bool {
		_, exists := h.actions.Transfer(domain.DownloadTransfer, w.ID)
		return !exists && !h.actions.TransferHistory().Saving
	})
	if data, err := os.ReadFile(path); err != nil || string(data) != "original" {
		t.Fatal("deleting history removed its original")
	}
}

func TestTransferHistoryRestartIsPausedAndClearKeepsPendingRecords(t *testing.T) {
	h := newHistoryUIHarness(t)
	walls := h.controller.Recommendations()[0].Wallpapers
	path := filepath.Join(h.directory, "completed.png")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	h.load(t, []domain.Transfer{{Kind: domain.DownloadTransfer, Wallpaper: walls[0], Directory: h.directory, Running: true, Queued: true, Received: 64, Total: 128}, {Kind: domain.DownloadTransfer, Wallpaper: walls[1], Directory: h.directory, Completed: true, Path: path, CompletedAt: time.Now().UTC()}})
	if !h.transfer(walls[0].ID).Paused || len(h.files.calls) != 0 {
		t.Fatal("restored work started downloading automatically")
	}
	h.tester.SetSize(800, 540)
	h.openDownloads(t)
	click(t, h.tester, "清空已完成记录")
	h.wait(t, func() bool {
		_, exists := h.actions.Transfer(domain.DownloadTransfer, walls[1].ID)
		return !exists && !h.actions.TransferHistory().Saving
	})
	if !h.transfer(walls[0].ID).Paused {
		t.Fatal("clearing completed history removed pending task")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("clearing records removed original")
	}
}

func TestTransferHistorySaveFailureCanRetryWithoutReloading(t *testing.T) {
	h := newHistoryUIHarness(t)
	h.store.mu.Lock()
	h.store.saveErr = errors.New("历史文件暂时不可写")
	h.store.mu.Unlock()
	nextActionCall(t, h.store.loads) <- historyUIReply{}
	h.wait(t, func() bool {
		return h.actions.TransferHistory().Ready && !h.actions.TransferHistory().Saving && h.actions.TransferHistory().Error != ""
	})
	h.openDownloads(t)
	if !h.tester.HasText("历史文件暂时不可写") {
		t.Fatal("history save failure is hidden")
	}
	h.store.mu.Lock()
	h.store.saveErr = nil
	h.store.mu.Unlock()
	click(t, h.tester, "重试")
	h.wait(t, func() bool { return !h.actions.TransferHistory().Saving && h.actions.TransferHistory().Error == "" })
	if len(h.store.loads) != 0 {
		t.Fatal("save retry attempted to reload over current tasks")
	}
}

func TestTransferHistoryCancelWaitsForWorkerAndRetries(t *testing.T) {
	h := newHistoryUIHarness(t)
	h.load(t, nil)
	w := h.controller.Recommendations()[0].Wallpapers[0]
	key := "download-" + w.ID
	click(t, h.tester, "下载 "+w.ID)
	call := nextActionCall(t, h.files.calls)
	h.openDownloads(t)
	click(t, h.tester, "取消 "+key)
	select {
	case <-call.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not reach worker")
	}
	if access, ok := h.tester.Access("停止中 " + key); !ok || !access.Disabled {
		t.Fatalf("cancel did not guard stopping task: %+v", access)
	}
	close(call.allowStop)
	h.wait(t, func() bool { return h.transfer(w.ID).Canceled && !h.transfer(w.ID).Stopping })
	if discarded := nextActionCall(t, h.files.discarded); discarded != key {
		t.Fatalf("discarded %q want %q", discarded, key)
	}
	click(t, h.tester, "重试 "+key)
	nextActionCall(t, h.files.calls).reply <- actionFileReply{err: errors.New("连接暂时中断")}
	h.wait(t, func() bool { return h.transfer(w.ID).Error != "" })
	if !h.tester.HasText("连接暂时中断") {
		t.Fatal("retry error is hidden")
	}
}

func TestTransferHistoryPausedUnknownSizeDoesNotAnimateAsRunning(t *testing.T) {
	h := newHistoryUIHarness(t)
	w := h.controller.Recommendations()[0].Wallpapers[0]
	h.load(t, []domain.Transfer{{Kind: domain.DownloadTransfer, Wallpaper: w, Directory: h.directory, Paused: true, Received: 64}})
	h.openDownloads(t)
	h.tester.Move(0, 120)
	h.tester.Advance(500 * time.Millisecond)
	if _, exists := h.tester.Find("进度 download-" + w.ID); exists {
		t.Fatal("paused unknown-size task shows an indeterminate running animation")
	}
	if h.tester.HasText("速度：") || !h.tester.HasText("已暂停") {
		t.Fatal("paused task is displayed as active")
	}
}

func TestTransferHistoryNativePreview(t *testing.T) {
	dir := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if dir == "" {
		t.Skip("preview not requested")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		h := newHistoryUIHarness(t)
		walls := h.controller.Recommendations()[0].Wallpapers
		h.load(t, []domain.Transfer{{Kind: domain.DownloadTransfer, Wallpaper: walls[0], Directory: h.directory, Paused: true, Received: 524288, Total: 1048576}, {Kind: domain.DownloadTransfer, Wallpaper: walls[1], Directory: h.directory, Completed: true, Path: filepath.Join(h.directory, "wallpaper.png"), CompletedAt: time.Date(2026, 11, 14, 10, 30, 0, 0, time.UTC)}})
		h.attachPreferences(t)
		h.tester.SetDark(dark)
		h.tester.SetSize(800, 540)
		h.openDownloads(t)
		writeListPreview(t, h.tester, filepath.Join(dir, fmt.Sprintf("history-800-dark-%t.png", dark)))
		click(t, h.tester, "继续 download-"+walls[0].ID)
		call := nextActionCall(t, h.files.calls)
		call.progress <- [2]int64{524288, 1048576}
		h.wait(t, func() bool { return !h.transfer(walls[0].ID).Queued })
		writeListPreview(t, h.tester, filepath.Join(dir, fmt.Sprintf("history-running-800-dark-%t.png", dark)))
	}
}
