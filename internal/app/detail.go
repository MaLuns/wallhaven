package app

import (
	"context"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

// DetailState is UI-thread-owned, like the search snapshot. The selected
// listing is available immediately while its richer metadata loads.
type DetailState struct {
	Open, Loading bool
	Wallpaper     domain.Wallpaper
	Detail        domain.WallpaperDetail
	Error         string
	Generation    uint64
	Navigation    DetailNavigation
}

func (c *Controller) DetailState() DetailState {
	state := c.detail
	state.Detail = cloneDetail(state.Detail)
	state.Navigation = c.detailNavigation()
	return state
}

func (c *Controller) OpenDetail(wallpaper domain.Wallpaper, list ...DetailList) {
	if c.closed {
		return
	}
	c.CloseDetail()
	if len(list) > 0 {
		c.detailList.DetailList = list[0]
		c.detailList.Wallpapers = cloneWallpapers(list[0].Wallpapers)
		switch list[0].Page {
		case domain.WallpaperPage:
			c.detailList.generation = c.searchGeneration
		case domain.Recommend:
			c.detailList.generation = c.recommendationGeneration
		}
	}
	c.selectDetail(wallpaper)
	c.syncDetailList()
}

// selectDetail replaces only the image request, retaining its source list.
func (c *Controller) selectDetail(wallpaper domain.Wallpaper) {
	if c.cancelDetail != nil {
		c.cancelDetail()
		c.cancelDetail = nil
	}
	c.detailGeneration++
	generation := c.detailGeneration
	c.detail = DetailState{Open: true, Wallpaper: wallpaper, Detail: domain.WallpaperDetail{Wallpaper: wallpaper}, Generation: generation}
	if wallpaper.ID == "" {
		c.detail.Error = "壁纸编号无效"
		return
	}
	if c.details == nil {
		c.detail.Error = "此壁纸来源暂不提供详细信息"
		return
	}
	if c.dispatch == nil {
		c.detail.Error = "界面更新服务尚未就绪"
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.cancelDetail = cancel
	c.detail.Loading = true
	source, dispatch := c.details, c.dispatch
	go func() {
		result, err := source.Detail(ctx, wallpaper.ID)
		dispatch(func() {
			if c.closed || !c.detail.Open || generation != c.detailGeneration || ctx.Err() != nil {
				return
			}
			c.detail.Loading = false
			if err != nil {
				c.detail.Error = displayError(err)
				return
			}
			if result.Wallpaper.ID != wallpaper.ID {
				c.detail.Error = "壁纸服务返回了不匹配的详情"
				return
			}
			c.detail.Detail = cloneDetail(result)
			c.detail.Wallpaper = mergeWallpaper(wallpaper, result.Wallpaper)
			c.detail.Detail.Wallpaper = c.detail.Wallpaper
		})
	}()
}

func (c *Controller) CloseDetail() {
	if c.cancelDetail != nil {
		c.cancelDetail()
		c.cancelDetail = nil
	}
	c.detailGeneration++
	c.detail = DetailState{Generation: c.detailGeneration}
	c.detailList = detailList{}
}

func (c *Controller) RetryDetail() {
	if c.closed || !c.detail.Open || c.detail.Loading {
		return
	}
	c.selectDetail(c.detail.Wallpaper)
}

func cloneDetail(detail domain.WallpaperDetail) domain.WallpaperDetail {
	detail.Tags = append([]domain.WallpaperTag(nil), detail.Tags...)
	detail.Colors = append([]string(nil), detail.Colors...)
	return detail
}

// Preserve useful listing data if a source omits an optional detail field.
func mergeWallpaper(listing, detail domain.Wallpaper) domain.Wallpaper {
	if detail.URL == "" {
		detail.URL = listing.URL
	}
	if detail.Path == "" {
		detail.Path = listing.Path
	}
	if detail.Thumbnail == "" {
		detail.Thumbnail = listing.Thumbnail
	}
	if detail.OriginalThumbnail == "" {
		detail.OriginalThumbnail = listing.OriginalThumbnail
	}
	if detail.Resolution == "" {
		detail.Resolution = listing.Resolution
	}
	if detail.Width == 0 {
		detail.Width = listing.Width
	}
	if detail.Height == 0 {
		detail.Height = listing.Height
	}
	if detail.FileType == "" {
		detail.FileType = listing.FileType
	}
	if detail.FileSize == 0 {
		detail.FileSize = listing.FileSize
	}
	if detail.Category == "" {
		detail.Category = listing.Category
	}
	if detail.Purity == "" {
		detail.Purity = listing.Purity
	}
	return detail
}
