package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	imageFetchTimeout = 15 * time.Second
	maxImageBytes     = 12 << 20
)

// promptImageHosts 提示词封面所在的图床白名单，避免这个端点变成任意图片代理。
var promptImageHosts = map[string]bool{
	"raw.githubusercontent.com":    true,
	"pbs.twimg.com":                true,
	"cdn.imgedify.com":             true,
	"cms-assets.youmind.com":       true,
	"marketing-assets.youmind.com": true,
}

type videoContentRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

// VideoContent 代下载需要 Authorization 才能取到的视频内容，浏览器无法直接带上渠道 API Key。
func VideoContent(w http.ResponseWriter, r *http.Request) {
	var payload videoContentRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		FailStatus(w, http.StatusBadRequest, "视频下载请求无效")
		return
	}
	target := strings.TrimSpace(payload.URL)
	apiKey := strings.TrimSpace(payload.APIKey)
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		FailStatus(w, http.StatusBadRequest, "视频下载地址无效")
		return
	}
	if apiKey == "" {
		FailStatus(w, http.StatusBadRequest, "视频下载缺少 API Key")
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		FailStatus(w, http.StatusBadRequest, "视频下载地址无效")
		return
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		FailStatus(w, http.StatusBadGateway, "视频下载失败，请稍后重试")
		return
	}
	defer response.Body.Close()

	for _, key := range []string{"Content-Type", "Content-Disposition", "Content-Length"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

// PromptImage 代理白名单图床的提示词封面，绕开第三方站点的防盗链。
func PromptImage(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(r.URL.Query().Get("url"))
	if err != nil {
		http.Error(w, "图片地址无效", http.StatusBadRequest)
		return
	}
	if target.Scheme != "https" || !promptImageHosts[strings.ToLower(target.Hostname())] {
		http.Error(w, "图片地址不受支持", http.StatusBadRequest)
		return
	}

	client := &http.Client{Timeout: imageFetchTimeout}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		http.Error(w, "图片地址无效", http.StatusBadRequest)
		return
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; InfiniteCanvas/1.0)")
	response, err := client.Do(request)
	if err != nil {
		http.Error(w, "图片加载失败", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		http.Error(w, "图片加载失败", response.StatusCode)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "响应不是图片", http.StatusUnsupportedMediaType)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes+1))
	if err != nil {
		http.Error(w, "图片加载失败", http.StatusBadGateway)
		return
	}
	if len(data) > maxImageBytes {
		http.Error(w, "图片过大", http.StatusRequestEntityTooLarge)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=604800")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
