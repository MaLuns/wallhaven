package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type detailReply struct {
	result domain.WallpaperDetail
	err    error
}
type detailRequest struct {
	ctx   context.Context
	id    string
	reply chan detailReply
}
type delayedDetails struct {
	requests chan detailRequest
	stop     chan struct{}
}

func (s *delayedDetails) Detail(ctx context.Context, id string) (domain.WallpaperDetail, error) {
	r := detailRequest{ctx: ctx, id: id, reply: make(chan detailReply, 1)}
	select {
	case s.requests <- r:
	case <-s.stop:
		return domain.WallpaperDetail{}, context.Canceled
	}
	// Deliberately allow replies after cancellation to test the generation guard.
	select {
	case reply := <-r.reply:
		return reply.result, reply.err
	case <-s.stop:
		return domain.WallpaperDetail{}, context.Canceled
	}
}

type detailFixture struct {
	controller *Controller
	source     *delayedDetails
	updates    chan func()
}

func newDetailFixture(t *testing.T) *detailFixture {
	t.Helper()
	f := &detailFixture{source: &delayedDetails{requests: make(chan detailRequest, 10), stop: make(chan struct{})}, updates: make(chan func(), 20)}
	f.controller = New(Options{Details: f.source, Dispatch: func(fn func()) { f.updates <- fn }})
	t.Cleanup(func() { f.controller.Close(); close(f.source.stop) })
	return f
}
func (f *detailFixture) request(t *testing.T) detailRequest {
	t.Helper()
	select {
	case r := <-f.source.requests:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("detail request not started")
		return detailRequest{}
	}
}
func (f *detailFixture) update(t *testing.T) func() {
	t.Helper()
	select {
	case fn := <-f.updates:
		return fn
	case <-time.After(3 * time.Second):
		t.Fatal("detail response not dispatched")
		return nil
	}
}
func wallpaperDetail(id string) domain.WallpaperDetail {
	return domain.WallpaperDetail{Wallpaper: domain.Wallpaper{ID: id, Path: "https://images.example/" + id + ".png", Width: 3840, Height: 2160}, Tags: []domain.WallpaperTag{{ID: 1, Name: "landscape", Purity: "sfw"}}, Colors: []string{"#abcdef"}}
}

func TestDetailOpensImmediatelyMergesMetadataAndCopiesSlices(t *testing.T) {
	f := newDetailFixture(t)
	listing := domain.Wallpaper{ID: "abc123", Thumbnail: "thumb.jpg", Resolution: "3840x2160", FileType: "image/png", FileSize: 12345}
	f.controller.OpenDetail(listing)
	state := f.controller.DetailState()
	if !state.Open || !state.Loading || state.Wallpaper != listing || state.Detail.Wallpaper != listing {
		t.Fatalf("initial detail=%+v", state)
	}
	r := f.request(t)
	if r.id != "abc123" {
		t.Fatal(r.id)
	}
	metadata := wallpaperDetail("abc123")
	r.reply <- detailReply{result: metadata}
	apply := f.update(t)
	if !f.controller.DetailState().Loading {
		t.Fatal("worker updated state outside Dispatch")
	}
	apply()
	metadata.Tags[0].Name = "mutated"
	metadata.Colors[0] = "#000000"
	state = f.controller.DetailState()
	if state.Loading || state.Error != "" || state.Wallpaper.Path != "https://images.example/abc123.png" || state.Wallpaper.Thumbnail != "thumb.jpg" || state.Wallpaper.Resolution != "3840x2160" || state.Wallpaper.FileSize != 12345 || state.Detail.Wallpaper != state.Wallpaper || state.Detail.Tags[0].Name != "landscape" || state.Detail.Colors[0] != "#abcdef" {
		t.Fatalf("merged detail=%+v", state)
	}
	state.Detail.Tags[0].Name = "getter-mutated"
	state.Detail.Colors[0] = "#ffffff"
	if f.controller.DetailState().Detail.Tags[0].Name != "landscape" || f.controller.DetailState().Detail.Colors[0] != "#abcdef" {
		t.Fatal("detail getter exposed owned slices")
	}
}

