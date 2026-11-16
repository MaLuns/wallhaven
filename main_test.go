package main

import (
	"testing"

	"github.com/egoist/mygo/i18n"
)

func TestDownloadDirectoryDialogPreservesNativeLabels(t *testing.T) {
	current := t.TempDir()
	previous := i18n.Locale()
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return previous }) })
	for _, test := range []struct{ language, title string }{
		{"zh-Hans", "选择下载目录"},
		{"en", "Choose download folder"},
	} {
		i18n.SetLocaleFunc(func() string { return test.language })
		options := downloadDirectoryDialogOptions(nil, current)
		if options.Title != test.title || options.DefaultPath != current || !options.Directory || !options.CreateDirectories {
			t.Fatalf("%s folder picker options = %+v", test.language, options)
		}
		if options.ButtonLabel != "" || options.Message != "" {
			t.Fatalf("folder picker overrides native labels: %+v", options)
		}
	}
}
