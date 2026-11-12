package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func wallpaperFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("image bytes validated by OriginalFiles"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetPassesFilePathOnlyAsData(t *testing.T) {
	name := "壁纸 'quoted' $(touch marker); \".png"
	if runtime.GOOS == "windows" {
		name = "壁纸 'quoted' $(touch marker);.png"
	}
	path := wallpaperFile(t, name)
	var calls int
	wallpaper := NewWithOptions(Options{Platform: "darwin", Runner: func(ctx context.Context, program string, args ...string) ([]byte, error) {
		calls++
		if _, exists := ctx.Deadline(); !exists {
			t.Error("wallpaper command had no time limit")
		}
		if program != "/usr/bin/osascript" || len(args) != 5 || args[0] != "-l" || args[1] != "JavaScript" || args[2] != "-e" || args[3] != wallpaperScript || args[4] != path {
			t.Fatalf("unsafe command contract: program=%s args=%q", program, args)
		}
		if strings.Contains(args[3], path) || strings.Contains(args[3], "System Events") || !strings.Contains(args[3], "$.NSScreen.screens") || !strings.Contains(args[3], "setDesktopImageURLForScreenOptionsError") {
			t.Fatal("script interpolated input or failed to use native AppKit screens")
		}
		return nil, nil
	}})
	if err := wallpaper.Set(context.Background(), path); err != nil || calls != 1 {
		t.Fatalf("Set error=%v, calls=%d", err, calls)
	}
}

func TestSetRejectsUnsupportedAndMissingFiles(t *testing.T) {
	called := false
	runner := func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil }
	if err := NewWithOptions(Options{Platform: "freebsd", Runner: runner}).Set(context.Background(), "anything"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported error=%v", err)
	}
	directory := t.TempDir()
	file := wallpaperFile(t, "valid.png")
	symlink := filepath.Join(directory, "symlink.png")
	linkErr := os.Symlink(file, symlink)
	empty := filepath.Join(directory, "empty.png")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	wallpaper := NewWithOptions(Options{Platform: "darwin", Runner: runner})
	paths := []string{"", filepath.Join(directory, "missing.png"), directory, empty}
	if linkErr == nil {
		paths = append(paths, symlink)
	}
	for _, path := range paths {
		if err := wallpaper.Set(context.Background(), path); err == nil {
			t.Errorf("accepted invalid local wallpaper %q", path)
		}
	}
	if called {
		t.Fatal("invalid file launched a wallpaper command")
	}
}

func TestSetReportsCommandFailureCancellationAndTimeout(t *testing.T) {
	path := wallpaperFile(t, "image.png")
	wallpaper := NewWithOptions(Options{Platform: "darwin", Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("private path and script error"), errors.New("secret command failure")
	}})
	if err := wallpaper.Set(context.Background(), path); err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("command failure exposed raw diagnostics: %v", err)
	}
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := wallpaper.Set(ctx, path); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		wallpaper := NewWithOptions(Options{Platform: "darwin", Timeout: 15 * time.Millisecond, Runner: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}})
		if err := wallpaper.Set(context.Background(), path); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error=%v", err)
		}
	})
}

func TestWallpaperScriptCompilesWithoutExecuting(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("JXA compiler is provided by macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// osacompile only compiles. It never calls run or changes the desktop.
	command := exec.CommandContext(ctx, "/usr/bin/osacompile", "-l", "JavaScript", "-e", wallpaperScript, "-o", filepath.Join(t.TempDir(), "wallpaper.scpt"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("JXA compile failed: %v %s", err, output)
	}
}

func TestAppKitContractWithoutSettingWallpaper(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("AppKit is provided by macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// This read-only probe checks the ObjC bridge and thread. It does not
	// invoke setDesktopImageURL, launch another app, or request automation.
	const probe = `ObjC.import("AppKit"); function run(argv) { return JSON.stringify({main:$.NSThread.isMainThread,setter:$.NSWorkspace.sharedWorkspace.respondsToSelector("setDesktopImageURL:forScreen:options:error:"),options:$.NSWorkspace.sharedWorkspace.respondsToSelector("desktopImageOptionsForScreen:"),screens:Number($.NSScreen.screens.count),argument:argv[0]}); }`
	const argument = "壁纸 'quoted' $(touch marker); \".png"
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", probe, argument).CombinedOutput()
	if err != nil {
		t.Fatalf("read-only AppKit probe failed: %v %s", err, output)
	}
	var result struct {
		Main, Setter, Options bool
		Screens               int
		Argument              string
	}
	if err := json.Unmarshal(output, &result); err != nil || !result.Main || !result.Setter || !result.Options || result.Argument != argument {
		t.Fatalf("AppKit bridge contract unavailable: %s (%v)", output, err)
	}
}
