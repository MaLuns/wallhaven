package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type preferenceCache struct {
	usage  func(context.Context) (domain.CacheUsage, error)
	limits func(context.Context, int64, int64) error
	clear  func(context.Context) error
}

func (c preferenceCache) Usage(ctx context.Context) (domain.CacheUsage, error) {
	if c.usage != nil {
		return c.usage(ctx)
	}
	return domain.CacheUsage{}, nil
}
func (c preferenceCache) SetLimits(ctx context.Context, thumbnail, original int64) error {
	if c.limits != nil {
		return c.limits(ctx, thumbnail, original)
	}
	return nil
}
func (c preferenceCache) Clear(ctx context.Context) error {
	if c.clear != nil {
		return c.clear(ctx)
	}
	return nil
}

type cacheUsageReply struct {
	usage domain.CacheUsage
	err   error
}

func TestPreferencesCacheStartupWaitsForLimitsAndMeasuresUsage(t *testing.T) {
	updates := make(chan func(), 4)
	limits := make(chan [2]int64, 1)
	release := make(chan struct{})
	usageStarted := make(chan struct{}, 1)
	usageReply := make(chan cacheUsageReply, 1)
	values := domain.DefaultSettings(t.TempDir())
	values.ThumbnailCacheMiB, values.OriginalCacheMiB = 512, 2048
	cache := preferenceCache{
		limits: func(_ context.Context, thumbnail, original int64) error {
			limits <- [2]int64{thumbnail, original}
			<-release
			return nil
		},
		usage: func(context.Context) (domain.CacheUsage, error) {
			usageStarted <- struct{}{}
			reply := <-usageReply
			return reply.usage, reply.err
		},
	}
	p := NewPreferences(PreferencesOptions{Store: preferenceStore{load: func(context.Context) (domain.Preferences, error) { return values, nil }}, Cache: cache, Dispatch: func(fn func()) { updates <- fn }})
	t.Cleanup(p.Close)
	p.Load()
	if got := receiveAction(t, limits); got != [2]int64{512 << 20, 2048 << 20} {
		t.Fatalf("startup limits = %v", got)
	}
	if p.State().Ready || !p.State().Loading || !p.State().CacheBusy || !p.State().Busy() {
		t.Fatal("startup exposed settings before attempting the saved cache limits")
	}
	close(release)
	receiveAction(t, usageStarted)
	if p.State().Ready {
		t.Fatal("startup finished before the cache usage query")
	}
	want := domain.CacheUsage{ThumbnailBytes: 120, OriginalBytes: 900, ThumbnailEntries: 2, OriginalEntries: 1}
	usageReply <- cacheUsageReply{usage: want}
	receiveAction(t, updates)()
	if !p.State().Ready || p.State().Values != values || !p.State().CacheReady || p.State().Cache != want || p.State().Busy() {
		t.Fatalf("startup cache state = %+v", p.State())
	}
}

func TestPreferencesCacheStartupFailureDoesNotDisableOtherSettings(t *testing.T) {
	updates := make(chan func(), 2)
	values := domain.DefaultSettings(t.TempDir())
	want := domain.CacheUsage{ThumbnailBytes: 123, ThumbnailEntries: 1}
	p := NewPreferences(PreferencesOptions{
		Store:    preferenceStore{load: func(context.Context) (domain.Preferences, error) { return values, nil }},
		Cache:    preferenceCache{limits: func(context.Context, int64, int64) error { return errors.New("缓存目录只读") }, usage: func(context.Context) (domain.CacheUsage, error) { return want, nil }},
		Dispatch: func(fn func()) { updates <- fn },
	})
	t.Cleanup(p.Close)
	p.Load()
	receiveAction(t, updates)()
	state := p.State()
	if !state.Ready || state.Error != "" || state.Values != values || state.Cache != want || !state.CacheReady || !strings.Contains(state.CacheError, "缓存目录只读") {
		t.Fatalf("optional cache failure disabled loaded preferences: %+v", state)
	}
}

