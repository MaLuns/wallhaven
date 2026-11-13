//go:build !darwin && !windows && !linux

package desktop

import "time"

func newPreviewPlatform(func(time.Duration, func()) func()) previewPlatform {
	return unsupportedPreviewPlatform{}
}
