package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/app"
	"github.com/MaLuns/wallhaven-electron/internal/changelog"
	kit "github.com/egoist/mygo/ui"
)

func TestReleaseLogFromJSON(t *testing.T) {
	entries := changelog.Entries()
	for _, dark := range []bool{false, true} {
		screen := New(app.New())
		tester := kit.NewTesterWith(screen.View, 800, 540, kit.TesterOptions{Dark: dark})
		click(t, tester, "功能菜单")
		click(t, tester, "日志")
		if !tester.HasText("v" + entries[0].Version + "  " + entries[0].Date) {
			t.Fatalf("current JSON release is missing: %q", tester.Texts())
		}
		if !tester.HasText("· " + entries[0].Changes[0]) {
			t.Fatalf("the JSON log content is missing: %q", tester.Texts())
		}
		if directory := os.Getenv("WALLHAVEN_PREVIEW_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			name := "release-log-light.png"
			if dark {
				name = "release-log-dark.png"
			}
			writeListPreview(t, tester, filepath.Join(directory, name))
		}
	}
}
