package app

import (
	"errors"
	"testing"
)

type previewHostStub struct {
	available   bool
	open, close []func(error)
}

func (h *previewHostStub) Available() bool        { return h.available }
func (h *previewHostStub) Open(done func(error))  { h.open = append(h.open, done) }
func (h *previewHostStub) Close(done func(error)) { h.close = append(h.close, done) }

func TestDesktopPreviewLifecycleRejectsDuplicateEntryAndLateCompletion(t *testing.T) {
	host := &previewHostStub{available: true}
	p := NewDesktopPreview(host, nil)
	p.Enter()
	p.Enter()
	if len(host.open) != 1 || p.State().Phase != DesktopPreviewEntering {
		t.Fatal("duplicate entry was not rejected")
	}
	p.Exit()
	p.Exit()
	if len(host.close) != 1 || p.State().Phase != DesktopPreviewExiting {
		t.Fatal("cancel did not start exactly one cleanup")
	}
	host.open[0](nil)
	if p.State().Phase != DesktopPreviewExiting {
		t.Fatal("late entry reopened the desktop preview")
	}
	host.close[0](nil)
	if p.NeedsCleanup() {
		t.Fatal("cleanup did not release the session")
	}
	p.Enter()
	host.open[1](nil)
	if p.State().Phase != DesktopPreviewActive {
		t.Fatal("could not enter after cancel")
	}
	p.Exit()
	host.close[1](nil)
	if p.State().Phase != DesktopPreviewIdle {
		t.Fatal("normal exit did not finish")
	}
}

func TestDesktopPreviewPartialFailureRestoresAndFailedRestoreCanRetry(t *testing.T) {
	host := &previewHostStub{available: true}
	p := NewDesktopPreview(host, nil)
	p.Enter()
	host.open[0](errors.New("hide failed"))
	if len(host.close) != 1 || p.State().Phase != DesktopPreviewExiting {
		t.Fatal("entry failure did not roll back")
	}
	host.close[0](errors.New("restore failed"))
	if p.State().Phase != DesktopPreviewRestoreFailed || !p.NeedsCleanup() {
		t.Fatal("failed restoration lost its cleanup obligation")
	}
	p.Enter()
	if len(host.open) != 1 {
		t.Fatal("entry replaced an unrestored session")
	}
	p.Exit()
	host.close[1](nil)
	if p.NeedsCleanup() {
		t.Fatal("retry did not clear cleanup obligation")
	}
	if p.State().Error != "hide failed" {
		t.Fatal("successful restore retained an obsolete restoration error")
	}
}

func TestDesktopPreviewUnsupportedDoesNotTouchHostAndCloseCancelsEntry(t *testing.T) {
	host := &previewHostStub{}
	p := NewDesktopPreview(host, nil)
	p.Enter()
	if len(host.open) != 0 || p.State().Error == "" || p.NeedsCleanup() {
		t.Fatal("unsupported platform changed system state")
	}
	host.available = true
	p.Enter()
	p.Close()
	host.open[0](nil)
	host.close[0](nil)
	p.Enter()
	if len(host.open) != 1 || p.NeedsCleanup() {
		t.Fatal("closed preview accepted another entry")
	}
}
