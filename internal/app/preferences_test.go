package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MaLuns/wallhaven-electron/internal/adapters/storage"
	"github.com/MaLuns/wallhaven-electron/internal/domain"
)

type preferenceStore struct {
	load func(context.Context) (domain.Preferences, error)
	save func(context.Context, domain.Preferences) error
}

func (s preferenceStore) Load(ctx context.Context) (domain.Preferences, error) { return s.load(ctx) }
func (s preferenceStore) Save(ctx context.Context, values domain.Preferences) error {
	return s.save(ctx, values)
}

type preferenceLogin struct {
	mu          sync.Mutex
	enabled     bool
	setErr      error
	failDisable bool
	calls       []bool
}

func (l *preferenceLogin) Enabled(context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enabled, nil
}

func (l *preferenceLogin) SetEnabled(_ context.Context, enabled bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, enabled)
	if l.setErr != nil {
		return l.setErr
	}
	if !enabled && l.failDisable {
		return errors.New("系统拒绝恢复登录项")
	}
	l.enabled = enabled
	return nil
}

type preferenceDirectories struct {
	err    error
	opened chan string
}

func (d preferenceDirectories) Validate(context.Context, string) error { return d.err }
func (d preferenceDirectories) Open(_ context.Context, path string) error {
	if d.opened != nil {
		d.opened <- path
	}
	return d.err
}

type preferencesHarness struct {
	p       *Preferences
	store   *storage.Settings
	login   *preferenceLogin
	updates chan func()
	changes []domain.Preferences
}

func newPreferencesHarness(t *testing.T) *preferencesHarness {
	t.Helper()
	h := &preferencesHarness{updates: make(chan func(), 20), login: &preferenceLogin{}}
	defaults := domain.DefaultSettings(t.TempDir())
	h.store = storage.NewSettings(filepath.Join(t.TempDir(), "settings.json"), defaults)
	h.p = NewPreferences(PreferencesOptions{Store: h.store, Login: h.login, Defaults: defaults, Directories: preferenceDirectories{}, Dispatch: func(fn func()) { h.updates <- fn }, OnChange: func(values domain.Preferences) { h.changes = append(h.changes, values) }})
	t.Cleanup(h.p.Close)
	h.p.Load()
	h.until(t, func() bool { return h.p.State().Ready })
	return h
}

func (h *preferencesHarness) until(t *testing.T, ready func() bool) {
	t.Helper()
	for !ready() {
		receiveAction(t, h.updates)()
	}
}

func TestPreferencesCommitOnlyAfterSaveAndRestoreOnRestart(t *testing.T) {
	h := newPreferencesHarness(t)
	type write struct {
		values domain.Preferences
		reply  chan error
	}
	writes := make(chan write, 2)
	h.p.options.Store = preferenceStore{load: h.store.Load, save: func(ctx context.Context, values domain.Preferences) error {
		call := write{values, make(chan error, 1)}
		writes <- call
		if err := <-call.reply; err != nil {
			return err
		}
		return h.store.Save(ctx, values)
	}}
	h.p.SetPreviewMode(domain.PreviewTile)
	call := receiveAction(t, writes)
	if !h.p.State().Saving || h.p.State().Values.PreviewMode != domain.PreviewWaterfall {
		t.Fatal("applied a preview mode before saving")
	}
	h.p.SetOpenAtLogin(true) // Must not submit another edit during this save.
	call.reply <- errors.New("磁盘写入失败")
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values.PreviewMode != domain.PreviewWaterfall || h.p.State().Error == "" || len(h.changes) != 1 {
		t.Fatal("failed save changed the active settings or hid its error")
	}
	h.p.SetPreviewMode(domain.PreviewTile)
	receiveAction(t, writes).reply <- nil
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values.PreviewMode != domain.PreviewTile || h.p.State().Error != "" || len(h.changes) != 2 {
		t.Fatal("successful settings were not applied")
	}
	restarted := NewPreferences(PreferencesOptions{Store: h.store, Login: h.login, Dispatch: func(fn func()) { h.updates <- fn }})
	defer restarted.Close()
	restarted.Load()
	h.until(t, func() bool { return restarted.State().Ready })
	if restarted.State().Values.PreviewMode != domain.PreviewTile || len(h.login.calls) != 0 {
		t.Fatal("restart lost the mode or changed the system login item")
	}
}

