package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/locale"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

type PreferencesOptions struct {
	Store       ports.SettingsStore
	Login       ports.LoginItems
	Directories ports.SettingsDirectories
	Cache       ports.CacheManager
	Defaults    domain.Preferences
	Dispatch    func(func())
	OnChange    func(domain.Preferences)
	// OnLoaded runs on the UI thread after each attempted Load completes,
	// including failures, so optional startup services can leave their gate.
	OnLoaded func()
}

type SettingsState struct {
	Values                          domain.Preferences
	Ready, Loading, Saving, Opening bool
	Refreshing                      bool
	Error                           string
	Cache                           domain.CacheUsage
	CacheReady, CacheBusy           bool
	CacheError                      string
}

func (s SettingsState) Busy() bool {
	return s.Loading || s.Saving || s.Opening || s.Refreshing || s.CacheBusy
}

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
		p.state.Error = locale.T("设置服务尚未就绪")
		return
	}
	p.state.Loading, p.state.Error, p.state.CacheError = true, "", ""
	p.state.CacheBusy = p.options.Cache != nil
	ctx, store, login, cache, dispatch := p.ctx, p.options.Store, p.options.Login, p.options.Cache, p.options.Dispatch
	go func() {
		var usage domain.CacheUsage
		var usageError, cacheApplyError error
		values, err := store.Load(ctx)
		if err == nil {
			err = values.Validate()
		}
		if err == nil && cache != nil {
			cacheApplyError = cache.SetLimits(ctx, values.ThumbnailCacheMiB<<20, values.OriginalCacheMiB<<20)
			usage, usageError = cache.Usage(ctx)
		}
		if err == nil && login != nil {
			values.OpenAtLogin, err = login.Enabled(ctx)
		}
		dispatch(func() {
			if p.closed {
				return
			}
			p.state.Loading, p.state.CacheBusy = false, false
			if err != nil {
				p.state.Error = locale.T("加载设置失败：") + displayError(err)
				p.loaded()
				return
			}
			p.state.Values, p.state.Ready = values, true
			if cache != nil {
				p.setCacheUsage(usage, usageError)
				if cacheApplyError != nil {
					message := locale.T("应用缓存上限失败：") + displayError(cacheApplyError)
					if p.state.CacheError != "" {
						message += "；" + p.state.CacheError
					}
					p.state.CacheError = message
				}
			}
			p.changed()
			p.loaded()
		})
	}()
}

