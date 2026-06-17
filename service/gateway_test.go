package service

import (
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

func TestRuntimeGatewayModelBaseURLUsesContentPathForSeedance(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{}

	baseURL := runtimeGatewayModelBaseURL("https://gateway.example.com", "doubao-seedance-2.0-fast")
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

func TestRuntimeGatewayModelBaseURLRewritesPublicV1ForSeedance(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{GatewayPublicBaseURL: "https://public-gateway.example.com/v1"}

	got := runtimeGatewayModelBaseURL("https://account-gateway.example.com", "doubao-seedance-2.0")
	want := "https://public-gateway.example.com/api/v3"
	if got != want {
		t.Fatalf("runtimeGatewayModelBaseURL = %q, want %q", got, want)
	}
}

func TestRuntimeGatewayModelBaseURLKeepsExplicitPlanPathForSeedance(t *testing.T) {
	oldConfig := config.Cfg
	t.Cleanup(func() { config.Cfg = oldConfig })
	config.Cfg = config.Config{GatewayPublicBaseURL: "https://plan-gateway.example.com/api/plan/v3"}

	got := runtimeGatewayModelBaseURL("https://account-gateway.example.com", "doubao-seedance-2.0")
	want := "https://plan-gateway.example.com/api/plan/v3"
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
