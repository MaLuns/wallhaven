package ui

import kit "github.com/egoist/mygo/ui"

// Every functional icon uses the same 24-unit canvas, rounded strokes and
// optical padding. SVGs are parsed once and drawn by MyGO in inherited text
// color; no font glyphs or bitmap sizes participate in their layout.
var appIcons = func() map[string]*kit.SVG {
	paths := map[string]string{
		"minimize": `<path d="M5 12h14"/>`,
		"refresh":  `<path d="M20 11a8 8 0 1 0-2.34 6.66M20 4v7h-7"/>`,
		"wallpaper": `<rect x="3" y="4" width="18" height="12" rx="2"/>
<path d="m6 13 4-4 3 3 2-2 3 3M8 20h8m-4-4v4"/>`,
		"back":         `<path d="M20 12H4m7-7-7 7 7 7"/>`,
		"download":     `<path d="M12 3v12m-5-5 5 5 5-5M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>`,
		"search":       `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>`,
		"about":        `<circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10h.01"/>`,
		"settings":     `<path d="m9 3-.7 2.3-2.1 1.2L4 6 3 9l1.8 1.6v2.8L3 15l1 3 2.2-.5 2.1 1.2L9 21h6l.7-2.3 2.1-1.2 2.2.5 1-3-1.8-1.6v-2.8L21 9l-1-3-2.2.5-2.1-1.2L15 3Z"/><circle cx="12" cy="12" r="3"/>`,
		"log":          `<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M8.5 8h7m-7 4h7m-7 4h4"/>`,
		"theme":        `<path d="M12 3a9 9 0 1 0 0 18h1.3a2.3 2.3 0 0 0 1.4-4.1 1.5 1.5 0 0 1 1-2.6H18a3 3 0 0 0 3-3A8.5 8.5 0 0 0 12 3Z"/><path d="M7 10h.01M10 6.8h.01M14.5 7h.01M17 10.5h.01"/>`,
		"menu":         `<path d="M4 6h16M4 12h16M4 18h16"/>`,
		"close":        `<path d="m6 6 12 12M18 6 6 18"/>`,
		"favorite":     `<path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-2.9-5.6 2.9 1.1-6.2L3 9.6l6.2-.9Z"/>`,
		"reset":        `<path d="M4 11a8 8 0 1 1 2.34 6.66M4 4v7h7"/>`,
		"check":        `<path d="m5 12 4 4L19 6"/>`,
		"chevron-up":   `<path d="m5 15 7-7 7 7"/>`,
		"chevron-down": `<path d="m5 9 7 7 7-7"/>`,
		"left":         `<path d="m15 5-7 7 7 7"/>`,
		"right":        `<path d="m9 5 7 7-7 7"/>`,

		"desktop-preview": `<path d="M2 12C4 7.5 7.5 5 12 5s8 2.5 10 7c-2 4.5-5.5 7-10 7S4 16.5 2 12Z"/><circle cx="12" cy="12" r="3"/>`,
	}
	icons := make(map[string]*kit.SVG, len(paths)+3)
	for name, path := range paths {
		icons[name] = kit.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.85" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
	}
	icons["filter-up"] = icons["chevron-up"]
	icons["filter-down"] = icons["chevron-down"]
	icons["clear"] = icons["close"]
	return icons
}()

func appIcon(c *kit.Context, name string, size float32) kit.Element {
	icon := appIcons[name]
	if icon == nil {
		icon = appIcons["about"]
	}
	return kit.Icon(c, icon).Size(size, size).Shrink(0).AlignSelf(kit.Center).IgnoreInput(true)
}
