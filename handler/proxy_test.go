package handler

import (
	"net/http"
	"net/http/httptest"
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
	MediaContent(recorder, request)

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
	MediaContent(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-test", gotAuth)
	}
}
