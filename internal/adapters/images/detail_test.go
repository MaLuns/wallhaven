package images

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func detailPicture(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.SetRGBA(0, 0, color.RGBA{100, 150, 200, 255})
	var data bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&data, img, nil)
	} else {
		err = png.Encode(&data, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestDetailLoaderReturnsOriginalDimensions(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			data := detailPicture(t, format, 1200, 700)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "image/*" || r.Header.Get("User-Agent") != "MyGO-Wallhaven/0.1.0" {
					t.Error("missing image request headers")
				}
				_, _ = w.Write(data)
			}))
			defer server.Close()
			bitmap, err := NewDetailLoader(DetailOptions{}).Load(context.Background(), server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			if width, height := bitmap.Size(); width != 1200 || height != 700 {
				t.Fatalf("original dimensions were resized: %dx%d", width, height)
			}
		})
	}
}

func TestDetailLoaderReportsTransferProgress(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(strconv.FormatBool(chunked), func(t *testing.T) {
			data := detailPicture(t, "png", 80, 50)
			resume := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !chunked {
					w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				}
				_, _ = w.Write(data[:len(data)/2])
				w.(http.Flusher).Flush()
				select {
				case <-resume:
					_, _ = w.Write(data[len(data)/2:])
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			events := make(chan [2]int64, 16)
			var reports [][2]int64
			finished := make(chan error, 1)
			go func() {
				_, err := NewDetailLoader(DetailOptions{}).Load(ctx, server.URL, func(received, total int64) {
					event := [2]int64{received, total}
					reports = append(reports, event)
					events <- event
				})
				finished <- err
			}()
			next := func() [2]int64 {
				t.Helper()
				select {
				case event := <-events:
					return event
				case <-ctx.Done():
					t.Fatal("missing transfer progress before the response finished")
					return [2]int64{}
				}
			}
			total := int64(len(data))
			if chunked {
				total = 0
			}
			if initial := next(); initial != [2]int64{0, total} {
				t.Fatalf("initial progress=%v", initial)
			}
			if partial := next(); partial[0] <= 0 || partial[0] >= int64(len(data)) || partial[1] != total {
				t.Fatalf("progress did not reflect the incomplete transfer: %v", partial)
			}
			close(resume)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if final := reports[len(reports)-1]; final != [2]int64{int64(len(data)), int64(len(data))} {
				t.Fatalf("completed progress=%v", final)
			}
			var previous int64
			for _, report := range reports {
				if report[0] < previous || report[0] > int64(len(data)) {
					t.Fatalf("progress did not follow the received byte count: %v", reports)
				}
				previous = report[0]
			}
		})
	}
}

func TestDetailLoaderBoundsResponseAndDecodedDimensions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    []byte
		options DetailOptions
		chunked bool
		want    string
	}{
		{"content-length", []byte("123456789"), DetailOptions{MaxBytes: 8}, false, "文件超过"},
		{"chunked", []byte("123456789"), DetailOptions{MaxBytes: 8}, true, "文件超过"},
		{"pixel-count", detailPicture(t, "png", 3, 2), DetailOptions{MaxPixels: 5}, false, "尺寸超过"},
		{"dimension", detailPicture(t, "png", detailDimension+1, 1), DetailOptions{}, false, "尺寸超过"},
		{"format", []byte("secret invalid content"), DetailOptions{}, false, "格式无效"},
		{"truncated-image", detailPicture(t, "png", 3, 2)[:33], DetailOptions{}, false, "解码失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.chunked {
					w.(http.Flusher).Flush()
				} else {
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
				}
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			got, err := NewDetailLoader(tc.options).Load(context.Background(), server.URL+"?token=secret", nil)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid image bitmap=%v, error=%v", got, err)
			}
		})
	}
}

func TestDetailLoaderRejectsURLAndHTTPFailures(t *testing.T) {
	loader := NewDetailLoader(DetailOptions{})
	for _, raw := range []string{"", "file:///tmp/private.png", "ftp://host/picture", "https://user:secret@host/image", "https://host/path#fragment", "://host"} {
		if image, err := loader.Load(context.Background(), raw, nil); image != nil || err == nil || err.Error() != "原图地址无效" {
			t.Fatalf("URL %q bitmap=%v err=%v", raw, image, err)
		}
	}
	for _, status := range []int{401, 403, 404, 410, 429, 500, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private server message: secret"))
			}))
			defer server.Close()
			if image, err := loader.Load(context.Background(), server.URL, nil); image != nil || err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("HTTP failure bitmap=%v err=%v", image, err)
			}
		})
	}
}

func TestDetailLoaderCancellationAndTimeout(t *testing.T) {
	started := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := NewDetailLoader(DetailOptions{}).Load(ctx, server.URL, nil)
		finished <- err
	}()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || err.Error() != "原图加载已取消" {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not cancel")
	}
	loader := NewDetailLoader(DetailOptions{HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	if _, err := loader.Load(context.Background(), server.URL, nil); !errors.Is(err, context.DeadlineExceeded) || err.Error() != "原图加载超时，请重试" {
		t.Fatalf("timeout error=%v", err)
	}
}

func TestDetailLoaderSerializesWorkAndCancelsQueuedLoads(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	loader := NewDetailLoader(DetailOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := loader.Load(ctx, server.URL, nil); finished <- err }()
	<-started
	queued, cancelQueued := context.WithCancel(context.Background())
	cancelQueued()
	if _, err := loader.Load(queued, server.URL, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("queued request started a second transfer")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDetailLoaderRejectsCredentialRedirectWithoutMutatingClient(t *testing.T) {
	client := &http.Client{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://user:secret@"+r.Host+"/image", http.StatusFound)
	}))
	defer server.Close()
	loader := NewDetailLoader(DetailOptions{HTTPClient: client})
	if _, err := loader.Load(context.Background(), server.URL, nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("credential redirect error=%v", err)
	}
	if client.CheckRedirect != nil {
		t.Fatal("loader modified supplied HTTP client")
	}
}
