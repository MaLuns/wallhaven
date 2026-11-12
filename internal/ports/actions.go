package ports

import (
	"context"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type FavoritesStore interface {
	Load(context.Context) ([]domain.Wallpaper, error)
	Save(context.Context, []domain.Wallpaper) error
}

// OriginalFiles saves a validated original into directory without overwriting
// unrelated files. Progress callbacks may run on the calling worker.
type OriginalFiles interface {
	Download(context.Context, domain.Wallpaper, string, func(received, total int64)) (string, error)
}

// ResumableOriginalFiles retains validated checkpoints on interruption. Discard
// removes only its checkpoint and unfinished data, never a published original.
type ResumableOriginalFiles interface {
	DownloadResumable(context.Context, domain.Wallpaper, string, string, func(received, total int64)) (string, error)
	Discard(context.Context, string) error
}

type TransferStore interface {
	Load(context.Context) ([]domain.Transfer, error)
	Save(context.Context, []domain.Transfer) error
}

// EncodedOriginals shares immutable original file bytes between preview and
// download workers. Consumers must not mutate the returned slice.
type EncodedOriginals interface {
	Get(string) ([]byte, bool)
	Put(string, []byte)
}

type DesktopWallpaper interface {
	Set(context.Context, string) error
}
