package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/service"
)

func AIImagesGenerations(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/images/generations")
}

func AIImagesEdits(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/images/edits")
}

func AIChatCompletions(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/chat/completions")
}

func AIAudioSpeech(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/audio/speech")
}

func AIVideos(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/videos")
}

func AIVideoGenerations(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/videos/generations")
}

func AIVideo(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/videos/"+id)
}

func AIVideoContent(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/videos/"+id+"/content")
}

func proxyAIGetRequest(w http.ResponseWriter, r *http.Request, path string) {
	modelName := r.URL.Query().Get("model")
	if strings.TrimSpace(modelName) == "" {
		modelName = "grok-imagine-video"
	}
	user, _ := service.UserFromContext(r.Context())
	channel, _, err := selectAIChannel(user.ID, modelName)
	if err != nil {
		log.Printf("AI proxy select channel failed: model=%s err=%v", modelName, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	path = resolveAIProxyPath(channel.BaseURL, modelName, path)
	request, err := http.NewRequest(http.MethodGet, service.BuildModelChannelURL(channel, path), nil)
	if err != nil {
		Fail(w, "AI 接口请求失败")
		return
	}
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	copyAIResponse(w, request, modelName, path, nil)
}

func proxyAIRequest(w http.ResponseWriter, r *http.Request, path string) {
	body, contentType, modelName, err := readAIRequest(r)
	if err != nil {
		log.Printf("AI proxy request read failed: %v", err)
		Fail(w, "AI 接口请求失败")
		return
	}
	user, ok := service.UserFromContext(r.Context())
	if !ok {
		Fail(w, "未登录或权限不足")
		return
	}
	channel, isGatewayChannel, err := selectAIChannel(user.ID, modelName)
	if err != nil {
		log.Printf("AI proxy select channel failed: model=%s err=%v", modelName, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	credits, err := service.ModelCost(modelName)
	if err != nil {
		log.Printf("AI proxy read model cost failed: model=%s err=%v", modelName, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	if isGatewayChannel {
		credits = 0
	}
	credits *= readAIRequestCount(body, contentType)
	proxyPath := resolveAIProxyPath(channel.BaseURL, modelName, path)
	body, contentType, err = prepareAIProxyBody(path, proxyPath, body, contentType)
	if err != nil {
		log.Printf("AI proxy prepare body failed: path=%s proxyPath=%s err=%v", path, proxyPath, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	request, err := http.NewRequest(http.MethodPost, service.BuildModelChannelURL(channel, proxyPath), bytes.NewReader(body))
	if err != nil {
		log.Printf("AI proxy build request failed: url=%s err=%v", service.BuildModelChannelURL(channel, proxyPath), err)
		Fail(w, "AI 接口请求失败")
		return
	}
	request.ContentLength = int64(len(body))
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if err := service.ConsumeUserCredits(user.ID, modelName, credits, proxyPath); err != nil {
		FailError(w, err)
		return
	}
	copyAIResponse(w, request, modelName, proxyPath, func() {
		if err := service.RefundUserCredits(user.ID, modelName, credits, proxyPath); err != nil {
			log.Printf("AI proxy refund credits failed: user=%s model=%s credits=%d err=%v", user.ID, modelName, credits, err)
		}
	})
}

func copyAIResponse(w http.ResponseWriter, request *http.Request, modelName string, path string, onFailure func()) {
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		log.Printf("AI proxy request failed: url=%s path=%s model=%s err=%v", request.URL.String(), path, modelName, err)
		if onFailure != nil {
			onFailure()
		}
		Fail(w, "AI 接口请求失败")
		return
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		log.Printf("AI upstream error: url=%s path=%s model=%s status=%d detail=%s", request.URL.String(), path, modelName, response.StatusCode, aiUpstreamErrorDetail(body))
		if onFailure != nil {
			onFailure()
		}
		Fail(w, aiUpstreamStatusMessage(response.StatusCode, body))
		return
	}

	for key, values := range response.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func readAIRequest(r *http.Request) ([]byte, string, string, error) {
	contentType := r.Header.Get("Content-Type")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", "", err
	}
	modelName := ""
	if strings.HasPrefix(contentType, "multipart/form-data") {
		modelName = readMultipartModel(body, contentType)
	} else {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &payload)
		modelName = payload.Model
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, "", "", errMissingModel
	}
	return body, contentType, modelName, nil
}

func prepareAIProxyBody(path string, proxyPath string, body []byte, contentType string) ([]byte, string, error) {
	if path == "/videos/generations" {
		if proxyPath == "/videos" {
			return prepareLegacyGrokVideoBody(body, contentType)
		}
		if strings.HasPrefix(contentType, "application/json") {
			var payload struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(body, &payload)
			if isGrokPreviewVideo(payload.Model) {
				return prepareGrokVideoJSONBody(body, contentType, payload.Model)
			}
		}
		return body, contentType, nil
	}
	if proxyPath != "/videos" || !strings.HasPrefix(contentType, "application/json") {
		return body, contentType, nil
	}
	var payload struct {
		Model          string   `json:"model"`
		Prompt         string   `json:"prompt"`
		Seconds        string   `json:"seconds"`
		Size           string   `json:"size"`
		ResolutionName string   `json:"resolution_name"`
		Preset         string   `json:"preset"`
		InputReference []string `json:"input_reference"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	if isGrokImagineVideo(payload.Model) {
		return prepareGrokVideoJSONBody(body, contentType, payload.Model)
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	writeMultipartField(writer, "model", payload.Model)
	writeMultipartField(writer, "prompt", payload.Prompt)
	writeMultipartField(writer, "seconds", payload.Seconds)
	writeMultipartField(writer, "size", payload.Size)
	writeMultipartField(writer, "resolution_name", payload.ResolutionName)
	writeMultipartField(writer, "preset", payload.Preset)
	for index, reference := range payload.InputReference {
		if isHTTPURL(reference) {
			_ = writer.WriteField("input_reference[]", reference)
			continue
		}
		if err := writeMultipartDataURL(writer, "input_reference[]", reference, fmt.Sprintf("reference-%d", index+1)); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func prepareGrokVideoJSONBody(body []byte, contentType string, modelName string) ([]byte, string, error) {
	if !isGrokPreviewVideo(modelName) || modelName == "grok-imagine-video-1.5" {
		return body, contentType, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	payload["model"] = "grok-imagine-video-1.5"
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return normalized, contentType, nil
}

func prepareLegacyGrokVideoBody(body []byte, contentType string) ([]byte, string, error) {
	if !strings.HasPrefix(contentType, "application/json") {
		return body, contentType, nil
	}
	var payload struct {
		Model           string   `json:"model"`
		Prompt          string   `json:"prompt"`
		Duration        int      `json:"duration"`
		AspectRatio     string   `json:"aspect_ratio"`
		Resolution      string   `json:"resolution"`
		Image           string   `json:"image"`
		ReferenceImages []string `json:"reference_images"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	if isGrokPreviewVideo(payload.Model) {
		legacy := map[string]any{
			"model":   payload.Model,
			"prompt":  payload.Prompt,
			"seconds": fmt.Sprintf("%d", payload.Duration),
			"size":    grokAspectRatioSize(payload.AspectRatio),
			"images":  []string{payload.Image, payload.Image},
		}
		normalized, err := json.Marshal(legacy)
		return normalized, contentType, err
	}
	size := grokAspectRatioSize(payload.AspectRatio)
	resolution := payload.Resolution
	if resolution == "" {
		resolution = "720p"
	}
	videoConfig := map[string]any{
		"seconds":         payload.Duration,
		"duration":        payload.Duration,
		"size":            size,
		"aspect_ratio":    payload.AspectRatio,
		"resolution":      resolution,
		"resolution_name": resolution,
	}
	legacy := map[string]any{
		"model":        payload.Model,
		"stream":       false,
		"messages":     []map[string]any{{"role": "user", "content": []map[string]string{{"type": "text", "text": payload.Prompt}}}},
		"duration":     payload.Duration,
		"seconds":      payload.Duration,
		"aspect_ratio": payload.AspectRatio,
		"size":         size,
		"video_config": videoConfig,
		"metadata":     map[string]any{"video_config": videoConfig},
	}
	if len(payload.ReferenceImages) > 0 {
		legacy["input_reference"] = payload.ReferenceImages
	}
	normalized, err := json.Marshal(legacy)
	return normalized, contentType, err
}

func grokAspectRatioSize(aspectRatio string) string {
	switch strings.TrimSpace(aspectRatio) {
	case "9:16":
		return "720x1280"
	case "1:1":
		return "1024x1024"
	case "4:3":
		return "960x720"
	case "3:4":
		return "720x960"
	case "21:9":
		return "1280x544"
	default:
		return "1280x720"
	}
}

func writeMultipartField(writer *multipart.Writer, key string, value string) {
	if strings.TrimSpace(value) != "" {
		_ = writer.WriteField(key, value)
	}
}

func writeMultipartDataURL(writer *multipart.Writer, field string, dataURL string, fallbackName string) error {
	mimeType, data, err := decodeDataURL(dataURL)
	if err != nil {
		return err
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeMultipartQuote(field), escapeMultipartQuote(fallbackName+dataURLExt(mimeType))))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

func decodeDataURL(dataURL string) (string, []byte, error) {
	header, content, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasPrefix(header, "data:") {
		return "", nil, fmt.Errorf("invalid data url")
	}
	mimeType := strings.TrimPrefix(strings.Split(strings.TrimPrefix(header, "data:"), ";")[0], " ")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if strings.Contains(header, ";base64") {
		data, err := base64.StdEncoding.DecodeString(content)
		return mimeType, data, err
	}
	text, err := url.PathUnescape(content)
	return mimeType, []byte(text), err
}

func dataURLExt(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	default:
		return ""
	}
}

func escapeMultipartQuote(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(value)
}

func isHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func isGrokImagineVideo(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "grok-imagine-video")
}

func isGrokPreviewVideo(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "grok-imagine-video-1.5")
}

func readMultipartModel(body []byte, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		return ""
	}
	defer form.RemoveAll()
	if values := form.Value["model"]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func readAIRequestCount(body []byte, contentType string) int {
	count := 1
	if strings.HasPrefix(contentType, "multipart/form-data") {
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return count
		}
		form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(32 << 20)
		if err != nil {
			return count
		}
		defer form.RemoveAll()
		if values := form.Value["n"]; len(values) > 0 {
			_, _ = fmt.Sscan(values[0], &count)
		}
	} else {
		var payload struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(body, &payload)
		count = payload.N
	}
	if count < 1 {
		return 1
	}
	return count
}

var errMissingModel = &aiError{"缺少模型名称"}

func resolveAIProxyPath(baseURL string, modelName string, path string) string {
	if path == "/videos/generations" && isGrokImagineVideo(modelName) && !isXAIChannel(baseURL) {
		return "/videos"
	}
	if !isArkSeedanceVideo(baseURL, modelName) {
		return path
	}
	if path == "/videos" {
		return "/contents/generations/tasks"
	}
	if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
		return "/contents/generations/tasks/" + strings.TrimPrefix(path, "/videos/")
	}
	return path
}

func isXAIChannel(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.Contains(strings.ToLower(baseURL), "x.ai")
	}
	return strings.HasSuffix(strings.ToLower(parsed.Hostname()), "x.ai")
}

func isArkSeedanceVideo(baseURL string, modelName string) bool {
	base := strings.ToLower(baseURL)
	model := strings.ToLower(modelName)
	return strings.Contains(model, "doubao-seedance") || strings.Contains(base, "/api/v3") || strings.Contains(base, "/api/plan/v3")
}

func aiStatusMessage(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "AI 接口鉴权失败，请检查 API Key、套餐权限或模型权限"
	case http.StatusTooManyRequests:
		return "AI 接口限流或额度不足，请稍后重试或检查额度"
	default:
		return "AI 接口请求失败"
	}
}

