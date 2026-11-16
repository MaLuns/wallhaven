package ports

import (
	"context"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type SettingsStore interface {
	Load(context.Context) (domain.Preferences, error)
	Save(context.Context, domain.Preferences) error
}

type LoginItems interface {
	Enabled(context.Context) (bool, error)
	SetEnabled(context.Context, bool) error
}

type SettingsDirectories interface {
	Validate(context.Context, string) error
	Open(context.Context, string) error
}

// CacheManager manages the disposable image caches, independently of saved
// wallpapers and downloads. All methods are called by background workers.
type CacheManager interface {
	Usage(context.Context) (domain.CacheUsage, error)
	SetLimits(context.Context, int64, int64) error
	Clear(context.Context) error
}
