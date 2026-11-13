package ports

// DesktopPreview owns the temporary desktop surfaces and the system state it
// changes. All methods and completions run on the UI thread. Open may partially
// succeed; Close must undo that work, including an unfinished Open, and be safe
// to retry after an error. It must never apply a system wallpaper.
type DesktopPreview interface {
	Available() bool
	Open(complete func(error))
	Close(complete func(error))
}
