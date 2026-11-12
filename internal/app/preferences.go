package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

type PreferencesOptions struct {
	Store       ports.SettingsStore
	Login       ports.LoginItems
	Directories ports.SettingsDirectories
	Defaults    domain.Preferences
	Dispatch    func(func())
	OnChange    func(domain.Preferences)
}

type SettingsState struct {
	Values                          domain.Preferences
	Ready, Loading, Saving, Opening bool
	Refreshing                      bool
	Error                           string
}

func (s SettingsState) Busy() bool { return s.Loading || s.Saving || s.Opening || s.Refreshing }

// Preferences owns UI-thread state; all disk and system calls run in workers.
// Values are committed only after saving succeeds. The login switch is read
// from the OS, so an externally disabled login item is never re-enabled on boot.
type Preferences struct {
	options PreferencesOptions
	state   SettingsState
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
}

func NewPreferences(options PreferencesOptions) *Preferences {
	ctx, cancel := context.WithCancel(context.Background())
	return &Preferences{options: options, state: SettingsState{Values: options.Defaults}, ctx: ctx, cancel: cancel}
}

func (p *Preferences) State() SettingsState { return p.state }

func (p *Preferences) Close() {
	if p.closed {
		return
	}
	p.closed = true
	p.cancel()
}

func (p *Preferences) Load() {
	if p.closed || p.state.Busy() || p.state.Ready {
		return
	}
	if p.options.Store == nil || p.options.Dispatch == nil {
		p.state.Error = "设置服务尚未就绪"
		return
	}
	p.state.Loading, p.state.Error = true, ""
	ctx, store, login, dispatch := p.ctx, p.options.Store, p.options.Login, p.options.Dispatch
	go func() {
		values, err := store.Load(ctx)
		if err == nil {
			err = values.Validate()
		}
		if err == nil && login != nil {
			values.OpenAtLogin, err = login.Enabled(ctx)
		}
		dispatch(func() {
			if p.closed {
				return
			}
			p.state.Loading = false
			if err != nil {
				p.state.Error = "加载设置失败：" + displayError(err)
				return
			}
			p.state.Values, p.state.Ready = values, true
			p.changed()
		})
	}()
}

func (p *Preferences) RefreshLogin() {
	if p.closed || !p.state.Ready || p.state.Busy() || p.options.Login == nil {
		return
	}
	p.state.Refreshing = true
	ctx, login, dispatch := p.ctx, p.options.Login, p.options.Dispatch
	go func() {
		enabled, err := login.Enabled(ctx)
		dispatch(func() {
			if p.closed {
				return
			}
			p.state.Refreshing = false
			if err != nil {
				p.state.Error = "读取开机启动状态失败：" + displayError(err)
				return
			}
			p.state.Values.OpenAtLogin = enabled
		})
	}()
}

func (p *Preferences) SetPreviewMode(mode domain.PreviewMode) {
	next := p.state.Values
	next.PreviewMode = mode
	p.save(next)
}

func (p *Preferences) SetDownloadDirectory(path string) {
	next := p.state.Values
	next.DownloadPath = path
	p.save(next)
}

func (p *Preferences) SetOpenAtLogin(enabled bool) {
	next := p.state.Values
	next.OpenAtLogin = enabled
	p.save(next)
}

func (p *Preferences) ReportError(err error) {
	if !p.closed && err != nil {
		p.state.Error = displayError(err)
	}
}

func (p *Preferences) save(next domain.Preferences) {
	if p.closed || p.state.Busy() {
		return
	}
	if !p.state.Ready {
		p.state.Error = "设置尚未载入，请重试"
		return
	}
	if err := next.Validate(); err != nil {
		p.state.Error = err.Error()
		return
	}
	previous := p.state.Values
	if next == previous {
		return
	}
	p.state.Saving, p.state.Error = true, ""
	ctx, options := p.ctx, p.options
	go func() {
		values, err := savePreferences(ctx, options, previous, next)
		options.Dispatch(func() {
			if p.closed {
				return
			}
			p.state.Saving = false
			p.state.Values = values
			if err != nil {
				p.state.Error = "保存设置失败：" + displayError(err)
				return
			}
			p.changed()
		})
	}()
}

func savePreferences(ctx context.Context, options PreferencesOptions, previous, next domain.Preferences) (domain.Preferences, error) {
	if next.DownloadPath != previous.DownloadPath {
		if options.Directories == nil {
			return previous, errors.New("下载目录服务尚未就绪")
		}
		if err := options.Directories.Validate(ctx, next.DownloadPath); err != nil {
			return previous, err
		}
	}
	loginChanged := next.OpenAtLogin != previous.OpenAtLogin
	if loginChanged {
		if options.Login == nil {
			return previous, errors.New("当前平台不支持开机启动设置")
		}
		if err := options.Login.SetEnabled(ctx, next.OpenAtLogin); err != nil {
			// A platform error may still have changed its state (e.g. approval).
			if actual, readErr := options.Login.Enabled(ctx); readErr == nil {
				previous.OpenAtLogin = actual
			}
			return previous, fmt.Errorf("设置开机启动失败：%w", err)
		}
		actual, err := options.Login.Enabled(ctx)
		if err != nil || actual != next.OpenAtLogin {
			if err == nil {
				err = errors.New("系统尚未应用开机启动设置，请检查系统的登录项权限")
			}
			return rollbackLogin(options, previous, err)
		}
	}
	if err := options.Store.Save(ctx, next); err != nil {
		if loginChanged {
			return rollbackLogin(options, previous, err)
		}
		return previous, err
	}
	return next, nil
}

func rollbackLogin(options PreferencesOptions, previous domain.Preferences, cause error) (domain.Preferences, error) {
	// Rollback must finish even if the window/application canceled its work.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := options.Login.SetEnabled(ctx, previous.OpenAtLogin); err != nil {
		cause = fmt.Errorf("%w；恢复开机启动状态失败：%v", cause, err)
	}
	if actual, err := options.Login.Enabled(ctx); err == nil {
		previous.OpenAtLogin = actual
	}
	return previous, cause
}

func (p *Preferences) changed() {
	if p.options.OnChange != nil {
		p.options.OnChange(p.state.Values)
	}
}

func (p *Preferences) OpenDownloadDirectory() {
	if p.closed || !p.state.Ready || p.state.Busy() || p.options.Directories == nil {
		return
	}
	p.state.Opening, p.state.Error = true, ""
	ctx, directories, path, dispatch := p.ctx, p.options.Directories, p.state.Values.DownloadPath, p.options.Dispatch
	go func() {
		err := directories.Open(ctx, path)
		dispatch(func() {
			if p.closed {
				return
			}
			p.state.Opening = false
			if err != nil {
				p.state.Error = "打开下载目录失败：" + displayError(err)
			}
		})
	}()
}
