package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func assertDetailSelection(t *testing.T, c *Controller, id string, previous, next bool) {
	t.Helper()
	state := c.DetailState()
	if !state.Open || state.Wallpaper.ID != id || state.Navigation.HasPrevious != previous || state.Navigation.HasNext != next {
		t.Fatalf("selection=%+v, want %s previous=%t next=%t", state, id, previous, next)
	}
}

func TestDetailUsesSearchOrderAndContinuesWithCommittedQuery(t *testing.T) {
	f := newFixture(t)
	query := domain.DefaultSearchQuery()
	query.Query, query.Categories, query.Sorting = "landscape", "100", "random"
	query.Resolutions = []string{"2560x1440"}
	f.controller.Search(query)
	first := f.nextRequest(t)
	result := pageResult(1, 2, "a", "b")
	result.Seed = "ABC123"
	first.reply <- response{result: result}
	f.nextUpdate(t)()
	committed := f.controller.SearchState().Query
	f.controller.OpenDetail(domain.Wallpaper{ID: "a"}, DetailList{Page: domain.WallpaperPage})
	assertDetailSelection(t, f.controller, "a", false, true)
	f.controller.PreviousDetail()
	assertDetailSelection(t, f.controller, "a", false, true)
	f.controller.NextDetail()
	assertDetailSelection(t, f.controller, "b", true, true)
	second := f.nextRequest(t) // Reaching b automatically requests the next page.
	if second.page != 2 || !reflect.DeepEqual(second.query, committed) {
		t.Fatalf("pagination changed committed query: %+v", second)
	}
	for range 3 {
		f.controller.NextDetail()
	}
	if navigation := f.controller.DetailState().Navigation; !navigation.Loading || !navigation.Waiting {
		t.Fatalf("next did not wait for page: %+v", navigation)
	}
	select {
	case request := <-f.source.requests:
		t.Fatalf("repeated next duplicated page request: %+v", request)
	default:
	}
	second.reply <- response{result: pageResult(2, 2, "b", "c", "d")}
	f.nextUpdate(t)()
	assertDetailSelection(t, f.controller, "c", true, true)
	if navigation := f.controller.DetailState().Navigation; navigation.Loading || navigation.Waiting || navigation.Error != "" {
		t.Fatalf("navigation did not finish: %+v", navigation)
	}
	f.controller.PreviousDetail()
	assertDetailSelection(t, f.controller, "b", true, true)
	f.controller.NextDetail()
	f.controller.NextDetail()
	assertDetailSelection(t, f.controller, "d", true, false)
	f.controller.NextDetail()
	assertDetailSelection(t, f.controller, "d", true, false)
	state := f.controller.SearchState()
	if len(state.Wallpapers) != 4 || state.CurrentPage != 2 || !reflect.DeepEqual(state.Query, committed) {
		t.Fatalf("viewer did not append to its source list: %+v", state)
	}
}

func TestDetailNextPageFailureRetryAndPreviousCancelsQueuedAdvance(t *testing.T) {
	f := newFixture(t)
	f.controller.Search(domain.DefaultSearchQuery())
	first := f.nextRequest(t)
	first.reply <- response{result: pageResult(1, 2, "a", "b")}
	f.nextUpdate(t)()
	f.controller.OpenDetail(domain.Wallpaper{ID: "b"}, DetailList{Page: domain.WallpaperPage})
	second := f.nextRequest(t)
	f.controller.NextDetail()
	second.reply <- response{err: errors.New("temporary page failure")}
	f.nextUpdate(t)()
	assertDetailSelection(t, f.controller, "b", true, true)
	if navigation := f.controller.DetailState().Navigation; navigation.Error != "temporary page failure" || navigation.Loading || navigation.Waiting {
		t.Fatalf("failed page state=%+v", navigation)
	}
	f.controller.NextDetail()
	retry := f.nextRequest(t)
	if retry.page != 2 {
		t.Fatalf("retry skipped failed page: %d", retry.page)
	}
	f.controller.PreviousDetail()
	retry.reply <- response{result: pageResult(2, 2, "c")}
	f.nextUpdate(t)()
	assertDetailSelection(t, f.controller, "a", false, true)
	if f.controller.DetailState().Navigation.Waiting {
		t.Fatal("previous retained queued next")
	}
}

