// Package app owns application state without depending on MyGO.
package app

import (
	"context"
	"errors"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

type Options struct {
	Source ports.WallpaperSource
	// Details optionally overrides the richer metadata source. If omitted, New
	// uses Source when it implements ports.WallpaperDetails.
	Details ports.WallpaperDetails
	// Dispatch schedules state updates on the UI thread (Window.Update).
	Dispatch func(func())
	// Prefetch fetches page two after a successful first search page. Later
	// pages remain driven by scrolling or the explicit load-more action.
	Prefetch bool
}

type RecommendationGroup struct {
	Key, Title      string
	Wallpapers      []domain.Wallpaper
	Loading, Loaded bool
	Error           string
	CurrentPage     int
	HasMore         bool
}

type SearchState struct {
	Query                        domain.SearchQuery
	Wallpapers                   []domain.Wallpaper
	Loading, Loaded              bool
	Error                        string
	CurrentPage, LastPage, Total int
	HasMore                      bool
	Generation                   uint64
}

// Controller is confined to the UI thread, including its getters and lifecycle
// methods. Workers capture immutable requests and publish only through Dispatch.
type Controller struct {
	current                  domain.Page
	source                   ports.WallpaperSource
	dispatch                 func(func())
	ctx                      context.Context
	cancel                   context.CancelFunc
	closed, started          bool
	prefetch                 bool
	recommendations          []RecommendationGroup
	recommendationGeneration uint64
	recommendationContext    context.Context
	cancelRecommendations    context.CancelFunc
	search                   SearchState
	searchGeneration         uint64
	searchRevision           uint64
	cancelSearch             context.CancelFunc
	details                  ports.WallpaperDetails
	detail                   DetailState
	detailGeneration         uint64
	cancelDetail             context.CancelFunc
	detailList               detailList
}

func New(options ...Options) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		current: domain.Pages()[0], ctx: ctx, cancel: cancel,
		recommendations: []RecommendationGroup{
			{Key: "date_added", Title: "最近更新"},
			{Key: "hot", Title: "热榜"},
			{Key: "favorites", Title: "收藏榜"},
			{Key: "views", Title: "查看榜"},
		},
		search: SearchState{Query: domain.DefaultSearchQuery()},
	}
	if len(options) > 0 {
		c.source, c.dispatch = options[0].Source, options[0].Dispatch
		c.prefetch = options[0].Prefetch
		c.details = options[0].Details
		if c.details == nil {
			c.details, _ = c.source.(ports.WallpaperDetails)
		}
	}
	return c
}

func (c *Controller) CurrentPage() domain.Page { return c.current }

func (c *Controller) Recommendations() []RecommendationGroup {
	groups := append([]RecommendationGroup(nil), c.recommendations...)
	for i := range groups {
		groups[i].Wallpapers = cloneWallpapers(groups[i].Wallpapers)
	}
	return groups
}

func (c *Controller) SearchState() SearchState {
	state := c.search
	state.Generation = c.searchGeneration
	state.Query = cloneQuery(state.Query)
	state.Wallpapers = cloneWallpapers(state.Wallpapers)
	return state
}

// SearchRevision lets a view reuse its private snapshot between state updates,
// while SearchState keeps its defensive-copy contract for all consumers.
func (c *Controller) SearchRevision() uint64 { return c.searchRevision }

// Start performs the first recommendation load exactly once.
func (c *Controller) Start() {
	if c.closed || c.started {
		return
	}
	c.started = true
	c.refreshRecommendations()
}

func (c *Controller) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.CloseDetail()
	c.cancel()
	for i := range c.recommendations {
		c.recommendations[i].Loading = false
	}
	c.search.Loading = false
	c.searchRevision++
}

// Navigate preserves loaded data and filter state between pages.
func (c *Controller) Navigate(id domain.PageID) bool {
	if c.closed {
		return false
	}
	for _, page := range domain.Pages() {
		if page.ID == id {
			if c.detail.Open {
				c.CloseDetail()
			}
			c.current = page
			switch id {
			case domain.Recommend:
				c.Start()
			case domain.WallpaperPage:
				if !c.search.Loaded && !c.search.Loading {
					c.Search(c.search.Query)
				}
			}
			return true
		}
	}
	return false
}

func (c *Controller) Refresh() {
	if c.closed {
		return
	}
	switch c.current.ID {
	case domain.Recommend:
		c.started = true
		c.refreshRecommendations()
	case domain.WallpaperPage:
		query := cloneQuery(c.search.Query)
		query.Seed = ""
		c.Search(query)
	}
}

func (c *Controller) refreshRecommendations() {
	if c.detail.Open && c.detailList.Page == domain.Recommend {
		c.CloseDetail()
	}
	if c.cancelRecommendations != nil {
		c.cancelRecommendations()
	}
	c.recommendationGeneration++
	generation := c.recommendationGeneration
	ctx, cancel := context.WithCancel(c.ctx)
	c.recommendationContext = ctx
	c.cancelRecommendations = cancel
	for i := range c.recommendations {
		group := &c.recommendations[i]
		group.Wallpapers, group.Error = nil, ""
		group.Loading, group.Loaded = true, false
		group.CurrentPage, group.HasMore = 0, false
		if message := c.unavailable(); message != "" {
			group.Loading, group.Loaded, group.Error = false, true, message
			continue
		}
		c.loadRecommendationPage(ctx, generation, i, 1)
	}
}

