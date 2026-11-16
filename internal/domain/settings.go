package domain

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/MaLuns/wallhaven-electron/internal/locale"
)

type PreviewMode string

const (
	PreviewWaterfall PreviewMode = "waterfall"
	PreviewTile      PreviewMode = "tile"

	DefaultThumbnailCacheMiB int64 = 256
	DefaultOriginalCacheMiB  int64 = 1024
)

type Preferences struct {
	PreviewMode       PreviewMode `json:"previewMode"`
	OpenAtLogin       bool        `json:"openAtLogin"`
	DownloadPath      string      `json:"downloadPath"`
	ThumbnailCacheMiB int64       `json:"thumbnailCacheMiB"`
	OriginalCacheMiB  int64       `json:"originalCacheMiB"`
}

func DefaultSettings(downloadPath string) Preferences {
	return Preferences{PreviewMode: PreviewWaterfall, DownloadPath: downloadPath, ThumbnailCacheMiB: DefaultThumbnailCacheMiB, OriginalCacheMiB: DefaultOriginalCacheMiB}
}

func (s Preferences) Validate() error {
	if s.PreviewMode != PreviewWaterfall && s.PreviewMode != PreviewTile {
		return errors.New(locale.T("预览模式无效"))
	}
	if strings.TrimSpace(s.DownloadPath) == "" || !filepath.IsAbs(s.DownloadPath) || strings.ContainsRune(s.DownloadPath, 0) || len(s.DownloadPath) > 4096 {
		return errors.New(locale.T("下载目录必须是有效的绝对路径"))
	}
	if s.ThumbnailCacheMiB < 64 || s.ThumbnailCacheMiB > 4096 {
		return errors.New(locale.T("缩略图缓存上限必须介于 64 和 4096 MiB 之间"))
	}
	if s.OriginalCacheMiB < 64 || s.OriginalCacheMiB > 16384 {
		return errors.New(locale.T("原图缓存上限必须介于 64 和 16384 MiB 之间"))
	}
	return nil
}

// CacheUsage reports the encoded image files currently retained on disk.
type CacheUsage struct {
	ThumbnailBytes, OriginalBytes     int64
	ThumbnailEntries, OriginalEntries int
}
