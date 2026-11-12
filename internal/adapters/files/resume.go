package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

var _ ports.ResumableOriginalFiles = (*Downloader)(nil)

// DownloadResumable retains its own checkpoint on interruption. Per-key gates
// make a pause followed immediately by resume wait for the old writer to close.
func (d *Downloader) DownloadResumable(ctx context.Context, wallpaper domain.Wallpaper, directory, key string, progress func(int64, int64)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !wallpaperID.MatchString(wallpaper.ID) {
		return "", errors.New("壁纸编号无效")
	}
	address, err := url.Parse(strings.TrimSpace(wallpaper.Path))
	if err != nil || !allowedURL(address) || len(address.String()) > 8192 {
		return "", errors.New("原图地址无效")
	}
	if strings.TrimSpace(directory) == "" || strings.ContainsRune(directory, 0) {
		return "", errors.New("请选择有效下载目录")
	}
	directory, err = filepath.Abs(directory)
	if err != nil || len(directory) > 4096 {
		return "", errors.New("下载目录无效")
	}
	statePath, err := d.checkpointPath(key)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	release, err := acquireCheckpoint(ctx, statePath)
	if err != nil {
		return "", contextError(ctx, err)
	}
	defer release()
	state, err := d.readCheckpoint(ctx, statePath, key)
	if err != nil {
		return "", err
	}
	if state != nil && (state.URL != address.String() || state.Directory != directory || state.ID != wallpaper.ID) {
		if !state.Complete {
			return "", errors.New("未完成的下载与当前原图或目录不一致，请先取消旧任务")
		}
		// A fresh submission may use a new chosen directory. Only its old
		// private partial is cleaned; the published original is retained.
		if err := removePartial(*state); err != nil {
			return "", err
		}
		state = nil
	}
	if state != nil && state.Published != "" {
		path, valid, err := d.validReceipt(ctx, *state)
		if err != nil {
			return "", err
		}
		if valid {
			if !state.Complete {
				state.Complete = true
				if err := writeCheckpoint(ctx, statePath, *state); err != nil {
					return "", err
				}
			}
			if err := removePartial(*state); err != nil {
				return "", err
			}
			notifyProgress(progress, state.Size, state.Size)
			return path, nil
		}
	}
	if state == nil {
		state = &checkpoint{Version: 1, Key: key, ID: wallpaper.ID, URL: address.String(), Directory: directory}
		if _, err := os.Lstat(partialPath(*state)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("发现未关联的下载临时文件，未覆盖现有文件")
		}
		if err := writeCheckpoint(ctx, statePath, *state); err != nil {
			return "", err
		}
	}
	file, err := d.openPartial(*state)
	if err != nil {
		return "", err
	}
	defer file.Close()
	defer file.Sync()
	if state.Published != "" {
		// A crash between linking the original and saving its receipt can
		// leave a fully validated private file. Finish without another GET.
		info, _ := file.Stat()
		digest, hashErr := fileDigest(ctx, file)
		if hashErr != nil {
			return "", hashErr
		}
		if info.Size() == state.Size && digest == state.Digest {
			return d.finishResumable(ctx, file, statePath, state, progress)
		}
		clearReceipt(state)
		if err := writeCheckpoint(ctx, statePath, *state); err != nil {
			return "", err
		}
	}
	if d.originals != nil {
		if data, found := d.originals.Get(state.URL); found {
			if int64(len(data)) > d.maxBytes {
				return "", errors.New("原图文件超过大小限制")
			}
			if err := d.restartPartial(ctx, file, statePath, state); err != nil {
				return "", err
			}
			state.Total = int64(len(data))
			if err := writeCheckpoint(ctx, statePath, *state); err != nil {
				return "", err
			}
			response := &http.Response{Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
			if err := d.copyResumable(ctx, response, file, 0, state.Total, state.Total-1, progress); err != nil {
				return "", err
			}
			return d.finishResumable(ctx, file, statePath, state, progress)
		}
	}
	// A changed validator or an unsatisfied old range gets one safe full
	// retry. Invalid Content-Range responses never get appended.
	for attempt := 0; attempt < 2; attempt++ {
		info, err := file.Stat()
		if err != nil {
			return "", errors.New("无法读取下载断点长度")
		}
		offset := info.Size()
		if offset > 0 && state.ETag == "" && state.LastModified == "" {
			if err := d.restartPartial(ctx, file, statePath, state); err != nil {
				return "", err
			}
			offset = 0
		}
		notifyProgress(progress, offset, state.Total)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, state.URL, nil)
		request.Header.Set("Accept", "image/*")
		request.Header.Set("User-Agent", "MyGO-Wallhaven/0.1.0")
		request.Header.Set("Accept-Encoding", "identity")
		if offset > 0 {
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			validator := state.ETag
			if validator == "" {
				validator = state.LastModified
			}
			request.Header.Set("If-Range", validator)
		}
		response, err := d.client.Do(request)
		if err != nil {
			if canceled := contextError(ctx, err); canceled != nil {
				return "", canceled
			}
			return "", errors.New("原图连接失败，已保留断点供继续下载")
		}
		if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
			response.Body.Close()
			return "", errors.New("服务器返回了不支持的原图内容编码")
		}
		var expectedEnd int64 = -1
		switch response.StatusCode {
		case http.StatusOK:
			if response.ContentLength > d.maxBytes {
				response.Body.Close()
				return "", errors.New("原图文件超过大小限制")
			}
			if err := d.restartPartial(ctx, file, statePath, state); err != nil {
				response.Body.Close()
				return "", err
			}
			offset = 0
			state.Total = max(0, response.ContentLength)
			state.ETag, state.LastModified = responseValidators(response.Header)
			if response.ContentLength >= 0 {
				expectedEnd = response.ContentLength - 1
			}
		case http.StatusPartialContent:
			start, end, total, valid := parseContentRange(response.Header.Get("Content-Range"))
			if !valid || start != offset || total > d.maxBytes || response.ContentLength >= 0 && response.ContentLength != end-start+1 {
				response.Body.Close()
				return "", errors.New("服务器返回的下载范围无效，未拼接原图")
			}
			if offset > 0 && (validatorChanged(*state, response.Header) || state.Total > 0 && state.Total != total) {
				response.Body.Close()
				if err := d.restartPartial(ctx, file, statePath, state); err != nil {
					return "", err
				}
				continue
			}
			state.Total, expectedEnd = total, end
			etag, modified := responseValidators(response.Header)
			if etag != "" || offset == 0 {
				state.ETag = etag
			}
			if modified != "" || offset == 0 {
				state.LastModified = modified
			}
		case http.StatusRequestedRangeNotSatisfiable:
			total, valid := parseUnsatisfiedRange(response.Header.Get("Content-Range"))
			response.Body.Close()
			if offset > 0 && valid && total == offset && total <= d.maxBytes && !validatorChanged(*state, response.Header) && (state.Total == 0 || state.Total == total) {
				state.Total = total
				return d.finishResumable(ctx, file, statePath, state, progress)
			}
			if err := d.restartPartial(ctx, file, statePath, state); err != nil {
				return "", err
			}
			continue
		default:
			response.Body.Close()
			return "", fmt.Errorf("原图下载失败（HTTP %d），已保留断点", response.StatusCode)
		}
		if err := writeCheckpoint(ctx, statePath, *state); err != nil {
			response.Body.Close()
			return "", err
		}
		err = d.copyResumable(ctx, response, file, offset, state.Total, expectedEnd, progress)
		response.Body.Close()
		if err != nil {
			return "", err
		}
		return d.finishResumable(ctx, file, statePath, state, progress)
	}
	return "", errors.New("原图来源已变化，请重试下载")
}

