package desktop

import (
	"errors"
	"strings"
	"time"
)

// Implementations retain the identity of an application, rather than looking
// it up again by PID (which may have been reused). These methods run on the UI
// thread. Hide/Unhide returns are intentionally not used: on macOS 26.2 the
// request can take effect even when the immediate Bool result is false.
type previewApplication interface {
	Name() string
	Hidden() bool
	Terminated() bool
	Hide()
	Unhide()
	Release()
}

type previewVisibility struct {
	snapshot   func() ([]previewApplication, error)
	after      func(time.Duration, func()) func()
	now        func() time.Time
	timeout    time.Duration
	changed    []previewApplication
	request    uint64
	stopTimer  func()
	completion func(error)
}

var errPreviewInterrupted = errors.New("桌面预览已取消")

func (s *previewVisibility) interrupt() {
	s.request++
	if s.stopTimer != nil {
		s.stopTimer()
		s.stopTimer = nil
	}
	if done := s.completion; done != nil {
		s.completion = nil
		done(errPreviewInterrupted)
	}
}

func (s *previewVisibility) hide(complete func(error)) {
	s.interrupt()
	if len(s.changed) != 0 {
		complete(errors.New("请先恢复上次桌面预览隐藏的应用"))
		return
	}
	applications, err := s.snapshot()
	if err != nil {
		complete(err)
		return
	}
	for _, application := range applications {
		if application.Hidden() || application.Terminated() {
			application.Release()
			continue
		}
		// Record before sending the asynchronous request, even if its immediate
		// result suggests failure. Cancellation must still restore this app.
		s.changed = append(s.changed, application)
		application.Hide()
	}
	s.wait(true, complete)
}

func (s *previewVisibility) restore(complete func(error)) {
	s.interrupt()
	for _, application := range s.changed {
		if !application.Terminated() {
			application.Unhide()
		}
	}
	s.wait(false, complete)
}

func (s *previewVisibility) wait(hidden bool, complete func(error)) {
	if len(s.changed) == 0 {
		complete(nil)
		return
	}
	s.completion = complete
	request := s.request
	deadline := s.now().Add(s.timeout)
	var check func()
	check = func() {
		if request != s.request {
			return
		}
		var pending []string
		for _, application := range s.changed {
			if !application.Terminated() && application.Hidden() != hidden {
				pending = append(pending, application.Name())
			}
		}
		if len(pending) != 0 && s.now().Before(deadline) {
			s.stopTimer = s.after(75*time.Millisecond, check)
			return
		}
		if !hidden {
			remaining := s.changed[:0]
			for _, application := range s.changed {
				if application.Terminated() || !application.Hidden() {
					application.Release()
				} else {
					remaining = append(remaining, application)
				}
			}
			clear(s.changed[len(remaining):])
			s.changed = remaining
		}
		s.completion, s.stopTimer = nil, nil
		if len(pending) != 0 {
			action := "隐藏"
			if !hidden {
				action = "恢复"
			}
			complete(errors.New("未能" + action + "应用：" + strings.Join(pending, "、") + "，请重试"))
		} else {
			complete(nil)
		}
	}
	// Always give the main run loop a turn before reading isHidden; otherwise
	// AppKit can return its pre-request cached value, especially on cancel.
	s.stopTimer = s.after(75*time.Millisecond, check)
}
