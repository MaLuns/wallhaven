package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

func favoriteFixture() domain.Wallpaper {
	return domain.Wallpaper{
		ID: "abc123", URL: "https://wallhaven.cc/w/abc123", Path: "https://w.wallhaven.cc/full/ab/wallhaven-abc123.png",
		Thumbnail: "https://th.wallhaven.cc/small/ab/abc123.jpg", OriginalThumbnail: "https://th.wallhaven.cc/orig/ab/abc123.jpg",
		Resolution: "3840x2160", FileType: "image/png", Category: "general", Purity: "sfw", Width: 3840, Height: 2160, FileSize: 7654321,
	}
}

func readFavoritesBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFavoritesRoundTripReloadAndPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "favorites.json")
	store := NewFavorites(path)
	got, err := store.Load(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: got=%v err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("loading a missing collection created a directory")
	}
	want := []domain.Wallpaper{favoriteFixture()}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err = NewFavorites(path).Load(context.Background())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded collection changed fields: got=%+v err=%v", got, err)
	}
	if runtime.GOOS != "windows" {
		for _, tc := range []struct {
			path string
			mode os.FileMode
		}{{filepath.Dir(path), 0700}, {path, 0600}} {
			info, err := os.Stat(tc.path)
			if err != nil || info.Mode().Perm() != tc.mode {
				t.Fatalf("private permissions: %s got=%v err=%v", tc.path, info, err)
			}
		}
	}
	if err := store.Save(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("empty collection: got=%v err=%v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "favorites.json" {
		t.Fatalf("temporary files remained: entries=%v err=%v", entries, err)
	}
}

func TestFavoritesDeduplicatesIDsWithoutChangingFirstRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	first := favoriteFixture()
	duplicate := first
	duplicate.Path = "different-original.png"
	other := first
	other.ID = "def456"
	input := []domain.Wallpaper{first, duplicate, other, first}
	store := NewFavorites(path)
	if err := store.Save(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	want := []domain.Wallpaper{first, other}
	if got, err := store.Load(context.Background()); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("save deduplication: got=%+v err=%v", got, err)
	}
	// A valid older file may already contain duplicates; loading normalizes
	// them without silently rewriting any data on disk.
	data, _ := json.Marshal(favoritesFile{Version: favoritesVersion, Wallpapers: input})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(context.Background()); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("load deduplication: got=%+v err=%v", got, err)
	}
	if !bytes.Equal(readFavoritesBytes(t, path), data) {
		t.Fatal("load modified the collection file")
	}
}

