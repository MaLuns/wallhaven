package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

const (
	transfersVersion = 1
	maxTransfers     = 10000
	maxTransfersSize = 16 << 20
)

type transfersFile struct {
	Version   int              `json:"version"`
	Transfers boundedTransfers `json:"transfers"`
}

type boundedTransfers []domain.Transfer

func (items *boundedTransfers) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if start, err := decoder.Token(); err != nil || start != json.Delim('[') {
		return errors.New("下载历史列表不是数组")
	}
	result := make(boundedTransfers, 0)
	for decoder.More() {
		if len(result) >= maxTransfers {
			return errors.New("下载历史数量超过上限")
		}
		var transfer domain.Transfer
		if err := decoder.Decode(&transfer); err != nil {
			return err
		}
		result = append(result, transfer)
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
		return errors.New("下载历史列表不完整")
	}
	*items = result
	return nil
}

// Transfers atomically replaces records only. It never opens or removes any
// original or checkpoint path described by those records.
type Transfers struct {
	mu   sync.Mutex
	path string
}

var _ ports.TransferStore = (*Transfers)(nil)

func NewTransfers(path string) *Transfers { return &Transfers{path: path} }

func (s *Transfers) Load(ctx context.Context) ([]domain.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *Transfers) load(ctx context.Context) ([]domain.Transfer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.path) == "" {
		return nil, errors.New("下载历史文件路径为空")
	}
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Transfer{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取下载历史失败：%w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTransfersSize {
		return nil, errors.New("下载历史文件类型或大小无效")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, maxTransfersSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTransfersSize {
		return nil, errors.New("下载历史文件超过大小限制")
	}
	var record transfersFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("下载历史文件损坏：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("下载历史包含多余数据")
	}
	if record.Version != transfersVersion || record.Transfers == nil {
		return nil, errors.New("下载历史文件版本或内容无效")
	}
	return validateTransfers(ctx, record.Transfers)
}

func (s *Transfers) Save(ctx context.Context, transfers []domain.Transfer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clean, err := validateTransfers(ctx, transfers)
	if err != nil {
		return err
	}
	if _, err := s.load(ctx); err != nil {
		return fmt.Errorf("未保存下载历史：%w", err)
	}
	data, err := json.Marshal(transfersFile{Version: transfersVersion, Transfers: clean})
	if err != nil {
		return err
	}
	if len(data) > maxTransfersSize {
		return errors.New("下载历史文件超过大小限制")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".transfers-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Write(data[:min(len(data), 32<<10)])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}

func validateTransfers(ctx context.Context, transfers []domain.Transfer) ([]domain.Transfer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(transfers) > maxTransfers {
		return nil, errors.New("下载历史数量超过上限")
	}
	seen := make(map[string]bool, len(transfers))
	clean := make([]domain.Transfer, 0, len(transfers))
	textSize := 0
	for _, transfer := range transfers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if transfer.Kind != domain.DownloadTransfer && transfer.Kind != domain.WallpaperTransfer {
			return nil, errors.New("下载历史任务类型无效")
		}
		if strings.TrimSpace(transfer.Wallpaper.ID) == "" {
			return nil, errors.New("下载历史壁纸编号为空")
		}
		key := string(transfer.Kind) + "\x00" + transfer.Wallpaper.ID
		if seen[key] {
			return nil, errors.New("下载历史任务重复")
		}
		seen[key] = true
		if transfer.Received < 0 || transfer.Total < 0 || transfer.BytesPerSecond < 0 || math.IsInf(transfer.BytesPerSecond, 0) || math.IsNaN(transfer.BytesPerSecond) {
			return nil, errors.New("下载历史进度无效")
		}
		for _, path := range []string{transfer.Path, transfer.Directory} {
			if path != "" && (!filepath.IsAbs(path) || strings.ContainsRune(path, 0) || len(path) > 4096) {
				return nil, errors.New("下载历史文件路径无效")
			}
		}
		w := transfer.Wallpaper
		for _, value := range []string{transfer.Path, transfer.Directory, transfer.Error, w.ID, w.URL, w.Path, w.Thumbnail, w.OriginalThumbnail, w.Resolution, w.FileType, w.Category, w.Purity} {
			if len(value) > maxTransfersSize-textSize {
				return nil, errors.New("下载历史文件超过大小限制")
			}
			textSize += len(value)
		}
		clean = append(clean, transfer)
	}
	return clean, nil
}