func TestDetailSharesInFlightPrefetchAndSkipsDuplicateOnlyPage(t *testing.T) {
	f := newFixture(t, true)
	f.controller.Search(domain.DefaultSearchQuery())
	first := f.nextRequest(t)
	first.reply <- response{result: pageResult(1, 3, "a")}
	f.nextUpdate(t)()
	second := f.nextRequest(t)
	f.controller.OpenDetail(domain.Wallpaper{ID: "a"}, DetailList{Page: domain.WallpaperPage})
	f.controller.NextDetail()
	second.reply <- response{result: pageResult(2, 3, "a")}
	f.nextUpdate(t)()
	third := f.nextRequest(t)
	if third.page != 3 {
		t.Fatalf("viewer duplicated the in-flight page: %d", third.page)
	}
	third.reply <- response{result: pageResult(3, 3, "a", "b")}
	f.nextUpdate(t)()
	assertDetailSelection(t, f.controller, "b", true, false)
}

func TestClosingOrReplacingDetailDoesNotAdvanceOnLatePage(t *testing.T) {
	for _, action := range []string{"close", "new-detail", "search"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			f.controller.Search(domain.DefaultSearchQuery())
			first := f.nextRequest(t)
			first.reply <- response{result: pageResult(1, 2, "a")}
			f.nextUpdate(t)()
			f.controller.OpenDetail(domain.Wallpaper{ID: "a"}, DetailList{Page: domain.WallpaperPage})
			second := f.nextRequest(t)
			f.controller.NextDetail()
			switch action {
			case "close":
				f.controller.CloseDetail()
			case "new-detail":
				f.controller.OpenDetail(domain.Wallpaper{ID: "local"})
			case "search":
				query := domain.DefaultSearchQuery()
				query.Query = "different"
				f.controller.Search(query)
			}
			second.reply <- response{result: pageResult(2, 2, "b")}
			f.nextUpdate(t)()
			state := f.controller.DetailState()
			if action == "new-detail" {
				assertDetailSelection(t, f.controller, "local", false, false)
			} else if state.Open {
				t.Fatalf("late page reopened viewer: %+v", state)
			}
			if action == "search" && len(f.controller.SearchState().Wallpapers) != 0 {
				t.Fatal("late page appended to a replacement query")
			}
		})
	}
}

func TestDetailRecommendationStaysInOpeningGroupAndPaginates(t *testing.T) {
	f := newFixture(t)
	f.controller.Start()
	for range 4 {
		r := f.nextRequest(t)
		// Every group contains the same IDs; only its explicit key identifies
		// which ranking the user opened.
		r.reply <- response{result: pageResult(1, 2, "shared", "last")}
		f.nextUpdate(t)()
	}
	f.controller.OpenDetail(domain.Wallpaper{ID: "shared"}, DetailList{Page: domain.Recommend, Recommendation: "hot"})
	f.controller.NextDetail()
	second := f.nextRequest(t)
	if second.query.Sorting != "hot" || second.query.Purity != "110" || second.page != 2 {
		t.Fatalf("wrong recommendation continuation: %+v", second)
	}
	f.controller.NextDetail()
	second.reply <- response{result: pageResult(2, 2, "last", "hot-next")}
	f.nextUpdate(t)()
	assertDetailSelection(t, f.controller, "hot-next", true, false)
	for _, group := range f.controller.Recommendations() {
		want := 2
		if group.Key == "hot" {
			want = 3
		}
		if len(group.Wallpapers) != want {
			t.Fatalf("pagination changed wrong group: %+v", group)
		}
	}
}

func TestDetailLocalListSnapshotSurvivesRetryAndHasNoOnlineContinuation(t *testing.T) {
	f := newDetailFixture(t)
	wallpapers := []domain.Wallpaper{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	f.controller.OpenDetail(wallpapers[1], DetailList{Page: domain.Favorites, Wallpapers: wallpapers})
	wallpapers[0].ID = "changed"
	first := f.request(t)
	first.reply <- detailReply{err: errors.New("metadata failure")}
	f.update(t)()
	f.controller.RetryDetail()
	_ = f.request(t)
	f.controller.PreviousDetail()
	assertDetailSelection(t, f.controller, "a", false, true)
	_ = f.request(t)
	f.controller.NextDetail()
	_ = f.request(t)
	f.controller.NextDetail()
	_ = f.request(t)
	assertDetailSelection(t, f.controller, "c", true, false)
	f.controller.NextDetail()
	assertDetailSelection(t, f.controller, "c", true, false)
}
