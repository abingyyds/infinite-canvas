package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAIUpstreamErrorDetail(t *testing.T) {
	got := aiUpstreamErrorDetail([]byte(`{"error":{"code":"InvalidParameter","message":"reference video fps is invalid"}}`))
	if got != "InvalidParameter reference video fps is invalid" {
		t.Fatalf("detail = %q", got)
	}
}

func TestAIUpstreamErrorDetailExplainsSensitiveVideo(t *testing.T) {
	got := aiUpstreamErrorDetail([]byte(`{"error":{"code":"InputVideoSensitiveContentDetected.PrivacyInformation","message":"The request failed because the input video may contain real person."}}`))
	if !strings.Contains(got, "参考视频疑似包含真人") || !strings.Contains(got, "asset://") {
		t.Fatalf("detail = %q", got)
	}
}

func TestSafeUpstreamTextTruncates(t *testing.T) {
	got := safeUpstreamText(strings.Repeat("错", 320))
	if len([]rune(got)) != 303 {
		t.Fatalf("truncated rune length = %d", len([]rune(got)))
	}
}

func TestResolveAIProxyPathKeepsXAIVideoGenerations(t *testing.T) {
	got := resolveAIProxyPath("https://api.x.ai/v1", "grok-imagine-video", "/videos/generations")
	if got != "/videos/generations" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathKeepsVideoGenerationsForNonXAIGrokGateway(t *testing.T) {
	got := resolveAIProxyPath("https://gateway.example.com/v1", "grok-imagine-video", "/videos/generations")
	if got != "/videos/generations" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathDowngradesPreviewGatewayToVideos(t *testing.T) {
	got := resolveAIProxyPath("https://ai.orbitlink.me/v1", "grok-imagine-video-1.5-preview", "/videos/generations")
	if got != "/videos" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathDowngradesSubRouterGrokVideoToVideos(t *testing.T) {
	got := resolveAIProxyPath("https://subrouter.ai/v1", "grok-video-1.5", "/videos/generations")
	if got != "/videos" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathRoutesSubRouterDoubaoSeedanceToUnifiedVideo(t *testing.T) {
	got := resolveAIProxyPath("https://subrouter.example.com/v1", "Doubao-Seedance-2.0", "/videos")
	if got != "/video/generations" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathRoutesSubRouterDoubaoSeedancePollToUnifiedVideo(t *testing.T) {
	got := resolveAIProxyPath("https://subrouter.example.com/v1", "Doubao-Seedance-2.0", "/videos/task_abc")
	if got != "/video/generations/task_abc" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathUsesArkTaskEndpointOnlyForArkBaseURL(t *testing.T) {
	got := resolveAIProxyPath("https://ark.cn-beijing.volces.com/api/v3", "Doubao-Seedance-2.0", "/videos")
	if got != "/contents/generations/tasks" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathKeepsSubRouterSingularVideoEndpoint(t *testing.T) {
	got := resolveAIProxyPath("https://subrouter.example.com/v1", "seedance-2-0", "/video/generations")
	if got != "/video/generations" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveAIProxyPathKeepsGatewaySeedanceOnVideos(t *testing.T) {
	for _, path := range []string{"/videos", "/videos/task_abc", "/videos/task_abc/content"} {
		got := resolveAIProxyPath("https://subrouter.example.com/v1", "seedance-2.0-480p", path)
		if got != path {
			t.Fatalf("path %q rewritten to %q", path, got)
		}
	}
}

func TestResolveAIProxyPathDoesNotRewriteNonSeedanceArkVideo(t *testing.T) {
	got := resolveAIProxyPath("https://ark.cn-beijing.volces.com/api/v3", "grok-video", "/videos")
	if got != "/videos" {
		t.Fatalf("path = %q", got)
	}
}

func TestPrepareAIProxyBodyConvertsArkSeedanceJSONToUnifiedJSON(t *testing.T) {
	raw := []byte(`{"model":"Doubao-Seedance-2.0","content":[{"type":"text","text":"猫在草地奔跑"},{"type":"image_url","image_url":{"url":"https://example.com/ref.png"},"role":"reference_image"}],"ratio":"4:3","resolution":"720p","duration":10,"generate_audio":true,"watermark":false}`)
	body, contentType, err := prepareAIProxyBody("/videos", "/video/generations", raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("contentType = %q", contentType)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "Doubao-Seedance-2.0" || payload["prompt"] != "猫在草地奔跑" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["duration"] != float64(10) {
		t.Fatalf("duration = %#v", payload["duration"])
	}
	if payload["image"] != "https://example.com/ref.png" {
		t.Fatalf("image = %#v", payload["image"])
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata["ratio"] != "4:3" || metadata["resolution"] != "720p" || metadata["generate_audio"] != true || metadata["watermark"] != false {
		t.Fatalf("metadata = %#v", metadata)
	}
	if _, ok := payload["content"]; ok {
		t.Fatalf("payload keeps ark content: %#v", payload)
	}
}

func TestPrepareAIProxyBodyMapsDynamicSeedanceDurationToDefault(t *testing.T) {
	raw := []byte(`{"model":"Doubao-Seedance-2.0","content":[{"type":"text","text":"p"}],"duration":-1}`)
	body, _, err := prepareAIProxyBody("/videos", "/video/generations", raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["duration"] != float64(6) {
		t.Fatalf("duration = %#v", payload["duration"])
	}
}

func TestPrepareAIProxyBodyKeepsGatewaySeedanceJSONOnVideos(t *testing.T) {
	raw := []byte(`{"model":"seedance-2.0-480p","prompt":"p","duration":6,"metadata":{"ratio":"16:9","resolution":"480p"}}`)
	body, contentType, err := prepareAIProxyBody("/videos", "/videos", raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" || !bytes.Equal(body, raw) {
		t.Fatalf("body = %s, contentType = %q", body, contentType)
	}
}

func TestPrepareAIProxyBodyKeepsUnifiedVideoJSONUntouched(t *testing.T) {
	raw := []byte(`{"model":"seedance-2.0","prompt":"p","duration":6,"metadata":{"ratio":"16:9"}}`)
	body, contentType, err := prepareAIProxyBody("/video/generations", "/video/generations", raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" || !bytes.Equal(body, raw) {
		t.Fatalf("body = %s, contentType = %q", body, contentType)
	}
}

func TestPrepareAIProxyBodyConvertsSubRouterGrokVideoToLegacyJSON(t *testing.T) {
	raw := []byte(`{"model":"grok-video-1.5","prompt":"产品图轻微旋转展示","image":{"url":"https://example.com/first.jpg"},"duration":6,"aspect_ratio":"9:16"}`)
	body, contentType, err := prepareAIProxyBody("/videos/generations", "/videos", raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("contentType = %q", contentType)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "grok-video-1.5" {
		t.Fatalf("model = %q", payload["model"])
	}
	if payload["prompt"] != "产品图轻微旋转展示" {
		t.Fatalf("prompt = %q", payload["prompt"])
	}
	if payload["image"] != "https://example.com/first.jpg" {
		t.Fatalf("image = %#v", payload["image"])
	}
	if payload["duration"] != float64(6) || payload["seconds"] != "6" || payload["aspect_ratio"] != "9:16" {
		t.Fatalf("video params = %#v", payload)
	}
}

func TestPrepareGrokPreviewLegacyVideoJSONBodyKeepsSubRouterModelName(t *testing.T) {
	raw := []byte(`{"model":"grok-video-1.5","image":"https://example.com/first.jpg","duration":6}`)
	body, _, err := prepareGrokPreviewLegacyVideoJSONBody(raw, "application/json", "grok-video-1.5")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "grok-video-1.5" {
		t.Fatalf("model = %q", payload["model"])
	}
}

func TestPrepareGrokVideoJSONBodyNormalizesPreviewModel(t *testing.T) {
	raw := []byte(`{"model":"grok-imagine-video-1.5-preview","prompt":"p","image":"https://example.com/first.png","duration":12,"aspect_ratio":"9:16","reference_images":[{"url":"https://example.com/ref.png"}],"messages":[],"stream":false}`)
	body, contentType, err := prepareGrokVideoJSONBody(raw, "application/json", "grok-imagine-video-1.5-preview")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("contentType = %q", contentType)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "grok-imagine-video-1.5" {
		t.Fatalf("model = %q", payload["model"])
	}
	image, _ := payload["image"].(map[string]any)
	if image["url"] != "https://example.com/first.png" {
		t.Fatalf("image = %#v", payload["image"])
	}
	if payload["duration"] != float64(12) || payload["aspect_ratio"] != "9:16" {
		t.Fatalf("video params = %#v", payload)
	}
	for _, key := range []string{"prompt", "reference_images", "messages", "stream"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("payload contains unsupported field %q: %#v", key, payload)
		}
	}
}

func TestPrepareGrokPreviewLegacyVideoJSONBodyUsesImageString(t *testing.T) {
	raw := []byte(`{"model":"grok-imagine-video-1.5-preview","image":{"url":"https://example.com/first.png"},"duration":20,"reference_images":[{"url":"https://example.com/ref.png"}],"stream":false}`)
	body, contentType, err := prepareGrokPreviewLegacyVideoJSONBody(raw, "application/json", "grok-imagine-video-1.5-preview")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("contentType = %q", contentType)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "grok-imagine-video-1.5-preview" || payload["prompt"] != "animate" {
		t.Fatalf("identity fields = %#v", payload)
	}
	if payload["image"] != "https://example.com/first.png" {
		t.Fatalf("image = %#v", payload["image"])
	}
	if payload["duration"] != float64(15) || payload["seconds"] != "15" {
		t.Fatalf("duration fields = %#v", payload)
	}
	for _, key := range []string{"reference_images", "stream"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("payload contains unsupported field %q: %#v", key, payload)
		}
	}
}

func TestPrepareGrokPreviewLegacyVideoJSONBodyUsesPreviewModelForLegacyGateway(t *testing.T) {
	raw := []byte(`{"model":"grok-imagine-video-1.5","image":"https://example.com/first.png","duration":6}`)
	body, _, err := prepareGrokPreviewLegacyVideoJSONBody(raw, "application/json", "grok-imagine-video-1.5")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "grok-imagine-video-1.5-preview" {
		t.Fatalf("model = %q", payload["model"])
	}
}

func TestPrepareGrokVideoJSONBodyUsesLegacyImagesFirstFrame(t *testing.T) {
	raw := []byte(`{"model":"grok-imagine-video-1.5","images":["data:image/png;base64,aaa","data:image/png;base64,bbb"],"seconds":"15"}`)
	body, _, err := prepareGrokVideoJSONBody(raw, "application/json", "grok-imagine-video-1.5")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	image, _ := payload["image"].(map[string]any)
	if image["url"] != "data:image/png;base64,aaa" {
		t.Fatalf("image = %#v", payload["image"])
	}
	if payload["duration"] != float64(15) {
		t.Fatalf("duration = %#v", payload["duration"])
	}
	if _, ok := payload["images"]; ok {
		t.Fatalf("payload still contains images: %#v", payload)
	}
}

func TestPrepareGrokVideoJSONBodyRequiresFirstFrame(t *testing.T) {
	raw := []byte(`{"model":"grok-imagine-video-1.5-preview","prompt":"p"}`)
	_, _, err := prepareGrokVideoJSONBody(raw, "application/json", "grok-imagine-video-1.5-preview")
	if err == nil {
		t.Fatal("expected missing first frame error")
	}
}

func TestPrepareResolvedModelBodyRewritesModel(t *testing.T) {
	body, contentType, err := prepareResolvedModelBody([]byte(`{"model":"grok-imagine-video-1.5-preview","prompt":"p"}`), "application/json", "grok-imagine-video-1.5")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("contentType = %q", contentType)
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "grok-imagine-video-1.5" {
		t.Fatalf("model = %q", payload.Model)
	}
}

func TestModelNameCandidatesGrokPreview(t *testing.T) {
	got := modelNameCandidates("grok-imagine-video-1.5-preview")
	want := []string{"grok-imagine-video-1.5-preview", "grok-imagine-video-1.5"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidates = %#v", got)
		}
	}
}
