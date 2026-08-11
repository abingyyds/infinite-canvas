package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

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
	// 复用 AI 代理那个带超时的 client：http.DefaultClient 没有超时，渠道卡住时会一直占着连接。
	response, err := aiUpstreamClient.Do(request)
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