func TestPreferencesCacheOperationsSerializeAndReportRemainingUsage(t *testing.T) {
	h := newPreferencesHarness(t)
	usageCalls := make(chan chan cacheUsageReply, 4)
	clearCalls := make(chan chan error, 2)
	h.p.options.Cache = preferenceCache{
		usage: func(context.Context) (domain.CacheUsage, error) {
			reply := make(chan cacheUsageReply, 1)
			usageCalls <- reply
			result := <-reply
			return result.usage, result.err
		},
		clear: func(context.Context) error { reply := make(chan error, 1); clearCalls <- reply; return <-reply },
	}
	h.p.RefreshCache()
	usage := receiveAction(t, usageCalls)
	before := h.p.State().Values
	h.p.SetPreviewMode(domain.PreviewTile)
	h.p.ClearCache()
	if !h.p.State().CacheBusy || !h.p.State().Busy() || h.p.State().Saving || len(clearCalls) != 0 || h.p.State().Values != before {
		t.Fatal("cache refresh overlapped another operation or settings save")
	}
	usage <- cacheUsageReply{usage: domain.CacheUsage{ThumbnailBytes: 80, OriginalBytes: 100, ThumbnailEntries: 4, OriginalEntries: 2}}
	h.until(t, func() bool { return !h.p.State().CacheBusy })

	h.p.ClearCache()
	clear := receiveAction(t, clearCalls)
	h.p.RefreshCache()
	h.p.SetCacheLimits(512, 2048)
	if len(usageCalls) != 0 || h.p.State().Saving {
		t.Fatal("clear overlapped a usage query or limit change")
	}
	clear <- errors.New("部分缓存文件无法删除")
	remaining := domain.CacheUsage{ThumbnailBytes: 19, OriginalBytes: 7, ThumbnailEntries: 1, OriginalEntries: 1}
	receiveAction(t, usageCalls) <- cacheUsageReply{usage: remaining}
	h.until(t, func() bool { return !h.p.State().CacheBusy })
	if !h.p.State().CacheReady || h.p.State().Cache != remaining || !strings.Contains(h.p.State().CacheError, "无法删除") {
		t.Fatalf("partial clear hid its failure or assumed zero usage: %+v", h.p.State())
	}
	h.p.RefreshCache()
	receiveAction(t, usageCalls) <- cacheUsageReply{err: errors.New("缓存用量不可读")}
	h.until(t, func() bool { return !h.p.State().CacheBusy })
	if h.p.State().CacheReady || h.p.State().Cache != remaining || !strings.Contains(h.p.State().CacheError, "不可读") {
		t.Fatal("usage read failure replaced confirmed usage with an assumed value")
	}
}

func TestPreferencesCacheLimitsPersistBeforeApplyAndRefreshUsage(t *testing.T) {
	h := newPreferencesHarness(t)
	limits := make(chan [2]int64, 1)
	release := make(chan struct{})
	wantUsage := domain.CacheUsage{ThumbnailBytes: 22, OriginalBytes: 33}
	h.p.options.Cache = preferenceCache{
		limits: func(_ context.Context, thumbnail, original int64) error {
			limits <- [2]int64{thumbnail, original}
			<-release
			return nil
		},
		usage: func(context.Context) (domain.CacheUsage, error) { return wantUsage, nil },
	}
	previous := h.p.State().Values
	h.p.SetCacheLimits(512, 2048)
	if got := receiveAction(t, limits); got != [2]int64{512 << 20, 2048 << 20} {
		t.Fatalf("applied cache bytes = %v", got)
	}
	saved, err := h.store.Load(context.Background())
	if err != nil || saved.ThumbnailCacheMiB != 512 || saved.OriginalCacheMiB != 2048 || !h.p.State().Saving || h.p.State().Values != previous {
		t.Fatal("limits were applied before persistence, or shown before application completed")
	}
	close(release)
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values != saved || h.p.State().Cache != wantUsage || !h.p.State().CacheReady || h.p.State().CacheError != "" || len(h.changes) != 2 {
		t.Fatalf("saved limits were not committed with refreshed usage: %+v", h.p.State())
	}
}

func TestPreferencesCacheApplyFailureRollsBackRuntimeAndPersistence(t *testing.T) {
	h := newPreferencesHarness(t)
	previous := h.p.State().Values
	var calls [][2]int64
	h.p.options.Cache = preferenceCache{limits: func(_ context.Context, thumbnail, original int64) error {
		calls = append(calls, [2]int64{thumbnail, original})
		if thumbnail == 512<<20 {
			return errors.New("无法应用缓存容量")
		}
		return nil
	}}
	h.p.SetCacheLimits(512, 2048)
	h.until(t, func() bool { return !h.p.State().Saving })
	stored, err := h.store.Load(context.Background())
	if err != nil || stored != previous || h.p.State().Values != previous || !strings.Contains(h.p.State().CacheError, "无法应用") || len(h.changes) != 1 {
		t.Fatalf("failed limit application was committed or hidden: state=%+v stored=%+v err=%v", h.p.State(), stored, err)
	}
	if !slices.Equal(calls, [][2]int64{{512 << 20, 2048 << 20}, {256 << 20, 1024 << 20}}) {
		t.Fatalf("runtime limits were not restored: %v", calls)
	}
}

