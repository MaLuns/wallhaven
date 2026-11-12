package app

import (
	"context"
	"errors"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

var (
	errTransferPaused   = errors.New("transfer paused")
	errTransferCanceled = errors.New("transfer canceled by user")
	errTransferShutdown = errors.New("application shutdown")
)

type TransferHistoryState struct {
	Loading, Ready, Saving bool
	Error                  string
}

func (a *Actions) TransferHistory() TransferHistoryState { return a.history }

func (a *Actions) loadTransfers() {
	if a.options.Transfers == nil {
		a.history.Ready = true
		return
	}
	if a.options.Dispatch == nil {
		a.history.Error = "下载历史服务尚未就绪"
		return
	}
	a.history.Loading, a.history.Error = true, ""
	ctx, store, dispatch := a.ctx, a.options.Transfers, a.options.Dispatch
	go func() {
		records, err := store.Load(ctx)
		dispatch(func() {
			if a.closed || ctx.Err() != nil {
				return
			}
			a.history.Loading = false
			if err != nil {
				a.history.Error = "读取下载历史失败：" + displayError(err)
				return
			}
			a.jobs = map[transferKey]*transferJob{}
			a.order = nil
			for _, record := range records {
				key := transferKey{record.Kind, record.Wallpaper.ID}
				if _, exists := a.jobs[key]; exists {
					continue
				}
				if record.Completed {
					record.Paused, record.Canceled = false, false
				} else if record.Running || record.Queued {
					record.Paused = !record.Canceled
				}
				record.Running, record.Queued, record.Stopping = false, false, false
				record.BytesPerSecond = 0
				a.jobs[key] = &transferJob{state: record}
				a.order = append(a.order, key)
			}
			a.history.Ready = true
			a.persistTransfers()
		})
	}()
}

func (a *Actions) RetryTransfers() {
	if !a.closed && !a.history.Loading && !a.history.Saving {
		if a.history.Ready {
			a.persistTransfers()
		} else {
			a.loadTransfers()
		}
	}
}

func (a *Actions) persistTransfers() {
	if a.closed || a.options.Transfers == nil || !a.history.Ready {
		return
	}
	if a.history.Saving {
		a.historyPending = true
		return
	}
	a.history.Saving = true
	a.historyPending = false
	ctx, store, dispatch, snapshot := a.ctx, a.options.Transfers, a.options.Dispatch, a.Transfers()
	go func() {
		err := store.Save(ctx, snapshot)
		dispatch(func() {
			if a.closed {
				return
			}
			a.history.Saving = false
			if err != nil {
				a.history.Error = "保存下载历史失败：" + displayError(err)
				a.notify(a.history.Error, true)
			} else {
				a.history.Error = ""
			}
			if a.historyPending {
				a.persistTransfers()
			}
		})
	}()
}

func (a *Actions) closeTransfers() {
	for _, job := range a.jobs {
		if job.cancel != nil {
			job.cancel(errTransferShutdown)
		}
		if job.state.Completed {
			job.state.Running, job.state.Queued, job.state.Paused, job.state.Canceled = false, false, false, false
		} else if job.state.Running || job.state.Queued {
			job.state.Running, job.state.Queued, job.state.Stopping = false, false, false
			job.state.Canceled = job.stop == "cancel"
			job.state.Paused = !job.state.Canceled
		} else if job.stop == "cancel" && job.state.Stopping {
			job.state.Canceled, job.state.Paused = true, false
		}
		// Removing a completed history entry also uses Stopping while its
		// private checkpoint is cleaned. An interrupted removal retains the
		// completed record; it must never become a paused download.
		job.state.Stopping = false
		job.state.BytesPerSecond = 0
	}
	if a.options.Transfers != nil && a.history.Ready {
		// An older asynchronous save uses the now-canceled app context. The
		// store serializes writes, so this final complete snapshot wins.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.options.Transfers.Save(ctx, a.Transfers()); err != nil {
			a.history.Error = "保存下载历史失败：" + displayError(err)
		}
	}
}

func (a *Actions) Transfers() []domain.Transfer {
	result := make([]domain.Transfer, 0, len(a.order))
	for _, key := range a.order {
		result = append(result, a.jobs[key].state)
	}
	return result
}
func (a *Actions) Transfer(kind domain.TransferKind, id string) (domain.Transfer, bool) {
	job, ok := a.jobs[transferKey{kind, id}]
	if !ok {
		return domain.Transfer{}, false
	}
	return job.state, true
}
func transferResumeKey(key transferKey) string     { return string(key.kind) + "-" + key.id }
func (a *Actions) Download(w domain.Wallpaper)     { a.beginTransfer(domain.DownloadTransfer, w) }
func (a *Actions) SetWallpaper(w domain.Wallpaper) { a.beginTransfer(domain.WallpaperTransfer, w) }
func (a *Actions) SetDownloadDirectory(path string) {
	if !a.closed {
		a.options.DownloadDir = path
	}
}

