//go:build windows

package desktop

import "time"

// Implement previewPlatform here to add Windows desktop surfaces and reversible
// application visibility. The common viewer and lifecycle need no changes.
func newPreviewPlatform(func(time.Duration, func()) func()) previewPlatform {
	return unsupportedPreviewPlatform{}
}