func TestDetailRejectsOldSelectionQueuedResponseAndClosedUpdates(t *testing.T) {
	f := newDetailFixture(t)
	f.controller.OpenDetail(domain.Wallpaper{ID: "abc123"})
	old := f.request(t)
	old.reply <- detailReply{result: wallpaperDetail("abc123")}
	oldUpdate := f.update(t)
	f.controller.OpenDetail(domain.Wallpaper{ID: "def456"})
	fresh := f.request(t)
	if old.ctx.Err() != context.Canceled {
		t.Fatal("new selection did not cancel old request")
	}
	oldUpdate()
	if got := f.controller.DetailState(); got.Wallpaper.ID != "def456" || !got.Loading {
		t.Fatalf("old selection applied=%+v", got)
	}
	f.controller.CloseDetail()
	if fresh.ctx.Err() != context.Canceled || f.controller.DetailState().Open {
		t.Fatal("close did not cancel request")
	}
	fresh.reply <- detailReply{result: wallpaperDetail("def456")}
	f.update(t)()
	if f.controller.DetailState().Open || f.controller.DetailState().Wallpaper.ID != "" {
		t.Fatal("late result reopened detail")
	}
}

func TestDetailRetryPreservesListingAndNavigationCancels(t *testing.T) {
	f := newDetailFixture(t)
	listing := domain.Wallpaper{ID: "abc123", Path: "known-original.jpg"}
	f.controller.OpenDetail(listing)
	r := f.request(t)
	r.reply <- detailReply{err: errors.New("详情暂时不可用")}
	f.update(t)()
	if got := f.controller.DetailState(); got.Error != "详情暂时不可用" || got.Wallpaper.Path != "known-original.jpg" || !got.Open || got.Loading {
		t.Fatalf("error state=%+v", got)
	}
	f.controller.RetryDetail()
	retry := f.request(t)
	if retry.id != listing.ID || r.ctx.Err() != context.Canceled {
		t.Fatal("retry did not release previous request")
	}
	f.controller.Navigate(domain.Favorites)
	if retry.ctx.Err() != context.Canceled || f.controller.DetailState().Open {
		t.Fatal("navigation did not cancel detail")
	}
	retry.reply <- detailReply{result: wallpaperDetail(listing.ID)}
	f.update(t)()
	if f.controller.DetailState().Open || f.controller.CurrentPage().ID != domain.Favorites {
		t.Fatal("late detail changed navigation")
	}
}

func TestDetailControllerCloseAndMismatchedResponse(t *testing.T) {
	f := newDetailFixture(t)
	f.controller.OpenDetail(domain.Wallpaper{ID: "abc123"})
	r := f.request(t)
	r.reply <- detailReply{result: wallpaperDetail("def456")}
	f.update(t)()
	if got := f.controller.DetailState(); got.Error == "" || got.Wallpaper.ID != "abc123" {
		t.Fatalf("mismatched result accepted=%+v", got)
	}
	f.controller.RetryDetail()
	r = f.request(t)
	r.reply <- detailReply{result: wallpaperDetail("abc123")}
	apply := f.update(t)
	f.controller.Close()
	apply()
	if r.ctx.Err() != context.Canceled || f.controller.DetailState().Open {
		t.Fatal("controller close did not invalidate detail")
	}
	f.controller.OpenDetail(domain.Wallpaper{ID: "def456"})
	if f.controller.DetailState().Open {
		t.Fatal("closed controller opened detail")
	}
}

type detailAndSearchSource struct{ *delayedDetails }

func (*detailAndSearchSource) Search(context.Context, domain.SearchQuery, int) (domain.SearchResult, error) {
	return domain.SearchResult{}, nil
}
func TestDetailSourceInferenceAndUnavailableFallback(t *testing.T) {
	source := &detailAndSearchSource{&delayedDetails{requests: make(chan detailRequest, 1), stop: make(chan struct{})}}
	controller := New(Options{Source: source, Dispatch: func(func()) {}})
	if controller.details != source {
		t.Fatal("Source detail capability not inferred")
	}
	controller.Close()
	close(source.stop)
	controller = New()
	defer controller.Close()
	w := domain.Wallpaper{ID: "abc123", Path: "known-original.jpg"}
	controller.OpenDetail(w)
	if got := controller.DetailState(); !got.Open || got.Loading || got.Wallpaper != w || got.Error == "" {
		t.Fatalf("unavailable source fallback=%+v", got)
	}
	controller = New(Options{Details: &delayedDetails{}})
	defer controller.Close()
	controller.OpenDetail(w)
	if got := controller.DetailState(); got.Loading || got.Error != "界面更新服务尚未就绪" {
		t.Fatalf("no dispatcher=%+v", got)
	}
}
