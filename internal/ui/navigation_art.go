package ui

import kit "github.com/egoist/mygo/ui"

const navigationBadgeScale = float32(5.0 / 6)

// The selected navigation scales the original 96×48 artwork to 80×40. Only
// vector decoration lives in SVG; the Chinese title remains native text so
// the backing scale never enlarges a rasterized label.
var navigationCard = kit.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="96" height="48" viewBox="0 0 96 48">
<defs><clipPath id="card"><path d="M10 0h77c6 0 9 4 8 10l-3 29c-.6 6-3.7 9-10 9H9c-6 0-9-4-8-10L3 9c.5-6 2-9 7-9Z"/></clipPath></defs>
<path d="M10 0h77c6 0 9 4 8 10l-3 29c-.6 6-3.7 9-10 9H9c-6 0-9-4-8-10L3 9c.5-6 2-9 7-9Z" fill="#2197f9"/>
<g clip-path="url(#card)">
<path d="M11 3h75c4.5 0 6.5 2.5 6 7l-2.8 28c-.4 4.7-2.6 7-7.2 7H10c-4.5 0-6.5-2.5-6-7L6 10c.3-4.6 1-7 5-7Z" fill="#3da7fe"/>
<path d="M3 32 94 14v12L2 44Z" fill="#5ab4fe" opacity=".72"/>
<path d="M4 32 94 14v3L3 35Z" fill="#83c8ff" opacity=".26"/>
<path d="M11 3h75" stroke="#71c0ff" stroke-width="1" stroke-linecap="round" opacity=".75"/>
<path d="M64 43h14m6 0h5" stroke="#fff" stroke-width="2" stroke-linecap="round" opacity=".95"/>
</g></svg>`))

var navigationMarks = func() map[string]*kit.SVG {
	marks := map[string]string{
		"recommend": `<path d="m17 3 3.6 8.2 8.8-1.4-5.7 7 4.7 7.9-8.8-2.8-5.9 7-.3-9.3-8.3-3.7 8.5-4.1Z"/>
<path d="m2 15 5-2-2 5Zm5 12 4-2-2 5Z" opacity=".82"/>`,
		"wallpaper": `<path d="m15 3 4 10-9 1Zm7 2 9 7-11 3Zm7 15-6 10-4-11ZM13 20l1 11-10-8Z"/>
<path d="M3 5h3v3H3Zm3 23h2v2H6Z" opacity=".82"/>`,
		"component": `<path d="M10 9h5V7a3 3 0 1 1 6 0v2h7v6h-2a3 3 0 0 0 0 6h2v7H10v-6H8a3 3 0 1 1 0-6h2Z"/>
<path d="M5 3H2v3m27 20v3h-3" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" opacity=".82"/>`,
		"my": `<path d="M5 8v8a5 5 0 0 0 10 0V8m5 0v18m0-13a5 5 0 1 0 10 0 5 5 0 1 0-10 0" fill="none" stroke="#fff" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"/>
<rect x="1" y="3" width="3" height="3" rx=".5" opacity=".82"/><rect x="27" y="28" width="2.5" height="2.5" rx=".5" opacity=".82"/>`,
	}
	result := make(map[string]*kit.SVG, len(marks))
	for name, mark := range marks {
		result[name] = kit.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32" fill="#fff">` + mark + `</svg>`))
	}
	return result
}()

func navigationBadge(c *kit.Context, key, title string) {
	mark := navigationMarks[key]
	if mark == nil {
		mark = navigationMarks["recommend"]
	}
	kit.Box(c.Key("selected-"+key)).Size(80, 40).Shrink(0).IgnoreInput(true).Children(func() {
		kit.Image(c, navigationCard).Fill().IgnoreInput(true)
		kit.Image(c, mark).Absolute().Left(7*navigationBadgeScale).Top(8*navigationBadgeScale).Size(32*navigationBadgeScale, 32*navigationBadgeScale).IgnoreInput(true)
		kit.Row(c).Absolute().Left(40 * navigationBadgeScale).Right(7 * navigationBadgeScale).Top(0).Bottom(0).Center().IgnoreInput(true).Children(func() {
			kit.Text(c, title).FontSize(17).FontWeight(800).TextColor(kit.RGB(255, 255, 255)).SingleLine().IgnoreInput(true)
		})
	})
}