func TestPreferencesLoginUsesSystemStateAndRollsBackFailedPersistence(t *testing.T) {
	h := newPreferencesHarness(t)
	h.login.mu.Lock()
	h.login.enabled = true // A setting changed outside this process.
	h.login.mu.Unlock()
	h.p.RefreshLogin()
	h.until(t, func() bool { return !h.p.State().Refreshing })
	if !h.p.State().Values.OpenAtLogin || len(h.login.calls) != 0 {
		t.Fatal("reading login state changed a login item")
	}
	h.p.SetOpenAtLogin(false)
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values.OpenAtLogin || h.p.State().Error != "" {
		t.Fatal("system login change did not commit")
	}
	before, _ := h.store.Load(context.Background())
	h.p.options.Store = preferenceStore{save: func(context.Context, domain.Preferences) error { return errors.New("磁盘已满") }}
	h.p.SetOpenAtLogin(true)
	h.until(t, func() bool { return !h.p.State().Saving })
	actual, _ := h.login.Enabled(context.Background())
	if actual || h.p.State().Values.OpenAtLogin || !strings.Contains(h.p.State().Error, "磁盘已满") {
		t.Fatal("failed persistence left login enabled")
	}
	if !slices.Equal(h.login.calls, []bool{false, true, false}) {
		t.Fatalf("login calls: %v", h.login.calls)
	}
	if after, _ := h.store.Load(context.Background()); after != before {
		t.Fatal("failed persistence changed the saved settings")
	}
	h.login.failDisable = true
	h.p.SetOpenAtLogin(true)
	h.until(t, func() bool { return !h.p.State().Saving })
	if !h.p.State().Values.OpenAtLogin || !strings.Contains(h.p.State().Error, "恢复开机启动状态失败") {
		t.Fatal("failed rollback did not show the actual system state")
	}
}

func TestPreferencesRejectSystemAndDirectoryFailures(t *testing.T) {
	h := newPreferencesHarness(t)
	h.login.setErr = errors.New("请在系统设置中允许登录项")
	h.p.SetOpenAtLogin(true)
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values.OpenAtLogin || !strings.Contains(h.p.State().Error, "允许登录项") {
		t.Fatal("system denial was reported as success")
	}
	before := h.p.State().Values
	h.p.options.Directories = preferenceDirectories{err: errors.New("下载目录不可写")}
	h.p.SetDownloadDirectory(t.TempDir())
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values != before || !strings.Contains(h.p.State().Error, "不可写") {
		t.Fatal("unusable download directory became active")
	}
	h.p.SetPreviewMode("invalid")
	if h.p.State().Saving || !strings.Contains(h.p.State().Error, "预览模式无效") {
		t.Fatal("accepted an invalid preview mode")
	}
}

func TestPreferencesCloseRejectsLateLoadAndRollsBackCanceledLoginSave(t *testing.T) {
	updates := make(chan func(), 2)
	defaults := domain.DefaultSettings(t.TempDir())
	p := NewPreferences(PreferencesOptions{Store: preferenceStore{load: func(context.Context) (domain.Preferences, error) { return defaults, nil }}, Dispatch: func(fn func()) { updates <- fn }})
	p.Load()
	late := receiveAction(t, updates)
	p.Close()
	late()
	if p.State().Ready {
		t.Fatal("late load updated closed preferences")
	}
	h := newPreferencesHarness(t)
	started := make(chan struct{}, 1)
	h.p.options.Store = preferenceStore{save: func(ctx context.Context, _ domain.Preferences) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	h.p.SetOpenAtLogin(true)
	receiveAction(t, started)
	h.p.Close()
	receiveAction(t, h.updates)()
	actual, _ := h.login.Enabled(context.Background())
	if actual || !slices.Equal(h.login.calls, []bool{true, false}) || h.p.State().Values.OpenAtLogin {
		t.Fatal("closing during save left an unpersisted login item enabled")
	}
}

func TestPreferencesDirectoryAppliesOnlyAfterValidationAndOpensCurrentPath(t *testing.T) {
	h := newPreferencesHarness(t)
	opened := make(chan string, 1)
	h.p.options.Directories = preferenceDirectories{opened: opened}
	directory := t.TempDir()
	h.p.SetDownloadDirectory(directory)
	h.until(t, func() bool { return !h.p.State().Saving })
	if h.p.State().Values.DownloadPath != directory || h.changes[len(h.changes)-1].DownloadPath != directory {
		t.Fatal("saved download path did not reach the application")
	}
	h.p.OpenDownloadDirectory()
	if got := receiveAction(t, opened); got != directory {
		t.Fatalf("opened directory %q, want %q", got, directory)
	}
	h.until(t, func() bool { return !h.p.State().Opening })
}
