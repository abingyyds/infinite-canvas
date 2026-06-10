package service

import (
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
