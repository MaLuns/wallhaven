package wallhaven

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func newTestClient(t *testing.T, handler http.HandlerFunc, key string) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Options{BaseURL: server.URL + "/api/v1/", APIKey: key, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestSearchEncodesFiltersAndMapsResponse(t *testing.T) {
	query := domain.SearchQuery{
		Query: "+猫 +night -snow & stars", Categories: "010", Purity: "111",
		Resolutions: []string{"1920x1080", "3840x2160"}, Ratios: []string{"16x9", "portrait"},
		Color: "#FFCC33", Sorting: "toplist", Order: "asc", TopRange: "1y ",
	}
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		want := map[string][]string{
			"q": {query.Query}, "categories": {"010"}, "purity": {"111"},
			"resolutions": {"1920x1080,3840x2160"}, "ratios": {"16x9,portrait"},
			"colors": {"ffcc33"}, "sorting": {"toplist"}, "order": {"asc"}, "topRange": {"1y"}, "page": {"2"},
		}
		if !reflect.DeepEqual(map[string][]string(r.URL.Query()), want) {
			t.Errorf("query=%#v, want %#v", r.URL.Query(), want)
		}
		if r.Header.Get("X-API-Key") != "private-key" || strings.Contains(r.URL.String(), "private-key") {
			t.Error("credential must be passed only in the documented header")
		}
		fmt.Fprint(w, `{"data":[{"id":"abc123","url":"https://wallhaven.cc/w/abc123","path":"https://w.wallhaven.cc/full/ab/wallhaven-abc123.jpg","dimension_x":1920,"dimension_y":1080,"resolution":"1920x1080","file_size":12345,"file_type":"image/jpeg","category":"anime","purity":"sfw","thumbs":{"small":"https://th.wallhaven.cc/small/ab/abc123.jpg"}}],"meta":{"current_page":2,"last_page":5,"total":105,"seed":null}}`)
	}, "private-key")
	got, err := client.Search(context.Background(), query, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentPage != 2 || got.LastPage != 5 || got.Total != 105 || len(got.Wallpapers) != 1 {
		t.Fatalf("unexpected result %#v", got)
	}
	image := got.Wallpapers[0]
	if image.ID != "abc123" || image.Width != 1920 || image.Height != 1080 || image.FileSize != 12345 || image.Thumbnail != "https://th.wallhaven.cc/small/ab/abc123.jpg" || image.FileType != "image/jpeg" || image.Purity != "sfw" {
		t.Fatalf("response mapping: %#v", image)
	}
}

func TestSearchDefaultsMinimumSizeAndEmptyResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		want := map[string][]string{"categories": {"111"}, "purity": {"100"}, "sorting": {"date_added"}, "order": {"desc"}, "page": {"1"}, "atleast": {"1920x1080"}}
		if !reflect.DeepEqual(map[string][]string(r.URL.Query()), want) {
			t.Errorf("query=%#v", r.URL.Query())
		}
		if r.Header.Get("X-API-Key") != "" {
			t.Error("anonymous search must not invent an API key")
		}
		fmt.Fprint(w, `{"data":[],"meta":{"current_page":1,"last_page":1,"total":0}}`)
	}, "")
	got, err := client.Search(context.Background(), domain.SearchQuery{AtLeast: "1920x1080", TopRange: "1y", Seed: "abc123"}, 1)
	if err != nil || got.Total != 0 || len(got.Wallpapers) != 0 {
		t.Fatalf("empty result=%#v, err=%v", got, err)
	}
}

func TestRandomSeedAndThumbnailFallback(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("seed") != "abc123" || r.URL.Query().Get("topRange") != "" {
			t.Errorf("random query=%v", r.URL.Query())
		}
		fmt.Fprint(w, `{"data":[{"id":"abc123","thumbs":{"original":"original.jpg"}}],"meta":{"current_page":2,"last_page":20,"total":480,"seed":"abc123"}}`)
	}, "")
	got, err := client.Search(context.Background(), domain.SearchQuery{Sorting: "random", Seed: "abc123", TopRange: "1M"}, 2)
	if err != nil || got.Seed != "abc123" || got.Wallpapers[0].Thumbnail != "original.jpg" {
		t.Fatalf("random response=%#v, err=%v", got, err)
	}
}

func TestSearchRejectsInvalidResponsesWithoutLeakingRemoteBody(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		body    string
		message string
	}{
		{"unauthorized", 401, "credential private-key", "401"},
		{"forbidden", 403, "private-key", "403"},
		{"limited", 429, "private-key", "429"},
		{"server", 503, "private-key", "503"},
		{"invalid-json", 200, "<html>private-key</html>", "无效数据"},
		{"no-meta", 200, `{"data":[]}`, "无效数据"},
		{"wrong-page", 200, `{"data":[],"meta":{"current_page":2,"last_page":2,"total":0}}`, "分页信息"},
		{"wrong-wire-type", 200, `{"data":[],"meta":{"current_page":"1","last_page":1,"total":0}}`, "无效数据"},
		{"missing-id", 200, `{"data":[{}],"meta":{"current_page":1,"last_page":1,"total":1}}`, "图片信息"},
		{"too-large", 200, strings.Repeat("x", maxResponseBytes+1), "大小限制"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}, "private-key")
			_, err := client.Search(context.Background(), domain.DefaultSearchQuery(), 1)
			if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "private-key") {
				t.Fatalf("error=%v, want redacted message containing %q", err, test.message)
			}
		})
	}
}

