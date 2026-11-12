package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func TestSettingsPersistAcrossInstancesAndCanceledSave(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	defaults := domain.DefaultSettings(t.TempDir())
	store := NewSettings(path, defaults)
	if got, err := store.Load(ctx); err != nil || got != defaults {
		t.Fatalf("first launch: %+v, %v", got, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reading defaults created a settings file")
	}
	want := defaults
	want.PreviewMode, want.OpenAtLogin, want.DownloadPath = domain.PreviewTile, true, filepath.Join(t.TempDir(), "图片 & wallpapers")
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, err := NewSettings(path, defaults).Load(ctx); err != nil || got != want {
		t.Fatalf("restarted settings: %+v, %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions: %v, %v", info, err)
	}
	before, _ := os.ReadFile(path)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Save(canceled, defaults); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("canceled save changed the previous settings")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".settings-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestSettingsRejectDamagedOrNewerFilesWithoutOverwriting(t *testing.T) {
	defaults := domain.DefaultSettings(t.TempDir())
	for _, data := range []string{
		`{`,
		`{"version":2,"settings":{}}`,
		`{"version":1,"settings":null}`,
		`{"version":1,"settings":{"previewMode":"unknown","downloadPath":"/tmp"}}`,
		`{"version":1,"settings":{"previewMode":"tile","downloadPath":"relative"}}`,
		`{"version":1,"settings":{"previewMode":"tile","downloadPath":"/tmp","extra":true}}`,
		`{"version":1,"settings":{"previewMode":"tile","downloadPath":"/tmp"}} {}`,
		strings.Repeat(" ", maxSettingsSize+1),
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		store := NewSettings(path, defaults)
		if _, err := store.Load(context.Background()); err == nil {
			t.Fatalf("accepted invalid settings: %.80q", data)
		}
		if err := store.Save(context.Background(), defaults); err == nil {
			t.Fatal("overwrote an unreadable settings file")
		}
		if after, _ := os.ReadFile(path); string(after) != data {
			t.Fatal("invalid settings were modified")
		}
	}
}