func (d *Downloader) openPartial(state checkpoint) (*os.File, error) {
	if err := os.MkdirAll(state.Directory, 0700); err != nil {
		return nil, errors.New("无法创建下载目录，请检查写入权限")
	}
	path := partialPath(state)
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("下载临时文件类型无效，未覆盖任何文件")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.New("无法打开下载临时文件")
	}
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || !os.SameFile(opened, current) || info != nil && !os.SameFile(info, opened) || opened.Size() > d.maxBytes {
		file.Close()
		return nil, errors.New("下载临时文件已变更或超过大小限制")
	}
	return file, nil
}

func (d *Downloader) restartPartial(ctx context.Context, file *os.File, statePath string, state *checkpoint) error {
	if err := contextError(ctx, nil); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return errors.New("无法重置下载临时文件")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errors.New("无法重置下载临时文件")
	}
	// Truncation must be durable before the sidecar names the new validator.
	if err := file.Sync(); err != nil {
		return errors.New("无法同步下载临时文件")
	}
	state.ETag, state.LastModified, state.Total = "", "", 0
	clearReceipt(state)
	return writeCheckpoint(ctx, statePath, *state)
}

func clearReceipt(state *checkpoint) {
	state.Published, state.Digest, state.Size, state.Complete = "", "", 0, false
}

