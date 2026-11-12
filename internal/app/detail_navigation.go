package app

import "github.com/MaLuns/wallhaven-electron/internal/domain"

// DetailList identifies the list that opened the viewer. Online lists stay
// connected to their controller state; local lists use a private snapshot.
type DetailList struct {
	Page           domain.PageID
	Recommendation string
	Wallpapers     []domain.Wallpaper
}

type DetailNavigation struct {
	HasPrevious, HasNext bool
	Loading, Waiting     bool
	Error                string
}

type detailList struct {
	DetailList
	generation  uint64
	pendingNext bool
}

type detailListState struct {
	wallpapers       []domain.Wallpaper
	index            int
	loading, hasMore bool
	error            string
}

func (c *Controller) detailListState() detailListState {
	state := detailListState{index: -1}
	if !c.detail.Open {
		return state
	}
	switch c.detailList.Page {
	case domain.WallpaperPage:
		if c.detailList.generation != c.searchGeneration {
			return state
		}
		state.wallpapers = c.search.Wallpapers
		state.loading, state.hasMore, state.error = c.search.Loading, c.search.HasMore, c.search.Error
	case domain.Recommend:
		if c.detailList.generation != c.recommendationGeneration {
			return state
		}
		for _, group := range c.recommendations {
			if group.Key == c.detailList.Recommendation {
				state.wallpapers = group.Wallpapers
				state.loading, state.hasMore, state.error = group.Loading, group.HasMore, group.Error
				break
			}
		}
	default:
		state.wallpapers = c.detailList.Wallpapers
	}
	for i, wallpaper := range state.wallpapers {
		if wallpaper.ID == c.detail.Wallpaper.ID {
			state.index = i
			break
		}
	}
	return state
}

func (c *Controller) detailNavigation() DetailNavigation {
	state := c.detailListState()
	if state.index < 0 {
		return DetailNavigation{}
	}
	atEnd := state.index == len(state.wallpapers)-1
	navigation := DetailNavigation{
		HasPrevious: state.index > 0,
		HasNext:     !atEnd || state.hasMore,
		Loading:     atEnd && state.loading,
		Waiting:     c.detailList.pendingNext,
	}
	if atEnd {
		navigation.Error = state.error
	}
	return navigation
}

func (c *Controller) PreviousDetail() { c.moveDetail(-1) }
func (c *Controller) NextDetail()     { c.moveDetail(1) }

func (c *Controller) moveDetail(direction int) {
	if c.closed || !c.detail.Open {
		return
	}
	state := c.detailListState()
	if state.index < 0 {
		return
	}
	c.detailList.pendingNext = false
	next := state.index + direction
	if next >= 0 && next < len(state.wallpapers) {
		c.selectDetail(state.wallpapers[next])
		c.syncDetailList()
	} else if direction > 0 && state.hasMore {
		// A second press during the request still means one next image.
		c.detailList.pendingNext = true
		c.loadDetailPage()
	}
}

func (c *Controller) loadDetailPage() {
	switch c.detailList.Page {
	case domain.WallpaperPage:
		c.LoadMore()
	case domain.Recommend:
		c.loadMoreRecommendation(c.detailList.Recommendation)
	}
}

// Called after selection and page completion. Reaching the current end warms
// the next page; a queued Next advances only after a new unique image arrives.
func (c *Controller) syncDetailList() {
	if c.closed || !c.detail.Open {
		return
	}
	state := c.detailListState()
	if state.index < 0 {
		c.detailList.pendingNext = false
		return
	}
	if c.detailList.pendingNext {
		if state.index+1 < len(state.wallpapers) {
			c.detailList.pendingNext = false
			c.selectDetail(state.wallpapers[state.index+1])
			state.index++
		} else if state.error != "" || !state.hasMore {
			c.detailList.pendingNext = false
		}
	}
	if state.index == len(state.wallpapers)-1 && state.hasMore && !state.loading && state.error == "" {
		c.loadDetailPage()
	}
}