func selectAIChannel(userID string, modelName string) (model.ModelChannel, bool, error) {
	channel, ok, err := service.UserGatewayChannel(userID, modelName)
	if err != nil {
		return model.ModelChannel{}, false, err
	}
	if ok {
		return channel, true, nil
	}
	selected, err := service.SelectModelChannel(modelName)
	return selected, false, err
}

func aiUpstreamStatusMessage(statusCode int, body []byte) string {
	base := aiStatusMessage(statusCode)
	detail := aiUpstreamErrorDetail(body)
	if detail == "" {
		return base
	}
	return base + "：" + detail
}

func aiUpstreamErrorDetail(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	var payload struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Error.Message != "" {
			if detail := friendlyUpstreamError(payload.Error.Code, payload.Error.Message); detail != "" {
				return safeUpstreamText(detail)
			}
			if payload.Error.Code != "" {
				return safeUpstreamText(payload.Error.Code + " " + payload.Error.Message)
			}
			return safeUpstreamText(payload.Error.Message)
		}
		if payload.Detail != "" {
			return safeUpstreamText(payload.Detail)
		}
		if payload.Msg != "" {
			return safeUpstreamText(payload.Msg)
		}
		if payload.Message != "" {
			return safeUpstreamText(payload.Message)
		}
	}
	return safeUpstreamText(text)
}

func friendlyUpstreamError(code string, message string) string {
	lowerCode := strings.ToLower(strings.TrimSpace(code))
	if strings.Contains(lowerCode, "inputvideosensitivecontentdetected") || strings.Contains(lowerCode, "privacyinformation") {
		return strings.TrimSpace(code + " 参考视频疑似包含真人或隐私信息，火山方舟拒绝使用普通 URL 作为真人视频参考；请改用不含真人的视频、官方允许的模型产物，或已授权的 asset:// 素材。原始错误：" + message)
	}
	return ""
}

func safeUpstreamText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) > 300 {
		return string(runes[:300]) + "..."
	}
	return text
}

type aiError struct {
	message string
}

func (err *aiError) Error() string {
	return err.message
}
