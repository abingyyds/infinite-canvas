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
	"strconv"
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

func AIVideoGenerationsLegacy(w http.ResponseWriter, r *http.Request) {
	proxyAIRequest(w, r, "/video/generations")
}

func AIVideo(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/videos/"+id)
}

func AIVideoContent(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/videos/"+id+"/content")
}

func AIVideoLegacy(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/video/generations/"+id)
}

func AIVideoContentLegacy(w http.ResponseWriter, r *http.Request, id string) {
	proxyAIGetRequest(w, r, "/video/generations/"+id+"/content")
}

func proxyAIGetRequest(w http.ResponseWriter, r *http.Request, path string) {
	modelName := r.URL.Query().Get("model")
	if strings.TrimSpace(modelName) == "" {
		modelName = "grok-imagine-video"
	}
	user, _ := service.UserFromContext(r.Context())
	channel, resolvedModelName, _, err := selectAIChannel(user.ID, modelName)
	if err != nil {
		log.Printf("AI proxy select channel failed: model=%s err=%v", modelName, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	path = resolveAIProxyPath(channel.BaseURL, resolvedModelName, path)
	request, err := http.NewRequest(http.MethodGet, service.BuildModelChannelURL(channel, path), nil)
	if err != nil {
		Fail(w, "AI 接口请求失败")
		return
	}
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	copyAIResponse(w, request, resolvedModelName, path, nil)
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
	channel, resolvedModelName, isGatewayChannel, err := selectAIChannel(user.ID, modelName)
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
	body, contentType, err = prepareResolvedModelBody(body, contentType, resolvedModelName)
	if err != nil {
		log.Printf("AI proxy resolve model body failed: model=%s resolved=%s err=%v", modelName, resolvedModelName, err)
		Fail(w, "AI 接口请求失败")
		return
	}
	proxyPath := resolveAIProxyPath(channel.BaseURL, resolvedModelName, path)
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
	if err := service.ConsumeUserCredits(user.ID, resolvedModelName, credits, proxyPath); err != nil {
		FailError(w, err)
		return
	}
	copyAIResponse(w, request, resolvedModelName, proxyPath, func() {
		if err := service.RefundUserCredits(user.ID, resolvedModelName, credits, proxyPath); err != nil {
			log.Printf("AI proxy refund credits failed: user=%s model=%s credits=%d err=%v", user.ID, resolvedModelName, credits, err)
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
		if strings.HasPrefix(contentType, "application/json") {
			var payload struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(body, &payload)
			if isGrokPreviewVideo(payload.Model) {
				if proxyPath == "/videos" {
					return prepareGrokPreviewLegacyVideoJSONBody(body, contentType, payload.Model)
				}
				return prepareGrokVideoJSONBody(body, contentType, payload.Model)
			}
		}
		return body, contentType, nil
	}
	if (proxyPath != "/videos" && proxyPath != "/video/generations") || !strings.HasPrefix(contentType, "application/json") {
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
		Ratio          string   `json:"ratio"`
		Resolution     string   `json:"resolution"`
		Duration       any      `json:"duration"`
		GenerateAudio  any      `json:"generate_audio"`
		Watermark      any      `json:"watermark"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	if isGrokImagineVideo(payload.Model) {
		if isGrokPreviewVideo(payload.Model) {
			return prepareGrokPreviewLegacyVideoJSONBody(body, contentType, payload.Model)
		}
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
	writeMultipartField(writer, "ratio", payload.Ratio)
	writeMultipartField(writer, "resolution", payload.Resolution)
	writeMultipartValue(writer, "duration", payload.Duration)
	writeMultipartValue(writer, "generate_audio", payload.GenerateAudio)
	writeMultipartValue(writer, "watermark", payload.Watermark)
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
	if !isGrokPreviewVideo(modelName) {
		return body, contentType, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	image, err := grokVideoFirstFrameImage(payload)
	if err != nil {
		return nil, "", err
	}
	normalizedPayload := map[string]any{
		"model": "grok-imagine-video-1.5",
		"image": image,
	}
	if duration, ok := grokVideoDuration(payload["duration"]); ok {
		normalizedPayload["duration"] = duration
	} else if duration, ok := grokVideoDuration(payload["seconds"]); ok {
		normalizedPayload["duration"] = duration
	}
	copyGrokVideoJSONField(normalizedPayload, payload, "aspect_ratio", "aspect_ratio")
	copyGrokVideoJSONField(normalizedPayload, payload, "resolution", "resolution")
	copyGrokVideoJSONField(normalizedPayload, payload, "storage_options", "storage_options")
	copyGrokVideoJSONField(normalizedPayload, payload, "output", "output")
	copyGrokVideoJSONField(normalizedPayload, payload, "user", "user")
	normalized, err := json.Marshal(normalizedPayload)
	if err != nil {
		return nil, "", err
	}
	return normalized, contentType, nil
}

func prepareGrokPreviewLegacyVideoJSONBody(body []byte, contentType string, modelName string) ([]byte, string, error) {
	if !isGrokPreviewVideo(modelName) {
		return body, contentType, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	image, err := grokVideoFirstFrameImageString(payload)
	if err != nil {
		return nil, "", err
	}
	prompt, ok := grokStringField(payload, "prompt")
	if !ok {
		prompt = "animate"
	}
	normalizedPayload := map[string]any{
		"model":  legacyGrokPreviewModelName(modelName),
		"prompt": prompt,
		"image":  image,
	}
	if duration, ok := grokVideoDuration(payload["duration"]); ok {
		normalizedPayload["duration"] = duration
		normalizedPayload["seconds"] = fmt.Sprint(duration)
	} else if duration, ok := grokVideoDuration(payload["seconds"]); ok {
		normalizedPayload["duration"] = duration
		normalizedPayload["seconds"] = fmt.Sprint(duration)
	}
	copyGrokVideoJSONField(normalizedPayload, payload, "aspect_ratio", "aspect_ratio")
	normalized, err := json.Marshal(normalizedPayload)
	if err != nil {
		return nil, "", err
	}
	return normalized, contentType, nil
}

func grokVideoFirstFrameImage(payload map[string]any) (map[string]any, error) {
	if image, ok := grokVideoImageObject(payload["image"]); ok {
		return image, nil
	}
	images, _ := payload["images"].([]any)
	for _, item := range images {
		if image, ok := grokVideoImageObject(item); ok {
			return image, nil
		}
	}
	return nil, fmt.Errorf("grok 1.5 video requires a first frame image")
}

func grokVideoFirstFrameImageString(payload map[string]any) (string, error) {
	image, err := grokVideoFirstFrameImage(payload)
	if err != nil {
		return "", err
	}
	if url, ok := grokStringField(image, "url"); ok {
		return url, nil
	}
	return "", fmt.Errorf("grok 1.5 video requires a first frame image url")
}

func grokVideoImageObject(value any) (map[string]any, bool) {
	switch image := value.(type) {
	case string:
		url := strings.TrimSpace(image)
		if url == "" {
			return nil, false
		}
		return map[string]any{"url": url}, true
	case map[string]any:
		if url, ok := grokStringField(image, "url"); ok {
			return map[string]any{"url": url}, true
		}
		if url, ok := grokStringField(image, "image_url"); ok {
			return map[string]any{"url": url}, true
		}
		if fileID, ok := grokStringField(image, "file_id"); ok {
			return map[string]any{"file_id": fileID}, true
		}
		return nil, false
	default:
		return nil, false
	}
}

func grokStringField(payload map[string]any, key string) (string, bool) {
	value, _ := payload[key].(string)
	value = strings.TrimSpace(value)
	return value, value != ""
}

func grokVideoDuration(value any) (int, bool) {
	var seconds int
	switch duration := value.(type) {
	case float64:
		seconds = int(duration)
	case int:
		seconds = duration
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(duration))
		if err != nil {
			return 0, false
		}
		seconds = parsed
	default:
		return 0, false
	}
	if seconds < 1 {
		return 1, true
	}
	if seconds > 15 {
		return 15, true
	}
	return seconds, true
}

func copyGrokVideoJSONField(target map[string]any, source map[string]any, targetKey string, sourceKey string) {
	value, ok := source[sourceKey]
	if !ok || value == nil {
		return
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return
	}
	target[targetKey] = value
}

func prepareResolvedModelBody(body []byte, contentType string, resolvedModelName string) ([]byte, string, error) {
	if !strings.HasPrefix(contentType, "application/json") || strings.TrimSpace(resolvedModelName) == "" {
		return body, contentType, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	if current, _ := payload["model"].(string); current == resolvedModelName {
		return body, contentType, nil
	}
	payload["model"] = resolvedModelName
	normalized, err := json.Marshal(payload)
	return normalized, contentType, err
}

func writeMultipartField(writer *multipart.Writer, key string, value string) {
	if strings.TrimSpace(value) != "" {
		_ = writer.WriteField(key, value)
	}
}

func writeMultipartValue(writer *multipart.Writer, key string, value any) {
	if value == nil {
		return
	}
	writeMultipartField(writer, key, fmt.Sprint(value))
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

func legacyGrokPreviewModelName(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "grok-imagine-video-1.5" {
		return "grok-imagine-video-1.5-preview"
	}
	return modelName
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
	if isGrokPreviewVideo(modelName) && !isOfficialXAIBaseURL(baseURL) && path == "/videos/generations" {
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
	if path == "/video/generations" {
		return "/contents/generations/tasks"
	}
	if strings.HasPrefix(path, "/video/generations/") && !strings.HasSuffix(path, "/content") {
		return "/contents/generations/tasks/" + strings.TrimPrefix(path, "/video/generations/")
	}
	return path
}

func isOfficialXAIBaseURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.Contains(strings.ToLower(baseURL), "api.x.ai")
	}
	return strings.EqualFold(parsed.Hostname(), "api.x.ai")
}

func isArkSeedanceVideo(baseURL string, modelName string) bool {
	base := strings.ToLower(baseURL)
	model := strings.ToLower(strings.TrimSpace(modelName))
	return strings.Contains(model, "seedance") && (strings.Contains(base, "ark.cn-beijing.volces.com") || strings.Contains(base, "/api/v3") || strings.Contains(base, "/api/plan/v3"))
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

func selectAIChannel(userID string, modelName string) (model.ModelChannel, string, bool, error) {
	var lastErr error
	for _, candidate := range modelNameCandidates(modelName) {
		channel, ok, err := service.UserGatewayChannel(userID, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if ok {
			return channel, candidate, true, nil
		}
		selected, err := service.SelectModelChannel(candidate)
		if err == nil {
			return selected, candidate, false, nil
		}
		lastErr = err
	}
	return model.ModelChannel{}, "", false, lastErr
}

func modelNameCandidates(modelName string) []string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "grok-imagine-video-1.5-preview" {
		return []string{modelName, "grok-imagine-video-1.5"}
	}
	if modelName == "grok-imagine-video-1.5" {
		return []string{modelName, "grok-imagine-video-1.5-preview"}
	}
	return []string{modelName}
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
