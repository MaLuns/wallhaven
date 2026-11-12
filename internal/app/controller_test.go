package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type response struct {
	result domain.SearchResult
	err    error
}

type request struct {
	ctx   context.Context
	query domain.SearchQuery
	page  int
	reply chan response
}

// This source deliberately accepts replies even after cancellation: the
// controller must reject late results itself, not depend on cooperative I/O.
type delayedSource struct {
	requests chan request
	stop     chan struct{}
}

func (s *delayedSource) Search(ctx context.Context, query domain.SearchQuery, page int) (domain.SearchResult, error) {
	r := request{ctx: ctx, query: query, page: page, reply: make(chan response, 1)}
	select {
	case s.requests <- r:
	case <-s.stop:
		return domain.SearchResult{}, context.Canceled
	}
	select {
	case response := <-r.reply:
		return response.result, response.err
	case <-s.stop:
		return domain.SearchResult{}, context.Canceled
	}
}

type fixture struct {
	controller *Controller
	source     *delayedSource
	updates    chan func()
}

func newFixture(t *testing.T, prefetch ...bool) *fixture {
	t.Helper()
	f := &fixture{source: &delayedSource{requests: make(chan request, 64), stop: make(chan struct{})}, updates: make(chan func(), 128)}
	f.controller = New(Options{Source: f.source, Dispatch: func(fn func()) { f.updates <- fn }, Prefetch: len(prefetch) > 0 && prefetch[0]})
	t.Cleanup(func() { f.controller.Close(); close(f.source.stop) })
	return f
}

func (f *fixture) nextRequest(t *testing.T) request {
	t.Helper()
	select {
	case r := <-f.source.requests:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("request was not started")
		return request{}
	}
}

func (f *fixture) nextUpdate(t *testing.T) func() {
	t.Helper()
	select {
	case fn := <-f.updates:
		return fn
	case <-time.After(3 * time.Second):
		t.Fatal("result was not dispatched to UI")
		return nil
	}
}

func (f *fixture) finish(t *testing.T, r request, ids ...string) {
	t.Helper()
	r.reply <- response{result: pageResult(r.page, r.page, ids...)}
	f.nextUpdate(t)()
}

func pageResult(page, last int, ids ...string) domain.SearchResult {
	result := domain.SearchResult{CurrentPage: page, LastPage: last, Total: 50}
	for _, id := range ids {
		result.Wallpapers = append(result.Wallpapers, domain.Wallpaper{ID: id})
	}
	return result
}

func TestRecommendationsIndependentLoadingAndRefreshGeneration(t *testing.T) {
	f := newFixture(t)
	f.controller.Start()
	f.controller.Start()
	old := make(map[string]request)
	for range 4 {
		r := f.nextRequest(t)
		old[r.query.Sorting] = r
		if r.page != 1 || r.query.Categories != "111" || r.query.Purity != "110" || r.query.Order != "desc" {
			t.Fatalf("recommendation query: %+v, page %d", r.query, r.page)
		}
	}
	for _, key := range []string{"date_added", "hot", "favorites", "views"} {
		if _, ok := old[key]; !ok {
			t.Fatalf("missing recommendation %s", key)
		}
	}
	old["date_added"].reply <- response{result: pageResult(1, 2, "old")}
	oldUpdate := f.nextUpdate(t)
	f.controller.Refresh()
	oldUpdate()
	for _, r := range old {
		if r.ctx.Err() != context.Canceled {
			t.Fatal("refresh did not cancel old recommendation")
		}
	}
	for _, group := range f.controller.Recommendations() {
		if len(group.Wallpapers) != 0 || !group.Loading {
			t.Fatalf("stale result applied: %+v", group)
		}
	}
	for key, r := range old {
		if key != "date_added" {
			f.finish(t, r, "ignored")
		}
	}
	fresh := make(map[string]request)
	for range 4 {
		r := f.nextRequest(t)
		fresh[r.query.Sorting] = r
	}
	fresh["hot"].reply <- response{err: errors.New("热榜暂时不可用")}
	f.nextUpdate(t)()
	for key, r := range fresh {
		if key != "hot" {
			f.finish(t, r, key)
		}
	}
	for _, group := range f.controller.Recommendations() {
		if group.Loading || !group.Loaded {
			t.Fatalf("unfinished group: %+v", group)
		}
		if group.Key == "hot" {
			if group.Error != "热榜暂时不可用" || len(group.Wallpapers) != 0 {
				t.Fatalf("error group: %+v", group)
			}
		} else if len(group.Wallpapers) != 1 || group.Wallpapers[0].ID != group.Key {
			t.Fatalf("group: %+v", group)
		}
	}
}

