package domain

import "time"

// TransferKind distinguishes user downloads from files retained for the desktop.
type TransferKind string

const (
	DownloadTransfer  TransferKind = "download"
	WallpaperTransfer TransferKind = "wallpaper"
)

type Transfer struct {
	Kind                                 TransferKind
	Wallpaper                            Wallpaper
	Received, Total                      int64
	Path, Error                          string
	Running, Queued, Canceled, Completed bool
	Paused, Stopping                     bool
	Directory                            string
	BytesPerSecond                       float64
	CreatedAt, CompletedAt               time.Time
}
