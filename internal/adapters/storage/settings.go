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
	"sync"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

const maxSettingsSize = 32 << 10

type settingsFile struct {
	Version  int                 `json:"version"`
	Settings *domain.Preferences `json:"settings"`
}

type Settings struct {
	mu       sync.Mutex
	path     string
	defaults domain.Preferences
}

var _ ports.SettingsStore = (*Settings)(nil)

func NewSettings(path string, defaults domain.Preferences) *Settings {
	return &Settings{path: path, defaults: defaults}
}

func (s *Settings) Load(ctx context.Context) (domain.Preferences, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *Settings) load(ctx context.Context) (domain.Preferences, error) {
	if err := ctx.Err(); err != nil {
		return domain.Preferences{}, err
	}
	if s.path == "" {
		return domain.Preferences{}, errors.New("设置文件路径为空")
	}
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s.defaults, s.defaults.Validate()
	}
	if err != nil {
		return domain.Preferences{}, fmt.Errorf("读取设置文件失败：%w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return domain.Preferences{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSettingsSize {
		return domain.Preferences{}, errors.New("设置文件类型或大小无效")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, maxSettingsSize+1))
	if err != nil {
		return domain.Preferences{}, err
	}
	if len(data) > maxSettingsSize {
		return domain.Preferences{}, errors.New("设置文件超过大小限制")
	}
	var record settingsFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return domain.Preferences{}, fmt.Errorf("设置文件损坏：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return domain.Preferences{}, errors.New("设置文件损坏：包含多余数据")
	}
	if record.Version != 1 || record.Settings == nil {
		return domain.Preferences{}, errors.New("设置文件版本或内容无效")
	}
	if err := record.Settings.Validate(); err != nil {
		return domain.Preferences{}, fmt.Errorf("设置文件内容无效：%w", err)
	}
	return *record.Settings, ctx.Err()
}

func (s *Settings) Save(ctx context.Context, values domain.Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := values.Validate(); err != nil {
		return err
	}
	// A failed load must never silently replace damaged or newer data.
	if _, err := s.load(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(settingsFile{Version: 1, Settings: &values})
	if err != nil {
		return err
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
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
