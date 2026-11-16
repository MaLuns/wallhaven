package imagecache

import (
	"context"
	"errors"
	"fmt"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/locale"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

// Manager exposes the two disposable disk caches to the settings worker.
// It never manages downloaded files or the images currently held in memory.
type Manager struct {
	thumbnails, originals *Disk
}

var _ ports.CacheManager = (*Manager)(nil)

func NewManager(thumbnails, originals *Disk) *Manager {
	return &Manager{thumbnails: thumbnails, originals: originals}
}

func (m *Manager) Usage(ctx context.Context) (domain.CacheUsage, error) {
	if err := ctx.Err(); err != nil {
		return domain.CacheUsage{}, err
	}
	thumbnails, thumbnailError := m.thumbnails.Stats()
	originals, originalError := m.originals.Stats()
	return domain.CacheUsage{
		ThumbnailBytes: thumbnails.Bytes, OriginalBytes: originals.Bytes,
		ThumbnailEntries: thumbnails.Entries, OriginalEntries: originals.Entries,
	}, errors.Join(thumbnailError, originalError, ctx.Err())
}

func (m *Manager) SetLimits(ctx context.Context, thumbnailBytes, originalBytes int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if thumbnailBytes <= 0 || originalBytes <= 0 {
		return errors.New(locale.T("缓存上限必须大于零"))
	}
	previous, previousOriginal := m.thumbnails.Limit(), m.originals.Limit()
	if err := m.thumbnails.SetLimit(thumbnailBytes); err != nil {
		return errors.Join(err, m.thumbnails.SetLimit(previous))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, m.thumbnails.SetLimit(previous))
	}
	if err := m.originals.SetLimit(originalBytes); err != nil {
		return errors.Join(err, m.thumbnails.SetLimit(previous), m.originals.SetLimit(previousOriginal))
	}
	return nil
}

func (m *Manager) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Both generations advance even if one directory cannot be cleaned.
	thumbnailError := m.thumbnails.Clear()
	originalError := m.originals.Clear()
	if err := errors.Join(thumbnailError, originalError, ctx.Err()); err != nil {
		return fmt.Errorf(locale.T("未能完全清理磁盘缓存：%w"), err)
	}
	return nil
}
