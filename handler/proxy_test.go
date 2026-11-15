package handler

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// 图片外链（预签名 URL）不需要也不能带 Authorization：S3/R2 遇到双重鉴权会直接拒绝。
func TestMediaContentWithoutAPIKeySendsNoAuthorization(t *testing.T) {
	gotAuth := "unset"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer upstream.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/media-content", strings.NewReader(`{"url":"`+upstream.URL+`"}`))
	recorder := httptest.NewRecorder()
	serveMediaContent(recorder, request, upstream.Client())

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gotAuth != "" {
		t.Errorf("upstream Authorization = %q, want none for key-less downloads", gotAuth)
	}
	if recorder.Body.String() != "jpeg-bytes" {
		t.Errorf("body = %q, want the upstream bytes", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", got)
	}
}

func TestMediaContentForwardsAPIKeyAsBearer(t *testing.T) {
	gotAuth := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("video-bytes"))
	}))
	defer upstream.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/video-content", strings.NewReader(`{"url":"`+upstream.URL+`","apiKey":"sk-test"}`))
	recorder := httptest.NewRecorder()
	serveMediaContent(recorder, request, upstream.Client())

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-test", gotAuth)
	}
}

func TestMediaNetworkPolicy(t *testing.T) {
	for _, tc := range []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"100.64.0.1", false},
		{"169.254.169.254", false},
		{"::1", false},
		{"::ffff:10.0.0.1", false},
		{"fd00::1", false},
	} {
		if got := isPublicMediaIP(netip.MustParseAddr(tc.ip)); got != tc.public {
			t.Errorf("isPublicMediaIP(%s) = %t, want %t", tc.ip, got, tc.public)
		}
	}
}

func TestMediaContentDoesNotServeActiveContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		_, _ = w.Write([]byte("fixture"))
	}))
	defer upstream.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/media-content", strings.NewReader(`{"url":"`+upstream.URL+`"}`))
	recorder := httptest.NewRecorder()
	serveMediaContent(recorder, request, upstream.Client())
	if recorder.Header().Get("Content-Type") != "application/octet-stream" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("active media response was not served as a download")
	}
}

func TestMediaRedirectCredentialsStayOnOrigin(t *testing.T) {
	original := httptest.NewRequest(http.MethodGet, "https://media.example.com/video", nil)
	for _, tc := range []struct{ target, auth string }{
		{"https://media.example.com/asset", "Bearer fixture"},
		{"https://cdn.example.com/asset", ""},
		{"http://media.example.com/asset", ""},
	} {
		redirect := httptest.NewRequest(http.MethodGet, tc.target, nil)
		redirect.Header.Set("Authorization", "Bearer fixture")
		redirect.Header.Set("X-Goog-Api-Key", "fixture")
		if err := newMediaClient().CheckRedirect(redirect, []*http.Request{original}); err != nil {
			t.Fatal(err)
		}
		if got := redirect.Header.Get("Authorization"); got != tc.auth {
			t.Errorf("redirect to %s has unexpected Authorization header", tc.target)
		}
		if got := redirect.Header.Get("X-Goog-Api-Key"); (got == "fixture") != (tc.auth != "") {
			t.Errorf("redirect to %s has unexpected Gemini authentication header", tc.target)
		}
	}
}
