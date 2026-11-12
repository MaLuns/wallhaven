package domain

// WallpaperDetail is the metadata returned for one wallpaper. Wallpaper keeps
// the full-size image path separate from thumbnails so a viewer never mistakes
// a preview image for the original.
type WallpaperDetail struct {
	Wallpaper                   Wallpaper
	Views, Favorites            int
	Source, ShortURL, CreatedAt string
	Uploader                    string
	Tags                        []WallpaperTag
	Colors                      []string
}

type WallpaperTag struct {
	ID           int
	Name, Purity string
}
