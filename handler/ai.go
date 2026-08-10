package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"time"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/model"
	"github.com/basketikun/infinite-canvas/service"
)

// 网关自己在 10 分钟返 504，这里留出余量让它的具体错误先到；这个上限只兜底
// 网关完全不响应的情况，客户端提前离开靠请求上下文取消来收尾。
const aiUpstreamTimeout = 11 * time.Minute

// http.DefaultClient 没有超时，网关卡住时会把 goroutine 和连接一直占着。
var aiUpstreamClient = &http.Client{Timeout: aiUpstreamTimeout}

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
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, service.BuildModelChannelURL(channel, path), nil)
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
	// 带上客户端的请求上下文：客户端断开时立刻放弃上游调用，不再空等到网关超时。
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, service.BuildModelChannelURL(channel, proxyPath), bytes.NewReader(body))
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
	response, err := aiUpstreamClient.Do(request)
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
		FailStatus(w, proxyFailureStatus(response.StatusCode), aiUpstreamStatusMessage(response.StatusCode, body))
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
	_, _ = io.Copy(w, publicMediaBody(response, request.URL))
}

// URL 型响应只有几百字节；b64 型响应可以到几十 MB，但里面没有地址可改，超过上限就原样透传。
const mediaRewriteLimit = 1 << 20

// 网关按收到的 Host 生成图片/视频代理地址。我们走 Railway 私网调用它，返回的
// *.railway.internal 地址浏览器解析不了，这里换回公网域名。
func publicMediaBody(response *http.Response, upstream *url.URL) io.Reader {
	internal := internalUpstreamOrigin(upstream)
	public := publicGatewayOrigin()
	if internal == "" || public == "" || internal == public {
		return response.Body
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "json") {
		return response.Body
	}
	head := make([]byte, mediaRewriteLimit)
	read, err := io.ReadFull(response.Body, head)
	head = head[:read]
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return io.MultiReader(bytes.NewReader(head), response.Body)
	}
	return bytes.NewReader(replaceOrigin(head, internal, public))
}

func replaceOrigin(body []byte, internal string, public string) []byte {
	return bytes.ReplaceAll(body, []byte(internal), []byte(public))
}

// 只改内网地址：网关本来就返回公网地址时不能碰。
func internalUpstreamOrigin(upstream *url.URL) string {
	if upstream == nil || !strings.HasSuffix(strings.ToLower(upstream.Hostname()), ".railway.internal") {
		return ""
	}
	return upstream.Scheme + "://" + upstream.Host
}

