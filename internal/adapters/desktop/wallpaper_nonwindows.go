//go:build !windows

package desktop

import "context"

func setWindowsWallpaper(context.Context, string) error { return ErrUnsupported }
