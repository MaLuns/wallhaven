package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo"
)

// These synchronous callbacks model native message dispatch reentering the
// preview lifecycle while Explorer is creating or showing a desktop surface.
// No real windows or desktop state are involved.
type previewSessionPlatform struct {
	newWindow  func(mygo.WindowOptions) (*mygo.Window, error)
	showWindow func(*mygo.Window) error
	restores   int
}

func (*previewSessionPlatform) available() bool                             { return true }
func (*previewSessionPlatform) prepareWindow(uintptr, mygo.Rectangle) error { return nil }
func (*previewSessionPlatform) hideApplications(done func(error))           { done(nil) }
func (p *previewSessionPlatform) restoreApplications(done func(error)) {
	p.restores++
	done(nil)
}
func (p *previewSessionPlatform) newBackground(options mygo.WindowOptions) (*mygo.Window, error) {
	return p.newWindow(options)
}
func (p *previewSessionPlatform) showBackground(window *mygo.Window) error {
	return p.showWindow(window)
}

func TestPreviewBackgroundHelpersPreserveCancellationDuringNativeCalls(t *testing.T) {
	for _, operation := range []string{"create", "show"} {
		for _, nativeFails := range []bool{false, true} {
			name := operation + " succeeds after cancellation"
			var nativeError error
			if nativeFails {
				name = operation + " fails after cancellation"
				nativeError = errors.New("native operation failed after close")
			}
			t.Run(name, func(t *testing.T) {
				platform := &previewSessionPlatform{}
				preview := &Preview{platform: platform}
				closeCompletions := 0
				cancel := func() {
					preview.Close(func(err error) {
						if err != nil {
							t.Fatalf("close failed: %v", err)
						}
						closeCompletions++
					})
				}
				platform.newWindow = func(mygo.WindowOptions) (*mygo.Window, error) {
					cancel()
					return nil, nativeError
				}
				platform.showWindow = func(window *mygo.Window) error {
					if window != nil {
						t.Fatal("test must not touch a real MyGO window")
					}
					cancel()
					return nativeError
				}
				var err error
				if operation == "create" {
					var window *mygo.Window
					window, err = preview.newBackground(mygo.WindowOptions{})
					if window != nil {
						t.Fatal("canceled creation returned a window")
					}
				} else {
					err = preview.showBackground(nil)
				}
				if !errors.Is(err, errPreviewInterrupted) {
					t.Fatalf("helper error = %v, want interruption even when native error = %v", err, nativeError)
				}
				if platform.restores != 1 || closeCompletions != 1 {
					t.Fatalf("restores = %d, close completions = %d; want one of each", platform.restores, closeCompletions)
				}
			})
		}
	}
}

func TestPreviewBackgroundHelpersKeepNativeResultWithoutCancellation(t *testing.T) {
	want := errors.New("native failure without cancellation")
	platform := &previewSessionPlatform{
		newWindow:  func(mygo.WindowOptions) (*mygo.Window, error) { return nil, want },
		showWindow: func(*mygo.Window) error { return want },
	}
	preview := &Preview{platform: platform}
	if window, err := preview.newBackground(mygo.WindowOptions{}); window != nil || !errors.Is(err, want) {
		t.Fatalf("creation result = %v, %v; want original native failure", window, err)
	}
	if err := preview.showBackground(nil); !errors.Is(err, want) {
		t.Fatalf("show error = %v, want original native failure", err)
	}
	if platform.restores != 0 {
		t.Fatal("a helper unexpectedly initiated lifecycle cleanup")
	}
}
