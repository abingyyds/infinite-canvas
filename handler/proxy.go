package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type mediaContentRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

// MediaContent 代下载浏览器直接取不到的媒体内容：需要 Authorization 的视频地址，
// 以及没有 CORS 头的图片外链（预签名 URL 不能带 Authorization，所以 apiKey 可选）。
func MediaContent(w http.ResponseWriter, r *http.Request) {
	var payload mediaContentRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		FailStatus(w, http.StatusBadRequest, "媒体下载请求无效")
		return
	}
	target := strings.TrimSpace(payload.URL)
	apiKey := strings.TrimSpace(payload.APIKey)
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		FailStatus(w, http.StatusBadRequest, "媒体下载地址无效")
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		FailStatus(w, http.StatusBadRequest, "媒体下载地址无效")
		return
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// 复用 AI 代理那个带超时的 client：http.DefaultClient 没有超时，渠道卡住时会一直占着连接。
	response, err := aiUpstreamClient.Do(request)
	if err != nil {
		FailStatus(w, http.StatusBadGateway, "媒体下载失败，请稍后重试")
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
