package domain

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// SearchQuery contains only public filters, never authentication credentials.
type SearchQuery struct {
	Query, Categories, Purity, AtLeast, Color, Sorting, Order, TopRange, Seed string
	Resolutions, Ratios                                                       []string
}

func DefaultSearchQuery() SearchQuery {
	return SearchQuery{Categories: "111", Purity: "100", Sorting: "date_added", Order: "desc"}
}

func (q SearchQuery) Normalize() SearchQuery {
	q.Query = strings.TrimSpace(q.Query)
	q.Categories = strings.TrimSpace(q.Categories)
	q.Purity = strings.TrimSpace(q.Purity)
	q.AtLeast = strings.TrimSpace(q.AtLeast)
	q.Color = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q.Color), "#"))
	q.Sorting = strings.TrimSpace(q.Sorting)
	q.Order = strings.TrimSpace(q.Order)
	q.TopRange = strings.TrimSpace(q.TopRange)
	q.Seed = strings.TrimSpace(q.Seed)
	if q.Categories == "" {
		q.Categories = "111"
	}
	if q.Purity == "" || q.Purity == "000" {
		q.Purity = "100"
	}
	if q.Sorting == "" {
		q.Sorting = "date_added"
	}
	if q.Order == "" {
		q.Order = "desc"
	}
	if q.Sorting == "toplist" && q.TopRange == "" {
		q.TopRange = "1M"
	}
	q.Resolutions = cleanValues(q.Resolutions)
	q.Ratios = cleanValues(q.Ratios)
	return q
}

var (
	searchMask  = regexp.MustCompile(`^[01]{3}$`)
	searchSize  = regexp.MustCompile(`^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$`)
	searchColor = regexp.MustCompile(`^[0-9a-f]{6}$`)
	searchSeed  = regexp.MustCompile(`^[a-zA-Z0-9]{6}$`)
)

func (q SearchQuery) Validate() error {
	q = q.Normalize()
	if !searchMask.MatchString(q.Categories) || q.Categories == "000" {
		return errors.New("请选择至少一种有效分类")
	}
	if !searchMask.MatchString(q.Purity) {
		return errors.New("内容等级无效")
	}
	if !slices.Contains([]string{"date_added", "relevance", "hot", "random", "views", "favorites", "toplist"}, q.Sorting) {
		return errors.New("排序方式无效")
	}
	if q.Order != "asc" && q.Order != "desc" {
		return errors.New("排序方向无效")
	}
	if q.Sorting == "toplist" && !slices.Contains([]string{"1d", "3d", "1w", "1M", "3M", "6M", "1y"}, q.TopRange) {
		return errors.New("排行榜时间范围无效")
	}
	if q.AtLeast != "" && !searchSize.MatchString(q.AtLeast) {
		return errors.New("最小分辨率格式应为宽x高，例如 1920x1080")
	}
	if q.AtLeast != "" && len(q.Resolutions) != 0 {
		return errors.New("至少分辨率与精确分辨率不能同时使用")
	}
	for _, size := range q.Resolutions {
		if !searchSize.MatchString(size) {
			return errors.New("精确分辨率格式应为宽x高，例如 1920x1080")
		}
	}
	for _, ratio := range q.Ratios {
		if ratio != "landscape" && ratio != "portrait" && !searchSize.MatchString(ratio) {
			return errors.New("宽高比格式应为宽x高，例如 16x9")
		}
	}
	if q.Color != "" && !searchColor.MatchString(q.Color) {
		return errors.New("颜色应为六位十六进制值")
	}
	if q.Sorting == "random" && q.Seed != "" && !searchSeed.MatchString(q.Seed) {
		return errors.New("随机种子应为六位字母或数字")
	}
	return nil
}

func cleanValues(values []string) []string {
	var clean []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(clean, value) {
			clean = append(clean, value)
		}
	}
	return clean
}

type Wallpaper struct {
	OriginalThumbnail                                                string
	ID, URL, Path, Thumbnail, Resolution, FileType, Category, Purity string
	Width, Height                                                    int
	FileSize                                                         int64
}

type SearchResult struct {
	Wallpapers                   []Wallpaper
	CurrentPage, LastPage, Total int
	Seed                         string
}