func TestNewSearchRejectsQueuedOldResultsAndPreservesFilterValues(t *testing.T) {
	f := newFixture(t)
	query := domain.DefaultSearchQuery()
	query.Query = "old"
	f.controller.Search(query)
	old := f.nextRequest(t)
	old.reply <- response{result: pageResult(1, 2, "old")}
	oldUpdate := f.nextUpdate(t)
	query.Query = "new"
	query.Resolutions = []string{"1920x1080"}
	f.controller.Search(query)
	query.Resolutions[0] = "3840x2160"
	fresh := f.nextRequest(t)
	if old.ctx.Err() != context.Canceled {
		t.Fatal("new search did not cancel old")
	}
	oldUpdate()
	if state := f.controller.SearchState(); state.Query.Query != "new" || len(state.Wallpapers) != 0 || !state.Loading {
		t.Fatalf("stale result applied: %+v", state)
	}
	if fresh.query.Resolutions[0] != "1920x1080" {
		t.Fatal("request shared caller slice")
	}
	f.finish(t, fresh, "new")
	state := f.controller.SearchState()
	state.Wallpapers[0].ID = "changed"
	state.Query.Resolutions[0] = "changed"
	if current := f.controller.SearchState(); current.Wallpapers[0].ID != "new" || current.Query.Resolutions[0] != "1920x1080" {
		t.Fatalf("getter exposed owned state: %+v", current)
	}
	f.controller.Navigate(domain.Favorites)
	f.controller.Navigate(domain.WallpaperPage)
	if current := f.controller.SearchState(); current.Query.Query != "new" || len(current.Wallpapers) != 1 {
		t.Fatalf("navigation reset search: %+v", current)
	}
}

