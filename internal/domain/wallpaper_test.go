package domain

import "testing"

func TestNormalizeSearchClonesAndCleansFilters(t *testing.T) {
	input := SearchQuery{Query: " +猫 -snow ", Purity: "000", Color: " #FFCC33 ", Resolutions: []string{"1920x1080 ", "", "1920x1080"}, Sorting: "toplist", TopRange: "1y "}
	got := input.Normalize()
	if got.Query != "+猫 -snow" || got.Categories != "111" || got.Purity != "100" || got.Order != "desc" || got.TopRange != "1y" || got.Color != "ffcc33" || len(got.Resolutions) != 1 {
		t.Fatalf("normalized query: %#v", got)
	}
	got.Resolutions[0] = "3840x2160"
	if input.Resolutions[0] != "1920x1080 " {
		t.Fatal("normalization retained caller's mutable slice")
	}
}
