package app

import (
	"reflect"
	"testing"
)

type activationProbe struct {
	minimized bool
	calls     []string
}

func (w *activationProbe) IsMinimized() bool { return w.minimized }
func (w *activationProbe) Restore()          { w.calls = append(w.calls, "restore") }
func (w *activationProbe) Show()             { w.calls = append(w.calls, "show") }
func (w *activationProbe) Focus()            { w.calls = append(w.calls, "focus") }
func TestActivationRestoresBeforeShowingAndFocusing(t *testing.T) {
	for _, minimized := range []bool{false, true} {
		w := &activationProbe{minimized: minimized}
		ActivateWindow(w)
		want := []string{"show", "focus"}
		if minimized {
			want = append([]string{"restore"}, want...)
		}
		if !reflect.DeepEqual(w.calls, want) {
			t.Fatalf("minimized=%v: %v", minimized, w.calls)
		}
	}
}