func notifyProgress(progress func(int64, int64), received, total int64) {
	if progress != nil {
		progress(received, total)
	}
}

func (d *Downloader) copyResumable(ctx context.Context, response *http.Response, file *os.File, offset, total, end int64, progress func(int64, int64)) error {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return errors.New("无法定位下载断点")
	}
	notifyProgress(progress, offset, total)
	buffer := make([]byte, 32<<10)
	received := offset
	for {
		if err := contextError(ctx, nil); err != nil {
			return err
		}
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if int64(n) > d.maxBytes-received || end >= 0 && int64(n) > end+1-received {
				return errors.New("原图长度超过声明范围或大小限制")
			}
			written, err := file.Write(buffer[:n])
			if err != nil || written != n {
				return errors.New("原图保存失败，已保留可用断点")
			}
			received += int64(n)
			notifyProgress(progress, received, total)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				if canceled := contextError(ctx, readErr); canceled != nil {
					return canceled
				}
				return errors.New("原图读取中断，已保留断点供继续下载")
			}
			break
		}
	}
	if err := contextError(ctx, nil); err != nil {
		return err
	}
	if end >= 0 && received != end+1 || total > 0 && received != total {
		return errors.New("原图尚未下载完整，已保留断点供继续下载")
	}
	return nil
}

func (d *Downloader) finishResumable(ctx context.Context, file *os.File, statePath string, state *checkpoint, progress func(int64, int64)) (string, error) {
	extension, err := d.validate(ctx, file)
	if err != nil {
		if ctx.Err() == nil {
			if resetErr := d.restartPartial(ctx, file, statePath, state); resetErr != nil {
				return "", resetErr
			}
		}
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", errors.New("原图保存失败，请检查磁盘空间")
	}
	info, err := file.Stat()
	if err != nil {
		return "", errors.New("无法校验已下载原图")
	}
	digest, err := fileDigest(ctx, file)
	if err != nil {
		return "", err
	}
	state.Total, state.Size, state.Digest, state.Complete = info.Size(), info.Size(), digest, false
	for suffix := 0; ; suffix++ {
		if err := contextError(ctx, nil); err != nil {
			return "", err
		}
		name := "wallhaven-" + state.ID
		if suffix > 0 {
			name += fmt.Sprintf("-%d", suffix)
		}
		state.Published = name + extension
		destination := filepath.Join(state.Directory, state.Published)
		if _, err := os.Lstat(destination); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("无法读取原图保存目录")
		}
		// Persist the intended link first. If the app stops after Link, the
		// next run verifies this candidate and upgrades it to a receipt.
		if err := writeCheckpoint(ctx, statePath, *state); err != nil {
			return "", err
		}
		if err := os.Link(file.Name(), destination); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", errors.New("无法保存原图，请检查目录权限及文件系统支持")
		}
		state.Complete = true
		if err := writeCheckpoint(ctx, statePath, *state); err != nil {
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", errors.New("无法关闭已保存原图")
		}
		if err := removePartial(*state); err != nil {
			return "", err
		}
		d.rememberOriginal(ctx, state.URL, destination)
		notifyProgress(progress, state.Size, state.Size)
		return destination, nil
	}
}

