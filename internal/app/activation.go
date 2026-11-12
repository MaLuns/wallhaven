package app

// ActivationWindow is the small UI-thread-only surface needed when a second
// launch asks the running application to become visible.
type ActivationWindow interface {
	IsMinimized() bool
	Restore()
	Show()
	Focus()
}

func ActivateWindow(window ActivationWindow) {
	if window.IsMinimized() {
		window.Restore()
	}
	window.Show()
	window.Focus()
}
