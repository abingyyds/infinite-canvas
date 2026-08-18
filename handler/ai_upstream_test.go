package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAIUpstreamClientHasTimeout(t *testing.T) {
	if aiUpstreamClient.Timeout <= 0 {
		t.Fatal("aiUpstreamClient needs a timeout; http.DefaultClient has none and pins goroutines on a hung gateway")
	}
	if aiUpstreamClient == http.DefaultClient {
		t.Fatal("aiUpstreamClient must not be http.DefaultClient")
	}
}

// 客户端断开后不能再空等网关：日志里出现过 11 条整整 600 秒的请求，客户端早就走了。
func TestCopyAIResponseStopsWhenClientDisconnects(t *testing.T) {
	released := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(released)
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	refunded := false
	recorder := httptest.NewRecorder()
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	copyAIResponse(recorder, request, "test-model", "/images/generations", func() { refunded = true }, nil)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("copyAIResponse blocked for %v after the client went away", elapsed)
	}
	if !refunded {
		t.Error("expected the refund callback to run when the upstream call is aborted")
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (Fail writes a JSON envelope)", recorder.Code, http.StatusOK)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Error("upstream request was not cancelled")
	}
}

// 网关密钥在网关侧被删掉后所有调用永久 401，重签后必须带着原来的请求体重发一次。
func TestCopyAIResponseRetriesOnceWithARefreshedKey(t *testing.T) {
	var seen []string
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Header.Get("Authorization"))
		bodies = append(bodies, string(payload))
		if len(seen) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"ok"}]}`))
	}))
	defer upstream.Close()

	request, err := http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader(`{"model":"gpt-image-2"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer stale-key")

	refunded := false
	recorder := httptest.NewRecorder()
	copyAIResponse(recorder, request, "gpt-image-2", "/images/generations", func() { refunded = true }, func() (*http.Request, bool) {
		return retryRequestWithKey(request, "fresh-key")
	})

	if len(seen) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(seen))
	}
	if seen[1] != "Bearer fresh-key" {
		t.Errorf("retry Authorization = %q, want %q", seen[1], "Bearer fresh-key")
	}
	if bodies[1] != `{"model":"gpt-image-2"}` {
		t.Errorf("retry body = %q, want the original body replayed", bodies[1])
	}
	if refunded {
		t.Error("credits must not be refunded when the retry succeeds")
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.String() != `{"data":[{"b64_json":"ok"}]}` {
		t.Errorf("body = %q, want the retried upstream payload", recorder.Body.String())
	}
}

// 重签不出来就别再打一次，直接把失败还给前端。
func TestCopyAIResponseFailsWhenTheKeyCannotBeRefreshed(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌"}}`))
	}))
	defer upstream.Close()

	request, err := http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader(`{"model":"gpt-image-2"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	refunded := false
	recorder := httptest.NewRecorder()
	copyAIResponse(recorder, request, "gpt-image-2", "/images/generations", func() { refunded = true }, func() (*http.Request, bool) {
		return nil, false
	})

	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
	if !refunded {
		t.Error("credits must be refunded when the request finally fails")
	}
	// 上游 401 透传成 502，否则前端会把它当成自家会话失效直接登出
	if recorder.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
}