func TestPreferencesCacheValidationAndPersistenceFailureDoNotApplyLimits(t *testing.T) {
	h := newPreferencesHarness(t)
	calls := 0
	h.p.options.Cache = preferenceCache{limits: func(context.Context, int64, int64) error { calls++; return nil }}
	previous := h.p.State().Values
	for _, invalid := range [][2]int64{{0, 1024}, {4097, 1024}, {256, 63}, {256, 16385}} {
		h.p.SetCacheLimits(invalid[0], invalid[1])
		if h.p.State().Saving || h.p.State().Error == "" || h.p.State().Values != previous || calls != 0 {
			t.Fatalf("invalid limits reached the cache manager: %v", invalid)
		}
	}
	h.p.options.Store = preferenceStore{save: func(context.Context, domain.Preferences) error { return errors.New("设置文件不可写") }}
	h.p.SetCacheLimits(512, 2048)
	h.until(t, func() bool { return !h.p.State().Saving })
	if calls != 0 || h.p.State().Values != previous || !strings.Contains(h.p.State().CacheError, "不可写") {
		t.Fatal("failed persistence changed runtime limits or hid its error")
	}
}

func TestPreferencesLoginRefreshChainsCacheUsage(t *testing.T) {
	h := newPreferencesHarness(t)
	want := domain.CacheUsage{OriginalBytes: 234, OriginalEntries: 2}
	h.p.options.Cache = preferenceCache{usage: func(context.Context) (domain.CacheUsage, error) { return want, nil }}
	h.p.RefreshLogin()
	h.until(t, func() bool { return !h.p.State().Busy() })
	if h.p.State().Cache != want || !h.p.State().CacheReady {
		t.Fatal("opening settings refreshed login state without refreshing cache usage")
	}
	h.p.options.Login = nil
	h.p.RefreshLogin()
	h.until(t, func() bool { return !h.p.State().Busy() })
	if h.p.State().Cache != want || !h.p.State().CacheReady {
		t.Fatal("settings without a login provider failed to refresh cache usage")
	}
}