func (a *Actions) PauseTransfer(kind domain.TransferKind, id string) {
	if a.closed {
		return
	}
	job := a.jobs[transferKey{kind, id}]
	if job == nil || !job.state.Running || job.state.Stopping {
		return
	}
	if _, ok := a.options.Files.(ports.ResumableOriginalFiles); !ok {
		a.notify("当前下载服务不支持暂停", true)
		return
	}
	job.stop = "pause"
	job.state.Stopping = true
	job.cancel(errTransferPaused)
}
func (a *Actions) ResumeTransfer(kind domain.TransferKind, id string) {
	if a.closed {
		return
	}
	job := a.jobs[transferKey{kind, id}]
	if job != nil && !job.state.Running && !job.state.Stopping && !job.state.Completed {
		a.startTransfer(kind, job.state.Wallpaper, true)
	}
}
func (a *Actions) CancelTransfer(kind domain.TransferKind, id string) {
	if a.closed {
		return
	}
	key := transferKey{kind, id}
	job := a.jobs[key]
	if job == nil || job.state.Stopping || job.state.Completed {
		return
	}
	if _, ok := a.options.Files.(ports.ResumableOriginalFiles); !ok {
		if job.state.Running {
			job.cancel(errTransferCanceled)
			job.generation++
			job.state.Running, job.state.Queued = false, false
		}
		job.state.Canceled, job.state.Paused = true, false
		job.state.Error = ""
		job.state.BytesPerSecond = 0
		a.persistTransfers()
		return
	}
	job.stop = "cancel"
	job.state.Stopping = true
	if job.state.Running {
		job.cancel(errTransferCanceled)
		return
	}
	a.discardTransfer(key, job, false)
}

func (a *Actions) RemoveTransfer(kind domain.TransferKind, id string) {
	if a.closed || !a.history.Ready {
		return
	}
	key := transferKey{kind, id}
	job := a.jobs[key]
	if job == nil || job.state.Running || job.state.Stopping {
		return
	}
	job.state.Stopping = true
	a.discardTransfer(key, job, true)
}
func (a *Actions) ClearCompletedTransfers() {
	for _, record := range a.Transfers() {
		if record.Completed && !record.Running && !record.Stopping {
			a.RemoveTransfer(record.Kind, record.Wallpaper.ID)
		}
	}
}
func (a *Actions) discardTransfer(key transferKey, job *transferJob, remove bool) {
	generation := job.generation
	files, dispatch, ctx := a.options.Files, a.options.Dispatch, a.ctx
	go func() {
		var err error
		if resumable, ok := files.(ports.ResumableOriginalFiles); ok {
			err = resumable.Discard(ctx, transferResumeKey(key))
		}
		dispatch(func() {
			if a.closed || a.jobs[key] != job || job.generation != generation {
				return
			}
			job.state.Stopping = false
			if err != nil {
				message := "清理下载记录失败：" + displayError(err)
				if !remove {
					job.state.Error = message
				}
				a.notify(message, true)
				return
			}
			if remove {
				delete(a.jobs, key)
				for index, value := range a.order {
					if value == key {
						a.order = append(a.order[:index], a.order[index+1:]...)
						break
					}
				}
			} else {
				job.state.Canceled, job.state.Paused = true, false
				job.state.Error = ""
				job.state.BytesPerSecond = 0
			}
			a.persistTransfers()
		})
	}()
}

func (a *Actions) beginTransfer(kind domain.TransferKind, wallpaper domain.Wallpaper) {
	a.startTransfer(kind, wallpaper, false)
}

