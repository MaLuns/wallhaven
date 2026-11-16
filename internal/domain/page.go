// Package domain contains application values independent of the UI toolkit.
package domain

import "github.com/MaLuns/wallhaven-electron/internal/locale"

type PageID string

const (
	Recommend     PageID = "recommend"
	WallpaperPage PageID = "wallpaper"
	Favorites     PageID = "favorites"
	Downloads     PageID = "downloads"
	Settings      PageID = "settings"
	Components    PageID = "components"
)

type Page struct {
	ID          PageID
	Title       string
	Description string
}

// Pages returns a separate navigation list so callers cannot alter the registry.
func Pages() []Page {
	return []Page{
		{Recommend, locale.T("推荐"), locale.T("最近更新、热门与人气壁纸。")},
		{WallpaperPage, locale.T("壁纸"), locale.T("按关键词、分类和尺寸寻找壁纸。")},
		{Favorites, locale.T("收藏"), locale.T("保存和浏览喜欢的壁纸。")},
		{Downloads, locale.T("下载"), locale.T("管理图片下载与本地文件。")},
		{Settings, locale.T("设置"), locale.T("管理外观、下载位置与应用偏好。")},
		{Components, locale.T("组件"), locale.T("桌面组件。")},
	}
}