func publicGatewayOrigin() string {
	for _, candidate := range []string{config.Cfg.GatewayMediaBaseURL, config.Cfg.GatewayBaseURL} {
		parsed, err := url.Parse(strings.TrimSpace(candidate))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".railway.internal") {
			continue
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	return ""
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
	if !strings.HasPrefix(contentType, "application/json") {
		return body, contentType, nil
	}
	var videoHead struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &videoHead)
	if proxyPath == "/video/generations" || isUnifiedJSONVideo(videoHead.Model) {
		return prepareUnifiedVideoJSONBody(body, contentType)
	}
	if proxyPath != "/videos" {
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
	for index, reference := range payload.InputReference {
		if !strings.HasPrefix(strings.TrimSpace(reference), "data:") {
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

// prepareUnifiedVideoJSONBody rewrites Ark-style seedance JSON (content array plus
// native fields) into the new-api unified video JSON; other JSON passes through.
func prepareUnifiedVideoJSONBody(body []byte, contentType string) ([]byte, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", err
	}
	content, ok := payload["content"].([]any)
	if !ok {
		return body, contentType, nil
	}
	prompt, _ := payload["prompt"].(string)
	images := []string{}
	dropped := 0
	for _, item := range content {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := entry["text"].(string); ok && prompt == "" && strings.TrimSpace(text) != "" {
			prompt = text
		}
		if url := nestedMediaURL(entry, "image_url"); url != "" {
			images = append(images, url)
		}
		if nestedMediaURL(entry, "video_url") != "" || nestedMediaURL(entry, "audio_url") != "" {
			dropped++
		}
	}
	if dropped > 0 {
		log.Printf("AI proxy unified video: dropped %d non-image references unsupported by /video/generations", dropped)
	}
	unified := map[string]any{"model": payload["model"], "prompt": prompt}
	if duration, ok := unifiedVideoDuration(payload["duration"]); ok {
		unified["duration"] = duration
	}
	metadata := map[string]any{}
	for _, key := range []string{"ratio", "resolution", "generate_audio", "watermark"} {
		if value, ok := payload[key]; ok && value != nil {
			metadata[key] = value
		}
	}
	if len(metadata) > 0 {
		unified["metadata"] = metadata
	}
	if len(images) > 0 {
		unified["image"] = images[0]
		if len(images) > 1 {
			unified["images"] = images
		}
	}
	normalized, err := json.Marshal(unified)
	if err != nil {
		return nil, "", err
	}
	return normalized, contentType, nil
}

func unifiedVideoDuration(value any) (int, bool) {
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
	if seconds <= 0 {
		// ark duration -1 means adaptive; unified API needs a concrete value
		return 6, true
	}
	return seconds, true
}

func nestedMediaURL(entry map[string]any, key string) string {
	object, _ := entry[key].(map[string]any)
	url, _ := object["url"].(string)
	return strings.TrimSpace(url)
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

func isGrokImagineVideo(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "grok-imagine-video") || isGrokPreviewVideo(modelName)
}

func isGrokPreviewVideo(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	// "grok-video-1.5" is the SubRouter-style name for the same first-frame video model.
	return strings.Contains(name, "grok-imagine-video-1.5") || strings.Contains(name, "grok-video-1.5")
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
	if isArkSeedanceVideo(baseURL, modelName) {
		if path == "/videos" || path == "/video/generations" {
			return "/contents/generations/tasks"
		}
		if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
			return "/contents/generations/tasks/" + strings.TrimPrefix(path, "/videos/")
		}
		if strings.HasPrefix(path, "/video/generations/") && !strings.HasSuffix(path, "/content") {
			return "/contents/generations/tasks/" + strings.TrimPrefix(path, "/video/generations/")
		}
		return path
	}
	if strings.Contains(strings.ToLower(modelName), "doubao-seedance") {
		// non-Ark channels exposing ark-named doubao-seedance serve it on the unified JSON endpoint
		if path == "/videos" {
			return "/video/generations"
		}
		if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
			return "/video/generations/" + strings.TrimPrefix(path, "/videos/")
		}
	}
	return path
}

// isUnifiedJSONVideo reports gateway-native video models, which serve video on
// OpenAI-style /videos with a unified JSON body instead of multipart.
func isUnifiedJSONVideo(modelName string) bool {
	model := strings.ToLower(strings.TrimSpace(modelName))
	if strings.Contains(model, "doubao-seedance") {
		return false
	}
	return strings.Contains(model, "seedance") || strings.Contains(model, "veo-") || strings.Contains(model, "omni-")
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
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "AI 上游服务暂时不可用，请稍后重试"
	default:
		return "AI 接口请求失败"
	}
}

// 上游状态透传给前端，让它能按状态区分限流 / 上游故障；但上游的 401 不能原样透传，
// 前端把自家 /api 的 401 当成会话失效会直接登出，而这里失效的是网关密钥不是用户会话。
func proxyFailureStatus(upstreamStatus int) int {
	if upstreamStatus == http.StatusUnauthorized {
		return http.StatusBadGateway
	}
	return upstreamStatus
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
	if hint := upstreamCodeHint(lowerCode); hint != "" {
		return strings.TrimSpace(hint + "原始错误：" + code + " " + message)
	}
	return ""
}

// 网关只回错误码不回人话，这里把常见的几个翻译成可操作的提示，原始错误仍然附在后面。
func upstreamCodeHint(lowerCode string) string {
	switch lowerCode {
	case "bad_response_status_code":
		return "网关调用上游模型时收到异常状态码，通常是上游过载或故障，请稍后重试。"
	case "empty_response":
		return "上游返回了结果但没有可用的图片或视频，可能是格式不符或内容被拦截，请重试或换个模型。"
	case "fail_to_fetch_task":
		return "任务已提交但网关拉不回结果，通常是上游生成超时，请稍后重试。"
	case "moderation_error":
		return "内容审核未通过，请调整提示词或参考图；提示词里的负面约束词也会被审核扫到。"
	case "do_request_failed":
		return "网关请求上游失败，请检查参考素材地址是否为可公网访问的 http/https 链接。"
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