func (a *Actions) startTransfer(kind domain.TransferKind, wallpaper domain.Wallpaper, resume bool) {
	if a.closed {
		return
	}
	if !a.history.Ready {
		a.notify("下载历史尚未载入，请在下载页面重试", true)
		return
	}
	if wallpaper.ID == "" {
		a.notify("壁纸编号无效", true)
		return
	}
	if a.options.Files == nil || a.options.Dispatch == nil {
		a.notify("原图下载服务尚未就绪", true)
		return
	}
	if kind == domain.WallpaperTransfer && a.options.Desktop == nil {
		a.notify("当前平台尚不支持设置壁纸", true)
		return
	}
	key := transferKey{kind, wallpaper.ID}
	job := a.jobs[key]
	if job != nil && (job.state.Running || job.state.Stopping) {
		return
	}
	directory := a.options.DownloadDir
	if kind == domain.WallpaperTransfer {
		directory = a.options.WallpaperDir
	}
	if job != nil && job.state.Directory != "" && (resume || (!job.state.Completed && !job.state.Canceled)) {
		directory = job.state.Directory
	}
	if directory == "" {
		a.notify("保存目录不可用", true)
		return
	}
	if job == nil {
		job = &transferJob{}
		a.jobs[key] = job
		a.order = append([]transferKey{key}, a.order...)
	}
	previous := job.state
	created := previous.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	job.generation++
	generation := job.generation
	ctx, cancel := context.WithCancelCause(a.ctx)
	job.cancel = cancel
	job.stop = ""
	job.state = domain.Transfer{Kind: kind, Wallpaper: wallpaper, Directory: directory, CreatedAt: created, Running: true, Queued: true, Received: previous.Received, Total: previous.Total}
	a.persistTransfers()
	files, desktop, details, dispatch, slots := a.options.Files, a.options.Desktop, a.options.Details, a.options.Dispatch, a.slots
	valid := func() bool { return !a.closed && a.jobs[key] == job && job.generation == generation }
	go func() {
		selected := wallpaper
		var path string
		var err error
		defer func() {
			cause := context.Cause(ctx)
			cancel(nil)
			var discardError error
			if errors.Is(cause, errTransferCanceled) {
				if resumable, ok := files.(ports.ResumableOriginalFiles); ok {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					cleanupErr := resumable.Discard(cleanup, transferResumeKey(key))
					stop()
					if cleanupErr != nil {
						discardError = cleanupErr
					}
				}
			}
			dispatch(func() {
				if !valid() {
					return
				}
				job.state.Running, job.state.Queued, job.state.Stopping = false, false, false
				job.state.BytesPerSecond = 0
				job.state.Wallpaper = selected
				if errors.Is(cause, errTransferPaused) || errors.Is(cause, errTransferShutdown) {
					job.state.Paused = true
					job.state.Error = ""
				} else if errors.Is(cause, errTransferCanceled) {
					job.state.Canceled = true
					job.state.Error = ""
					if discardError != nil {
						job.state.Error = "清理临时文件失败：" + displayError(discardError)
						a.notify(job.state.Error, true)
					}
				} else if err != nil {
					job.state.Path = path
					job.state.Error = displayError(err)
					a.notify(job.state.Error, true)
				} else {
					job.state.Path = path
					job.state.Completed = true
					job.state.CompletedAt = time.Now().UTC()
					message := "已下载到：" + path
					if kind == domain.WallpaperTransfer {
						message = "已设置为桌面壁纸"
					}
					a.notify(message, false)
				}
				a.persistTransfers()
			})
		}()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
			err = ctx.Err()
			return
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			return
		}
		dispatch(func() {
			if valid() {
				job.state.Queued = false
			}
		})
		if selected.Path == "" {
			if details == nil {
				err = errors.New("暂未获取到原图地址，请打开详情后重试")
			} else {
				var detail domain.WallpaperDetail
				detail, err = details.Detail(ctx, wallpaper.ID)
				if err == nil {
					if detail.Wallpaper.ID != wallpaper.ID {
						err = errors.New("壁纸详情与当前选择不匹配")
					} else {
						selected = mergeWallpaper(wallpaper, detail.Wallpaper)
					}
				}
			}
		}
		last, rateAt, persistAt := time.Time{}, time.Time{}, time.Time{}
		var rateBytes int64
		progress := func(received, total int64) {
			now := time.Now()
			speed := float64(0)
			if rateAt.IsZero() {
				rateAt = now
				rateBytes = received
			} else if elapsed := now.Sub(rateAt).Seconds(); elapsed > 0 {
				speed = float64(max(int64(0), received-rateBytes)) / elapsed
			}
			if !last.IsZero() && now.Sub(last) < 100*time.Millisecond && (total <= 0 || received < total) {
				return
			}
			last = now
			shouldPersist := persistAt.IsZero() || now.Sub(persistAt) >= time.Second
			if shouldPersist {
				persistAt = now
			}
			dispatch(func() {
				if valid() {
					job.state.Received, job.state.Total, job.state.BytesPerSecond = received, total, speed
					if shouldPersist {
						a.persistTransfers()
					}
				}
			})
		}
		if err == nil {
			if resumable, ok := files.(ports.ResumableOriginalFiles); ok {
				path, err = resumable.DownloadResumable(ctx, selected, directory, transferResumeKey(key), progress)
			} else {
				path, err = files.Download(ctx, selected, directory, progress)
			}
		}
		if err == nil && path == "" {
			err = errors.New("下载未返回有效文件")
		}
		if err == nil && kind == domain.WallpaperTransfer {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = desktop.Set(ctx, path)
			}
		}
	}()
}
