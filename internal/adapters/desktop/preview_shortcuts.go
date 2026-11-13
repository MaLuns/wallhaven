package desktop

import "fmt"

// These bindings exist only while previewing. A queued callback from a closed
// session must not affect another app, or a newly opened preview session.
type previewShortcuts struct {
	register   func(string, func()) error
	unregister func(string)
	owned      []string
	generation uint64
}

func (s *previewShortcuts) start(previous, next, exit func()) error {
	s.stop()
	generation := s.generation
	for _, binding := range []struct {
		key string
		run func()
	}{{"Left", previous}, {"Right", next}, {"Escape", exit}} {
		if binding.run == nil {
			continue
		}
		if err := s.register(binding.key, func() {
			if generation == s.generation {
				binding.run()
			}
		}); err != nil {
			s.stop()
			return fmt.Errorf("无法启用桌面预览快捷键 %s，请检查按键是否被其他程序占用", binding.key)
		}
		s.owned = append(s.owned, binding.key)
	}
	return nil
}

func (s *previewShortcuts) stop() {
	s.generation++
	for _, key := range s.owned {
		s.unregister(key)
	}
	s.owned = nil
}
