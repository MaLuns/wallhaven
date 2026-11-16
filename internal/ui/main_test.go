package ui

import (
	"os"
	"testing"

	"github.com/egoist/mygo/i18n"
)

// General UI tests use Chinese accessibility labels. Keep their baseline
// independent of the runner's language; localization tests switch and restore
// their own language without changing real system or application preferences.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	os.Exit(m.Run())
}
