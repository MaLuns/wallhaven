package imagecache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerUsageResizeAndClearOnlyImageCaches(t *testing.T) {
	root := t.TempDir()
	thumbnails := New(diskOptions(filepath.Join(root, "thumbnails")))
	originals := New(diskOptions(filepath.Join(root, "originals")))
	thumbnails.Put("thumb", []byte("thumbnail"))
	originals.Put("original", []byte("original"))
	download := filepath.Join(root, "downloaded-wallpaper.png")
	if err := os.WriteFile(download, []byte("download"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(thumbnails, originals)
	usage, err := manager.Usage(context.Background())
	if err != nil || usage.ThumbnailEntries != 1 || usage.OriginalEntries != 1 || usage.ThumbnailBytes != int64(headerSize+len("thumbnail")) || usage.OriginalBytes != int64(headerSize+len("original")) {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	if err := manager.SetLimits(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	usage, err = manager.Usage(context.Background())
	if err != nil || usage.ThumbnailBytes != 0 || usage.OriginalBytes != 0 {
		t.Fatalf("smaller limits did not immediately evict images: %+v, %v", usage, err)
	}
	if err := manager.SetLimits(context.Background(), 1<<20, 1<<20); err != nil {
		t.Fatal(err)
	}
	thumbnails.Put("thumb", []byte("thumbnail"))
	originals.Put("original", []byte("original"))
	if err := manager.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	usage, err = manager.Usage(context.Background())
	if err != nil || usage.ThumbnailBytes != 0 || usage.OriginalBytes != 0 {
		t.Fatalf("clear left cached images: %+v, %v", usage, err)
	}
	if data, err := os.ReadFile(download); err != nil || string(data) != "download" {
		t.Fatal("cache management touched a downloaded wallpaper")
	}
}

func TestManagerReportsUnavailableCacheAndStillClearsOtherCache(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "file")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	thumbnails := New(diskOptions(filepath.Join(root, "thumbnails")))
	thumbnails.Put("thumb", []byte("thumbnail"))
	manager := NewManager(thumbnails, New(diskOptions(filepath.Join(blocked, "originals"))))
	usage, err := manager.Usage(context.Background())
	if err == nil || usage.ThumbnailEntries != 1 {
		t.Fatalf("unavailable disk was reported as successful zero usage: %+v, %v", usage, err)
	}
	before, _ := thumbnails.Stats()
	if err := manager.SetLimits(context.Background(), 128, 128); err == nil {
		t.Fatal("unavailable cache accepted changed limits")
	}
	after, _ := thumbnails.Stats()
	if after.Limit != before.Limit {
		t.Fatal("failed limit change retained a partial new limit")
	}
	if err := manager.Clear(context.Background()); err == nil {
		t.Fatal("partial clear did not report its failure")
	}
	if _, found := thumbnails.Get("thumb"); found {
		t.Fatal("clear abandoned the available cache after another failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Usage(ctx); err == nil {
		t.Fatal("cache usage ignored cancellation")
	}
}
