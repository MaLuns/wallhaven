// Package storage persists application data without depending on the UI.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

const (
	favoritesVersion = 1
	maxFavorites     = 10000
	maxFavoritesSize = 16 << 20
)

type favoritesFile struct {
	Version    int              `json:"version"`
	Wallpapers boundedFavorites `json:"wallpapers"`
}

type boundedFavorites []domain.Wallpaper

func (wallpapers *boundedFavorites) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	start, err := decoder.Token()
	if err != nil || start != json.Delim('[') {
		return errors.New("收藏壁纸列表不是数组")
	}
	items := make(boundedFavorites, 0)
	for decoder.More() {
		// Limit allocation while decoding, before a damaged file can create
		// hundreds of thousands of tiny or duplicate wallpaper records.
		if len(items) >= maxFavorites {
			return errors.New("收藏数量超过上限")
		}
		var wallpaper domain.Wallpaper
		if err := decoder.Decode(&wallpaper); err != nil {
			return err
		}
		items = append(items, wallpaper)
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
		return errors.New("收藏壁纸列表不完整")
	}
	*wallpapers = items
	return nil
}

// Favorites serializes access by one application instance. Each successful Save
// replaces one complete file; malformed existing data is never overwritten.
type Favorites struct {
	mu   sync.Mutex
	path string
}

var _ ports.FavoritesStore = (*Favorites)(nil)

func NewFavorites(path string) *Favorites { return &Favorites{path: path} }

func (f *Favorites) Load(ctx context.Context) ([]domain.Wallpaper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load(ctx)
}

func (f *Favorites) load(ctx context.Context) ([]domain.Wallpaper, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(f.path) == "" {
		return nil, errors.New("收藏文件路径为空")
	}
	file, err := os.Open(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Wallpaper{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取收藏文件失败：%w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("读取收藏文件信息失败：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("收藏文件不是普通文件")
	}
	if info.Size() > maxFavoritesSize {
		return nil, errors.New("收藏文件超过大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, maxFavoritesSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取收藏文件失败：%w", err)
	}
	if len(data) > maxFavoritesSize {
		return nil, errors.New("收藏文件超过大小限制")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var record favoritesFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("收藏文件损坏：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("收藏文件损坏：包含多余数据")
	}
	if record.Version != favoritesVersion {
		return nil, fmt.Errorf("不支持的收藏文件版本：%d", record.Version)
	}
	if record.Wallpapers == nil {
		return nil, errors.New("收藏文件损坏：缺少壁纸列表")
	}
	return normalizeFavorites(ctx, record.Wallpapers)
}

func (f *Favorites) Save(ctx context.Context, wallpapers []domain.Wallpaper) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	clean, err := normalizeFavorites(ctx, wallpapers)
	if err != nil {
		return err
	}
	// Refuse to replace unreadable, damaged or newer-version data, even when
	// the caller has a valid in-memory collection.
	if _, err := f.load(ctx); err != nil {
		return fmt.Errorf("未保存收藏：%w", err)
	}
	data, err := json.Marshal(favoritesFile{Version: favoritesVersion, Wallpapers: clean})
	if err != nil {
		return fmt.Errorf("编码收藏失败：%w", err)
	}
	if len(data) > maxFavoritesSize {
		return errors.New("收藏文件超过大小限制")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(f.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("创建收藏目录失败：%w", err)
	}
	file, err := os.CreateTemp(directory, ".favorites-*")
	if err != nil {
		return fmt.Errorf("创建收藏临时文件失败：%w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("设置收藏文件权限失败：%w", err)
	}
	// Chunking gives cancellation a chance before all bytes have been written.
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Write(data[:min(len(data), 32<<10)])
		if err != nil {
			return fmt.Errorf("写入收藏失败：%w", err)
		}
		if n == 0 {
			return fmt.Errorf("写入收藏失败：%w", io.ErrShortWrite)
		}
		data = data[n:]
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步收藏文件失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭收藏文件失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporary, f.path); err != nil {
		return fmt.Errorf("替换收藏文件失败：%w", err)
	}
	return nil
}

// Deduplication is stable: the first complete record for an ID wins. No fields
// are trimmed or reconstructed, so original download paths and metadata survive.
func normalizeFavorites(ctx context.Context, wallpapers []domain.Wallpaper) ([]domain.Wallpaper, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, min(len(wallpapers), maxFavorites))
	clean := make([]domain.Wallpaper, 0, min(len(wallpapers), maxFavorites))
	size := 0
	for _, wallpaper := range wallpapers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(wallpaper.ID) == "" {
			return nil, errors.New("收藏壁纸编号为空")
		}
		if _, found := seen[wallpaper.ID]; found {
			continue
		}
		if len(clean) >= maxFavorites {
			return nil, errors.New("收藏数量超过上限")
		}
		// Reject obviously oversized strings before JSON allocates its buffer.
		for _, value := range []string{wallpaper.ID, wallpaper.URL, wallpaper.Path, wallpaper.Thumbnail, wallpaper.OriginalThumbnail, wallpaper.Resolution, wallpaper.FileType, wallpaper.Category, wallpaper.Purity} {
			if len(value) > maxFavoritesSize-size {
				return nil, errors.New("收藏文件超过大小限制")
			}
			size += len(value)
		}
		seen[wallpaper.ID] = struct{}{}
		clean = append(clean, wallpaper)
	}
	return clean, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
