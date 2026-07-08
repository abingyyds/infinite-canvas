package handler

import (
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
