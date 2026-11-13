package app

import (
	"errors"

	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

type DesktopPreviewPhase uint8

const (
	DesktopPreviewIdle DesktopPreviewPhase = iota
	DesktopPreviewEntering
	DesktopPreviewActive
	DesktopPreviewExiting
	DesktopPreviewRestoreFailed
)

type DesktopPreviewState struct {
	Phase DesktopPreviewPhase
	Error string
}

// DesktopPreview is the common lifecycle for every platform. It deliberately
// has no dependency on Actions or DesktopWallpaper: selecting a preview cannot
// submit a wallpaper-setting operation.
type DesktopPreview struct {
	host       ports.DesktopPreview
	state      DesktopPreviewState
	request    uint64
	onChange   func()
	closed     bool
	entryError string
}

func NewDesktopPreview(host ports.DesktopPreview, onChange func()) *DesktopPreview {
	return &DesktopPreview{host: host, onChange: onChange}
}

func (p *DesktopPreview) State() DesktopPreviewState { return p.state }
func (p *DesktopPreview) Available() bool            { return p.host != nil && p.host.Available() }
func (p *DesktopPreview) NeedsCleanup() bool         { return p.state.Phase != DesktopPreviewIdle }

func (p *DesktopPreview) changed() {
	if p.onChange != nil {
		p.onChange()
	}
}

func (p *DesktopPreview) Enter() {
	if p.closed || p.state.Phase != DesktopPreviewIdle {
		return
	}
	if !p.Available() {
		p.state.Error = "当前平台暂不支持桌面预览"
		p.changed()
		return
	}
	p.request++
	request := p.request
	p.entryError = ""
	p.state = DesktopPreviewState{Phase: DesktopPreviewEntering}
	p.changed()
	p.host.Open(func(err error) {
		if request != p.request || p.state.Phase != DesktopPreviewEntering {
			return
		}
		if err != nil {
			p.entryError = err.Error()
			p.state.Error = err.Error()
			p.Exit() // The same cleanup path handles partial entry and normal exit.
			return
		}
		p.state.Phase = DesktopPreviewActive
		p.changed()
	})
}

func (p *DesktopPreview) Exit() {
	if p.state.Phase == DesktopPreviewIdle || p.state.Phase == DesktopPreviewExiting {
		return
	}
	p.request++
	request := p.request
	entryError := p.entryError
	p.state.Phase = DesktopPreviewExiting
	p.changed()
	p.host.Close(func(err error) {
		if request != p.request || p.state.Phase != DesktopPreviewExiting {
			return
		}
		if err != nil {
			p.state = DesktopPreviewState{Phase: DesktopPreviewRestoreFailed, Error: errors.Join(errorText(entryError), err).Error()}
		} else {
			p.state = DesktopPreviewState{Error: entryError}
		}
		p.changed()
	})
}

func (p *DesktopPreview) Close() { p.closed = true; p.Exit() }

func errorText(message string) error {
	if message == "" {
		return nil
	}
	return errors.New(message)
}
