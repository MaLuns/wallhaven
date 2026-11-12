package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/files"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/images"
	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type interruptedHistoryCleanup struct {
	entered chan struct{}
	reply   chan error
}

func (*interruptedHistoryCleanup) Download(context.Context, domain.Wallpaper, string, func(int64, int64)) (string, error) {
	panic("completed history must not download again")
}
func (*interruptedHistoryCleanup) DownloadResumable(context.Context, domain.Wallpaper, string, string, func(int64, int64)) (string, error) {
	panic("completed history must not resume")
}
func (f *interruptedHistoryCleanup) Discard(ctx context.Context, _ string) error {
	close(f.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-f.reply:
		return err
	}
}

func TestInterruptedOrFailedHistoryRemovalPreservesTerminalState(t *testing.T) {
	for _, test := range []struct {
		name      string
		completed bool
		taskError string
		fail      bool
	}{
		{name: "completed removal interrupted", completed: true},
		{name: "failed transfer removal interrupted", taskError: "连接中断"},
		{name: "completed removal fails", completed: true, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "wallhaven-abc123.png")
			if err := os.WriteFile(path, []byte("retained original"), 0600); err != nil {
				t.Fatal(err)
			}
			store := storage.NewTransfers(filepath.Join(directory, "transfers.json"))
			record := domain.Transfer{Kind: domain.DownloadTransfer, Wallpaper: domain.Wallpaper{ID: "abc123"}, Directory: directory, Path: path, Completed: test.completed, Error: test.taskError, CompletedAt: time.Now().UTC(), Paused: test.completed, Canceled: test.completed, Stopping: true}
			if err := store.Save(context.Background(), []domain.Transfer{record}); err != nil {
				t.Fatal(err)
			}
			updates := make(chan func(), 100)
			files := &interruptedHistoryCleanup{entered: make(chan struct{}), reply: make(chan error, 1)}
			actions := app.NewActions(app.ActionsOptions{Transfers: store, Files: files, Dispatch: func(fn func()) { updates <- fn }})
			t.Cleanup(actions.Close)
			actions.Start()
			pumpTransfers(t, updates, func() bool { return actions.TransferHistory().Ready && !actions.TransferHistory().Saving })
			restored, _ := actions.Transfer(domain.DownloadTransfer, "abc123")
			if restored.Completed != test.completed || restored.Paused || restored.Stopping || restored.Canceled || restored.Error != test.taskError {
				t.Fatalf("interrupted history cleanup changed terminal state: %+v", restored)
			}
			actions.RemoveTransfer(domain.DownloadTransfer, "abc123")
			select {
			case <-files.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("history cleanup did not start")
			}
			if test.fail {
				files.reply <- errors.New("无法清理断点")
				pumpTransfers(t, updates, func() bool {
					value, _ := actions.Transfer(domain.DownloadTransfer, "abc123")
					return !value.Stopping && actions.Notice().Error
				})
			}
			actions.Close()
			records, err := store.Load(context.Background())
			if err != nil || len(records) != 1 || records[0].Completed != test.completed || records[0].Paused || records[0].Stopping || records[0].Path != path || records[0].Error != test.taskError {
				t.Fatalf("record removal changed terminal state: %+v, %v", records, err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("history cleanup removed the original: %v", err)
			}
		})
	}
}

