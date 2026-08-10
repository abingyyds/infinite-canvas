package handler

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/basketikun/infinite-canvas/config"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}

func TestInternalUpstreamOrigin(t *testing.T) {
	cases := map[string]string{
		"http://subrouter.railway.internal:8080/v1/images/generations": "http://subrouter.railway.internal:8080",
		"https://subrouter.ai/v1/images/generations":                   "",
		"https://api.43-161-200-52.sslip.io/v1/images/generations":     "",
	}
	for raw, want := range cases {
		if got := internalUpstreamOrigin(mustParse(t, raw)); got != want {
			t.Errorf("internalUpstreamOrigin(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := internalUpstreamOrigin(nil); got != "" {
		t.Errorf("internalUpstreamOrigin(nil) = %q, want empty", got)
	}
}

func TestPublicGatewayOriginPrefersMediaBaseURL(t *testing.T) {
	original := config.Cfg
	defer func() { config.Cfg = original }()

	config.Cfg.GatewayMediaBaseURL = "https://subrouter.ai/v1"
	config.Cfg.GatewayBaseURL = "https://other.example/v1"
	if got := publicGatewayOrigin(); got != "https://subrouter.ai" {
		t.Errorf("origin = %q, want https://subrouter.ai", got)
	}

	config.Cfg.GatewayMediaBaseURL = ""
	if got := publicGatewayOrigin(); got != "https://other.example" {
		t.Errorf("fallback origin = %q, want https://other.example", got)
	}

	// 两个都指向内网时没有可用的公网地址，必须返回空而不是内网地址。
	config.Cfg.GatewayBaseURL = "http://subrouter.railway.internal:8080/v1"
	if got := publicGatewayOrigin(); got != "" {
		t.Errorf("origin = %q, want empty when only internal hosts are configured", got)
	}
}

func TestPublicMediaBodyRewritesInternalURL(t *testing.T) {
	original := config.Cfg
	defer func() { config.Cfg = original }()
	config.Cfg.GatewayMediaBaseURL = "https://subrouter.ai/v1"

	upstream := mustParse(t, "http://subrouter.railway.internal:8080/v1/images/generations")
	body := `{"created":1,"data":[{"url":"http://subrouter.railway.internal:8080/v1/images/proxy?token=abc"}]}`
	got, err := io.ReadAll(publicMediaBody(jsonResponse(body), upstream))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := `{"created":1,"data":[{"url":"https://subrouter.ai/v1/images/proxy?token=abc"}]}`
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPublicMediaBodyLeavesPublicUpstreamAlone(t *testing.T) {
	original := config.Cfg
	defer func() { config.Cfg = original }()
	config.Cfg.GatewayMediaBaseURL = "https://subrouter.ai/v1"

	upstream := mustParse(t, "https://api.43-161-200-52.sslip.io/v1/images/generations")
	body := `{"data":[{"url":"https://api.43-161-200-52.sslip.io/v1/images/proxy?token=abc"}]}`
	got, err := io.ReadAll(publicMediaBody(jsonResponse(body), upstream))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %s, want it untouched", got)
	}
}

// b64 响应可以到几十 MB，超过上限必须原样透传，不能截断。
func TestPublicMediaBodyPassesOversizedBodyThrough(t *testing.T) {
	original := config.Cfg
	defer func() { config.Cfg = original }()
	config.Cfg.GatewayMediaBaseURL = "https://subrouter.ai/v1"

	upstream := mustParse(t, "http://subrouter.railway.internal:8080/v1/images/generations")
	payload := `{"data":[{"b64_json":"` + strings.Repeat("A", mediaRewriteLimit+4096) + `"}]}`
	got, err := io.ReadAll(publicMediaBody(jsonResponse(payload), upstream))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("body length = %d, want %d", len(got), len(payload))
	}
	if string(got) != payload {
		t.Error("oversized body was modified")
	}
}

func TestPublicMediaBodySkipsNonJSON(t *testing.T) {
	original := config.Cfg
	defer func() { config.Cfg = original }()
	config.Cfg.GatewayMediaBaseURL = "https://subrouter.ai/v1"

	upstream := mustParse(t, "http://subrouter.railway.internal:8080/v1/images/proxy")
	body := "http://subrouter.railway.internal:8080/not-json"
	response := &http.Response{
		Header: http.Header{"Content-Type": []string{"image/png"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
	got, err := io.ReadAll(publicMediaBody(response, upstream))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %s, want it untouched", got)
	}
}
