package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadDirectoryValidationDoesNotLeaveFiles(t *testing.T) {
	adapter := SettingsDirectories{}
	directory := t.TempDir()
	if err := adapter.Validate(context.Background(), directory); err != nil {
		t.Fatal(err)
	}
	if files, _ := os.ReadDir(directory); len(files) != 0 {
		t.Fatal("directory validation left temporary files")
	}
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, filepath.Join(directory, "missing")} {
		if err := adapter.Validate(context.Background(), path); err == nil {
			t.Fatalf("accepted a non-directory: %q", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.Validate(ctx, directory); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := (LoginItems{}).SetEnabled(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled login mutation reached the system")
	}
}