func originalFixture(t *testing.T) []byte {
	t.Helper()
	bitmap := image.NewRGBA(image.Rect(0, 0, 512, 256))
	seed := uint32(1)
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			seed = seed*1664525 + 1013904223
			bitmap.SetRGBA(x, y, color.RGBA{uint8(seed >> 24), uint8(seed >> 16), uint8(seed >> 8), 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func pumpTransfers(t *testing.T, updates <-chan func(), done func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !done() {
		select {
		case update := <-updates:
			update()
		case <-deadline.C:
			t.Fatal("transfer state did not settle")
		}
	}
}

func TestDownloadPersistsPauseAndResumesAfterRestart(t *testing.T) {
	data := originalFixture(t)
	requests := make(chan string, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get("Range")
		requests <- value
		w.Header().Set("ETag", `"original-v1"`)
		if value == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			for offset := 0; offset < len(data)/2; offset += 8192 {
				if r.Context().Err() != nil {
					return
				}
				end := min(offset+8192, len(data)/2)
				_, _ = w.Write(data[offset:end])
				w.(http.Flusher).Flush()
				time.Sleep(20 * time.Millisecond)
			}
			<-r.Context().Done()
			return
		}
		start, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(value, "bytes="), "-"))
		if err != nil || start <= 0 || start >= len(data) || r.Header.Get("If-Range") != `"original-v1"` {
			http.Error(w, "invalid resume", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start:])
	}))
	defer server.Close()
	root := t.TempDir()
	target := filepath.Join(root, "downloads")
	checkpoints := filepath.Join(root, "checkpoints")
	history := filepath.Join(root, "transfers.json")
	newActions := func() (*app.Actions, chan func()) {
		updates := make(chan func(), 100)
		a := app.NewActions(app.ActionsOptions{Transfers: storage.NewTransfers(history), Files: files.New(files.Options{StateDir: checkpoints}), DownloadDir: target, Dispatch: func(update func()) { updates <- update }})
		a.Start()
		pumpTransfers(t, updates, func() bool { return a.TransferHistory().Ready })
		return a, updates
	}
	first, updates := newActions()
	w := domain.Wallpaper{ID: "abc123", Path: server.URL + "/original", Width: 512, Height: 256}
	first.Download(w)
	pumpTransfers(t, updates, func() bool { s, _ := first.Transfer(domain.DownloadTransfer, w.ID); return s.Received > 0 })
	first.PauseTransfer(domain.DownloadTransfer, w.ID)
	pumpTransfers(t, updates, func() bool {
		s, _ := first.Transfer(domain.DownloadTransfer, w.ID)
		return s.Paused && !s.Stopping && !first.TransferHistory().Saving
	})
	first.Close()
	second, secondUpdates := newActions()
	defer second.Close()
	paused, ok := second.Transfer(domain.DownloadTransfer, w.ID)
	if !ok || !paused.Paused || paused.Running || paused.Directory != target || paused.CreatedAt.IsZero() {
		t.Fatalf("paused task was not restored: %+v", paused)
	}
	if len(requests) != 1 {
		t.Fatal("restart automatically started a paused download")
	}
	second.SetDownloadDirectory(filepath.Join(root, "different-downloads"))
	second.ResumeTransfer(domain.DownloadTransfer, w.ID)
	pumpTransfers(t, secondUpdates, func() bool {
		s, _ := second.Transfer(domain.DownloadTransfer, w.ID)
		return !s.Running && !s.Stopping && !second.TransferHistory().Saving
	})
	completed, _ := second.Transfer(domain.DownloadTransfer, w.ID)
	if !completed.Completed || completed.Error != "" || completed.CompletedAt.IsZero() || filepath.Dir(completed.Path) != target {
		t.Fatalf("resumed task failed or changed destination: %+v", completed)
	}
	saved, err := os.ReadFile(completed.Path)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("resumed original differs: %v", err)
	}
	if initial := <-requests; initial != "" {
		t.Fatalf("unexpected first request: %s", initial)
	}
	if resumed := <-requests; !strings.HasPrefix(resumed, "bytes=") {
		t.Fatalf("task restarted instead of resuming: %s", resumed)
	}
	second.RemoveTransfer(domain.DownloadTransfer, w.ID)
	pumpTransfers(t, secondUpdates, func() bool { return len(second.Transfers()) == 0 && !second.TransferHistory().Saving })
	if _, err := os.Stat(completed.Path); err != nil {
		t.Fatal("history removal deleted the original")
	}
	records, err := storage.NewTransfers(history).Load(context.Background())
	if err != nil || len(records) != 0 {
		t.Fatalf("history deletion was not persisted: %v %v", records, err)
	}
}

func TestDownloadedOriginalReusesPreviewBytesWithoutAnotherRequest(t *testing.T) {
	data := originalFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(data) }))
	defer server.Close()
	cache := files.NewOriginalCache(64 << 20)
	loader := images.NewDetailLoader(images.DetailOptions{Originals: cache})
	if _, err := loader.Load(context.Background(), server.URL, nil); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	downloader := files.New(files.Options{StateDir: filepath.Join(directory, "state"), Originals: cache})
	path, err := downloader.DownloadResumable(context.Background(), domain.Wallpaper{ID: "abc123", Path: server.URL}, filepath.Join(directory, "downloads"), "download-abc123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("preview and save made %d network requests", requests.Load())
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("preview reuse changed original bytes: %v", err)
	}
}
