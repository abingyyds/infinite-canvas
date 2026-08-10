package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	copyAIResponse(recorder, request, "test-model", "/images/generations", func() { refunded = true })
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
