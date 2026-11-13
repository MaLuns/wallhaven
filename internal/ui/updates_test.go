package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	kit "github.com/egoist/mygo/ui"
)

func TestCheckForUpdatesFromMenu(t *testing.T) {
	for _, dark := range []bool{false, true} {
		checks := 0
		screen := New(app.New(), Options{Version: "1.2.3", OnCheckForUpdates: func() { checks++ }})
		tester := kit.NewTesterWith(screen.View, 800, 540, kit.TesterOptions{Dark: dark})
		click(t, tester, "功能菜单")
		button, ok := tester.Find("更新")
		if !ok || button.X < 0 || button.Y < 0 || button.X+button.W > 800 || button.Y+button.H > 540 {
			t.Fatalf("update button is outside the minimum window: %+v", button)
		}
		about, ok := tester.Find("关于")
		if !ok || button.X <= about.X || button.Y != about.Y {
			t.Fatalf("update belongs to the empty slot right of About: update=%+v about=%+v", button, about)
		}
		if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			name := "updates-menu-light.png"
			if dark {
				name = "updates-menu-dark.png"
			}
			writeListPreview(t, tester, filepath.Join(directory, name))
		}
		click(t, tester, "更新")
		tester.Frame()
		if checks != 1 || screen.menuOpen {
			t.Fatalf("checks=%d, menu still open=%v", checks, screen.menuOpen)
		}
		click(t, tester, "功能菜单")
		click(t, tester, "关于")
		if !tester.HasText("当前版本：1.2.3") {
			t.Fatalf("current release version is missing: %q", tester.Texts())
		}
	}
}