func (p *Preferences) RefreshLogin() {
	if p.closed || !p.state.Ready || p.state.Busy() {
		return
	}
	if p.options.Login == nil {
		if p.options.Cache != nil {
			p.RefreshCache()
		}
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
				p.state.Error = locale.T("读取开机启动状态失败：") + displayError(err)
			} else {
				p.state.Values.OpenAtLogin = enabled
			}
			if p.options.Cache != nil {
				p.RefreshCache()
			}
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

func (p *Preferences) SetCacheLimits(thumbnailMiB, originalMiB int64) {
	next := p.state.Values
	next.ThumbnailCacheMiB, next.OriginalCacheMiB = thumbnailMiB, originalMiB
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
		p.state.Error = locale.T("设置尚未载入，请重试")
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
	cacheChanged := cacheLimitsChanged(previous, next)
	if cacheChanged {
		p.state.CacheError = ""
	}
	ctx, options := p.ctx, p.options
	go func() {
		values, err := savePreferences(ctx, options, previous, next)
		var usage domain.CacheUsage
		var usageError error
		if cacheChanged && options.Cache != nil {
			usage, usageError = options.Cache.Usage(ctx)
		}
		options.Dispatch(func() {
			if p.closed {
				return
			}
			p.state.Saving = false
			p.state.Values = values
			if cacheChanged && options.Cache != nil {
				p.setCacheUsage(usage, usageError)
			}
			if err != nil {
				p.state.Error = locale.T("保存设置失败：") + displayError(err)
				if cacheChanged {
					p.state.CacheError = p.state.Error
					if usageError != nil {
						p.state.CacheError += "；" + locale.T("读取缓存用量失败：") + displayError(usageError)
					}
				}
				return
			}
			p.changed()
		})
	}()
}

func savePreferences(ctx context.Context, options PreferencesOptions, previous, next domain.Preferences) (domain.Preferences, error) {
	cacheChanged := cacheLimitsChanged(previous, next)
	if cacheChanged && options.Cache == nil {
		return previous, errors.New(locale.T("缓存管理服务尚未就绪"))
	}
	if next.DownloadPath != previous.DownloadPath {
		if options.Directories == nil {
			return previous, errors.New(locale.T("下载目录服务尚未就绪"))
		}
		if err := options.Directories.Validate(ctx, next.DownloadPath); err != nil {
			return previous, err
		}
	}
	loginChanged := next.OpenAtLogin != previous.OpenAtLogin
	if loginChanged {
		if options.Login == nil {
			return previous, errors.New(locale.T("当前平台不支持开机启动设置"))
		}
		if err := options.Login.SetEnabled(ctx, next.OpenAtLogin); err != nil {
			// A platform error may still have changed its state (e.g. approval).
			if actual, readErr := options.Login.Enabled(ctx); readErr == nil {
				previous.OpenAtLogin = actual
			}
			return previous, fmt.Errorf(locale.T("设置开机启动失败：%w"), err)
		}
		actual, err := options.Login.Enabled(ctx)
		if err != nil || actual != next.OpenAtLogin {
			if err == nil {
				err = errors.New(locale.T("系统尚未应用开机启动设置，请检查系统的登录项权限"))
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
	if cacheChanged {
		if err := options.Cache.SetLimits(ctx, next.ThumbnailCacheMiB<<20, next.OriginalCacheMiB<<20); err != nil {
			values, cause := rollbackCacheLimits(options, previous, next, fmt.Errorf(locale.T("应用缓存上限失败：%w"), err))
			if loginChanged {
				loginValues, loginCause := rollbackLogin(options, previous, cause)
				values.OpenAtLogin, cause = loginValues.OpenAtLogin, loginCause
			}
			return values, cause
		}
	}
	return next, nil
}

func cacheLimitsChanged(previous, next domain.Preferences) bool {
	return previous.ThumbnailCacheMiB != next.ThumbnailCacheMiB || previous.OriginalCacheMiB != next.OriginalCacheMiB
}

func rollbackCacheLimits(options PreferencesOptions, previous, saved domain.Preferences, cause error) (domain.Preferences, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := options.Cache.SetLimits(ctx, previous.ThumbnailCacheMiB<<20, previous.OriginalCacheMiB<<20); err != nil {
		cause = fmt.Errorf(locale.T("%w；恢复缓存上限失败：%v"), cause, err)
	}
	if err := options.Store.Save(ctx, previous); err != nil {
		cause = fmt.Errorf(locale.T("%w；恢复缓存设置失败：%v"), cause, err)
		// Persistence failed to roll back. Show what is actually stored when
		// readable, otherwise retain the last confirmed successful write.
		if actual, readError := options.Store.Load(ctx); readError == nil && actual.Validate() == nil {
			return actual, cause
		}
		return saved, cause
	}
	return previous, cause
}

func rollbackLogin(options PreferencesOptions, previous domain.Preferences, cause error) (domain.Preferences, error) {
	// Rollback must finish even if the window/application canceled its work.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := options.Login.SetEnabled(ctx, previous.OpenAtLogin); err != nil {
		cause = fmt.Errorf(locale.T("%w；恢复开机启动状态失败：%v"), cause, err)
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

func (p *Preferences) loaded() {
	if !p.closed && p.options.OnLoaded != nil {
		p.options.OnLoaded()
	}
}

func (p *Preferences) setCacheUsage(usage domain.CacheUsage, err error) {
	p.state.CacheReady = err == nil
	p.state.CacheError = ""
	if err != nil {
		p.state.CacheError = locale.T("读取缓存用量失败：") + displayError(err)
		return
	}
	p.state.Cache = usage
}

func (p *Preferences) RefreshCache() { p.cacheOperation(false) }

func (p *Preferences) ClearCache() { p.cacheOperation(true) }

func (p *Preferences) cacheOperation(clear bool) {
	if p.closed || p.state.Busy() {
		return
	}
	if p.options.Cache == nil || p.options.Dispatch == nil {
		p.state.CacheReady = false
		p.state.CacheError = locale.T("缓存管理服务尚未就绪")
		return
	}
	if !p.state.Ready {
		p.state.CacheError = locale.T("设置尚未载入，请重试")
		return
	}
	p.state.CacheBusy, p.state.CacheError = true, ""
	ctx, cache, dispatch := p.ctx, p.options.Cache, p.options.Dispatch
	go func() {
		var clearError error
		if clear {
			clearError = cache.Clear(ctx)
		}
		// A partial clear is still observable: never replace measured usage
		// with an assumed zero when deletion or a later read fails.
		usage, usageError := cache.Usage(ctx)
		dispatch(func() {
			if p.closed {
				return
			}
			p.state.CacheBusy = false
			p.setCacheUsage(usage, usageError)
			if clearError != nil {
				message := locale.T("清理缓存失败：") + displayError(clearError)
				if p.state.CacheError != "" {
					message += "；" + p.state.CacheError
				}
				p.state.CacheError = message
			}
		})
	}()
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
				p.state.Error = locale.T("打开下载目录失败：") + displayError(err)
			}
		})
	}()
}
