package wallhaven

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

var _ ports.WallpaperDetails = (*Client)(nil)
var wallpaperID = regexp.MustCompile(`^[a-zA-Z0-9]{6}$`)

// Detail reads the official /w/{id} endpoint. Credentials stay in the same
// protected header as Search and response/transport errors do not expose URLs
// or remote bodies to the UI.
func (c *Client) Detail(ctx context.Context, id string) (domain.WallpaperDetail, error) {
	if !wallpaperID.MatchString(id) {
		return domain.WallpaperDetail{}, errors.New("壁纸编号无效")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u := c.endpoint
	u.Path = strings.TrimSuffix(u.Path, "/search") + "/w/" + id
	u.RawPath, u.RawQuery = "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return domain.WallpaperDetail{}, errors.New("无法创建壁纸详情请求")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "MyGO-Wallhaven/0.1.0")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return domain.WallpaperDetail{}, ctx.Err()
		}
		return domain.WallpaperDetail{}, errors.New("壁纸详情连接失败，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return domain.WallpaperDetail{}, errors.New("壁纸已删除或不存在（HTTP 404）")
	}
	if response.StatusCode != http.StatusOK {
		return domain.WallpaperDetail{}, statusError(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return domain.WallpaperDetail{}, ctx.Err()
		}
		return domain.WallpaperDetail{}, errors.New("壁纸详情读取失败，请重试")
	}
	if len(body) > maxResponseBytes {
		return domain.WallpaperDetail{}, errors.New("壁纸详情响应超过大小限制")
	}
	var payload struct {
		Data *detailResponse `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Data == nil {
		return domain.WallpaperDetail{}, errors.New("壁纸服务返回了无效详情数据")
	}
	image := payload.Data
	if image.ID != id || image.Width <= 0 || image.Height <= 0 || image.FileSize < 0 || image.Views < 0 || image.Favorites < 0 {
		return domain.WallpaperDetail{}, errors.New("壁纸服务返回了无效详情数据")
	}
	thumbnail := image.Thumbs.Small
	if thumbnail == "" {
		thumbnail = image.Thumbs.Original
	}
	if thumbnail == "" {
		thumbnail = image.Thumbs.Large
	}
	result := domain.WallpaperDetail{
		Wallpaper: domain.Wallpaper{ID: image.ID, URL: image.URL, Path: image.Path, Thumbnail: thumbnail, OriginalThumbnail: image.Thumbs.Original,
			Width: image.Width, Height: image.Height, Resolution: image.Resolution, FileSize: image.FileSize, FileType: image.FileType, Category: image.Category, Purity: image.Purity},
		Views: image.Views, Favorites: image.Favorites, Source: image.Source, ShortURL: image.ShortURL, CreatedAt: image.CreatedAt,
		Uploader: image.Uploader.Username, Colors: append([]string(nil), image.Colors...),
	}
	for _, tag := range image.Tags {
		result.Tags = append(result.Tags, domain.WallpaperTag{ID: tag.ID, Name: tag.Name, Purity: tag.Purity})
	}
	return result, nil
}

type detailResponse struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Path       string   `json:"path"`
	ShortURL   string   `json:"short_url"`
	Width      int      `json:"dimension_x"`
	Height     int      `json:"dimension_y"`
	Resolution string   `json:"resolution"`
	FileSize   int64    `json:"file_size"`
	FileType   string   `json:"file_type"`
	Category   string   `json:"category"`
	Purity     string   `json:"purity"`
	Views      int      `json:"views"`
	Favorites  int      `json:"favorites"`
	Source     string   `json:"source"`
	CreatedAt  string   `json:"created_at"`
	Colors     []string `json:"colors"`
	Uploader   struct {
		Username string `json:"username"`
	} `json:"uploader"`
	Thumbs struct{ Small, Original, Large string } `json:"thumbs"`
	Tags   []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Purity string `json:"purity"`
	} `json:"tags"`
}
