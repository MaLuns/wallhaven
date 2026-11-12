package domain

import (
	"errors"
	"path/filepath"
	"strings"
)

type PreviewMode string

const (
	PreviewWaterfall PreviewMode = "waterfall"
	PreviewTile      PreviewMode = "tile"
)

type Preferences struct {
	PreviewMode  PreviewMode `json:"previewMode"`
	OpenAtLogin  bool        `json:"openAtLogin"`
	DownloadPath string      `json:"downloadPath"`
}

func DefaultSettings(downloadPath string) Preferences {
	return Preferences{PreviewMode: PreviewWaterfall, DownloadPath: downloadPath}
}

func (s Preferences) Validate() error {
	if s.PreviewMode != PreviewWaterfall && s.PreviewMode != PreviewTile {
		return errors.New("预览模式无效")
	}
	if strings.TrimSpace(s.DownloadPath) == "" || !filepath.IsAbs(s.DownloadPath) || strings.ContainsRune(s.DownloadPath, 0) || len(s.DownloadPath) > 4096 {
		return errors.New("下载目录必须是有效的绝对路径")
	}
	return nil
}
