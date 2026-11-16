// Package locale translates application text using the language selected by MyGO.
package locale

import (
	_ "embed"
	"encoding/json"

	"github.com/egoist/mygo/i18n"

	"github.com/MaLuns/wallhaven-electron/internal/locale/catalogs"
)

// The source catalog doubles as a lookup from existing Chinese copy to stable
// message keys. Identifiers, API values and user paths never enter this index.
//
//go:embed catalogs/zh-Hans.json
var sourceJSON []byte

var sourceKeys = func() map[string]string {
	var messages map[string]string
	if err := json.Unmarshal(sourceJSON, &messages); err != nil {
		panic(err)
	}
	keys := make(map[string]string, len(messages))
	for key, source := range messages {
		if len(key) > 0 && key[0] != '@' {
			keys[source] = key
		}
	}
	return keys
}()

// T translates source into the app's current language. Unknown text, including
// user-provided paths and server responses, is returned unchanged.
func T(source string) string { return In(i18n.Locale(), source) }

// In translates source into a specific language, using the catalog's fallback.
func In(tag, source string) string {
	key, ok := sourceKeys[source]
	if !ok {
		return source
	}
	return catalogs.Catalog.Format(tag, key, nil)
}
