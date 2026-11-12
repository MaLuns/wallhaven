// Package files saves original wallpaper images into a user-selected directory.
package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
	_ "golang.org/x/image/webp"
)

const (
	defaultMaxBytes  = int64(64 << 20)
	defaultMaxPixels = int64(64 << 20)
	maxDimension     = 32768
)

var wallpaperID = regexp.MustCompile(`^[a-zA-Z0-9]{6}$`)

type Options struct {
	HTTPClient          *http.Client
	MaxBytes, MaxPixels int64
	Timeout             time.Duration
	StateDir            string
	Originals           ports.EncodedOriginals
}

type Downloader struct {
	client              *http.Client
	maxBytes, maxPixels int64
	timeout             time.Duration
	stateDir            string
	originals           ports.EncodedOriginals
}

var _ ports.OriginalFiles = (*Downloader)(nil)

func New(options Options) *Downloader {
	client := http.Client{}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !allowedURL(request.URL) || len(via) > 0 && via[0].URL.Scheme == "https" && request.URL.Scheme != "https" {
			return errors.New("原图重定向地址无效")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultMaxBytes
	}
	if options.MaxPixels <= 0 {
		options.MaxPixels = defaultMaxPixels
	}
	if options.Timeout <= 0 {
		options.Timeout = 60 * time.Second
	}
	return &Downloader{client: &client, maxBytes: options.MaxBytes, maxPixels: options.MaxPixels, timeout: options.Timeout, stateDir: options.StateDir, originals: options.Originals}
}

func allowedURL(value *url.URL) bool {
	return value != nil && value.Hostname() != "" && (value.Scheme == "http" || value.Scheme == "https") && value.User == nil && value.Fragment == ""
}

type downloadError struct {
	message string
	cause   error
}

func (e downloadError) Error() string { return e.message }
func (e downloadError) Unwrap() error { return e.cause }

func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return downloadError{"原图下载已取消", context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return downloadError{"原图下载超时，请重试", context.DeadlineExceeded}
	}
	return nil
}

