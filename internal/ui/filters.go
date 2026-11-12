package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	kit "github.com/egoist/mygo/ui"
)

type option struct{ title, value string }

var categoryOptions = []option{{"全部", "111"}, {"通用", "100"}, {"动漫", "010"}, {"人物", "001"}}
var sortingOptions = []option{{"最新", "date_added"}, {"热度", "hot"}, {"查看", "views"}, {"收藏", "favorites"}, {"随机", "random"}, {"排行榜", "toplist"}}
var orderOptions = []option{{"降序", "desc"}, {"升序", "asc"}}
var rangeOptions = []option{{"近一天", "1d"}, {"近三天", "3d"}, {"近一周", "1w"}, {"近一月", "1M"}, {"近三月", "3M"}, {"近半年", "6M"}, {"近一年", "1y"}}
var resolutions = []string{"2560x1080", "3440x1440", "3840x1600", "1280x720", "1600x900", "1920x1080", "2560x1440", "3840x2160", "1280x800", "1600x1000", "1920x1200", "2560x1600", "3840x2400", "1280x960", "1600x1200", "1920x1440", "2560x1920", "3840x2880", "1280x1024", "1600x1280", "1920x1536", "2560x2048", "3840x3072"}
var ratios = []option{{"所有横屏", "landscape"}, {"所有竖屏", "portrait"}, {"16:9", "16x9"}, {"16:10", "16x10"}, {"21:9", "21x9"}, {"32:9", "32x9"}, {"48:9", "48x9"}, {"9:16", "9x16"}, {"10:16", "10x16"}, {"9:18", "9x18"}, {"1:1", "1x1"}, {"3:2", "3x2"}, {"4:3", "4x3"}, {"5:4", "5x4"}}
var colors = []string{"660000", "990000", "cc0000", "cc3333", "ea4c88", "993399", "663399", "333399", "0066cc", "0099cc", "66cccc", "77cc33", "669900", "336600", "666600", "999900", "cccc33", "ffff00", "ffcc33", "ff9900", "ff6600", "cc6633", "996633", "663300", "000000", "999999", "cccccc", "ffffff", "424153"}

type searchFilters struct {
	keyword, category, sorting, order, topRange string
	safe, sketchy, nsfw                         bool
	resolutionMode, minimum, custom             string
	exact                                       [23]bool
	ratio                                       [14]bool
	color                                       string
}

func defaultFilters() searchFilters {
	return searchFilters{category: "全部", sorting: "最新", order: "降序", topRange: "近一月", safe: true, resolutionMode: "至少", minimum: "不限"}
}
func optionValue(options []option, title string) string {
	for _, o := range options {
		if o.title == title {
			return o.value
		}
	}
	return ""
}

func (f *searchFilters) query() domain.SearchQuery {
	q := domain.DefaultSearchQuery()
	q.Query = f.keyword
	q.Categories = optionValue(categoryOptions, f.category)
	q.Sorting = optionValue(sortingOptions, f.sorting)
	q.Order = optionValue(orderOptions, f.order)
	q.TopRange = optionValue(rangeOptions, f.topRange)
	purity := []byte("000")
	for i, v := range []bool{f.safe, f.sketchy, f.nsfw} {
		if v {
			purity[i] = '1'
		}
	}
	q.Purity = string(purity)
	if f.resolutionMode == "至少" {
		if f.minimum != "不限" {
			q.AtLeast = f.minimum
		}
		if strings.TrimSpace(f.custom) != "" {
			q.AtLeast = strings.TrimSpace(f.custom)
		}
	} else {
		for i, v := range f.exact {
			if v {
				q.Resolutions = append(q.Resolutions, resolutions[i])
			}
		}
		q.Resolutions = append(q.Resolutions, strings.Split(f.custom, ",")...)
	}
	for i, v := range f.ratio {
		if v {
			q.Ratios = append(q.Ratios, ratios[i].value)
		}
	}
	q.Color = f.color
	return q.Normalize()
}

func (a *App) submitSearch() {
	a.results.Y = 0
	a.autoPage = 0
	a.controller.Search(a.filters.query())
	if a.filters.query().Validate() == nil {
		a.advanced = false
	}
}

