// Package wallhaven adapts the official Wallhaven search API to application values.
package wallhaven

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MaLuns/wallhaven-electron/internal/domain"
	"github.com/MaLuns/wallhaven-electron/internal/ports"
)

const (
	DefaultBaseURL   = "https://wallhaven.cc/api/v1/"
	maxResponseBytes = 4 << 20
)

type Options struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Timeout    time.Duration
}

type Client struct {
	endpoint url.URL
	apiKey   string
	http     *http.Client
	timeout  time.Duration
}

var _ ports.WallpaperSource = (*Client)(nil)

func New(options Options) (*Client, error) {
	base := options.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("壁纸服务地址无效")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/search"
	client := http.Client{}
	if options.HTTPClient != nil {
		client = *options.HTTPClient
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// X-API-Key is a custom credential header; net/http does not strip it
		// automatically on cross-origin redirects as it does Authorization.
		if len(via) >= 10 || req.URL.Scheme != u.Scheme || req.URL.Host != u.Host {
			return errors.New("壁纸服务重定向无效")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{endpoint: *u, apiKey: strings.TrimSpace(options.APIKey), http: &client, timeout: timeout}, nil
}

// Search uses a header for credentials, so neither query snapshots nor URL
// errors contain the API key. Remote response bodies are not shown as errors.
func (c *Client) Search(ctx context.Context, query domain.SearchQuery, page int) (domain.SearchResult, error) {
	query = query.Normalize()
	if err := query.Validate(); err != nil {
		return domain.SearchResult{}, err
	}
	if page < 1 {
		return domain.SearchResult{}, errors.New("页码必须大于零")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u := c.endpoint
	u.RawQuery = parameters(query, page).Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return domain.SearchResult{}, errors.New("无法创建壁纸请求")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "MyGO-Wallhaven/0.1.0")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return domain.SearchResult{}, ctx.Err()
		}
		return domain.SearchResult{}, errors.New("壁纸服务连接失败，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return domain.SearchResult{}, statusError(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return domain.SearchResult{}, ctx.Err()
		}
		return domain.SearchResult{}, errors.New("壁纸响应读取失败，请重试")
	}
	if len(body) > maxResponseBytes {
		return domain.SearchResult{}, errors.New("壁纸服务响应超过大小限制")
	}
	var payload searchResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.Meta == nil {
		return domain.SearchResult{}, errors.New("壁纸服务返回了无效数据")
	}
	meta := payload.Meta
	emptyFirstPage := page == 1 && len(payload.Data) == 0 && meta.Total == 0 && meta.LastPage == 0
	if meta.CurrentPage != page || meta.LastPage < 0 || meta.Total < 0 || (!emptyFirstPage && (meta.LastPage < 1 || meta.CurrentPage > meta.LastPage)) {
		return domain.SearchResult{}, errors.New("壁纸服务分页信息无效")
	}
	result := domain.SearchResult{CurrentPage: meta.CurrentPage, LastPage: meta.LastPage, Total: meta.Total, Seed: meta.Seed}
	result.Wallpapers = make([]domain.Wallpaper, 0, len(payload.Data))
	for _, image := range payload.Data {
		if image.ID == "" {
			return domain.SearchResult{}, errors.New("壁纸服务返回了无效图片信息")
		}
		thumbnail := image.Thumbs.Small
		if thumbnail == "" {
			thumbnail = image.Thumbs.Original
		}
		if thumbnail == "" {
			thumbnail = image.Thumbs.Large
		}
		result.Wallpapers = append(result.Wallpapers, domain.Wallpaper{
			ID: image.ID, URL: image.URL, Path: image.Path, Thumbnail: thumbnail, OriginalThumbnail: image.Thumbs.Original,
			Width: image.Width, Height: image.Height, Resolution: image.Resolution,
			FileSize: image.FileSize, FileType: image.FileType, Category: image.Category, Purity: image.Purity,
		})
	}
	return result, nil
}

func parameters(query domain.SearchQuery, page int) url.Values {
	values := url.Values{
		"categories": {query.Categories}, "purity": {query.Purity},
		"sorting": {query.Sorting}, "order": {query.Order}, "page": {strconv.Itoa(page)},
	}
	for key, value := range map[string]string{
		"q": query.Query, "atleast": query.AtLeast,
		"resolutions": strings.Join(query.Resolutions, ","),
		"ratios":      strings.Join(query.Ratios, ","), "colors": query.Color,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if query.Sorting == "toplist" {
		values.Set("topRange", query.TopRange)
	}
	if query.Sorting == "random" && query.Seed != "" {
		values.Set("seed", query.Seed)
	}
	return values
}

func statusError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return errors.New("需要有效的 Wallhaven API Key，请检查配置（HTTP 401）")
	case http.StatusForbidden:
		return errors.New("壁纸服务拒绝访问，请稍后重试（HTTP 403）")
	case http.StatusTooManyRequests:
		return errors.New("请求过于频繁，请稍后重试（HTTP 429）")
	default:
		return fmt.Errorf("壁纸服务请求失败（HTTP %d），请重试", status)
	}
}

type searchResponse struct {
	Data []struct {
		ID         string `json:"id"`
		URL        string `json:"url"`
		Path       string `json:"path"`
		Width      int    `json:"dimension_x"`
		Height     int    `json:"dimension_y"`
		Resolution string `json:"resolution"`
		FileSize   int64  `json:"file_size"`
		FileType   string `json:"file_type"`
		Category   string `json:"category"`
		Purity     string `json:"purity"`
		Thumbs     struct {
			Small, Original, Large string
		} `json:"thumbs"`
	} `json:"data"`
	Meta *struct {
		CurrentPage int    `json:"current_page"`
		LastPage    int    `json:"last_page"`
		Total       int    `json:"total"`
		Seed        string `json:"seed"`
	} `json:"meta"`
}
