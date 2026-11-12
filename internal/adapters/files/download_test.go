package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func picture(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	bitmap := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			bitmap.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 150, A: 255})
		}
	}
	var body bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&body, bitmap, nil)
	} else {
		err = png.Encode(&body, bitmap)
	}
	if err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			t.Fatalf("download left a temporary file: %s", entry.Name())
		}
	}
}

func TestDownloadOriginalBytesFormatAndProgress(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			body := picture(t, format, 40, 30)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/original.wrong" || r.Header.Get("Accept") != "image/*" {
					t.Errorf("wrong original request: %s headers=%v", r.URL.Path, r.Header)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				_, _ = w.Write(body)
			}))
			defer server.Close()
			directory := filepath.Join(t.TempDir(), "chosen", "images")
			var progress [][2]int64
			path, err := New(Options{}).Download(context.Background(), domain.Wallpaper{ID: "abc123", Path: server.URL + "/original.wrong", FileType: "image/gif"}, directory, func(received, total int64) {
				progress = append(progress, [2]int64{received, total})
			})
			if err != nil {
				t.Fatal(err)
			}
			extension := ".png"
			if format == "jpeg" {
				extension = ".jpg"
			}
			if path != filepath.Join(directory, "wallhaven-abc123"+extension) {
				t.Fatalf("wrong decoded filename: %s", path)
			}
			saved, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(saved, body) {
				t.Fatalf("original bytes changed: %v", err)
			}
			if len(progress) < 2 || progress[0] != [2]int64{0, int64(len(body))} || progress[len(progress)-1] != [2]int64{int64(len(body)), int64(len(body))} {
				t.Fatalf("incomplete progress: %v", progress)
			}
			for i := 1; i < len(progress); i++ {
				if progress[i][0] < progress[i-1][0] {
					t.Fatal("progress moved backwards")
				}
			}
			assertNoTemporaryFiles(t, directory)
		})
	}
}

func TestDownloadRejectsErrorsAndLimitsWithoutPublishingFiles(t *testing.T) {
	valid := picture(t, "png", 3, 2)
	for _, test := range []struct {
		name    string
		body    []byte
		status  int
		chunked bool
		options Options
		want    string
	}{
		{"http", []byte("secret remote error"), 403, false, Options{}, "HTTP 403"},
		{"not-image", []byte("<html>secret</html>"), 200, false, Options{}, "格式无效"},
		{"truncated", valid[:len(valid)-15], 200, false, Options{}, "数据损坏"},
		{"declared-bytes", valid, 200, false, Options{MaxBytes: 10}, "大小限制"},
		{"streamed-bytes", valid, 200, true, Options{MaxBytes: 10}, "大小限制"},
		{"pixels", valid, 200, false, Options{MaxPixels: 5}, "尺寸超过"},
		{"dimension", picture(t, "png", 32769, 1), 200, false, Options{}, "尺寸超过"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "image/png")
				if !test.chunked {
					w.Header().Set("Content-Length", fmt.Sprint(len(test.body)))
				}
				w.WriteHeader(test.status)
				if test.chunked {
					w.(http.Flusher).Flush()
				}
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			directory := t.TempDir()
			path, err := New(test.options).Download(context.Background(), domain.Wallpaper{ID: "abc123", Path: server.URL + "?token=secret"}, directory, nil)
			if err == nil || path != "" || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("path=%s error=%v", path, err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed download published files: %v %v", entries, err)
			}
		})
	}
}

func TestDownloadCancellationAndTimeoutRemovePartialFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("partial image"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	wallpaper := domain.Wallpaper{ID: "abc123", Path: server.URL}
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		directory := t.TempDir()
		path, err := New(Options{}).Download(ctx, wallpaper, directory, func(received, _ int64) {
			if received > 0 {
				cancel()
			}
		})
		if path != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel path=%s error=%v", path, err)
		}
		assertNoTemporaryFiles(t, directory)
	})
	t.Run("timeout", func(t *testing.T) {
		directory := t.TempDir()
		path, err := New(Options{Timeout: 20 * time.Millisecond}).Download(context.Background(), wallpaper, directory, nil)
		if path != "" || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout path=%s error=%v", path, err)
		}
		assertNoTemporaryFiles(t, directory)
	})
}

func TestDownloadInvalidInputsNeverReachNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, test := range []struct{ id, path, directory string }{
		{"../bad", server.URL, t.TempDir()},
		{"abc123", "file:///private/data.png", t.TempDir()},
		{"abc123", "https://user:secret@example.test/image", t.TempDir()},
		{"abc123", server.URL + "#fragment", t.TempDir()},
		{"abc123", server.URL, ""},
	} {
		if _, err := New(Options{}).Download(context.Background(), domain.Wallpaper{ID: test.id, Path: test.path}, test.directory, nil); err == nil {
			t.Errorf("invalid input was accepted: %+v", test)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(Options{}).Download(ctx, domain.Wallpaper{ID: "abc123", Path: server.URL}, t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled download: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid inputs made %d requests", calls.Load())
	}
}

func TestConcurrentDownloadsNeverOverwriteExistingFiles(t *testing.T) {
	body := picture(t, "png", 8, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	directory := t.TempDir()
	unrelated := filepath.Join(directory, "wallhaven-abc123.png")
	if err := os.WriteFile(unrelated, []byte("unrelated user file"), 0600); err != nil {
		t.Fatal(err)
	}
	downloader := New(Options{})
	paths := make(chan string, 8)
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			path, err := downloader.Download(context.Background(), domain.Wallpaper{ID: "abc123", Path: server.URL}, directory, nil)
			paths <- path
			errors <- err
		})
	}
	workers.Wait()
	close(paths)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for path := range paths {
		if path == unrelated || seen[path] {
			t.Fatalf("colliding path: %s", path)
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, body) {
			t.Fatalf("published incomplete image: %s %v", path, err)
		}
	}
	if data, _ := os.ReadFile(unrelated); string(data) != "unrelated user file" {
		t.Fatal("existing unrelated file was overwritten")
	}
	assertNoTemporaryFiles(t, directory)
}

func TestDownloadRejectsHTTPSDowngrade(t *testing.T) {
	var calls atomic.Int64
	unsafe := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer unsafe.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, unsafe.URL, http.StatusFound) }))
	defer secure.Close()
	_, err := New(Options{HTTPClient: secure.Client()}).Download(context.Background(), domain.Wallpaper{ID: "abc123", Path: secure.URL}, t.TempDir(), nil)
	if err == nil || calls.Load() != 0 {
		t.Fatalf("HTTPS downgrade error=%v, reached=%d", err, calls.Load())
	}
}