// Download streams into a private temporary file, validates the actual image,
// and atomically publishes a complete file. Colliding names receive a suffix;
// hard-link creation never overwrites an existing file, even across workers.
func (d *Downloader) Download(ctx context.Context, wallpaper domain.Wallpaper, directory string, progress func(received, total int64)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !wallpaperID.MatchString(wallpaper.ID) {
		return "", errors.New("壁纸编号无效")
	}
	address, err := url.Parse(strings.TrimSpace(wallpaper.Path))
	if err != nil || !allowedURL(address) {
		return "", errors.New("原图地址无效")
	}
	if strings.TrimSpace(directory) == "" {
		return "", errors.New("请选择下载目录")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return "", errors.New("下载目录无效")
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	if err := contextError(ctx, nil); err != nil {
		return "", err
	}
	if d.originals != nil {
		if data, found := d.originals.Get(address.String()); found {
			return d.saveCached(ctx, wallpaper, directory, data, progress)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return "", errors.New("无法创建原图下载请求")
	}
	request.Header.Set("Accept", "image/*")
	request.Header.Set("User-Agent", "MyGO-Wallhaven/0.1.0")
	response, err := d.client.Do(request)
	if err != nil {
		if canceled := contextError(ctx, err); canceled != nil {
			return "", canceled
		}
		return "", errors.New("原图连接失败，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("原图下载失败（HTTP %d）", response.StatusCode)
	}
	if response.ContentLength > d.maxBytes {
		return "", errors.New("原图文件超过大小限制")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", errors.New("无法创建下载目录，请检查写入权限")
	}
	temporary, err := os.CreateTemp(directory, ".wallhaven-"+wallpaper.ID+"-*.part")
	if err != nil {
		return "", errors.New("无法写入下载目录，请检查写入权限")
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := d.transfer(ctx, response, temporary, progress); err != nil {
		return "", err
	}
	extension, err := d.validate(ctx, temporary)
	if err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", errors.New("原图保存失败，请检查磁盘空间")
	}
	if err := temporary.Close(); err != nil {
		return "", errors.New("原图保存失败，请检查磁盘空间")
	}
	if err := contextError(ctx, nil); err != nil {
		return "", err
	}
	return publishOriginal(ctx, temporary.Name(), directory, wallpaper.ID, extension)
}

func publishOriginal(ctx context.Context, temporary, directory, id, extension string) (string, error) {
	for suffix := 0; ; suffix++ {
		if err := contextError(ctx, nil); err != nil {
			return "", err
		}
		name := "wallhaven-" + id
		if suffix != 0 {
			name += fmt.Sprintf("-%d", suffix)
		}
		destination := filepath.Join(directory, name+extension)
		if err := os.Link(temporary, destination); err == nil {
			return destination, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", errors.New("无法保存原图，请检查目录权限及文件系统支持")
		}
		if err := contextError(ctx, nil); err != nil {
			return "", err
		}
	}
}

func (d *Downloader) saveCached(ctx context.Context, wallpaper domain.Wallpaper, directory string, data []byte, progress func(int64, int64)) (string, error) {
	if int64(len(data)) > d.maxBytes {
		return "", errors.New("原图文件超过大小限制")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", errors.New("无法创建下载目录，请检查写入权限")
	}
	file, err := os.CreateTemp(directory, ".wallhaven-"+wallpaper.ID+"-*.part")
	if err != nil {
		return "", errors.New("无法写入下载目录，请检查写入权限")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	response := &http.Response{Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
	if err := d.transfer(ctx, response, file, progress); err != nil {
		return "", err
	}
	extension, err := d.validate(ctx, file)
	if err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", errors.New("原图保存失败，请检查磁盘空间")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("原图保存失败，请检查磁盘空间")
	}
	return publishOriginal(ctx, file.Name(), directory, wallpaper.ID, extension)
}

func (d *Downloader) transfer(ctx context.Context, response *http.Response, destination *os.File, progress func(int64, int64)) error {
	total := max(int64(0), response.ContentLength)
	notify := func(received, expected int64) {
		if progress != nil {
			progress(received, expected)
		}
	}
	notify(0, total)
	buffer := make([]byte, 32<<10)
	var received int64
	for {
		if err := contextError(ctx, nil); err != nil {
			return err
		}
		count, readError := response.Body.Read(buffer)
		if count > 0 {
			if int64(count) > d.maxBytes-received {
				return errors.New("原图文件超过大小限制")
			}
			if _, err := destination.Write(buffer[:count]); err != nil {
				return errors.New("原图保存失败，请检查磁盘空间")
			}
			received += int64(count)
			notify(received, total)
		}
		if readError != nil {
			if errors.Is(readError, io.EOF) {
				break
			}
			if canceled := contextError(ctx, readError); canceled != nil {
				return canceled
			}
			return errors.New("原图读取失败，请重试")
		}
	}
	if err := contextError(ctx, nil); err != nil {
		return err
	}
	notify(received, received)
	return nil
}

type cancelReader struct {
	context context.Context
	reader  io.Reader
}

func (r cancelReader) Read(data []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func (d *Downloader) validate(ctx context.Context, file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", errors.New("无法校验原图文件")
	}
	config, format, err := image.DecodeConfig(cancelReader{ctx, file})
	if canceled := contextError(ctx, err); canceled != nil {
		return "", canceled
	}
	if err != nil {
		return "", errors.New("原图格式无效或不受支持")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxDimension || config.Height > maxDimension || int64(config.Width) > d.maxPixels/int64(config.Height) {
		return "", errors.New("原图尺寸超过下载限制")
	}
	extensions := map[string]string{"jpeg": ".jpg", "png": ".png", "webp": ".webp", "gif": ".gif"}
	extension := extensions[format]
	if extension == "" {
		return "", errors.New("原图格式无效或不受支持")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", errors.New("无法校验原图文件")
	}
	_, _, err = image.Decode(cancelReader{ctx, file})
	if canceled := contextError(ctx, err); canceled != nil {
		return "", canceled
	}
	if err != nil {
		return "", errors.New("原图数据损坏，请重试")
	}
	return extension, nil
}