func TestPaginationRetriesSamePageDeduplicatesAndRetainsRandomSeed(t *testing.T) {
	f := newFixture(t)
	query := domain.DefaultSearchQuery()
	query.Sorting = "random"
	f.controller.Search(query)
	first := f.nextRequest(t)
	result := pageResult(1, 3, "a", "b", "a")
	result.Seed = "ABC123"
	first.reply <- response{result: result}
	f.nextUpdate(t)()
	f.controller.LoadMore()
	second := f.nextRequest(t)
	if second.page != 2 || second.query.Seed != "ABC123" {
		t.Fatalf("second page: %+v", second)
	}
	second.reply <- response{err: errors.New("temporary")}
	f.nextUpdate(t)()
	state := f.controller.SearchState()
	if state.CurrentPage != 1 || !state.HasMore || state.Error != "temporary" || len(state.Wallpapers) != 2 {
		t.Fatalf("failed page changed data: %+v", state)
	}
	f.controller.LoadMore()
	retry := f.nextRequest(t)
	if retry.page != 2 || retry.query.Seed != "ABC123" {
		t.Fatalf("retry query: %+v", retry)
	}
	if second.ctx.Err() != context.Canceled {
		t.Fatal("completed failed page context not released before retry")
	}
	retry.reply <- response{result: pageResult(2, 3, "b", "c")}
	f.nextUpdate(t)()
	f.controller.LoadMore()
	last := f.nextRequest(t)
	last.reply <- response{result: pageResult(3, 3, "d")}
	f.nextUpdate(t)()
	state = f.controller.SearchState()
	var ids []string
	for _, wallpaper := range state.Wallpapers {
		ids = append(ids, wallpaper.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c", "d"}) || state.HasMore || state.CurrentPage != 3 || state.Error != "" {
		t.Fatalf("last page: %+v", state)
	}
	f.controller.LoadMore()
	if f.controller.SearchState().Loading {
		t.Fatal("loaded beyond the last page")
	}
}

func TestNewFiltersCancelPendingPaginationAndCloseRejectsQueuedUpdates(t *testing.T) {
	f := newFixture(t)
	f.controller.Navigate(domain.WallpaperPage)
	first := f.nextRequest(t)
	first.reply <- response{result: pageResult(1, 2, "first")}
	f.nextUpdate(t)()
	f.controller.LoadMore()
	oldPage := f.nextRequest(t)
	query := domain.DefaultSearchQuery()
	query.Query = "different"
	f.controller.Search(query)
	fresh := f.nextRequest(t)
	if oldPage.ctx.Err() != context.Canceled {
		t.Fatal("new filter did not cancel pagination")
	}
	f.finish(t, oldPage, "old-next-page")
	if len(f.controller.SearchState().Wallpapers) != 0 {
		t.Fatal("old page appended to new search")
	}
	fresh.reply <- response{result: pageResult(1, 1, "new")}
	update := f.nextUpdate(t)
	f.controller.Close()
	update()
	if state := f.controller.SearchState(); state.Loading || len(state.Wallpapers) != 0 {
		t.Fatalf("closed controller accepted update: %+v", state)
	}
	if fresh.ctx.Err() != context.Canceled {
		t.Fatal("close did not cancel request")
	}
	f.controller.Search(query)
	if f.controller.SearchState().Loading {
		t.Fatal("closed controller started a request")
	}
}

func TestInvalidQueryDoesNotCallSourceAndUnavailableSetupIsExplicit(t *testing.T) {
	f := newFixture(t)
	query := domain.DefaultSearchQuery()
	query.AtLeast = "invalid"
	f.controller.Search(query)
	if state := f.controller.SearchState(); state.Error == "" || !state.Loaded || state.Loading {
		t.Fatalf("invalid query: %+v", state)
	}
	select {
	case <-f.source.requests:
		t.Fatal("invalid query called source")
	default:
	}
	withoutSource := New()
	defer withoutSource.Close()
	withoutSource.Search(domain.DefaultSearchQuery())
	if state := withoutSource.SearchState(); state.Error == "" || state.Loading {
		t.Fatalf("source unavailable state: %+v", state)
	}
	withoutDispatch := New(Options{Source: f.source})
	defer withoutDispatch.Close()
	withoutDispatch.Start()
	for _, group := range withoutDispatch.Recommendations() {
		if group.Error == "" || group.Loading {
			t.Fatalf("unsafe missing dispatch state: %+v", group)
		}
	}
}

func TestPrefetchLoadsOnlySecondPageAndFailureCanRetry(t *testing.T) {
	f := newFixture(t, true)
	query := domain.DefaultSearchQuery()
	query.Sorting = "random"
	f.controller.Search(query)
	first := f.nextRequest(t)
	result := pageResult(1, 3, "first")
	result.Seed = "ABC123"
	first.reply <- response{result: result}
	f.nextUpdate(t)()
	if state := f.controller.SearchState(); !state.Loading || state.CurrentPage != 1 || len(state.Wallpapers) != 1 {
		t.Fatalf("first result was not displayed during prefetch: %+v", state)
	}
	second := f.nextRequest(t)
	if second.page != 2 || second.query.Seed != "ABC123" {
		t.Fatalf("prefetch did not use page two and the first seed: %+v", second)
	}
	second.reply <- response{err: errors.New("page two failed")}
	f.nextUpdate(t)()
	if state := f.controller.SearchState(); state.Loading || state.CurrentPage != 1 || len(state.Wallpapers) != 1 || !state.HasMore || state.Error != "page two failed" {
		t.Fatalf("prefetch failure lost the first page or retry: %+v", state)
	}
	f.controller.LoadMore()
	retry := f.nextRequest(t)
	if retry.page != 2 || retry.query.Seed != "ABC123" {
		t.Fatalf("retry skipped failed prefetch page: %+v", retry)
	}
	retry.reply <- response{result: pageResult(2, 3, "second")}
	f.nextUpdate(t)()
	if state := f.controller.SearchState(); state.CurrentPage != 2 || state.Loading || !state.HasMore || len(state.Wallpapers) != 2 || state.Error != "" {
		t.Fatalf("page two chained to page three or failed to append: %+v", state)
	}
}

func TestPrefetchRejectsStaleFirstPageAndCanceledSecondPage(t *testing.T) {
	f := newFixture(t, true)
	query := domain.DefaultSearchQuery()
	query.Query = "old"
	f.controller.Search(query)
	old := f.nextRequest(t)
	old.reply <- response{result: pageResult(1, 3, "old")}
	staleFirstUpdate := f.nextUpdate(t)
	query.Query = "new"
	f.controller.Search(query)
	staleFirstUpdate()
	fresh := f.nextRequest(t)
	if fresh.query.Query != "new" || fresh.page != 1 {
		t.Fatalf("stale first-page callback started a prefetch: %+v", fresh)
	}
	fresh.reply <- response{result: pageResult(1, 3, "new")}
	f.nextUpdate(t)()
	prefetch := f.nextRequest(t)
	if prefetch.page != 2 || prefetch.query.Query != "new" {
		t.Fatalf("wrong prefetch request: %+v", prefetch)
	}
	query.Query = "newer"
	f.controller.Search(query)
	if prefetch.ctx.Err() != context.Canceled {
		t.Fatal("new query did not cancel prefetched second page")
	}
	prefetch.reply <- response{result: pageResult(2, 3, "stale second page")}
	f.nextUpdate(t)()
	if state := f.controller.SearchState(); state.Query.Query != "newer" || len(state.Wallpapers) != 0 || !state.Loading {
		t.Fatalf("canceled prefetch appended to the new search: %+v", state)
	}
	newer := f.nextRequest(t)
	newer.reply <- response{result: pageResult(1, 3, "newer")}
	closedUpdate := f.nextUpdate(t)
	f.controller.Close()
	closedUpdate()
	if state := f.controller.SearchState(); state.Loading || len(state.Wallpapers) != 0 {
		t.Fatalf("closed first-page callback started a prefetch: %+v", state)
	}
}

func TestPrefetchStopsAtFirstPageWithoutMoreResults(t *testing.T) {
	for _, wallpapers := range [][]string{{"only"}, nil} {
		f := newFixture(t, true)
		f.controller.Search(domain.DefaultSearchQuery())
		first := f.nextRequest(t)
		first.reply <- response{result: pageResult(1, 1, wallpapers...)}
		f.nextUpdate(t)()
		if state := f.controller.SearchState(); state.Loading || state.HasMore || !state.Loaded {
			t.Fatalf("prefetched past a complete or empty first page: %+v", state)
		}
	}
}