func TestSearchCancellationAndTimeout(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Search(ctx, domain.DefaultSearchQuery(), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	client.timeout = 25 * time.Millisecond
	if _, err := client.Search(context.Background(), domain.DefaultSearchQuery(), 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
}

func TestCredentialCannotFollowCrossOriginRedirect(t *testing.T) {
	var reached atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer other.Close()
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }, "private-key")
	_, err := client.Search(context.Background(), domain.DefaultSearchQuery(), 1)
	if err == nil || reached.Load() || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("redirect err=%v, other origin reached=%v", err, reached.Load())
	}
}

func TestInvalidFiltersNeverReachNetwork(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }, "")
	for _, q := range []domain.SearchQuery{
		{Categories: "000"}, {Purity: "sfw"}, {Sorting: "bogus"}, {Order: "up"},
		{AtLeast: "0x1080"}, {AtLeast: "1920x1080", Resolutions: []string{"1920x1080"}},
		{Resolutions: []string{"fullhd"}}, {Ratios: []string{"wide"}}, {Color: "red"},
		{Sorting: "toplist", TopRange: "forever"}, {Sorting: "random", Seed: "wrong"},
	} {
		if _, err := client.Search(context.Background(), q, 1); err == nil {
			t.Fatalf("expected validation error for %#v", q)
		}
	}
	if _, err := client.Search(context.Background(), domain.DefaultSearchQuery(), 0); err == nil || calls.Load() != 0 {
		t.Fatalf("invalid request reached network: err=%v, calls=%d", err, calls.Load())
	}
}

func TestEmptyResponseWithZeroLastPage(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[],"meta":{"current_page":1,"last_page":0,"total":0}}`)
	}, "")
	got, err := client.Search(context.Background(), domain.DefaultSearchQuery(), 1)
	if err != nil || got.Total != 0 || len(got.Wallpapers) != 0 {
		t.Fatalf("zero-page empty response=%#v, err=%v", got, err)
	}
}

// Explicitly opt in when validating current upstream behavior; normal tests
// remain deterministic and do not consume the service's public request budget.
func TestLiveSearch(t *testing.T) {
	if os.Getenv("WALLHAVEN_LIVE_TEST") != "1" {
		t.Skip("set WALLHAVEN_LIVE_TEST=1 to verify the official API")
	}
	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sorting := range []string{"date_added", "hot", "favorites", "views"} {
		t.Run(sorting, func(t *testing.T) {
			query := domain.DefaultSearchQuery()
			query.Purity = "110"
			query.Sorting = sorting
			got, err := client.Search(context.Background(), query, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Wallpapers) == 0 || got.Wallpapers[0].Thumbnail == "" || got.Total == 0 {
				t.Fatalf("expected real recommendation data: %#v", got)
			}
			t.Logf("sorting=%s count=%d total=%d first=%s", sorting, len(got.Wallpapers), got.Total, got.Wallpapers[0].ID)
		})
	}
	t.Run("empty", func(t *testing.T) {
		query := domain.DefaultSearchQuery()
		query.Query = "mygozeroresulta47be90f1e3e5979ec8a7c0933cbb53e"
		got, err := client.Search(context.Background(), query, 1)
		if err != nil || got.Total != 0 {
			t.Fatalf("empty query result=%#v, err=%v", got, err)
		}
		t.Logf("empty metadata current=%d last=%d total=%d", got.CurrentPage, got.LastPage, got.Total)
	})
	t.Run("combined-filters", func(t *testing.T) {
		query := domain.DefaultSearchQuery()
		query.Query, query.Categories, query.Sorting, query.TopRange = "nature", "100", "toplist", "1y"
		query.AtLeast, query.Ratios, query.Color = "1920x1080", []string{"16x9"}, "0066cc"
		got, err := client.Search(context.Background(), query, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, image := range got.Wallpapers {
			if image.Width < 1920 || image.Height < 1080 || image.Category != "general" || image.Purity != "sfw" {
				t.Fatalf("upstream returned an image outside selected filters: %#v", image)
			}
		}
		t.Logf("combined filters count=%d total=%d", len(got.Wallpapers), got.Total)
	})
	t.Run("random-pagination", func(t *testing.T) {
		query := domain.DefaultSearchQuery()
		query.Query, query.Sorting = "nature", "random"
		first, err := client.Search(context.Background(), query, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Seed) != 6 || first.LastPage < 2 {
			t.Fatalf("expected random seed and multiple pages: %#v", first)
		}
		query.Seed = first.Seed
		second, err := client.Search(context.Background(), query, 2)
		if err != nil || second.CurrentPage != 2 || len(second.Wallpapers) == 0 {
			t.Fatalf("random pagination page=%d count=%d, err=%v", second.CurrentPage, len(second.Wallpapers), err)
		}
		if second.Seed != first.Seed {
			t.Log("upstream returned a new random seed despite the supplied seed; deterministic random order is not verified")
		}
		t.Logf("random pagination responses page1=%d page2=%d; supplied seed encoding is separately covered by the HTTP contract test", len(first.Wallpapers), len(second.Wallpapers))
	})
}
