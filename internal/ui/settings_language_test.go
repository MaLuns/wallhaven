package ui

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/i18n"
	kit "github.com/egoist/mygo/ui"
)

// The callback state stands in for the native application's saved language.
// These tests exercise the settings UI without starting the platform backend.
type languageSettingsHarness struct {
	*settingsHarness
	chosen, system string
	changes        []string
}

func newLanguageSettingsHarness(t *testing.T, system, chosen string) *languageSettingsHarness {
	t.Helper()
	previous := i18n.Locale()
	h := &languageSettingsHarness{system: system, chosen: chosen}
	h.applyLocale()
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return previous }) })
	h.settingsHarness = newSettingsHarness(t)
	h.screen.chosenLanguage = func() string { return h.chosen }
	h.screen.onChangeLanguage = h.choose
	h.settings.Frame()
	return h
}

func (h *languageSettingsHarness) applyLocale() {
	i18n.SetLocaleFunc(func() string {
		if h.chosen != "" {
			return h.chosen
		}
		return h.system
	})
}

func (h *languageSettingsHarness) choose(tag string) {
	h.chosen = tag
	h.changes = append(h.changes, tag)
	h.applyLocale()
}

func requireLanguageValue(t *testing.T, tester *kit.Tester, label, value string) {
	t.Helper()
	info, ok := tester.Access(label)
	if !ok || info.Role != kit.RolePopUpButton || info.Value != value || info.Disabled {
		t.Fatalf("language control %q = %+v, found %v; want enabled popup value %q", label, info, ok, value)
	}
}

func requireSettingsControlBounds(t *testing.T, tester *kit.Tester, labels ...string) {
	t.Helper()
	for _, label := range labels {
		bounds, ok := tester.Find(label)
		if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.X+bounds.W > 450 || bounds.Y < 0 || bounds.Y+bounds.H > 340 {
			t.Fatalf("setting %q is outside the 450×340 window: %+v", label, bounds)
		}
	}
}

func TestSettingsLanguageDefaultsToSystemAndEscapeKeepsChoice(t *testing.T) {
	h := newLanguageSettingsHarness(t, "zh-Hans", "")
	requireLanguageValue(t, h.settings, "界面语言", "跟随系统")
	if h.chosen != "" || len(h.changes) != 0 {
		t.Fatal("showing the default language selector saved an explicit language")
	}
	// A changed system language changes the displayed copy while keeping the
	// empty override, so System remains distinct from an explicit English choice.
	h.system = "en"
	h.settings.Frame()
	requireLanguageValue(t, h.settings, "Interface language", "System default")
	click(t, h.settings, "Interface language")
	for _, title := range []string{"System default", "简体中文", "English"} {
		option, ok := h.settings.Access(title)
		if !ok || option.Role != kit.RoleOption {
			t.Fatalf("language option %q is missing or is not an option: %+v", title, option)
		}
	}
	h.settings.Key(0, kit.KeyDown)
	h.settings.Key(0, kit.KeyEscape)
	if h.chosen != "" || len(h.changes) != 0 {
		t.Fatalf("Escape changed the language: chosen=%q changes=%q", h.chosen, h.changes)
	}
	if info, _ := h.settings.Access("Interface language"); info.Expanded {
		t.Fatal("Escape did not dismiss the language popup")
	}
	requireLanguageValue(t, h.settings, "Interface language", "System default")
}

func TestSettingsLanguageSwitchesBothViewsAndUsesSupportedTags(t *testing.T) {
	h := newLanguageSettingsHarness(t, "zh-Hans", "")
	click(t, h.settings, "界面语言")
	click(t, h.settings, "English")
	h.tester.Frame()
	h.settings.Frame()
	if h.chosen != "en" {
		t.Fatalf("English option did not save its exact tag: chosen=%q changes=%q", h.chosen, h.changes)
	}
	if !h.settings.HasText("Settings") || !h.settings.HasText("Language") || !h.tester.HasText("Featured") {
		t.Fatal("English choice did not immediately translate settings and main navigation")
	}
	requireLanguageValue(t, h.settings, "Interface language", "English")
	requireSettingsControlBounds(t, h.settings, "Interface language", "Close settings", "Waterfall", "Start at login switch", "Change folder", "Show folder")

	click(t, h.settings, "Interface language")
	click(t, h.settings, "简体中文")
	h.tester.Frame()
	h.settings.Frame()
	if h.chosen != "zh-Hans" {
		t.Fatalf("Chinese option did not save its exact tag: chosen=%q changes=%q", h.chosen, h.changes)
	}
	if !h.settings.HasText("设置") || !h.tester.HasText("推荐") {
		t.Fatal("Chinese choice did not immediately translate settings and main navigation")
	}
	requireLanguageValue(t, h.settings, "界面语言", "简体中文")
	requireSettingsControlBounds(t, h.settings, "界面语言", "关闭设置", "瀑布流模式", "开机启动开关", "修改目录", "打开文件位置")

	h.system = "en"
	click(t, h.settings, "界面语言")
	click(t, h.settings, "跟随系统")
	h.tester.Frame()
	h.settings.Frame()
	if h.chosen != "" || i18n.Locale() != "en" || !h.tester.HasText("Featured") {
		t.Fatalf("System did not clear the override and use the current system locale: chosen=%q locale=%q", h.chosen, i18n.Locale())
	}
	requireLanguageValue(t, h.settings, "Interface language", "System default")
	if !slices.Equal(h.changes, []string{"en", "zh-Hans", ""}) {
		t.Fatalf("selector emitted unsupported or duplicate tags: %q", h.changes)
	}
}

func TestSettingsLanguageReopenedViewReadsSavedChoice(t *testing.T) {
	h := newLanguageSettingsHarness(t, "zh-Hans", "")
	click(t, h.settings, "界面语言")
	click(t, h.settings, "English")
	// A fresh UI instance has no local select state. It must read the chosen
	// language callback, as a reopened settings window reads the app's choice.
	reopened := New(h.controller, Options{
		Preferences: h.prefs, ChosenLanguage: func() string { return h.chosen }, OnChangeLanguage: h.choose,
	})
	t.Cleanup(reopened.Close)
	settings := kit.NewTester(reopened.SettingsView, 450, 340)
	requireLanguageValue(t, settings, "Interface language", "English")
	if h.chosen != "en" || !slices.Equal(h.changes, []string{"en"}) {
		t.Fatalf("reopening settings mutated the saved callback choice: chosen=%q changes=%q", h.chosen, h.changes)
	}
	click(t, settings, "Interface language")
	settings.Key(0, kit.KeyHome)
	settings.Key(0, kit.KeyEnter)
	settings.Frame()
	if h.chosen != "" || i18n.Locale() != "zh-Hans" {
		t.Fatalf("keyboard System choice did not clear the saved override: chosen=%q locale=%q", h.chosen, i18n.Locale())
	}
	requireLanguageValue(t, settings, "界面语言", "跟随系统")
	click(t, settings, "界面语言")
	settings.Key(0, kit.KeyEnd)
	settings.Key(0, kit.KeyEnter)
	settings.Frame()
	requireLanguageValue(t, settings, "Interface language", "English")
	click(t, settings, "Interface language")
	settings.Key(0, kit.KeyHome)
	settings.Key(0, kit.KeyDown)
	settings.Key(0, kit.KeyEnter)
	settings.Frame()
	requireLanguageValue(t, settings, "界面语言", "简体中文")
	if !slices.Equal(h.changes, []string{"en", "", "en", "zh-Hans"}) {
		t.Fatalf("keyboard choices emitted wrong or duplicate saved tags: %q", h.changes)
	}
}
