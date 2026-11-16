package locale

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/i18n"
)

func TestCatalogsTranslateEverySourceAndPreserveFormats(t *testing.T) {
	data, err := os.ReadFile("catalogs/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var english map[string]string
	if err := json.Unmarshal(data, &english); err != nil {
		t.Fatal(err)
	}
	verbs := regexp.MustCompile(`%(?:\[[0-9]+\])?[+\-# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)
	for source, key := range sourceKeys {
		translation, ok := english[key]
		if !ok || strings.TrimSpace(translation) == "" {
			t.Errorf("missing English translation for %q (%s)", source, key)
			continue
		}
		if got := In("en-US", source); got != translation {
			t.Errorf("English translation of %q = %q, want %q", source, got, translation)
		}
		if got := In("zh-CN", source); got != source {
			t.Errorf("Chinese translation of %q = %q", source, got)
		}
		if !slices.Equal(verbs.FindAllString(source, -1), verbs.FindAllString(translation, -1)) {
			t.Errorf("format verbs changed: %q -> %q", source, translation)
		}
		if _, err := i18n.ParseMessage(translation); err != nil {
			t.Errorf("invalid English message %q: %v", translation, err)
		}
	}
}

func TestTranslationFallbackPreservesUserText(t *testing.T) {
	for _, source := range []string{"", "/Users/用户/Pictures/壁纸.png", "my tag 设置", "wallhaven-abc123"} {
		if got := In("en", source); got != source {
			t.Errorf("user text changed: %q -> %q", source, got)
		}
	}
	if got := In("fr", "设置"); got != "设置" {
		t.Errorf("unsupported language fallback = %q", got)
	}
}
