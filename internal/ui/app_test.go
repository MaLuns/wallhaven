package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	kit "github.com/egoist/mygo/ui"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

// Exercise navigation through the native controls, including pages disappearing
// and returning, to detect stale elements and mismatched UI/application state.
func TestShellNavigation(t *testing.T) {
	controller := app.New()
	screen := New(controller)
	tester := kit.NewTester(screen.View, 1200, 800)
	for _, dark := range []bool{false, true} {
		tester.SetDark(dark)
		tester.SetSize(800, 540)
		for round := 0; round < 2; round++ {
			for _, page := range domain.Pages() {
				if page.ID == domain.Downloads || page.ID == domain.Settings {
					click(t, tester, "功能菜单")
				}
				if err := tester.Click("前往" + page.Title); err != nil {
					t.Fatal(err)
				}
				tester.Frame()
				tester.Frame()
				if controller.CurrentPage().ID != page.ID || !tester.HasText("One Wallhaven") {
					t.Fatalf("navigation to %s: current=%s, texts=%q", page.ID, controller.CurrentPage().ID, tester.Texts())
				}
				label := "前往" + page.Title
				if page.ID == domain.Downloads || page.ID == domain.Settings {
					label = "功能菜单"
				}
				bounds, ok := tester.Find(label)
				if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 800 || bounds.Y+bounds.H > 540 {
					t.Fatalf("navigation %s is outside the minimum window: %+v", page.ID, bounds)
				}
			}
		}
	}
}

// Export only when requested, keeping generated previews out of the source tree.
func TestShellPreview(t *testing.T) {
	directory := os.Getenv("WALLHAVEN_PREVIEW_DIR")
	if directory == "" {
		t.Skip("set WALLHAVEN_PREVIEW_DIR to export UI previews")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		screen := New(app.New())
		tester := kit.NewTesterWith(screen.View, 1200, 800, kit.TesterOptions{Dark: dark})
		name := "shell-light.png"
		if dark {
			name = "shell-dark.png"
		}
		file, err := os.Create(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, tester.Image())
		closeErr := file.Close()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		navigation, _ := tester.Find("前往壁纸")
		tester.Move(navigation.X+navigation.W/2, navigation.Y+navigation.H/2)
		tester.Advance(650 * time.Millisecond)
		writeListPreview(t, tester, filepath.Join(directory, "tooltip-"+name))
	}
}