func TestPreferencesCloseRollsBackCanceledCacheApplication(t *testing.T) {
	h := newPreferencesHarness(t)
	previous := h.p.State().Values
	started := make(chan struct{}, 1)
	var calls [][2]int64
	h.p.options.Cache = preferenceCache{limits: func(ctx context.Context, thumbnail, original int64) error {
		calls = append(calls, [2]int64{thumbnail, original})
		if thumbnail == 512<<20 {
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		return ctx.Err()
	}}
	h.p.SetCacheLimits(512, 2048)
	receiveAction(t, started)
	h.p.Close()
	receiveAction(t, h.updates)()
	stored, err := h.store.Load(context.Background())
	if err != nil || stored != previous || h.p.State().Values != previous || len(h.changes) != 1 {
		t.Fatal("closing during application left an unconfirmed cache limit saved")
	}
	if !slices.Equal(calls, [][2]int64{{512 << 20, 2048 << 20}, {256 << 20, 1024 << 20}}) {
		t.Fatalf("close did not attempt to restore runtime caps using an independent context: %v", calls)
	}
}

type failedPreferenceLoginRead struct{}

func (failedPreferenceLoginRead) Enabled(context.Context) (bool, error) {
	return false, errors.New("登录项读取失败")
}
func (failedPreferenceLoginRead) SetEnabled(context.Context, bool) error { return nil }

func TestPreferencesOnLoadedRunsAfterStartupAttemptOnSuccessAndFailure(t *testing.T) {
	for _, failure := range []string{"", "store", "validation", "login"} {
		name := failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			updates := make(chan func(), 4)
			values := domain.DefaultSettings(t.TempDir())
			var order []string
			hookCalls := 0
			options := PreferencesOptions{
				Store: preferenceStore{load: func(context.Context) (domain.Preferences, error) {
					order = append(order, "load")
					if failure == "store" {
						return domain.Preferences{}, errors.New("设置文件不可读")
					}
					if failure == "validation" {
						invalid := values
						invalid.ThumbnailCacheMiB = 0
						return invalid, nil
					}
					return values, nil
				}},
				Cache: preferenceCache{
					limits: func(context.Context, int64, int64) error { order = append(order, "limits"); return nil },
					usage: func(context.Context) (domain.CacheUsage, error) {
						order = append(order, "usage")
						return domain.CacheUsage{}, nil
					},
				},
				Dispatch: func(fn func()) { updates <- fn },
				OnChange: func(domain.Preferences) { order = append(order, "change") },
			}
			if failure == "login" {
				options.Login = failedPreferenceLoginRead{}
			}
			var p *Preferences
			options.OnLoaded = func() {
				hookCalls++
				order = append(order, "loaded")
				if p.State().Loading || p.State().CacheBusy {
					t.Error("OnLoaded ran before clearing startup busy flags")
				}
				if failure == "" && (!p.State().Ready || !p.State().CacheReady || p.State().Values != values) {
					t.Error("OnLoaded ran before publishing loaded preferences and cache usage")
				}
				if failure != "" && (p.State().Ready || p.State().Error == "") {
					t.Error("OnLoaded ran before publishing the startup failure")
				}
			}
			p = NewPreferences(options)
			t.Cleanup(p.Close)
			p.Load()
			completion := receiveAction(t, updates)
			if hookCalls != 0 {
				t.Fatal("OnLoaded ran on the loading worker rather than UI dispatch")
			}
			completion()
			if hookCalls != 1 {
				t.Fatalf("load completion called OnLoaded %d times", hookCalls)
			}
			want := []string{"load", "loaded"}
			if failure == "" {
				want = []string{"load", "limits", "usage", "change", "loaded"}
			} else if failure == "login" {
				want = []string{"load", "limits", "usage", "loaded"}
			}
			if !slices.Equal(order, want) {
				t.Fatalf("startup hook order = %q, want %q", order, want)
			}
			p.Load()
			if failure == "" {
				if hookCalls != 1 {
					t.Fatal("an ignored second load repeated the startup hook")
				}
			} else {
				receiveAction(t, updates)()
				if hookCalls != 2 {
					t.Fatal("retry completion did not call its startup hook exactly once")
				}
			}
		})
	}
}

func TestPreferencesOnLoadedDoesNotRunAfterClose(t *testing.T) {
	updates := make(chan func(), 1)
	values := domain.DefaultSettings(t.TempDir())
	called := false
	p := NewPreferences(PreferencesOptions{
		Store:    preferenceStore{load: func(context.Context) (domain.Preferences, error) { return values, nil }},
		Dispatch: func(fn func()) { updates <- fn }, OnLoaded: func() { called = true },
	})
	p.Load()
	completion := receiveAction(t, updates)
	p.Close()
	completion()
	if called {
		t.Fatal("a late completion ran OnLoaded after preferences were closed")
	}
}

func TestPreferencesLoginFailureRestoresSavedLargerCacheLimitsBeforeOnLoaded(t *testing.T) {
	updates := make(chan func(), 1)
	values := domain.DefaultSettings(t.TempDir())
	values.ThumbnailCacheMiB, values.OriginalCacheMiB = 512, 2048
	var applied [2]int64
	usageRead, loaded := false, false
	p := NewPreferences(PreferencesOptions{
		Store: preferenceStore{load: func(context.Context) (domain.Preferences, error) { return values, nil }},
		Login: failedPreferenceLoginRead{},
		Cache: preferenceCache{
			limits: func(_ context.Context, thumbnail, original int64) error {
				applied = [2]int64{thumbnail, original}
				return nil
			},
			usage: func(context.Context) (domain.CacheUsage, error) { usageRead = true; return domain.CacheUsage{}, nil },
		},
		Dispatch: func(fn func()) { updates <- fn },
		OnLoaded: func() {
			loaded = true
			if applied != [2]int64{512 << 20, 2048 << 20} || !usageRead {
				t.Error("startup image gate was released before restoring the known saved cache limits")
			}
		},
	})
	t.Cleanup(p.Close)
	p.Load()
	receiveAction(t, updates)()
	if !loaded || p.State().Ready || !strings.Contains(p.State().Error, "登录项读取失败") {
		t.Fatalf("login failure did not complete the startup hook with its error: %+v", p.State())
	}
}
