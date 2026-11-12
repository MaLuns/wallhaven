package ui

import (
	"embed"
	"encoding/json"
	"sync"

	kit "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// Original brand and preview assets are retained from the Electron baseline;
// layout and interactions are drawn by MyGO, without a web renderer.
//
//go:embed assets/legacy/*
var legacyAssets embed.FS

var legacyAssetOnce sync.Once
var legacyBitmaps map[string]kit.ImageSource
var legacyLogs []struct {
	Title    string   `json:"title"`
	Time     string   `json:"time"`
	Children []string `json:"childer"`
}

func loadLegacyAssets() {
	legacyAssetOnce.Do(func() {
		legacyBitmaps = map[string]kit.ImageSource{}
		for _, name := range []string{"logo", "mini", "light", "dark", "empty"} {
			data, err := legacyAssets.ReadFile("assets/legacy/" + name + ".png")
			if err != nil {
				panic(err)
			}
			bitmap, err := kit.DecodeImage(data)
			if err != nil {
				panic(err)
			}
			legacyBitmaps[name] = bitmap
		}
		data, err := legacyAssets.ReadFile("assets/legacy/log.json")
		if err != nil {
			panic(err)
		}
		if err := json.Unmarshal(data, &legacyLogs); err != nil {
			panic(err)
		}
	})
}

type legacyColors struct{ Canvas, Header, Search, Text, Muted, Accent, Card, Border, Dialog kit.Color }

func legacyPalette(c *kit.Context) legacyColors {
	if c.Theme().Dark {
		return legacyColors{Canvas: hexColor("191d30"), Header: kit.RGBA(17, 20, 32, .88), Search: hexColor("292e47"), Text: hexColor("ffffff"), Muted: hexColor("888888"), Accent: hexColor("3da5ef"), Card: hexColor("1e2336"), Border: hexColor("787c90"), Dialog: hexColor("3b3f5f")}
	}
	return legacyColors{Canvas: hexColor("ffffff"), Header: hexColor("f6f7fa"), Search: hexColor("f3f7fa"), Text: hexColor("2c2e35"), Muted: hexColor("8c8c8c"), Accent: hexColor("40a8fe"), Card: hexColor("f3f7fa"), Border: hexColor("d8dae2"), Dialog: hexColor("ffffff")}
}

func (a *App) applyLegacyTheme(c *kit.Context) {
	dark := c.Theme().Dark
	if a.appearance != "" {
		dark = a.appearance == "dark"
	}
	theme := *kit.LightTheme()
	if dark {
		theme = *kit.DarkTheme()
	}
	theme.Dark = dark
	c.SetTheme(&theme)
	p := legacyPalette(c)
	theme.Background = p.Canvas
	theme.Surface = p.Search
	theme.Text = p.Text
	theme.TextMuted = p.Muted
	theme.Border = p.Border
	theme.Accent = p.Accent
	theme.Inverse, theme.InverseText = p.Dialog, p.Text
	theme.FontSize = 14
	c.SetTheme(&theme)
}

var frostedBackdrop = shader.MustCompile(`package main

var Tint vec4

func Fragment(dst vec4, pos vec2) vec4 {
	return vec4(Tint.rgb*Tint.a, Tint.a) + backdropSample(pos)*(1-Tint.a)
}
`)

// Blur only the content behind a panel, keeping its controls and text sharp.
func frostedBackground(tint kit.Color, radius float32) func(*kit.Painter, kit.Rect) {
	return func(painter *kit.Painter, bounds kit.Rect) {
		frostedBackdrop.Draw(painter, bounds, shader.Options{Uniforms: map[string]any{"Tint": tint}, Radius: radius, BackdropBlur: 20})
	}
}

func legacyIconButton(c *kit.Context, key, name, label string, iconSize ...float32) kit.Element {
	size := float32(18)
	if len(iconSize) > 0 {
		size = iconSize[0]
	}
	b := kit.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Size(32, 30).Padding(0).Radius(0).Center().Background(kit.Transparent)
	if b.Hovered() {
		b.TextColor(legacyPalette(c).Accent)
	}
	b.Children(func() { appIcon(c, name, size) })
	return b
}

func legacyChip(c *kit.Context, key, label string, selected bool) kit.Element {
	p := legacyPalette(c)
	b := kit.ButtonBase(c.Key(key)).Label(label).Tooltip(label).MinWidth(60).Height(30).Padding(0).Background(kit.Transparent)
	if selected {
		color := hexColor("3da7fe")
		if c.Theme().Dark {
			color = p.Text
		}
		b.TextColor(color).FontWeight(700)
	} else if b.Hovered() {
		b.TextColor(p.Accent)
	} else {
		b.TextColor(p.Text)
	}
	b.Children(func() {
		kit.Text(c, label).FontSize(14).IgnoreInput(true)
		if selected {
			kit.Column(c).Absolute().Bottom(0).Left(0).Right(0).Height(4).AlignItems(kit.Center).IgnoreInput(true).Children(func() { kit.Image(c, legacyBitmaps["mini"]).Size(30, 4).Fit(kit.Contain).IgnoreInput(true) })
		}
	})
	return b
}
