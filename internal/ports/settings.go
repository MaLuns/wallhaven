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
