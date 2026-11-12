// Package domain contains application values independent of the UI toolkit.
package domain

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
		{Recommend, "推荐", "最近更新、热门与人气壁纸。"},
		{WallpaperPage, "壁纸", "按关键词、分类和尺寸寻找壁纸。"},
		{Favorites, "收藏", "保存和浏览喜欢的壁纸。"},
		{Downloads, "下载", "管理图片下载与本地文件。"},
		{Settings, "设置", "管理外观、下载位置与应用偏好。"},
		{Components, "组件", "桌面组件。"},
	}
}
