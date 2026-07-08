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

func TestResolveAIProxyPathFallsBackForNonXAIGrokGateway(t *testing.T) {
	got := resolveAIProxyPath("https://gateway.example.com/v1", "grok-imagine-video", "/videos/generations")
	if got != "/videos" {
		t.Fatalf("path = %q", got)
	}
}

func TestPrepareGrokVideoJSONBodyNormalizesPreviewModel(t *testing.T) {
	body, contentType, err := prepareGrokVideoJSONBody([]byte(`{"model":"grok-imagine-video-1.5-preview","prompt":"p"}`), "application/json", "grok-imagine-video-1.5-preview")
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

func TestPrepareLegacyGrokVideoBodyConvertsOfficialPayload(t *testing.T) {
	body, contentType, err := prepareLegacyGrokVideoBody([]byte(`{"model":"grok-imagine-video","prompt":"p","duration":6,"aspect_ratio":"9:16","resolution":"720p","reference_images":["https://example.com/a.png"]}`), "application/json")
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
	if payload["size"] != "720x1280" || payload["stream"] != false {
		t.Fatalf("payload = %#v", payload)
	}
	if refs, ok := payload["input_reference"].([]any); !ok || len(refs) != 1 || refs[0] != "https://example.com/a.png" {
		t.Fatalf("input_reference = %#v", payload["input_reference"])
	}
}