func TestFavoritesRefusesInvalidExistingFilesWithoutOverwriting(t *testing.T) {
	for name, data := range map[string]string{
		"truncated":       `{"version":1,"wallpapers":[`,
		"version":         `{"version":2,"wallpapers":[]}`,
		"missing-version": `{"wallpapers":[]}`,
		"missing-list":    `{"version":1}`,
		"null-list":       `{"version":1,"wallpapers":null}`,
		"blank-id":        `{"version":1,"wallpapers":[{"ID":" "}]}`,
		"trailing-data":   `{"version":1,"wallpapers":[]} {}`,
		"unknown-field":   `{"version":1,"wallpapers":[],"future":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "favorites.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewFavorites(path)
			if _, err := store.Load(context.Background()); err == nil {
				t.Fatal("invalid file was accepted")
			}
			if err := store.Save(context.Background(), []domain.Wallpaper{favoriteFixture()}); err == nil {
				t.Fatal("save overwrote an invalid existing file")
			}
			if string(readFavoritesBytes(t, path)) != data {
				t.Fatal("invalid existing data was modified")
			}
		})
	}
}

func TestFavoritesInvalidInputAndCancellationKeepPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	store := NewFavorites(path)
	if err := store.Save(context.Background(), []domain.Wallpaper{favoriteFixture()}); err != nil {
		t.Fatal(err)
	}
	previous := readFavoritesBytes(t, path)
	for _, id := range []string{"", " \t"} {
		if err := store.Save(context.Background(), []domain.Wallpaper{{ID: id}}); err == nil {
			t.Fatal("empty ID was accepted")
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Load(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load error = %v", err)
	}
	if err := store.Save(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v", err)
	}
	// Cancel after a temporary file exists, before the replacement commits.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	staged := &cancelWhenStaged{Context: ctx, cancel: cancel, directory: filepath.Dir(path)}
	if err := store.Save(staged, nil); !errors.Is(err, context.Canceled) || !staged.observed {
		t.Fatalf("staged cancellation: observed=%t err=%v", staged.observed, err)
	}
	if !bytes.Equal(readFavoritesBytes(t, path), previous) {
		t.Fatal("failed save replaced the previous collection")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("canceled save left temporary files: entries=%v err=%v", entries, err)
	}
}

type cancelWhenStaged struct {
	context.Context
	cancel    context.CancelFunc
	directory string
	observed  bool
}

func (c *cancelWhenStaged) Err() error {
	entries, _ := os.ReadDir(c.directory)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".favorites-") {
			c.observed = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestFavoritesEnforcesSizeAndCountLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	store := NewFavorites(path)
	tooBig := favoriteFixture()
	tooBig.URL = strings.Repeat("a", maxFavoritesSize+1)
	if err := store.Save(context.Background(), []domain.Wallpaper{tooBig}); err == nil {
		t.Fatal("oversized collection was accepted")
	}
	tooMany := make([]domain.Wallpaper, maxFavorites+1)
	for i := range tooMany {
		tooMany[i].ID = fmt.Sprintf("id-%d", i)
	}
	if err := store.Save(context.Background(), tooMany); err == nil {
		t.Fatal("too many favorites were accepted")
	}
	tooManyData, err := json.Marshal(favoritesFile{Version: favoritesVersion, Wallpapers: tooMany})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tooManyData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("too many stored records were accepted")
	}
	if err := store.Save(context.Background(), nil); err == nil {
		t.Fatal("excessive stored records were overwritten")
	}
	if !bytes.Equal(readFavoritesBytes(t, path), tooManyData) {
		t.Fatal("rejected excessive collection was modified")
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxFavoritesSize + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("oversized file was accepted")
	}
	if err := store.Save(context.Background(), nil); err == nil {
		t.Fatal("oversized existing file was overwritten")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != maxFavoritesSize+1 {
		t.Fatalf("oversized file changed: info=%v err=%v", info, err)
	}
}

func TestFavoritesWriteFailureKeepsPreviousFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory permissions without root bypass")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "favorites.json")
	store := NewFavorites(path)
	if err := store.Save(context.Background(), []domain.Wallpaper{favoriteFixture()}); err != nil {
		t.Fatal(err)
	}
	previous := readFavoritesBytes(t, path)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0700)
	if err := store.Save(context.Background(), nil); err == nil {
		t.Fatal("save to an unwritable directory succeeded")
	}
	if !bytes.Equal(readFavoritesBytes(t, path), previous) {
		t.Fatal("write failure replaced the previous collection")
	}
}

func TestFavoritesConcurrentAccessKeepsCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	store := NewFavorites(path)
	var workers sync.WaitGroup
	for i := range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			wallpaper := favoriteFixture()
			wallpaper.ID = fmt.Sprintf("id-%d", i)
			if err := store.Save(context.Background(), []domain.Wallpaper{wallpaper}); err != nil {
				t.Error(err)
			}
			if got, err := store.Load(context.Background()); err != nil || len(got) != 1 {
				t.Errorf("concurrent load: got=%v err=%v", got, err)
			}
		}()
	}
	workers.Wait()
	if got, err := NewFavorites(path).Load(context.Background()); err != nil || len(got) != 1 {
		t.Fatalf("final file was incomplete: got=%v err=%v", got, err)
	}
}