func (c *Controller) loadMoreRecommendation(key string) {
	for i := range c.recommendations {
		group := &c.recommendations[i]
		if group.Key != key || group.Loading || !group.Loaded || !group.HasMore {
			continue
		}
		group.Loading, group.Error = true, ""
		c.loadRecommendationPage(c.recommendationContext, c.recommendationGeneration, i, group.CurrentPage+1)
		return
	}
}

func (c *Controller) loadRecommendationPage(ctx context.Context, generation uint64, index, page int) {
	query := domain.DefaultSearchQuery()
	query.Sorting, query.Purity = c.recommendations[index].Key, "110"
	c.request(ctx, query, page, func(result domain.SearchResult, err error) {
		if c.closed || generation != c.recommendationGeneration || ctx.Err() != nil {
			return
		}
		group := &c.recommendations[index]
		group.Loading, group.Loaded = false, true
		if err != nil {
			group.Error = displayError(err)
		} else {
			group.Wallpapers = uniqueWallpapers(group.Wallpapers, result.Wallpapers)
			group.CurrentPage = page
			group.HasMore = page < result.LastPage && len(result.Wallpapers) > 0
		}
		c.syncDetailList()
	})
}

// Search starts a new immutable request and cancels the old search or page load.
func (c *Controller) Search(query domain.SearchQuery) {
	if c.closed {
		return
	}
	if c.detail.Open && c.detailList.Page == domain.WallpaperPage {
		c.CloseDetail()
	}
	if c.cancelSearch != nil {
		c.cancelSearch()
	}
	c.searchGeneration++
	c.searchRevision++
	query = query.Normalize()
	c.search = SearchState{Query: cloneQuery(query), Loading: true}
	if err := query.Validate(); err != nil {
		c.search.Loading, c.search.Loaded, c.search.Error = false, true, err.Error()
		return
	}
	if message := c.unavailable(); message != "" {
		c.search.Loading, c.search.Loaded, c.search.Error = false, true, message
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.cancelSearch = cancel
	c.loadSearchPage(ctx, c.searchGeneration, query, 1)
}

func (c *Controller) LoadMore() {
	if c.closed || c.search.Loading || !c.search.Loaded || !c.search.HasMore {
		return
	}
	if c.cancelSearch != nil {
		c.cancelSearch()
	}
	c.search.Loading, c.search.Error = true, ""
	c.searchRevision++
	ctx, cancel := context.WithCancel(c.ctx)
	c.cancelSearch = cancel
	c.loadSearchPage(ctx, c.searchGeneration, cloneQuery(c.search.Query), c.search.CurrentPage+1)
}

func (c *Controller) loadSearchPage(ctx context.Context, generation uint64, query domain.SearchQuery, page int) {
	c.request(ctx, query, page, func(result domain.SearchResult, err error) {
		if c.closed || generation != c.searchGeneration || ctx.Err() != nil {
			return
		}
		c.searchRevision++
		c.search.Loading, c.search.Loaded = false, true
		if err != nil {
			c.search.Error = displayError(err)
			c.syncDetailList()
			return
		}
		c.search.Wallpapers = uniqueWallpapers(c.search.Wallpapers, result.Wallpapers)
		c.search.CurrentPage = page
		c.search.LastPage = max(page, result.LastPage)
		c.search.Total = result.Total
		c.search.HasMore = page < result.LastPage && len(result.Wallpapers) > 0
		if query.Sorting == "random" && query.Seed == "" {
			c.search.Query.Seed = result.Seed
		}
		c.syncDetailList()
		if page == 1 && c.prefetch && c.search.HasMore {
			c.LoadMore()
		}
	})
}

func (c *Controller) unavailable() string {
	if c.source == nil {
		return "壁纸数据服务尚未配置"
	}
	if c.dispatch == nil {
		return "界面更新服务尚未就绪"
	}
	return ""
}

func (c *Controller) request(ctx context.Context, query domain.SearchQuery, page int, apply func(domain.SearchResult, error)) {
	source, dispatch := c.source, c.dispatch
	query = cloneQuery(query)
	go func() {
		result, err := source.Search(ctx, query, page)
		dispatch(func() { apply(result, err) })
	}()
}

func cloneQuery(query domain.SearchQuery) domain.SearchQuery {
	query.Resolutions = append([]string(nil), query.Resolutions...)
	query.Ratios = append([]string(nil), query.Ratios...)
	return query
}

func cloneWallpapers(wallpapers []domain.Wallpaper) []domain.Wallpaper {
	return append([]domain.Wallpaper(nil), wallpapers...)
}

func uniqueWallpapers(existing, incoming []domain.Wallpaper) []domain.Wallpaper {
	result := cloneWallpapers(existing)
	seen := make(map[string]bool, len(existing)+len(incoming))
	for _, wallpaper := range existing {
		seen[wallpaper.ID] = true
	}
	for _, wallpaper := range incoming {
		if wallpaper.ID == "" || seen[wallpaper.ID] {
			continue
		}
		seen[wallpaper.ID] = true
		result = append(result, wallpaper)
	}
	return result
}

func displayError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时，请重试"
	}
	if errors.Is(err, context.Canceled) {
		return "请求已取消"
	}
	return err.Error()
}
