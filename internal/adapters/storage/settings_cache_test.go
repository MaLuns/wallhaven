package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func TestSettingsMigratesV1CacheDefaultsWithoutChangingExistingPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	defaults := domain.DefaultSettings(t.TempDir())
	legacy := map[string]any{"version": 1, "settings": map[string]any{"previewMode": "tile", "openAtLogin": true, "downloadPath": t.TempDir()}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	store := NewSettings(path, defaults)
	values, err := store.Load(context.Background())
	if err != nil || values.PreviewMode != domain.PreviewTile || !values.OpenAtLogin || values.DownloadPath != legacy["settings"].(map[string]any)["downloadPath"] || values.ThumbnailCacheMiB != 256 || values.OriginalCacheMiB != 1024 {
		t.Fatalf("legacy settings migration = %+v, %v", values, err)
	}
	if unchanged, _ := os.ReadFile(path); string(unchanged) != string(data) {
		t.Fatal("loading a legacy file changed it on disk")
	}
	values.ThumbnailCacheMiB, values.OriginalCacheMiB = 512, 2048
	if err := store.Save(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	if restored, err := NewSettings(path, defaults).Load(context.Background()); err != nil || restored != values {
		t.Fatalf("new cache limits did not persist after migration: %+v, %v", restored, err)
	}
}

func TestSettingsRejectsExplicitInvalidCacheLimits(t *testing.T) {
	for _, limits := range [][2]int64{{0, 1024}, {256, 0}, {63, 1024}, {4097, 1024}, {256, 16385}} {
		path := filepath.Join(t.TempDir(), "settings.json")
		values := domain.DefaultSettings(t.TempDir())
		values.ThumbnailCacheMiB, values.OriginalCacheMiB = limits[0], limits[1]
		data, err := json.Marshal(settingsFile{Version: 1, Settings: &values})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		store := NewSettings(path, domain.DefaultSettings(t.TempDir()))
		if _, err := store.Load(context.Background()); err == nil {
			t.Fatalf("explicit invalid limits were treated as missing defaults: %v", limits)
		}
		if err := store.Save(context.Background(), domain.DefaultSettings(t.TempDir())); err == nil {
			t.Fatal("saving replaced invalid settings without surfacing the load error")
		}
	}
}
