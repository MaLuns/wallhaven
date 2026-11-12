package ports

import (
	"context"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

// WallpaperDetails is optional for sources that can return richer single-image
// metadata than a search listing contains.
type WallpaperDetails interface {
	Detail(context.Context, string) (domain.WallpaperDetail, error)
}
