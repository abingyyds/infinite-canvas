package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basketikun/infinite-canvas/config"
	"github.com/basketikun/infinite-canvas/model"
)

func TestRuntimeGatewayModelBaseURLKeepsV1ForOpenAIModels(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{}

	got := runtimeGatewayModelBaseURL("https://gateway.example.com", "gpt-5.5")
	want := "https://gateway.example.com/v1"
	if got != want {
		t.Fatalf("runtimeGatewayModelBaseURL = %q, want %q", got, want)
	}
}

func TestRuntimeGatewayModelBaseURLUsesContentPathForArkBaseURL(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{}

	baseURL := runtimeGatewayModelBaseURL("https://gateway.example.com/api/v3", "doubao-seedance-2.0-fast")
	got := BuildModelChannelURL(model.ModelChannel{BaseURL: baseURL}, "/contents/generations/tasks")
	want := "https://gateway.example.com/api/v3/contents/generations/tasks"
	if got != want {
		t.Fatalf("gateway Seedance URL = %q, want %q", got, want)
	}
}

func TestRuntimeGatewayModelBaseURLKeepsV1ForOpenAICompatibleSeedance(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{}

	got := runtimeGatewayModelBaseURL("https://gateway.example.com", "seedance-2-beta-face")
	want := "https://gateway.example.com/v1"
	if got != want {
		t.Fatalf("runtimeGatewayModelBaseURL = %q, want %q", got, want)
	}
}

func TestRuntimeGatewayModelBaseURLNormalizesOpenAIVideoPath(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{}

	got := runtimeGatewayModelBaseURL("https://ai.orbitlink.me/v1/videos/generations", "grok-imagine-video-1.5-preview")
	want := "https://ai.orbitlink.me/v1"
	if got != want {
		t.Fatalf("runtimeGatewayModelBaseURL = %q, want %q", got, want)
	}
}

func TestRuntimeGatewayModelBaseURLKeepsSubRouterV1ForDoubaoSeedance(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{GatewayPublicBaseURL: "https://public-gateway.example.com/v1"}

	got := runtimeGatewayModelBaseURL("https://account-gateway.example.com", "doubao-seedance-2.0")
	want := "https://public-gateway.example.com/v1"
	if got != want {
		t.Fatalf("runtimeGatewayModelBaseURL = %q, want %q", got, want)
	}
}

func TestRuntimeGatewayModelBaseURLDoesNotInferArkFromPublicBaseURL(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{
		GatewayPublicBaseURL:     "https://public-gateway.example.com/v1",
		GatewayBaseURLCandidates: "https://plan-gateway.example.com/api/plan/v3",
	}

	got := runtimeGatewayModelBaseURL("https://account-gateway.example.com", "doubao-seedance-2.0")
	want := "https://public-gateway.example.com/v1"
	if got != want {
		t.Fatalf("runtimeGatewayModelBaseURL = %q, want %q", got, want)
	}
}

func TestResolveGatewaySiteHostUsesGatewayBaseURLSuffix(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{}

	got := resolveGatewaySiteHost(gatewayDistributorInfo{Slug: "studio"}, "", "https://subrouter.example.com")
	want := "studio.subrouter.example.com"
	if got != want {
		t.Fatalf("site host = %q, want %q", got, want)
	}
}

func TestLoginMainGatewayCompletesTwoFactorAndMergesCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/login":
			if got := r.URL.Query().Get("turnstile"); got != "captcha token" {
				t.Fatalf("turnstile = %q, want captcha token", got)
			}
			w.Header().Add("Set-Cookie", "session=pending; Path=/; HttpOnly")
			w.Header().Add("Set-Cookie", "device=known; Path=/")
			_, _ = w.Write([]byte(`{"success":true,"data":{"require_2fa":true,"id":42,"username":"alice"}}`))
		case "/api/user/login/2fa":
			if got := r.Header.Get("Cookie"); got != "session=pending; device=known" {
				t.Fatalf("verification cookie = %q", got)
			}
			w.Header().Set("Set-Cookie", "session=verified; Path=/; HttpOnly")
			_, _ = w.Write([]byte(`{"success":true,"data":{"verified":true}}`))
		case "/api/user/self/distributor":
			_, _ = w.Write([]byte(`{"success":true,"data":{"belongs_to_distributor":false}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldClient := gatewayHTTPClient
	t.Cleanup(func() { gatewayHTTPClient = oldClient })
	gatewayHTTPClient = server.Client()

	result, err := loginMainGateway(server.URL, "", "alice", "password", "captcha token", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalUserID != "42" || result.Username != "alice" {
		t.Fatalf("user = %#v", result)
	}
	if result.SessionCookie != "session=verified; device=known" {
		t.Fatalf("session cookie = %q", result.SessionCookie)
	}
}

func TestLoginMainGatewayRequiresTwoFactorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=pending; Path=/; HttpOnly")
		_, _ = w.Write([]byte(`{"success":true,"data":{"require_2fa":true}}`))
	}))
	defer server.Close()

	oldClient := gatewayHTTPClient
	t.Cleanup(func() { gatewayHTTPClient = oldClient })
	gatewayHTTPClient = server.Client()

	_, err := loginMainGateway(server.URL, "", "alice", "password", "", "")
	var twoFactorErr gatewayTwoFactorError
	if !errors.As(err, &twoFactorErr) || twoFactorErr.Code() != "TWO_FACTOR_REQUIRED" {
		t.Fatalf("error = %#v", err)
	}
}

func TestFetchGatewayModelsUsesDistributorTokenWithoutSiteHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-site-user" {
			t.Fatalf("Authorization = %q, want Bearer sk-site-user", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":["gpt-4o","text-embedding-3-small","claude-3-5-sonnet"]}`))
	}))
	defer server.Close()

	oldClient := gatewayHTTPClient
	oldConfig := config.Cfg
	t.Cleanup(func() {
		gatewayHTTPClient = oldClient
		config.Cfg = oldConfig
	})
	gatewayHTTPClient = server.Client()
	config.Cfg = config.Config{}

	result, err := fetchGatewayModels(model.GatewayAccount{
		Provider: model.GatewayProviderSite,
		BaseURL:  server.URL,
		APIKey:   "sk-site-user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source != "gateway" {
		t.Fatalf("source = %q, want gateway", result.Source)
	}
	want := []string{"claude-3-5-sonnet", "gpt-4o"}
	if len(result.Models) != len(want) {
		t.Fatalf("models = %#v, want %#v", result.Models, want)
	}
	for i := range want {
		if result.Models[i] != want[i] {
			t.Fatalf("models = %#v, want %#v", result.Models, want)
		}
	}
}