func (d *Downloader) validReceipt(ctx context.Context, state checkpoint) (string, bool, error) {
	path := filepath.Join(state.Directory, state.Published)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && (!info.Mode().IsRegular() || info.Size() != state.Size) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("无法读取已保存原图")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, errors.New("无法读取已保存原图")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", false, errors.New("已保存原图发生变更")
	}
	digest, err := fileDigest(ctx, file)
	if err != nil {
		return "", false, err
	}
	if digest != state.Digest {
		return "", false, nil
	}
	if _, err := d.validate(ctx, file); err != nil {
		if ctx.Err() != nil {
			return "", false, contextError(ctx, err)
		}
		return "", false, nil
	}
	return path, true, nil
}

func fileDigest(ctx context.Context, file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", errors.New("无法校验原图内容")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, cancelReader{ctx, file}); err != nil {
		if canceled := contextError(ctx, err); canceled != nil {
			return "", canceled
		}
		return "", errors.New("无法校验原图内容")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (d *Downloader) rememberOriginal(ctx context.Context, url, path string) {
	if d.originals == nil || ctx.Err() != nil {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(cancelReader{ctx, file}, d.maxBytes+1))
	if err == nil && int64(len(data)) <= d.maxBytes {
		d.originals.Put(url, data)
	}
}

// Discard removes only the validated key's sidecar and private unfinished file.
// Published files, including an interrupted completion's intended link, survive.
func (d *Downloader) Discard(ctx context.Context, key string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := d.checkpointPath(key)
	if err != nil {
		return err
	}
	release, err := acquireCheckpoint(ctx, path)
	if err != nil {
		return err
	}
	defer release()
	state, err := d.readCheckpoint(ctx, path, key)
	if err != nil || state == nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := removePartial(*state); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法清理下载断点")
	}
	return nil
}

func parseContentRange(value string) (start, end, total int64, valid bool) {
	if !strings.HasPrefix(value, "bytes ") {
		return
	}
	rangePart, totalPart, ok := strings.Cut(strings.TrimPrefix(value, "bytes "), "/")
	if !ok {
		return
	}
	first, last, ok := strings.Cut(rangePart, "-")
	if !ok {
		return
	}
	start, valid = parseNonnegative(first)
	if !valid {
		return
	}
	end, valid = parseNonnegative(last)
	if !valid {
		return
	}
	total, valid = parseNonnegative(totalPart)
	valid = valid && total > 0 && end >= start && end < total
	return
}

func parseUnsatisfiedRange(value string) (int64, bool) {
	if !strings.HasPrefix(value, "bytes */") {
		return 0, false
	}
	total, valid := parseNonnegative(strings.TrimPrefix(value, "bytes */"))
	return total, valid && total > 0
}

func parseNonnegative(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil
}

func responseValidators(headers http.Header) (string, string) {
	etag := strings.TrimSpace(headers.Get("ETag"))
	if strongETag(etag) && len(etag) <= 1024 {
		return etag, ""
	}
	// Last-Modified is used as an If-Range validator only when the stored
	// response Date is at least one minute later (RFC 9110 section 8.8.2.2).
	modified, modifiedErr := http.ParseTime(headers.Get("Last-Modified"))
	date, dateErr := http.ParseTime(headers.Get("Date"))
	if etag == "" && modifiedErr == nil && dateErr == nil && date.Sub(modified) >= time.Minute {
		return "", modified.UTC().Format(http.TimeFormat)
	}
	return "", ""
}

func validatorChanged(state checkpoint, headers http.Header) bool {
	if state.ETag != "" {
		value := strings.TrimSpace(headers.Get("ETag"))
		return value != "" && value != state.ETag
	}
	if state.LastModified != "" && headers.Get("Last-Modified") != "" {
		modified, err := http.ParseTime(headers.Get("Last-Modified"))
		return err != nil || modified.UTC().Format(http.TimeFormat) != state.LastModified
	}
	return false
}
