//go:build !darwin

package desktop

import "github.com/egoist/mygo"

// AttachSettingsChild leaves other platforms' native Parent implementation in
// MyGO in charge of ownership, ordering and lifecycle.
func AttachSettingsChild(parent, child *mygo.Window, onError ...func(error)) (func(), error) {
	return func() {}, nil
}
