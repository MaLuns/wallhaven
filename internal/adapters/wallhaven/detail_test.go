package wallhaven

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

const detailJSON = `{"data":{"id":"abc123","url":"https://wallhaven.cc/w/abc123","short_url":"https://whvn.cc/abc123","path":"https://w.wallhaven.cc/full/ab/wallhaven-abc123.png","dimension_x":3840,"dimension_y":2160,"resolution":"3840x2160","file_size":7654321,"file_type":"image/png","category":"general","purity":"sfw","views":198,"favorites":23,"source":"https://example.org/art","created_at":"2026-11-12 02:00:00","uploader":{"username":"artist"},"colors":["#123456","#ffffff"],"thumbs":{"small":"https://th.wallhaven.cc/small/ab/abc123.jpg","original":"https://th.wallhaven.cc/orig/ab/abc123.jpg"},"tags":[{"id":12,"name":"mountains","purity":"sfw"},{"id":31,"name":"landscape","purity":"sfw"}]}}`

func TestDetailUsesOfficialPathAndMapsMetadata(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/w/abc123" || r.URL.RawQuery != "" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-API-Key") != "private-key" || r.Header.Get("Accept") != "application/json" {
			t.Error("detail request headers missing")
		}
		fmt.Fprint(w, detailJSON)
	}, "private-key")
	got, err := client.Detail(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Wallpaper.ID != "abc123" || got.Wallpaper.Width != 3840 || got.Wallpaper.Height != 2160 || got.Wallpaper.FileSize != 7654321 || got.Wallpaper.FileType != "image/png" || !strings.HasSuffix(got.Wallpaper.Path, ".png") || got.Wallpaper.OriginalThumbnail != "https://th.wallhaven.cc/orig/ab/abc123.jpg" {
		t.Fatalf("wallpaper mapping=%+v", got.Wallpaper)
	}
	if got.Views != 198 || got.Favorites != 23 || got.Uploader != "artist" || got.CreatedAt != "2026-11-12 02:00:00" || got.Source != "https://example.org/art" || got.ShortURL != "https://whvn.cc/abc123" || len(got.Tags) != 2 || got.Tags[0].ID != 12 || got.Tags[0].Name != "mountains" || got.Tags[0].Purity != "sfw" || len(got.Colors) != 2 {
		t.Fatalf("detail mapping=%+v", got)
	}
}

func TestDetailRejectsInvalidIDWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }, "")
	for _, id := range []string{"", "../abc123", "abc123?apikey=secret", "a/b123", "abc12", "abc1234", "猫abc12", " abc123", "abc123%2f"} {
		if _, err := client.Detail(context.Background(), id); err == nil {
			t.Errorf("invalid ID %q accepted", id)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid ID reached network")
	}
}

func TestDetailErrorsAreBoundedAndRedacted(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        int
		body, message string
	}{
		{"not-found", 404, "private-key", "404"}, {"unauthorized", 401, "private-key", "401"}, {"forbidden", 403, "private-key", "403"}, {"rate-limit", 429, "private-key", "429"}, {"server", 503, "private-key", "503"},
		{"invalid-json", 200, "<html>private-key</html>", "无效详情"}, {"no-data", 200, `{}`, "无效详情"}, {"null", 200, `{"data":null}`, "无效详情"},
		{"mismatched-id", 200, strings.Replace(detailJSON, `"id":"abc123"`, `"id":"def456"`, 1), "无效详情"},
		{"invalid-dimensions", 200, strings.Replace(detailJSON, `"dimension_x":3840`, `"dimension_x":0`, 1), "无效详情"},
		{"too-large", 200, strings.Repeat("x", maxResponseBytes+1), "大小限制"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); fmt.Fprint(w, test.body) }, "private-key")
			_, err := client.Detail(context.Background(), "abc123")
			if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "private-key") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDetailCancellationTimeoutAndRedirect(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Detail(ctx, "abc123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
	client.timeout = 25 * time.Millisecond
	if _, err := client.Detail(context.Background(), "abc123"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
	var reached atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer other.Close()
	redirect := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }, "private-key")
	if _, err := redirect.Detail(context.Background(), "abc123"); err == nil || reached.Load() || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("redirect err=%v destination reached=%t", err, reached.Load())
	}
}

func TestLiveDetail(t *testing.T) {
	if os.Getenv("WALLHAVEN_LIVE_TEST") != "1" {
		t.Skip("set WALLHAVEN_LIVE_TEST=1 for live detail verification")
	}
	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	listing, err := client.Search(context.Background(), domain.DefaultSearchQuery(), 1)
	if err != nil || len(listing.Wallpapers) == 0 {
		t.Fatalf("live listing unavailable: %v", err)
	}
	id := listing.Wallpapers[0].ID
	got, err := client.Detail(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Wallpaper.ID != id || got.Wallpaper.Path == "" || got.Wallpaper.Width <= 0 || got.Wallpaper.Height <= 0 || got.CreatedAt == "" {
		t.Fatalf("incomplete live detail=%+v", got)
	}
	t.Logf("Live detail id=%s dimensions=%s tags=%d uploader=%s original image path received", got.Wallpaper.ID, got.Wallpaper.Resolution, len(got.Tags), got.Uploader)
}