// searchControls preserves the source application's 80px placeholder and
// absolutely positioned search panel. Expanding filters overlays the result
// list; it never reduces the list to a tiny strip.
func (a *App) searchControls(c *kit.Context) {
	p := legacyPalette(c)
	windowWidth, windowHeight := c.Size()
	panelHeight := float32(58)
	if a.advanced {
		panelHeight = advancedFilterPanelHeight(windowWidth, windowHeight, a.filters.sorting == "排行榜")
	}
	kit.Box(c.Key("search-placeholder")).Label("搜索筛选区").FillWidth().Height(80).Shrink(0).ZIndex(20).Children(func() {
		panel := kit.Column(c.Key("search-panel")).Absolute().Left(20).Right(20).Top(0).
			Height(panelHeight).Radius(10).Padding(14).Gap(10).Clip().Draw(frostedBackground(p.Search.Alpha(.65), 10)).
			Transition(kit.ElementTransition{Height: true, Duration: 180 * time.Millisecond, Ease: kit.EaseOut})
		if a.advanced {
			panel.Shadow(5, 7, 7, 0, kit.RGBA(0, 0, 0, .28))
		}
		panel.Children(func() {
			kit.Row(c.Key("search-bar")).FillWidth().Height(30).Shrink(0).Gap(10).AlignItems(kit.Center).Children(func() {
				// The legacy category controls and search field occupy one row.
				kit.Row(c.Key("categories")).Grow(1).MinWidth(0).Height(30).AlignItems(kit.Center).Children(func() {
					filterLabel(c, "分类")
					kit.Row(c).Gap(8).Children(func() {
						for _, o := range categoryOptions {
							filterChip(c, o.value, o.title, o.title, a.filters.category == o.title, 60, func() { a.filters.category = o.title })
						}
					})
				})
				kit.Row(c.Key("search-entry")).Width(min(360, max(236, windowWidth*.24))).Shrink(0).Height(28).Children(func() {
					kit.TextInput(c.Key("keywords"), &a.filters.keyword).Label("壁纸关键词").Placeholder("搜索...").
						Grow(1).MinWidth(0).Height(28).Background(kit.Transparent).Border(1, p.Border).
						Radius(4, 0, 0, 4).Padding(0, 7).FontSize(12).TextColor(p.Text).OnSubmit(a.submitSearch)
					legacyIconButton(c, "search-submit", "search", "搜索壁纸").Size(30, 28).
						Background(p.Accent).TextColor(kit.RGB(255, 255, 255)).Radius(0, 4, 4, 0).OnClick(a.submitSearch)
				})
				icon := "chevron-down"
				if a.advanced {
					icon = "chevron-up"
				}
				legacyIconButton(c, "advanced-toggle", icon, "高级筛选").Size(30, 30).
					OnClick(func() { a.advanced = !a.advanced })
			})
			if !a.advanced {
				return
			}
			kit.Scroll(c.Key("advanced-filters")).Label("高级筛选面板").Grow(1).MinHeight(0).FillWidth().Children(func() {
				kit.Column(c).FillWidth().Gap(filterGroupGap).Children(func() { a.legacyFilterRows(c) })
			})
		})
	})
}

const (
	filterControlHeight = float32(30)
	filterGridGap       = float32(6)
	filterGroupGap      = float32(10)
	filterLabelWidth    = float32(56)
	filterLabelGap      = float32(14)
	filterResolutionW   = float32(102)
	filterRatioW        = float32(74)
	filterSwatchW       = float32(40)
	filterHorizontalGap = float32(8)
)

type filterLayout struct {
	resolutionColumns, ratioColumns, colorColumns int
}

func responsiveFilterLayout(windowWidth float32) filterLayout {
	// The panel keeps its original 20px side margins and 14px padding.
	width := max(filterResolutionW, windowWidth-40-28-filterLabelWidth-filterLabelGap)
	columns := func(chipWidth float32, count int) int {
		return min(count, max(1, int((width+filterHorizontalGap)/(chipWidth+filterHorizontalGap))))
	}
	return filterLayout{columns(filterResolutionW, len(resolutions)), columns(filterRatioW, len(ratios)), columns(filterSwatchW, len(colors)+1)}
}

func filterGridHeight(count, columns int) float32 {
	rows := (count + columns - 1) / columns
	return float32(rows)*filterControlHeight + float32(max(0, rows-1))*filterGridGap
}

