package images

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/ports"
	kit "github.com/egoist/mygo/ui"
)

const (
	defaultDetailBytes  = int64(64 << 20)
	defaultDetailPixels = int64(64 << 20)
	detailDimension     = 32768
	detailTimeout       = 60 * time.Second
)

type DetailOptions struct {
	HTTPClient *http.Client
	Originals  ports.EncodedOriginals
	// MaxBytes bounds the encoded response. MaxPixels bounds the decoded base
	// image, whose RGBA pixels use four bytes each. Decoder conversion and GPU
	// mipmaps can temporarily need additional memory.
	MaxBytes, MaxPixels int64
}

// DetailLoader returns a full-resolution immutable bitmap. Its optional shared
// encoded cache lets file actions reuse an already fetched original. Load belongs on a worker; the caller publishes its result
// on the UI thread and cancels the context when a preview is replaced or closed.
// A single slot bounds concurrent transfer/decode work for this loader.
type DetailLoader struct {
	client              *http.Client
	maxBytes, maxPixels int64
	slot                chan struct{}
	originals           ports.EncodedOriginals
}

func NewDetailLoader(options DetailOptions) *DetailLoader {
	client := http.Client{}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !detailURLAllowed(req.URL) {
			return errors.New("原图重定向地址无效")
		}
		if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("原图重定向地址无效")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultDetailBytes
	}
	if options.MaxPixels <= 0 {
		options.MaxPixels = defaultDetailPixels
	}
	return &DetailLoader{client: &client, maxBytes: options.MaxBytes, maxPixels: options.MaxPixels, slot: make(chan struct{}, 1), originals: options.Originals}
}

func detailURLAllowed(u *url.URL) bool {
	return u != nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Fragment == ""
}

type detailLoadError struct {
	message string
	cause   error
}

func (e detailLoadError) Error() string { return e.message }
func (e detailLoadError) Unwrap() error { return e.cause }

func detailContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return detailLoadError{"原图加载已取消", context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return detailLoadError{"原图加载超时，请重试", context.DeadlineExceeded}
	}
	return nil
}

// progress runs on the loading worker and reports bytes received. A zero total
// means the response did not provide its length.
func (l *DetailLoader) Load(ctx context.Context, rawURL string, progress func(received, total int64)) (*kit.Bitmap, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !detailURLAllowed(u) {
		return nil, errors.New("原图地址无效")
	}
	ctx, cancel := context.WithTimeout(ctx, detailTimeout)
	defer cancel()
	select {
	case l.slot <- struct{}{}:
		defer func() { <-l.slot }()
	case <-ctx.Done():
		return nil, detailContextError(ctx, ctx.Err())
	}
	if err := detailContextError(ctx, nil); err != nil {
		return nil, err
	}
	if l.originals != nil {
		if data, ok := l.originals.Get(u.String()); ok {
			return l.decode(ctx, data, progress)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("无法创建原图请求")
	}
	request.Header.Set("Accept", "image/*")
	request.Header.Set("User-Agent", "MyGO-Wallhaven/0.1.0")
	response, err := l.client.Do(request)
	if err != nil {
		if canceled := detailContextError(ctx, err); canceled != nil {
			return nil, canceled
		}
		return nil, errors.New("原图连接失败，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, errors.New("原图访问被拒绝，请检查内容访问权限")
		case http.StatusNotFound, http.StatusGone:
			return nil, errors.New("原图不存在或已被移除")
		case http.StatusTooManyRequests:
			return nil, errors.New("原图请求过于频繁，请稍后重试")
		default:
			return nil, errors.New("原图服务暂时不可用，请重试")
		}
	}
	if response.ContentLength > l.maxBytes {
		return nil, errors.New("原图文件超过大小限制")
	}
	// Read one extra byte so chunked responses cannot bypass the same bound.
	limit := l.maxBytes
	if limit < int64(^uint64(0)>>1) {
		limit++
	}
	total := max(int64(0), response.ContentLength)
	if progress != nil {
		progress(0, total)
	}
	reader := &detailProgressReader{Reader: io.LimitReader(response.Body, limit), total: total, progress: progress}
	data, err := io.ReadAll(reader)
	if err != nil {
		if canceled := detailContextError(ctx, err); canceled != nil {
			return nil, canceled
		}
		return nil, errors.New("原图读取失败，请重试")
	}
	if int64(len(data)) > l.maxBytes {
		return nil, errors.New("原图文件超过大小限制")
	}
	if err := detailContextError(ctx, nil); err != nil {
		return nil, err
	}
	bitmap, err := l.decode(ctx, data, progress)
	if err == nil && l.originals != nil {
		l.originals.Put(u.String(), data)
	}
	return bitmap, err
}

func (l *DetailLoader) decode(ctx context.Context, data []byte, progress func(int64, int64)) (*kit.Bitmap, error) {
	if int64(len(data)) > l.maxBytes {
		return nil, errors.New("原图文件超过大小限制")
	}
	if err := detailContextError(ctx, nil); err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("原图格式无效或不受支持")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > detailDimension || config.Height > detailDimension || int64(config.Width) > l.maxPixels/int64(config.Height) {
		return nil, errors.New("原图尺寸超过预览限制")
	}
	bitmap, err := kit.DecodeBitmap(data)
	if canceled := detailContextError(ctx, nil); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, errors.New("原图解码失败，请重试")
	}
	if progress != nil {
		progress(int64(len(data)), int64(len(data)))
	}
	return bitmap, nil
}

type detailProgressReader struct {
	io.Reader
	received, total int64
	progress        func(int64, int64)
	reportedAt      time.Time
}

func (r *detailProgressReader) Read(data []byte) (int, error) {
	count, err := r.Reader.Read(data)
	r.received += int64(count)
	if count > 0 && r.progress != nil {
		now := time.Now()
		if r.reportedAt.IsZero() || now.Sub(r.reportedAt) >= 50*time.Millisecond || (r.total > 0 && r.received >= r.total) || err != nil {
			r.reportedAt = now
			r.progress(r.received, r.total)
		}
	}
	return count, err
}
