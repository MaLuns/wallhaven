// Package ports describes the external capabilities used by application cases.
package ports

import (
	"context"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type WallpaperSource interface {
	Search(context.Context, domain.SearchQuery, int) (domain.SearchResult, error)
}