func advancedFilterContentHeight(windowWidth float32, topRange bool) float32 {
	layout := responsiveFilterLayout(windowWidth)
	height := 4*filterControlHeight + 5*filterGroupGap + filterGridGap +
		filterGridHeight(len(resolutions), layout.resolutionColumns) +
		filterGridHeight(len(ratios), layout.ratioColumns) + filterGridHeight(len(colors)+1, layout.colorColumns)
	if topRange {
		height += filterControlHeight + filterGroupGap
	}
	return height
}

func advancedFilterPanelHeight(windowWidth, windowHeight float32, topRange bool) float32 {
	return min(68+advancedFilterContentHeight(windowWidth, topRange), max(240, windowHeight-130))
}

func filterLabel(c *kit.Context, label string) {
	color := hexColor("797979")
	if c.Theme().Dark {
		color = hexColor("e0e0e0")
	}
	// A text element with an explicit 30px height keeps its glyphs at the
	// top. Center the natural text line inside the same height as the chips.
	kit.Row(c.Key("label")).Label("筛选标签 "+label).Width(filterLabelWidth).Height(filterControlHeight).Shrink(0).Margin(0, filterLabelGap, 0, 0).
		Justify(kit.End).AlignItems(kit.Center).Children(func() {
		kit.Text(c, label).FontSize(13).Bold().TextColor(color).SingleLine()
	})
}

func filterRow(c *kit.Context, key, label string, build func()) {
	kit.Row(c.Key(key)).FillWidth().AlignItems(kit.Start).Children(func() {
		filterLabel(c, label)
		kit.Column(c).Grow(1).MinWidth(0).Children(build)
	})
}

func filterChip(c *kit.Context, key, display, label string, selected bool, width float32, run func()) {
	p := legacyPalette(c)
	button := kit.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Checked(selected).Width(width).MinWidth(0).Height(filterControlHeight).Shrink(0).Padding(0).Radius(5).
		Background(kit.Transparent).Border(1, kit.Transparent).TextColor(p.Text).
		Transition(kit.ElementTransition{Colors: true, Duration: 100 * time.Millisecond})
	if selected {
		button.Background(p.Accent.Alpha(.14)).Border(1, p.Accent.Alpha(.7)).TextColor(p.Accent)
		if c.Theme().Dark {
			button.Background(p.Accent.Alpha(.18)).TextColor(p.Text)
		}
	} else if button.Hovered() {
		button.Background(p.Accent.Alpha(.07)).Border(1, p.Accent.Alpha(.25))
	}
	button.Children(func() { kit.Text(c, display).FontSize(13).SingleLine().IgnoreInput(true) }).OnClick(run)
}

func filterGrid(c *kit.Context, columns, count int, build func(int)) {
	kit.Column(c).FillWidth().Gap(filterGridGap).Children(func() {
		for first := 0; first < count; first += columns {
			kit.Row(c.Key(first)).FillWidth().Height(filterControlHeight).Shrink(0).Gap(filterHorizontalGap).AlignItems(kit.Center).Children(func() {
				for index := first; index < min(first+columns, count); index++ {
					build(index)
				}
			})
		}
	})
}

