//go:build linux

package desktop

import "time"

// Implement previewPlatform here for supported desktop sessions. Unsupported
// compositors must fail before hiding applications or creating desktop surfaces.
func newPreviewPlatform(func(time.Duration, func()) func()) previewPlatform {
	return unsupportedPreviewPlatform{}
}
