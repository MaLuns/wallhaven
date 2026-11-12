package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const maxCheckpointBytes = 32 << 10

type checkpoint struct {
	Version      int    `json:"version"`
	Key          string `json:"key"`
	ID           string `json:"id"`
	URL          string `json:"url"`
	Directory    string `json:"directory"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Total        int64  `json:"total"`
	Published    string `json:"published,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Digest       string `json:"sha256,omitempty"`
	Complete     bool   `json:"complete,omitempty"`
}

func hashKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func partialPath(state checkpoint) string {
	return filepath.Join(state.Directory, ".wallhaven-"+hashKey(state.Key)+".part")
}

func (d *Downloader) checkpointPath(key string) (string, error) {
	if key == "" || len(key) > 256 || strings.ContainsRune(key, 0) {
		return "", errors.New("下载任务标识无效")
	}
	if strings.TrimSpace(d.stateDir) == "" || strings.ContainsRune(d.stateDir, 0) {
		return "", errors.New("下载状态目录尚未配置")
	}
	directory, err := filepath.Abs(d.stateDir)
	if err != nil {
		return "", errors.New("下载状态目录无效")
	}
	return filepath.Join(directory, hashKey(key)+".json"), nil
}

func (d *Downloader) readCheckpoint(ctx context.Context, path, key string) (*checkpoint, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCheckpointBytes {
		return nil, errors.New("下载断点文件类型或大小无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法读取下载断点")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("下载断点文件已变更")
	}
	data, err := io.ReadAll(io.LimitReader(cancelReader{ctx, file}, maxCheckpointBytes+1))
	if err != nil {
		if canceled := contextError(ctx, err); canceled != nil {
			return nil, canceled
		}
		return nil, errors.New("无法读取下载断点")
	}
	if len(data) > maxCheckpointBytes {
		return nil, errors.New("下载断点文件超过大小限制")
	}
	var state checkpoint
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || d.validateCheckpoint(state, key) != nil {
		return nil, errors.New("下载断点已损坏，未修改现有文件")
	}
	return &state, nil
}

func (d *Downloader) validateCheckpoint(state checkpoint, key string) error {
	address, err := url.Parse(state.URL)
	if state.Version != 1 || state.Key != key || !wallpaperID.MatchString(state.ID) || len(state.URL) > 8192 || err != nil || !allowedURL(address) {
		return errors.New("下载断点来源无效")
	}
	if state.Directory == "" || len(state.Directory) > 4096 || strings.ContainsRune(state.Directory, 0) || !filepath.IsAbs(state.Directory) || filepath.Clean(state.Directory) != state.Directory {
		return errors.New("下载断点目录无效")
	}
	if state.Total < 0 || state.Total > d.maxBytes || len(state.ETag) > 1024 || state.ETag != "" && !strongETag(state.ETag) {
		return errors.New("下载断点校验信息无效")
	}
	if state.LastModified != "" {
		if _, err := http.ParseTime(state.LastModified); err != nil || len(state.LastModified) > 128 {
			return errors.New("下载断点修改时间无效")
		}
	}
	if state.Published == "" {
		if state.Size != 0 || state.Digest != "" || state.Complete {
			return errors.New("下载完成记录不完整")
		}
		return nil
	}
	pattern := `^wallhaven-` + state.ID + `(?:-[1-9][0-9]*)?\.(?:jpg|png|gif|webp)$`
	if !regexp.MustCompile(pattern).MatchString(state.Published) || filepath.Base(state.Published) != state.Published || state.Size <= 0 || state.Size > d.maxBytes || state.Total != state.Size {
		return errors.New("下载完成路径无效")
	}
	if digest, err := hex.DecodeString(state.Digest); err != nil || len(digest) != sha256.Size {
		return errors.New("下载完成校验信息无效")
	}
	return nil
}

func writeCheckpoint(ctx context.Context, path string, state checkpoint) error {
	if err := contextError(ctx, nil); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxCheckpointBytes {
		return errors.New("下载断点信息超过大小限制")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法创建下载状态目录")
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return errors.New("无法保存下载断点")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return errors.New("无法保存下载断点")
	}
	if err := file.Sync(); err != nil {
		return errors.New("无法同步下载断点")
	}
	if err := file.Close(); err != nil {
		return errors.New("无法关闭下载断点")
	}
	if err := contextError(ctx, nil); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("无法替换下载断点")
	}
	return nil
}

// removePartial derives its private filename from the validated key; no path
// from a checkpoint can name an arbitrary user file or a published original.
func removePartial(state checkpoint) error {
	path := partialPath(state)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("下载临时文件类型无效，未删除任何文件")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("无法清理未完成的下载文件")
	}
	return nil
}

type keyGate struct {
	ready chan struct{}
	refs  int
}

var checkpointGates = struct {
	sync.Mutex
	keys map[string]*keyGate
}{keys: make(map[string]*keyGate)}

// Instances pointing at the same sidecar also share a gate within this process.
// Cancellation while waiting cannot acquire a slot or remove another worker's
// checkpoint. No cross-process lock or user-directory sweep is introduced.
func acquireCheckpoint(ctx context.Context, path string) (func(), error) {
	checkpointGates.Lock()
	gate := checkpointGates.keys[path]
	if gate == nil {
		gate = &keyGate{ready: make(chan struct{}, 1)}
		checkpointGates.keys[path] = gate
	}
	gate.refs++
	checkpointGates.Unlock()
	drop := func() {
		checkpointGates.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(checkpointGates.keys, path)
		}
		checkpointGates.Unlock()
	}
	select {
	case gate.ready <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.ready
			drop()
			return nil, err
		}
		return func() { <-gate.ready; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func strongETag(value string) bool {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for _, character := range value[1 : len(value)-1] {
		if character < 0x21 || character == '"' || character == 0x7f {
			return false
		}
	}
	return true
}