func (a *App) legacyFilterRows(c *kit.Context) {
	windowWidth, _ := c.Size()
	layout := responsiveFilterLayout(windowWidth)
	filterRow(c, "purity", "场景", func() {
		kit.Row(c).Gap(filterHorizontalGap).Children(func() {
			filterChip(c, "SFW", "SFW", "SFW", a.filters.safe, 74, func() { a.filters.safe = !a.filters.safe })
			filterChip(c, "Sketchy", "Sketchy", "Sketchy", a.filters.sketchy, 74, func() { a.filters.sketchy = !a.filters.sketchy })
			filterChip(c, "NSFW", "NSFW", "NSFW", a.filters.nsfw, 74, func() { a.filters.nsfw = !a.filters.nsfw })
		})
	})
	filterRow(c, "resolution", "分辨率", func() {
		kit.Column(c).FillWidth().Gap(filterGridGap).Children(func() {
			kit.Row(c).Height(filterControlHeight).Gap(filterHorizontalGap).Children(func() {
				for _, mode := range []string{"至少", "相等"} {
					filterChip(c, mode, mode, mode, a.filters.resolutionMode == mode, 74, func() { a.filters.resolutionMode = mode })
				}
			})
			filterGrid(c, layout.resolutionColumns, len(resolutions), func(index int) {
				size := resolutions[index]
				selected := a.filters.minimum == size
				if a.filters.resolutionMode == "相等" {
					selected = a.filters.exact[index]
				}
				filterChip(c, size, size, size, selected, filterResolutionW, func() {
					if a.filters.resolutionMode == "相等" {
						a.filters.exact[index] = !a.filters.exact[index]
					} else {
						a.filters.minimum = size
					}
				})
			})
		})
	})
	filterRow(c, "ratios", "比例", func() {
		filterGrid(c, layout.ratioColumns, len(ratios), func(index int) {
			ratio := ratios[index]
			filterChip(c, ratio.value, ratio.title, ratio.title, a.filters.ratio[index], filterRatioW, func() { a.filters.ratio[index] = !a.filters.ratio[index] })
		})
	})
	filterRow(c, "colors", "颜色", func() {
		filterGrid(c, layout.colorColumns, len(colors)+1, func(index int) {
			color := ""
			if index < len(colors) {
				color = colors[index]
			}
			a.filterSwatch(c, color)
		})
	})
	filterRow(c, "order", "排序", func() {
		kit.Row(c).Gap(filterHorizontalGap).AlignItems(kit.Center).Children(func() {
			for _, o := range orderOptions {
				filterChip(c, o.value, o.title, o.title, a.filters.order == o.title, 64, func() { a.filters.order = o.title })
			}
			kit.Box(c).Width(1).Height(18).Margin(0, 6).Background(legacyPalette(c).Border.Alpha(.6))
			for _, o := range sortingOptions {
				filterChip(c, o.value, o.title, o.title, a.filters.sorting == o.title, 68, func() { a.filters.sorting = o.title })
			}
		})
	})
	if a.filters.sorting == "排行榜" {
		filterRow(c, "top-range", "时间", func() {
			kit.Row(c).Gap(filterHorizontalGap).Children(func() {
				for _, o := range rangeOptions {
					filterChip(c, o.value, o.title, o.title, a.filters.topRange == o.title, 74, func() { a.filters.topRange = o.title })
				}
			})
		})
	}
	// The migration already exposes custom dimensions and reset. Keep these
	// secondary controls at the end without replacing the original presets.
	filterRow(c, "custom", "自定义", func() {
		kit.Row(c).Gap(12).AlignItems(kit.Center).Children(func() {
			kit.TextInput(c.Key("custom-resolution"), &a.filters.custom).Label("自定义分辨率").Placeholder("1920x1080").Width(260).Height(30).
				Background(kit.Transparent).Border(1, legacyPalette(c).Border).Radius(5).Padding(0, 8).FontSize(12).TextColor(legacyPalette(c).Text)
			filterChip(c, "reset", "重置", "重置筛选", false, 64, func() { a.filters = defaultFilters(); a.submitSearch() })
		})
	})
}

func (a *App) filterSwatch(c *kit.Context, color string) {
	label := "颜色 #" + color
	if color == "" {
		label = "不限颜色"
	}
	fill := hexColor(color)
	if color == "" {
		fill = kit.RGB(255, 255, 255)
	}
	selected := a.filters.color == color
	button := kit.ButtonBase(c.Key("swatch-"+color)).Size(filterSwatchW, 28).Shrink(0).Padding(0).Background(fill).Border(1, legacyPalette(c).Border.Alpha(.45)).Radius(5).
		Label(label).Tooltip(label).Checked(selected)
	if selected {
		button.Border(2, legacyPalette(c).Accent)
	}
	button.Children(func() {
		if color == "" {
			appIcon(c, "clear", 15).TextColor(kit.RGB(210, 55, 55))
		} else if selected {
			value, _ := strconv.ParseUint(color, 16, 32)
			contrast := kit.RGB(255, 255, 255)
			if float64(value>>16&255)*.299+float64(value>>8&255)*.587+float64(value&255)*.114 > 150 {
				contrast = hexColor("252b3d")
			}
			appIcon(c, "check", 15).TextColor(contrast)
		}
	}).OnClick(func() { a.filters.color = color })
}
